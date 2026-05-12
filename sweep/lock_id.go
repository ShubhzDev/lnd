package sweep

import (
	"crypto/sha256"

	"github.com/btcsuite/btcwallet/wtxmgr"
)

// SweeperLockID is the ID used by the sweeper to lease wallet UTXOs.
// It is the SHA256 of the string "lnd-sweeper-lock-id". Using a
// dedicated ID (separate from LndInternalLockID) lets operators and
// developers identify which subsystem holds a given lease when
// inspecting the wallet lock table.
var SweeperLockID = func() wtxmgr.LockID {
	h := sha256.Sum256([]byte("lnd-sweeper-lock-id"))
	var id wtxmgr.LockID
	copy(id[:], h[:])
	return id
}()
