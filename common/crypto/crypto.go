// Package crypto 提供 AES-256-GCM 加解密能力，独立于业务。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Service 加解密服务，持有主密钥。
type Service struct {
	aead cipher.AEAD
}

// NewService 用 base64 编码的主密钥创建加解密服务。
func NewService(secretKeyB64 string) (*Service, error) {
	key, err := base64.StdEncoding.DecodeString(secretKeyB64)
	if err != nil {
		return nil, fmt.Errorf("decode secret_key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("secret_key must be 32 bytes (base64 encoded)")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &Service{aead: aead}, nil
}

// Encrypt 加密明文，返回 nonce(12B) || ciphertext || tag 整体。
func (s *Service) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt 解密密文（nonce 前缀 12B）。
func (s *Service) Decrypt(ciphertext []byte) ([]byte, error) {
	nonceSize := s.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := s.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}

// GenerateKey 生成 32 字节随机主密钥，返回 base64 字符串。
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
