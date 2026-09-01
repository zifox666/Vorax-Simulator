package training

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"vorax/internal/ai"
	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

type Service struct {
	Rules *engine.Rules
	Spec  *Spec
	Codec *EpisodeCodec
}

func NewService(rules *engine.Rules, codec *EpisodeCodec) (*Service, error) {
	spec, err := NewSpec(rules)
	if err != nil {
		return nil, err
	}
	return &Service{Rules: rules, Spec: spec, Codec: codec}, nil
}

func randomHex(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Service) Reset(req *pb.TrainingResetRequest) (*pb.TrainingTransition, error) {
	if req == nil || req.PetRefreshes < 0 || req.PetRefreshes > 2 || len(req.Seed) > 256 {
		return nil, fmt.Errorf("INVALID_INPUT: 训练 reset 参数无效")
	}
	seed := req.Seed
	var err error
	if seed == "" {
		seed, err = randomHex(32)
		if err != nil {
			return nil, err
		}
	}
	runID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	state, err := engine.New(runID, "training", seed, req.PetRefreshes, s.Rules)
	if err != nil {
		return nil, err
	}
	return s.transition(state, 0)
}

func (s *Service) Step(req *pb.TrainingStepRequest) (*pb.TrainingTransition, error) {
	if req == nil || req.EpisodeToken == "" {
		return nil, fmt.Errorf("INVALID_INPUT: 缺少 episodeToken")
	}
	state, err := s.Codec.Open(req.EpisodeToken)
	if err != nil {
		return nil, err
	}
	if err := engine.ValidateState(state, s.Rules); err != nil {
		return nil, err
	}
	if state.Phase == pb.Phase_FINISHED || state.Offer == nil {
		return nil, fmt.Errorf("INVALID_ACTION: 对局已经结束")
	}
	var action *ai.Action
	switch selected := req.SelectedAction.(type) {
	case *pb.TrainingStepRequest_ActionIndex:
		action, err = s.Spec.Action(selected.ActionIndex)
	case *pb.TrainingStepRequest_Action:
		action = actionFromMessage(selected.Action)
	default:
		err = fmt.Errorf("INVALID_INPUT: 必须且只能提供 actionIndex 或 action")
	}
	if err != nil {
		return nil, err
	}
	obs := ai.FromGameState(state)
	legal := false
	for _, candidate := range ai.LegalActionsFromObservation(obs) {
		if actionKey(candidate) == actionKey(action) {
			legal = true
			break
		}
	}
	if !legal {
		return nil, fmt.Errorf("INVALID_ACTION: 动作不在当前 actionMask 中")
	}
	cmd := &pb.Command{Type: action.Type, CardId: action.CardID, OfferId: state.Offer.Id}
	for _, slot := range action.Slots {
		if slot < 0 || int(slot) >= len(state.Slots) || state.Slots[slot].Monster == nil {
			return nil, fmt.Errorf("INVALID_ACTION: 目标槽位无怪物")
		}
		cmd.TargetIds = append(cmd.TargetIds, state.Slots[slot].Monster.Id)
	}
	previousScore := state.Score
	next, _, err := engine.Apply(state, cmd, s.Rules)
	if err != nil {
		return nil, err
	}
	return s.transition(next, next.Score-previousScore)
}

func (s *Service) transition(state *pb.GameState, reward int64) (*pb.TrainingTransition, error) {
	token, err := s.Codec.Seal(state)
	if err != nil {
		return nil, err
	}
	obs := ai.FromGameState(state)
	semantic, tensor, legal, mask := s.Spec.Encode(obs)
	return &pb.TrainingTransition{
		EpisodeToken: token, Observation: semantic, TensorObservation: tensor, LegalActions: legal, ActionMask: mask,
		Reward: reward, Terminated: obs.Done(), Truncated: false,
		Info: &pb.TrainingInfo{Score: obs.Score, RulesVersion: s.Rules.Version, ContentVersion: s.Rules.ContentVersion, RngVersion: engine.RNGVersion, SpecVersion: SpecVersion},
	}, nil
}
