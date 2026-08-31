package application

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"vorax/internal/ai"
	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

type Service struct {
	Rules  *engine.Rules
	Signer *Signer
}

func (svc *Service) Create(req *pb.CreateRunRequest) (*pb.RunResponse, error) {
	if req.UserId == "" || len(req.UserId) > 128 || len(req.RequestId) < 8 || len(req.RequestId) > 128 {
		return nil, fmt.Errorf("INVALID_INPUT: 匿名标识或请求标识无效")
	}
	if err := svc.versions(req.RulesVersion, req.ContentVersion, req.RngVersion, true); err != nil {
		return nil, err
	}
	// Browser request IDs contain fresh random entropy. HMAC makes create
	// retries deterministic without persisting sessions or depending on Redis.
	derive := func(label string) string {
		m := hmac.New(sha256.New, svc.Signer.Keys[svc.Signer.Active])
		m.Write([]byte(label + "\x00" + req.UserId + "\x00" + req.RequestId))
		return hex.EncodeToString(m.Sum(nil))
	}
	seed := req.Seed
	if seed == "" {
		seed = derive("seed")
	}
	s, err := engine.New(derive("run")[:32], req.UserId, seed, req.PetRefreshes, svc.Rules)
	if err != nil {
		return nil, err
	}
	return svc.response(s, nil, req.RequestId)
}

func (svc *Service) open(token string) (*pb.GameState, error) {
	s, err := svc.Signer.Open(token)
	if err != nil {
		return nil, err
	}
	if err := engine.ValidateState(s, svc.Rules); err != nil {
		return nil, err
	}
	return s, nil
}

func (svc *Service) restore(token, runID string) (*pb.GameState, error) {
	s, err := svc.open(token)
	if err != nil {
		return nil, err
	}
	if s.RunId != runID {
		return nil, fmt.Errorf("INVALID_TOKEN: 存档不属于此对局")
	}
	return s, nil
}

func (svc *Service) Restore(runID string, req *pb.RestoreRequest) (*pb.RunResponse, error) {
	s, err := svc.restore(req.StateToken, runID)
	if err != nil {
		return nil, err
	}
	return svc.response(s, nil, "")
}

// AIDecide 从签名存档恢复真实状态，但只把"UI 可见观察"交给有限信息 AI，
// 返回 AI 建议的动作与它实际看到的观察（观察中不含 seed / RNG）。
func (svc *Service) AIDecide(stateToken string, strategy ai.Strategy, params ai.Params) (*ai.Action, *ai.Observation, error) {
	s, err := svc.open(stateToken)
	if err != nil {
		return nil, nil, err
	}
	obs := ai.FromGameState(s)
	act, err := ai.Decide(obs, strategy, params)
	return act, obs, err
}

func (svc *Service) Command(runID string, req *pb.CommandRequest) (*pb.RunResponse, error) {
	if len(req.RequestId) < 8 || len(req.RequestId) > 128 {
		return nil, fmt.Errorf("INVALID_INPUT: 请求标识无效")
	}
	s, err := svc.restore(req.StateToken, runID)
	if err != nil {
		return nil, err
	}
	if s.Revision != req.ExpectedRevision {
		return nil, fmt.Errorf("STALE_STATE: 请求版本与存档不一致")
	}
	next, events, err := engine.Apply(s, req.Command, svc.Rules)
	if err != nil {
		return nil, err
	}
	return svc.response(next, events, req.RequestId)
}

func (svc *Service) response(s *pb.GameState, events []*pb.GameEvent, requestID string) (*pb.RunResponse, error) {
	token, err := svc.Signer.Seal(s)
	if err != nil {
		return nil, err
	}
	hash, err := GameplayHash(s)
	if err != nil {
		return nil, err
	}
	return &pb.RunResponse{StateToken: token, View: engine.View(s, svc.Rules), Events: events, RequestId: requestID, GameplayHash: hash}, nil
}

func (svc *Service) versions(rules, content, rng string, allowEmpty bool) error {
	if (rules != svc.Rules.Version && !(allowEmpty && rules == "")) || (content != svc.Rules.ContentVersion && !(allowEmpty && content == "")) || (rng != engine.RNGVersion && !(allowEmpty && rng == "")) {
		return fmt.Errorf("VERSION_UNAVAILABLE: 旧版本不可用，不能用当前版本替代")
	}
	return nil
}

func (svc *Service) Replay(req *pb.ReplayRequest) (*pb.ReplayResponse, error) {
	if err := svc.versions(req.RulesVersion, req.ContentVersion, req.RngVersion, false); err != nil {
		return nil, err
	}
	if len(req.Commands) > 128 {
		return nil, fmt.Errorf("INVALID_INPUT: 操作记录过长")
	}
	s, err := engine.New("replay", "replay", req.Seed, req.PetRefreshes, svc.Rules)
	if err != nil {
		return nil, err
	}
	for i, cmd := range req.Commands {
		s, _, err = engine.Apply(s, cmd, svc.Rules)
		if err != nil {
			return nil, fmt.Errorf("REPLAY_INVALID: 第 %d 条操作失败：%w", i+1, err)
		}
	}
	hash, err := GameplayHash(s)
	if err != nil {
		return nil, err
	}
	if req.ExpectedGameplayHash != "" && req.ExpectedGameplayHash != hash {
		return nil, fmt.Errorf("REPLAY_MISMATCH: 重放结果与历史记录不一致")
	}
	return &pb.ReplayResponse{Verified: req.ExpectedGameplayHash != "", GameplayHash: hash, View: engine.View(s, svc.Rules)}, nil
}

func GameplayHash(s *pb.GameState) (string, error) {
	b, err := (proto.MarshalOptions{Deterministic: true}).Marshal(engine.GameplayCopy(s))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func ErrorCode(err error) string {
	code, _, found := strings.Cut(err.Error(), ":")
	if found && !strings.ContainsAny(code, " \n") {
		return code
	}
	return "INTERNAL_ERROR"
}
