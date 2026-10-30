package types

import (
	"cosmossdk.io/collections"
	shared "github.com/TrueOpen/node/x/shared/types"
)

var (
	BuilderCompensationAccountKey = MustVersionedStorePrefix("builder_compensation_account", CurrentStoreSchemaVersion)
	DispatchFaultKey              = MustVersionedStorePrefix("dispatch_fault", CurrentStoreSchemaVersion)
	DispatchFaultPruneIndexKey    = MustVersionedStorePrefix("dispatch_fault_prune", CurrentStoreSchemaVersion)
)

// DispatchFaultKeyPair selects one Builder's immutable fault receipt.
type DispatchFaultKeyPair = collections.Pair[string, shared.Hash32Key]

// DispatchFaultPruneKeyTriple bounds independent fault receipt retention.
type DispatchFaultPruneKeyTriple = collections.Triple[uint64, string, shared.Hash32Key]
