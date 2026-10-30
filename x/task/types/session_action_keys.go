package types

import "cosmossdk.io/collections"

var (
	SessionWithdrawIndexKey  = MustVersionedStorePrefix("session_withdraw_index", CurrentStoreSchemaVersion)
	SessionKeyChangeIndexKey = MustVersionedStorePrefix("session_key_change_index", CurrentStoreSchemaVersion)
	SessionTakeoverIndexKey  = MustVersionedStorePrefix("session_takeover_index", CurrentStoreSchemaVersion)
	OrderCancelIndexKey      = MustVersionedStorePrefix("order_cancel_index", CurrentStoreSchemaVersion)
)

// OrderCancelIndexKeyTriple includes the sequence so consumption can void the exact pending request.
type OrderCancelIndexKeyTriple = collections.Triple[uint64, Hash32Key, uint64]
