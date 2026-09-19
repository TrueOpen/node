package keeper

import (
	"encoding/hex"
)

// hex32 renders a canonical 32-byte ID as its lowercase-hex string form.
//
// Since X-16 this is a *rendering* helper and nothing else. It used to be the
// module's key encoding, which put a 64-byte text form of every task_id,
// session_id, commit_key and proposal_digest into the store — and, because IAVL
// inner nodes carry the same keys, roughly doubled the whole task keyspace.
// Those keys are now the raw 32 bytes (types.Hash32Key / types.Hash32KeyCodec),
// so hex32 no longer touches consensus state.
//
// What is left is the three places a Hash32 legitimately has to be text:
//
//   - the string-shaped half of the HubKeeper boundary — ReleaseTaskLiabilities,
//     DeleteOneClosedTaskLiability and DeleteFinalizedSettlementReceipts still
//     take session_id/task_id as strings, because x/hub keys its own rows
//     that way. Retyping that boundary is A-15b's neighbour and is deliberately
//     out of scope here;
//   - error, log and event text, where a %s of the raw bytes would emit
//     unprintable garbage;
//   - the in-memory maps the Genesis validator builds, where a readable key is
//     worth more than the bytes it saves.
//
// It must not appear in a store key, a hash preimage, or a canonical frame. Hex
// is order-preserving over the underlying bytes, so the switch away from it left
// every range scan and iteration order byte-for-byte unchanged; see
// types/key_codec.go for why that holds and for the no-migration policy that
// makes any later change to those codecs a state migration rather than a
// refactor.
func hex32(value []byte) string { return hex.EncodeToString(value) }
