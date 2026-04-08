package peer

import (
	"errors"

	"github.com/btcsuite/btclog/v2"
	"github.com/lightningnetwork/lnd/fn/v2"
	"github.com/lightningnetwork/lnd/onionmessage"
)

// ErrNoChannel is the sentinel error returned by allowOnionMessage when
// the incoming peer has no fully open channel with us. It is the
// primary Sybil-resistance layer on top of the byte-granular rate
// limiters: an attacker that can cheaply spin up new identities cannot
// burn any per-peer or global token budget because the channel gate
// runs before the IngressLimiter is consulted at all.
var ErrNoChannel = errors.New("peer has no open channel")

// allowOnionMessage applies the channel-presence gate and then, if the
// peer has at least one fully open channel with us, delegates to the
// IngressLimiter for the per-peer-then-global byte-granular rate limit
// check. The channel gate runs first on purpose: if it rejects, no rate
// limiter state is allocated for the no-channel peer and neither bucket
// is debited. A successful result wraps fn.Unit; a rejection wraps one
// of the sentinel errors ErrNoChannel,
// onionmessage.ErrPeerRateLimit, or onionmessage.ErrGlobalRateLimit so
// that callers can distinguish the drop reason via errors.Is.
//
// A nil IngressLimiter is treated as "disabled" and always accepts the
// message once the channel gate passes. This preserves the behavior of
// test and disabled-onion-messaging configurations without forcing
// callers to construct a real limiter.
func allowOnionMessage(limiter onionmessage.IngressLimiter,
	peerKey [33]byte, msgBytes int,
	hasChannel bool) fn.Result[fn.Unit] {

	if !hasChannel {
		return fn.Err[fn.Unit](ErrNoChannel)
	}
	if limiter == nil {
		return fn.Ok(fn.Unit{})
	}

	return limiter.AllowN(peerKey, msgBytes)
}

// logFirstOnionDrop emits a single info-level log line the first time
// the limiter identified by err trips. Subsequent drops fall through to
// the caller's debug logging so that operators get a clear "rate
// limiting is active" signal at info level without the log being
// flooded under sustained attack. The channel-gate drop path does not
// emit an info-level first-drop line because it is a per-peer policy
// decision rather than a resource-exhaustion signal.
func logFirstOnionDrop(log btclog.Logger, err error,
	limiter onionmessage.IngressLimiter) {

	if limiter == nil {
		return
	}

	switch {
	case errors.Is(err, onionmessage.ErrGlobalRateLimit):
		if limiter.FirstGlobalDropClaim() {
			log.Infof("onion message global rate limiter " +
				"engaged; further drops logged at debug")
		}

	case errors.Is(err, onionmessage.ErrPeerRateLimit):
		if limiter.FirstPeerDropClaim() {
			log.Infof("onion message per-peer rate limiter " +
				"engaged; further drops logged at debug")
		}
	}
}
