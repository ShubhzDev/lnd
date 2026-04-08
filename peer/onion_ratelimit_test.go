package peer

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lightningnetwork/lnd/onionmessage"
	"github.com/stretchr/testify/require"
)

// testMsgBytes is the on-the-wire size we charge the bucket per call in
// these tests. It is sized to approximate a spec-max onion message so
// that burst budgets scale naturally with the per-message cost.
const testMsgBytes = 32 * 1024

// stubGlobalLimiter is a RateLimiter whose AllowN result is controlled by
// a caller-supplied function. It is used to exercise the global-rejection
// branch of allowOnionMessage without needing a real token bucket.
type stubGlobalLimiter struct {
	allow func() bool
	calls atomic.Uint64
}

// AllowN records the call and dispatches to the configured predicate.
func (s *stubGlobalLimiter) AllowN(_ int) bool {
	s.calls.Add(1)
	return s.allow()
}

// TestAllowOnionMessageNilLimiters verifies that allowOnionMessage treats nil
// limiter values as "disabled" and unconditionally accepts messages.
func TestAllowOnionMessageNilLimiters(t *testing.T) {
	t.Parallel()

	var peer [33]byte
	reason, ok := allowOnionMessage(nil, nil, peer, testMsgBytes, true)
	require.True(t, ok)
	require.Empty(t, reason)
}

// TestAllowOnionMessageNoChannel verifies that messages from a peer that
// does not have a fully open channel with us are dropped unconditionally
// with the no-channel drop reason. This is the primary Sybil-resistance
// layer: an attacker that can cheaply spin up new identities is unable
// to burn any per-peer or global token budget since the gate runs before
// either limiter is consulted. The test also asserts that neither
// limiter is touched on a no-channel drop — no global call counter
// ticks, and the per-peer limiter does not allocate a bucket for the
// rejected peer.
func TestAllowOnionMessageNoChannel(t *testing.T) {
	t.Parallel()

	// A global limiter that would accept traffic if consulted — if it
	// records any call at all, the gate was skipped.
	global := &stubGlobalLimiter{allow: func() bool { return true }}
	// A per-peer limiter with plenty of headroom so that exhaustion
	// cannot explain any rejection we see.
	peer := onionmessage.NewPeerRateLimiter(1_000_000, 100*testMsgBytes)

	var key [33]byte
	key[0] = 0x07

	// With hasChannel=false the gate must reject immediately.
	reason, ok := allowOnionMessage(
		global, peer, key, testMsgBytes, false,
	)
	require.False(t, ok)
	require.Equal(t, "peer has no open channel", reason)

	// Neither limiter must have been touched: the global stub's call
	// counter must still be zero and the per-peer limiter must not
	// have recorded a drop or created a bucket for this key.
	require.Equal(t, uint64(0), global.calls.Load(),
		"no-channel drop must not consult the global limiter")
	require.Equal(t, uint64(0), peer.Dropped(),
		"no-channel drop must not consult the per-peer limiter")

	// Once a channel comes into existence (simulated here by flipping
	// the hasChannel flag), the same key must be allowed through and
	// both limiters should now be consulted.
	reason, ok = allowOnionMessage(
		global, peer, key, testMsgBytes, true,
	)
	require.True(t, ok)
	require.Empty(t, reason)
	require.Equal(t, uint64(1), global.calls.Load())
}

// TestAllowOnionMessagePeerRejectsFirst verifies that the per-peer limiter
// is consulted before the global limiter so that a hostile peer whose own
// bucket is already empty cannot burn global tokens on every rejected
// attempt and starve legitimate peers of the shared budget.
func TestAllowOnionMessagePeerRejectsFirst(t *testing.T) {
	t.Parallel()

	global := &stubGlobalLimiter{allow: func() bool { return true }}
	// Very low rate + burst exactly one message guarantees the second
	// call is rejected within the test window.
	peer := onionmessage.NewPeerRateLimiter(1, testMsgBytes)

	var key [33]byte
	key[0] = 0x03

	// First call consumes the single burst token. Both limiters are
	// consulted because the per-peer check passes and the code falls
	// through to the global check.
	reason, ok := allowOnionMessage(
		global, peer, key, testMsgBytes, true,
	)
	require.True(t, ok)
	require.Empty(t, reason)
	require.Equal(t, uint64(1), global.calls.Load())

	// Second call should trip the per-peer limiter and must not consult
	// the global limiter at all, preserving the global budget.
	reason, ok = allowOnionMessage(
		global, peer, key, testMsgBytes, true,
	)
	require.False(t, ok)
	require.Equal(t, "per-peer rate limit exceeded", reason)
	require.Equal(t, uint64(1), peer.Dropped())
	require.Equal(t, uint64(1), global.calls.Load(),
		"global limiter must not be consulted when per-peer rejects")
}

// TestAllowOnionMessageGlobalRejects verifies that when the per-peer limiter
// permits traffic but the global bucket is exhausted, allowOnionMessage
// reports the global drop reason.
func TestAllowOnionMessageGlobalRejects(t *testing.T) {
	t.Parallel()

	global := &stubGlobalLimiter{allow: func() bool { return false }}
	peer := onionmessage.NewPeerRateLimiter(1_000_000, 100*testMsgBytes)

	var key [33]byte
	key[0] = 0x02

	reason, ok := allowOnionMessage(
		global, peer, key, testMsgBytes, true,
	)
	require.False(t, ok)
	require.Equal(t, "global rate limit exceeded", reason)

	// The per-peer limiter is consulted first and allows the message,
	// consuming one of its tokens, before the global rejects it.
	require.Equal(t, uint64(0), peer.Dropped())
	require.Equal(t, uint64(1), global.calls.Load())
}

// TestAllowOnionMessageHappyPath verifies that when both limiters permit the
// traffic, the helper returns ok with an empty reason.
func TestAllowOnionMessageHappyPath(t *testing.T) {
	t.Parallel()

	global := &stubGlobalLimiter{allow: func() bool { return true }}
	peer := onionmessage.NewPeerRateLimiter(1_000_000, 100*testMsgBytes)

	var key [33]byte
	key[0] = 0x04

	for i := 0; i < 10; i++ {
		reason, ok := allowOnionMessage(
			global, peer, key, testMsgBytes, true,
		)
		require.True(t, ok, "iter %d", i)
		require.Empty(t, reason)
	}
	require.Equal(t, uint64(0), peer.Dropped())
}

// TestAllowOnionMessagePeerIsolation verifies at the peer-package level that
// exhausting one peer's bucket through allowOnionMessage does not affect a
// different peer's allowance — guarding against a regression where the
// helper might key the bucket incorrectly.
func TestAllowOnionMessagePeerIsolation(t *testing.T) {
	t.Parallel()

	global := &stubGlobalLimiter{allow: func() bool { return true }}
	peer := onionmessage.NewPeerRateLimiter(1, 2*testMsgBytes)

	var keyA, keyB [33]byte
	keyA[0] = 0x02
	keyB[0] = 0x03

	// Drain peer A.
	for i := 0; i < 2; i++ {
		_, ok := allowOnionMessage(
			global, peer, keyA, testMsgBytes, true,
		)
		require.True(t, ok)
	}
	_, ok := allowOnionMessage(
		global, peer, keyA, testMsgBytes, true,
	)
	require.False(t, ok)

	// Peer B must still have its full burst available.
	for i := 0; i < 2; i++ {
		_, ok := allowOnionMessage(
			global, peer, keyB, testMsgBytes, true,
		)
		require.True(t, ok, "peer B slot %d", i)
	}
}

// TestAllowOnionMessageConcurrent exercises concurrent access to
// allowOnionMessage across many goroutines. It asserts that the sum of
// accepted calls plus the per-peer dropped counter equals the total number
// of attempts, and that no race or panic occurs. Run with -race for the
// strongest signal.
func TestAllowOnionMessageConcurrent(t *testing.T) {
	t.Parallel()

	global := &stubGlobalLimiter{allow: func() bool { return true }}
	const burstMessages = 32
	peer := onionmessage.NewPeerRateLimiter(1, burstMessages*testMsgBytes)

	var key [33]byte
	key[0] = 0x05

	const workers = 16
	const perWorker = 64
	var wg sync.WaitGroup
	var accepted atomic.Uint64

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, ok := allowOnionMessage(
					global, peer, key, testMsgBytes,
					true,
				); ok {
					accepted.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	total := uint64(workers * perWorker)
	require.Equal(
		t, total, accepted.Load()+peer.Dropped(),
		"every attempt must be counted as accepted or dropped",
	)
	// With a near-zero refill rate the bucket can only issue at most
	// burstMessages accepts before refill; since the test runs much
	// faster than the refill interval, accepted should equal the burst.
	require.Equal(t, uint64(burstMessages), accepted.Load())
}
