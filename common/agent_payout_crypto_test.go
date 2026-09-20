package common

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentPayoutEncryptionRequiresPersistentKey(t *testing.T) {
	t.Setenv("AGENT_PAYOUT_ENCRYPTION_KEY", "")
	assert.False(t, AgentPayoutEncryptionReady())
	_, err := EncryptAgentPayoutText("recipient@example.com", "42:account")
	require.ErrorIs(t, err, ErrAgentPayoutEncryptionUnavailable)
	t.Setenv("AGENT_PAYOUT_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("too-short")))
	_, err = EncryptAgentPayoutText("recipient@example.com", "42:account")
	require.ErrorIs(t, err, ErrAgentPayoutEncryptionUnavailable)
}

func TestAgentPayoutEncryptionAuthenticatesOwnerAndDetectsCorruption(t *testing.T) {
	t.Setenv("AGENT_PAYOUT_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	require.True(t, AgentPayoutEncryptionReady())
	ciphertext, err := EncryptAgentPayoutText("recipient@example.com", "42:account")
	require.NoError(t, err)
	assert.NotContains(t, ciphertext, "recipient@example.com")
	plaintext, err := DecryptAgentPayoutText(ciphertext, "42:account")
	require.NoError(t, err)
	assert.Equal(t, "recipient@example.com", plaintext)
	_, err = DecryptAgentPayoutText(ciphertext, "43:account")
	require.Error(t, err)
	_, err = DecryptAgentPayoutText(ciphertext, "42:name")
	require.Error(t, err)
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(ciphertext, "v1:"))
	require.NoError(t, err)
	data[len(data)-1] ^= 1
	_, err = DecryptAgentPayoutText("v1:"+base64.RawStdEncoding.EncodeToString(data), "42:account")
	require.Error(t, err)
	t.Setenv("AGENT_PAYOUT_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32))))
	_, err = DecryptAgentPayoutText(ciphertext, "42:account")
	require.Error(t, err)
}
