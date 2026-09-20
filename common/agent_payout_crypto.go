package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
)

var ErrAgentPayoutEncryptionUnavailable = errors.New("agent payout encryption key is missing or invalid")

// A dedicated, persistent key avoids losing payout details when session secrets rotate.
func agentPayoutCipher() (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("AGENT_PAYOUT_ENCRYPTION_KEY")))
	if err != nil || len(key) != 32 {
		return nil, ErrAgentPayoutEncryptionUnavailable
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrAgentPayoutEncryptionUnavailable
	}
	return cipher.NewGCM(block)
}

func AgentPayoutEncryptionReady() bool {
	_, err := agentPayoutCipher()
	return err == nil
}

func EncryptAgentPayoutText(plaintext, purpose string) (string, error) {
	aead, err := agentPayoutCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", errors.New("cannot generate agent payout encryption nonce")
	}
	encrypted := aead.Seal(nonce, nonce, []byte(plaintext), []byte("agent-payout-v1:"+purpose))
	return "v1:" + base64.RawStdEncoding.EncodeToString(encrypted), nil
}

func DecryptAgentPayoutText(encrypted, purpose string) (string, error) {
	aead, err := agentPayoutCipher()
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(encrypted, "v1:") {
		return "", errors.New("invalid agent payout ciphertext")
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encrypted, "v1:"))
	if err != nil || len(data) < aead.NonceSize()+aead.Overhead() {
		return "", errors.New("invalid agent payout ciphertext")
	}
	plaintext, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte("agent-payout-v1:"+purpose))
	if err != nil {
		return "", errors.New("cannot decrypt agent payout details")
	}
	return string(plaintext), nil
}
