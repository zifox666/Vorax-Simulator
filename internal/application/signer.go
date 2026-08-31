package application

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/proto"
	pb "vorax/internal/protocol"
)

type Signer struct {
	Active string
	Keys   map[string][]byte
}

func NewSigner(id string, key []byte) (*Signer, error) {
	if id == "" || strings.Contains(id, ".") || len(key) < 32 {
		return nil, fmt.Errorf("签名密钥至少需要 32 字节，密钥版本不能包含点")
	}
	return &Signer{Active: id, Keys: map[string][]byte{id: key}}, nil
}

func (s *Signer) Seal(state *pb.GameState) (string, error) {
	b, err := (proto.MarshalOptions{Deterministic: true}).Marshal(state)
	if err != nil {
		return "", err
	}
	payload := s.Active + "." + base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, s.Keys[s.Active])
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Signer) Open(token string) (*pb.GameState, error) {
	if len(token) > 65536 {
		return nil, fmt.Errorf("INVALID_TOKEN: 存档过大")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("INVALID_TOKEN: 存档格式错误")
	}
	key, ok := s.Keys[parts[0]]
	if !ok {
		return nil, fmt.Errorf("KEY_UNAVAILABLE: 存档签名密钥版本不可用")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("INVALID_TOKEN: 存档签名无效")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, fmt.Errorf("INVALID_TOKEN: 存档签名不匹配，数据可能被修改")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("INVALID_TOKEN: 存档数据无效")
	}
	state := new(pb.GameState)
	if err := proto.Unmarshal(b, state); err != nil {
		return nil, fmt.Errorf("INVALID_TOKEN: 存档无法解析")
	}
	return state, nil
}

// Local development persists one key file. Never regenerate an existing key
// after an error: that would invalidate every browser checkpoint silently.
func LoadLocalSigner(path string) (*Signer, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if os.IsExist(e) {
			b, err = os.ReadFile(path)
		} else if e != nil {
			return nil, e
		} else {
			_, err = f.WriteString(base64.StdEncoding.EncodeToString(key))
			closeErr := f.Close()
			if err != nil {
				return nil, err
			}
			if closeErr != nil {
				return nil, closeErr
			}
			b = []byte(base64.StdEncoding.EncodeToString(key))
		}
	}
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("签名密钥文件损坏：%w", err)
	}
	return NewSigner("local-v1", key)
}
