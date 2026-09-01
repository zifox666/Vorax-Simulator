package training

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"vorax/internal/application"
	pb "vorax/internal/protocol"
)

const tokenPrefix = "vte1"

type EpisodeCodec struct{ signer *application.Signer }

func NewEpisodeCodec(signer *application.Signer) *EpisodeCodec { return &EpisodeCodec{signer: signer} }

func episodeAEAD(key []byte) (cipher.AEAD, error) {
	material := append(append([]byte{}, key...), []byte("\x00vorax-training-episode-v1")...)
	derived := sha256.Sum256(material)
	block, err := aes.NewCipher(derived[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (c *EpisodeCodec) Seal(state *pb.GameState) (string, error) {
	b, err := (proto.MarshalOptions{Deterministic: true}).Marshal(state)
	if err != nil {
		return "", err
	}
	keyID := c.signer.Active
	aead, err := episodeAEAD(c.signer.Keys[keyID])
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	aad := []byte(tokenPrefix + "." + keyID)
	sealed := aead.Seal(nonce, nonce, b, aad)
	return string(aad) + "." + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *EpisodeCodec) Open(token string) (*pb.GameState, error) {
	if len(token) == 0 || len(token) > 65536 {
		return nil, fmt.Errorf("INVALID_TOKEN: 训练令牌大小无效")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenPrefix {
		return nil, fmt.Errorf("INVALID_TOKEN: 训练令牌格式错误")
	}
	key, ok := c.signer.Keys[parts[1]]
	if !ok {
		return nil, fmt.Errorf("KEY_UNAVAILABLE: 训练令牌密钥版本不可用")
	}
	aead, err := episodeAEAD(key)
	if err != nil {
		return nil, err
	}
	sealed, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, fmt.Errorf("INVALID_TOKEN: 训练令牌数据无效")
	}
	nonce := sealed[:aead.NonceSize()]
	b, err := aead.Open(nil, nonce, sealed[aead.NonceSize():], []byte(parts[0]+"."+parts[1]))
	if err != nil {
		return nil, fmt.Errorf("INVALID_TOKEN: 训练令牌认证失败")
	}
	state := new(pb.GameState)
	if err := proto.Unmarshal(b, state); err != nil {
		return nil, fmt.Errorf("INVALID_TOKEN: 训练令牌无法解析")
	}
	return state, nil
}
