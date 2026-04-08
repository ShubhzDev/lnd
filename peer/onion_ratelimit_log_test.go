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

// newRealIngressLimiter constructs a real ingressLimiter backed by real
// per-peer and global limiters sized so the first message passes and
// every subsequent one trips the named side of the limiter. It is used
// by the log tests to exercise the one-shot claim path against real
// FirstDropClaim bookkeeping rather than a stub.
func newRealIngressLimiter(t *testing.T) onionmessage.IngressLimiter {
	t.Helper()

	// Burst == one max-sized message for both sides; rate of 1 Kbps
	// ensures neither bucket refills within the test window.
	peerLim := onionmessage.NewPeerRateLimiter(1, testMsgBytes)
	globalLim := onionmessage.NewGlobalLimiter(1, testMsgBytes)

	return onionmessage.NewIngressLimiter(peerLim, globalLim)
}

// TestLogFirstOnionDropGlobalOneShot verifies that logFirstOnionDrop
// emits exactly one info-level line for the global limiter's first
// drop and is silent on subsequent drops, so operators get a single
// "engaged" signal without log flooding under sustained attack.
func TestLogFirstOnionDropGlobalOneShot(t *testing.T) {
	t.Parallel()

	log, buf := newCapturingLogger()
	limiter := newRealIngressLimiter(t)

	// First drop log: must emit.
	logFirstOnionDrop(log, onionmessage.ErrGlobalRateLimit, limiter)
	require.Contains(t, buf.String(), "global rate limiter")

	// Second drop log: must be silent (buffer size unchanged).
	sizeAfterFirst := buf.Len()
	logFirstOnionDrop(log, onionmessage.ErrGlobalRateLimit, limiter)
	require.Equal(t, sizeAfterFirst, buf.Len(),
		"second drop must not re-log the first-drop line")
}

// TestLogFirstOnionDropPeerOneShot verifies the same one-shot property
// for the per-peer limiter and that the nil-limiter guard prevents a
// panic when onion message rate limiting is entirely disabled.
func TestLogFirstOnionDropPeerOneShot(t *testing.T) {
	t.Parallel()

	log, buf := newCapturingLogger()

	// Nil limiter: must not panic and must not log.
	logFirstOnionDrop(log, onionmessage.ErrPeerRateLimit, nil)
	require.Empty(t, buf.String())

	// Real limiter: emit once, then silent.
	limiter := newRealIngressLimiter(t)
	logFirstOnionDrop(log, onionmessage.ErrPeerRateLimit, limiter)
	require.Contains(t, buf.String(), "per-peer rate limiter")
	sizeAfterFirst := buf.Len()
	logFirstOnionDrop(log, onionmessage.ErrPeerRateLimit, limiter)
	require.Equal(t, sizeAfterFirst, buf.Len())
}

// TestLogFirstOnionDropUnknownReason verifies that an error that does
// not match any known drop reason is a no-op — neither limiter's
// first-drop flag is consumed. This guards against a typo or a new
// drop reason being added without a matching log case.
func TestLogFirstOnionDropUnknownReason(t *testing.T) {
	t.Parallel()

	log, buf := newCapturingLogger()
	limiter := newRealIngressLimiter(t)

	logFirstOnionDrop(log, ErrNoChannel, limiter)
	require.Empty(t, buf.String())

	// Both limiters' first-drop flags must still be unclaimed, so a
	// follow-up call with a valid reason still emits the info line.
	logFirstOnionDrop(log, onionmessage.ErrGlobalRateLimit, limiter)
	require.Contains(t, buf.String(), "global rate limiter")
}
