package peer

import (
	"github.com/btcsuite/btclog/v2"
	"github.com/lightningnetwork/lnd/onionmessage"
)

// Drop reason strings shared by allowOnionMessage and logFirstOnionDrop.
// Keeping them in one place avoids a silent break in the first-drop log if
// the allowOnionMessage reason text is ever edited.
const (
	dropReasonNoChannel   = "peer has no open channel"
	dropReasonPeerLimit   = "per-peer rate limit exceeded"
	dropReasonGlobalLimit = "global rate limit exceeded"
)

// allowOnionMessage applies the configured ingress gates and rate limiters
// to an incoming onion message of msgBytes bytes and reports whether it
// should be accepted. The checks run in three stages, cheapest-and-most-
// discriminating first:
//
//  1. Channel gate: if hasChannel is false — i.e. the peer has no fully
//     open channel with us — the message is dropped unconditionally.
//     This is the primary Sybil-resistance layer: onion message
//     forwarding is unpaid, so absent a channel requirement an attacker
//     could spin up arbitrarily many identities and each burn a full
//     per-peer budget. Requiring a funded channel turns new identities
//     into a capital cost and flips the Sybil economics on their head.
//     As a side benefit, no-channel peers never allocate any per-peer
//     rate limiter state, since the check runs before the limiters.
//
//  2. Per-peer rate limiter: bounds the sustained byte rate and burst
//     this specific peer can push at us.
//
//  3. Global rate limiter: bounds the sustained aggregate byte rate
//     across all peers so that a burst of channel peers cannot
//     collectively dwarf the node's payment traffic.
//
// Each limiter's token bucket holds bytes, not message counts, so
// msgBytes is the charge against the bucket — small messages pay for
// less of the budget than spec-max ones. The per-peer limiter is
// consulted before the global limiter on purpose: if we consulted the
// global limiter first, a hostile peer whose own per-peer bucket is
// already empty would still burn global tokens on every rejected attempt,
// letting a single peer drain the shared budget and starve legitimate
// peers. By checking the per-peer bucket first, over-limit traffic from
// one peer is rejected before it can touch the global bucket, and the
// global bucket only accounts for traffic that was within its source
// peer's allowance. If the message is rejected the returned string names
// the gate or limiter that triggered the drop and is suitable for
// logging. Nil limiter values are treated as "disabled" and always allow
// the message.
func allowOnionMessage(global onionmessage.RateLimiter,
	peer *onionmessage.PeerRateLimiter,
	peerKey [33]byte, msgBytes int, hasChannel bool) (string, bool) {

	if !hasChannel {
		return dropReasonNoChannel, false
	}
	if peer != nil && !peer.AllowN(peerKey, msgBytes) {
		return dropReasonPeerLimit, false
	}
	if global != nil && !global.AllowN(msgBytes) {
		return dropReasonGlobalLimit, false
	}

	return "", true
}

// logFirstOnionDrop emits a single info-level log line the first time a
// given limiter trips. Subsequent drops fall through to the caller's debug
// logging so that operators get a clear "rate limiting is active" signal at
// info level without the log being flooded under sustained attack.
func logFirstOnionDrop(log btclog.Logger, reason string,
	global onionmessage.RateLimiter,
	peer *onionmessage.PeerRateLimiter) {

	switch reason {
	case dropReasonGlobalLimit:
		if onionmessage.FirstGlobalDropClaim(global) {
			log.Infof("onion message global rate limiter " +
				"engaged; further drops logged at debug")
		}
	case dropReasonPeerLimit:
		if peer != nil && peer.FirstDropClaim() {
			log.Infof("onion message per-peer rate limiter " +
				"engaged; further drops logged at debug")
		}
	}
}
