package peer

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btclog/v2"
	"github.com/lightningnetwork/lnd/onionmessage"
	"github.com/stretchr/testify/require"
)

// newCapturingLogger builds a btclog.Logger backed by an in-memory buffer
// so tests can assert whether a given log line was emitted.
func newCapturingLogger() (btclog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	handler := btclog.NewDefaultHandler(buf, btclog.WithNoTimestamp())
	return btclog.NewSLogger(handler), buf
}

// TestLogFirstOnionDropGlobalOneShot verifies that logFirstOnionDrop emits
// exactly one info-level line for the global limiter's first drop and is
// silent on subsequent drops, so operators get a single "engaged" signal
// without log flooding under sustained attack.
func TestLogFirstOnionDropGlobalOneShot(t *testing.T) {
	t.Parallel()

	log, buf := newCapturingLogger()
	// Tiny bucket: one allow, then drops.
	global := onionmessage.NewGlobalLimiter(1, testMsgBytes)
	require.True(t, global.AllowN(testMsgBytes))
	require.False(t, global.AllowN(testMsgBytes))

	// First drop log: must emit.
	logFirstOnionDrop(log, dropReasonGlobalLimit, global, nil)
	require.Contains(t, buf.String(), "global rate limiter")

	// Second drop log: must be silent (buffer size unchanged).
	sizeAfterFirst := buf.Len()
	logFirstOnionDrop(log, dropReasonGlobalLimit, global, nil)
	require.Equal(t, sizeAfterFirst, buf.Len(),
		"second drop must not re-log the first-drop line")
}

// TestLogFirstOnionDropPeerOneShot verifies the same one-shot property for
// the per-peer limiter and that the nil-peer guard prevents a panic when
// the per-peer limiter is not configured.
func TestLogFirstOnionDropPeerOneShot(t *testing.T) {
	t.Parallel()

	log, buf := newCapturingLogger()
	peer := onionmessage.NewPeerRateLimiter(1, testMsgBytes)

	// Nil peer limiter: must not panic and must not log.
	logFirstOnionDrop(log, dropReasonPeerLimit, nil, nil)
	require.Empty(t, buf.String())

	// Real peer limiter: emit once, then silent.
	logFirstOnionDrop(log, dropReasonPeerLimit, nil, peer)
	require.Contains(t, buf.String(), "per-peer rate limiter")
	sizeAfterFirst := buf.Len()
	logFirstOnionDrop(log, dropReasonPeerLimit, nil, peer)
	require.Equal(t, sizeAfterFirst, buf.Len())
}

// TestLogFirstOnionDropUnknownReason verifies that a reason string that
// does not match any known drop reason is a no-op — neither limiter's
// first-drop flag is consumed. This guards against a typo breaking the
// dispatch silently.
func TestLogFirstOnionDropUnknownReason(t *testing.T) {
	t.Parallel()

	log, buf := newCapturingLogger()
	global := onionmessage.NewGlobalLimiter(1, testMsgBytes)
	peer := onionmessage.NewPeerRateLimiter(1, testMsgBytes)

	logFirstOnionDrop(log, "bogus reason", global, peer)
	require.Empty(t, buf.String())

	// Both limiters' first-drop flags must still be unclaimed, so a
	// follow-up call with a valid reason still emits the info line.
	logFirstOnionDrop(log, dropReasonGlobalLimit, global, peer)
	require.Contains(t, buf.String(), "global rate limiter")
}
