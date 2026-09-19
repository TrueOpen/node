package types

import (
	"fmt"
	"strconv"
	"strings"

	"cosmossdk.io/collections"
	"cosmossdk.io/collections/codec"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	// ModuleName is the hub-side global arbitration module name.
	ModuleName = "hub"

	// StoreKey is the hub module KV store key.
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module name to avoid importing x/gov in types.
	GovModuleName = "gov"

	// RewardsModuleName holds settled service-side earnings and reward accruals.
	RewardsModuleName = "hub_rewards"

	// EmissionModuleName holds protocol emission allocation.
	EmissionModuleName = "hub_emission"

	// TreasuryModuleName holds the protocol treasury pool.
	TreasuryModuleName = "hub_treasury"

	// ServiceBondModuleName holds service-side worker/verifier bonds.
	ServiceBondModuleName = "hub_service_bond"
)

const (
	CurrentStoreSchemaVersion = uint64(1)
)

func FormatVersionedStorePrefix(component string, version uint64) (string, error) {
	if err := validateStorePrefixComponent(component); err != nil {
		return "", err
	}
	if version == 0 {
		return "", fmt.Errorf("store prefix version must be greater than 0")
	}
	return ModuleName + "/" + component + "/v" + strconv.FormatUint(version, 10), nil
}

func MustVersionedStorePrefix(component string, version uint64) collections.Prefix {
	prefix, err := FormatVersionedStorePrefix(component, version)
	if err != nil {
		panic(err)
	}
	return collections.NewPrefix(prefix)
}

func validateStorePrefixComponent(component string) error {
	if component == "" {
		return fmt.Errorf("store prefix component is required")
	}
	if strings.TrimSpace(component) != component {
		return fmt.Errorf("store prefix component must be canonical")
	}
	if strings.ContainsAny(component, "/\x00") {
		return fmt.Errorf("store prefix component must not contain slash or NUL")
	}
	return nil
}

// ---- Store prefix layout (X-13) ----
//
// Every prefix in this module except ParamsKey is MustVersionedStorePrefix, i.e.
// the byte string "hub/<component>/v<n>". That form is not decoration: for
// two distinct components c1 != c2 neither "hub/c1/v1" nor
// "hub/c2/v1" can be a byte prefix of the other, because components are
// validated to contain no '/', so the first differing byte always lands inside
// the component or on its trailing '/'. Raw literals have no such property -
// "builder_set" is a byte prefix of "builder_set_prune".
//
// What that buys is hygiene and defence in depth, not the avoidance of a latent
// consensus bug. cosmossdk.io/collections rejects overlapping prefixes inside
// SchemaBuilder.Build (schema.go compares every registered prefix pair with
// strings.HasPrefix), and Keeper.NewKeeper panics on that error, so a colliding
// pair fails at keeper construction - at app startup and in every single test -
// and can never silently merge two keyspaces in committed state. The versioned
// form makes the collision structurally impossible so that guard never has to
// fire, and keeps the store self-describing when a future schema version needs
// two generations of the same component to coexist.
//
// TestHubStorePrefixesAreVersionedAndCollisionFree in keys_contract_test.go
// enforces both halves of this: the form and the no-byte-prefix property.
var (
	// ParamsKey is the one deliberate exception. "p_<module>" is the Cosmos SDK
	// convention for a module's collections.Item[Params], and the raw-store
	// tooling that reads params without the keeper (upgrade handlers, store
	// queries) expects exactly that byte string. Params also has no
	// version-scoped history to keep apart, so versioning it would buy nothing.
	// It is short and shares no prefix with "hub/...", so it cannot collide
	// with the versioned block below.
	ParamsKey = collections.NewPrefix("p_hub")

	// ParamsMetaKey holds the HubParamsMetaState singleton that
	// keeper_api_contract.md §18.0 needs for `expected_version == current_version`
	// and that §5.11 event 110 needs for `old_version`/`params_hash`.
	//
	// CONTRACT-GAP: keeper_data_structure_contract.md registers no HubParamsMetaState row;
	// it is the minimal state the two interface-contract requirements above
	// force into existence. Document side must register it.
	ParamsMetaKey = MustVersionedStorePrefix("params_meta", CurrentStoreSchemaVersion)
	// EndBlockBudget* are overwritten once per block and are deliberately not
	// exported. They carry the budget left by Hub to the Task EndBlocker, which
	// runs immediately after Hub in the frozen application order.
	EndBlockBudgetHeightKey         = MustVersionedStorePrefix("endblock_budget_height", CurrentStoreSchemaVersion)
	EndBlockBudgetRemainingItemsKey = MustVersionedStorePrefix("endblock_budget_remaining_items", CurrentStoreSchemaVersion)
	EndBlockBudgetRemainingBytesKey = MustVersionedStorePrefix("endblock_budget_remaining_bytes", CurrentStoreSchemaVersion)

	ModelStateKey                          = MustVersionedStorePrefix("model", CurrentStoreSchemaVersion)
	ProfileStateKey                        = MustVersionedStorePrefix("profile", CurrentStoreSchemaVersion)
	RegistrationReceiptKey                 = MustVersionedStorePrefix("registration_receipt", CurrentStoreSchemaVersion)
	CortexNodeKey                          = MustVersionedStorePrefix("cortex_node", CurrentStoreSchemaVersion)
	ServiceBondKey                         = MustVersionedStorePrefix("service_bond", CurrentStoreSchemaVersion)
	UnbondingKey                           = MustVersionedStorePrefix("unbonding", CurrentStoreSchemaVersion)
	UnbondingMaturityIndexKey              = MustVersionedStorePrefix("unbonding_maturity", CurrentStoreSchemaVersion)
	UnbondingByOperatorStatusIndexKey      = MustVersionedStorePrefix("unbonding_by_operator_status", CurrentStoreSchemaVersion)
	UnbondingReceiptKey                    = MustVersionedStorePrefix("unbonding_receipt", CurrentStoreSchemaVersion)
	UnbondingReceiptPruneIndexKey          = MustVersionedStorePrefix("unbonding_receipt_prune", CurrentStoreSchemaVersion)
	ServiceDescriptorKey                   = MustVersionedStorePrefix("service_descriptor", CurrentStoreSchemaVersion)
	CurrentServiceAddressIndexKey          = MustVersionedStorePrefix("current_service_address", CurrentStoreSchemaVersion)
	TaskLiabilityReservationKey            = MustVersionedStorePrefix("task_liability", CurrentStoreSchemaVersion)
	ActiveLiabilityByOperatorIndexKey      = MustVersionedStorePrefix("active_liability_by_operator", CurrentStoreSchemaVersion)
	TaskLiabilityByTaskIndexKey            = MustVersionedStorePrefix("task_liability_by_task", CurrentStoreSchemaVersion)
	ServiceKeyResponsibilityKey            = MustVersionedStorePrefix("service_key_responsibility", CurrentStoreSchemaVersion)
	ServiceKeyResponsibilityByTaskIndexKey = MustVersionedStorePrefix("service_key_responsibility_by_task", CurrentStoreSchemaVersion)
	ProfileCapabilityKey                   = MustVersionedStorePrefix("profile_capability", CurrentStoreSchemaVersion)
	ModelSupportKey                        = MustVersionedStorePrefix("model_support", CurrentStoreSchemaVersion)
	ModelSupportExpiryIndexKey             = MustVersionedStorePrefix("model_support_expiry", CurrentStoreSchemaVersion)
	ModelSupportPruneIndexKey              = MustVersionedStorePrefix("model_support_prune", CurrentStoreSchemaVersion)
	ModelSupportByProfileIndexKey          = MustVersionedStorePrefix("model_support_by_profile", CurrentStoreSchemaVersion)
	ModelSupportByOperatorIndexKey         = MustVersionedStorePrefix("model_support_by_operator", CurrentStoreSchemaVersion)
	DailySupportStateKey                   = MustVersionedStorePrefix("daily_support", CurrentStoreSchemaVersion)
	DailySupportExpiryIndexKey             = MustVersionedStorePrefix("daily_support_expiry", CurrentStoreSchemaVersion)
	// SupportDeactivateCursorKey backs GenesisState.support_deactivate_cursors
	// (field 19). One row per profile whose governance/freeze status change still
	// owes a bounded supporter fan-out; the EndBlock cursor deletes it on DONE.
	SupportDeactivateCursorKey = MustVersionedStorePrefix("support_deactivate_cursor", CurrentStoreSchemaVersion)

	// ---- Global epoch stable-slot CandidatePool (keeper_data_structure_contract.md §3.2) ----
	//
	// The six PR #88 per-(model, profile_version, duty) prefixes
	// (candidate_pool_snapshot as an inline candidate vector, candidate_pool_current,
	// candidate_pool_dirty, candidate_pool_overflow and the two height indexes keyed
	// by that triple) are deleted, not migrated: §3.2 replaces them with one global
	// pool per epoch. Fresh genesis, so no compatibility prefix is retained.
	//
	// The versioned form matters most here: the raw literal "candidate_pool"
	// would be a byte prefix of "candidate_pool_member", which SchemaBuilder.Build
	// rejects outright (see the layout note at the top of this var block).
	CandidatePoolSnapshotKey             = MustVersionedStorePrefix("candidate_pool_snapshot", CurrentStoreSchemaVersion)
	CandidatePoolActiveSegmentKey        = MustVersionedStorePrefix("candidate_pool_active_segment", CurrentStoreSchemaVersion)
	CandidatePoolMemberKey               = MustVersionedStorePrefix("candidate_pool_member", CurrentStoreSchemaVersion)
	CandidateSlotCurrentKey              = MustVersionedStorePrefix("candidate_slot_current", CurrentStoreSchemaVersion)
	CandidateSlotBindingKey              = MustVersionedStorePrefix("candidate_slot_binding", CurrentStoreSchemaVersion)
	OperatorCandidateSlotKey             = MustVersionedStorePrefix("operator_candidate_slot", CurrentStoreSchemaVersion)
	CandidatePoolBuildCursorKey          = MustVersionedStorePrefix("candidate_pool_build_cursor", CurrentStoreSchemaVersion)
	CandidatePoolTaskRefKey              = MustVersionedStorePrefix("candidate_pool_task_ref", CurrentStoreSchemaVersion)
	CandidatePoolExpiryIndexKey          = MustVersionedStorePrefix("candidate_pool_expiry", CurrentStoreSchemaVersion)
	CandidatePoolPruneIndexKey           = MustVersionedStorePrefix("candidate_pool_prune", CurrentStoreSchemaVersion)
	CandidateSlotBindingPruneIndexKey    = MustVersionedStorePrefix("candidate_slot_binding_prune", CurrentStoreSchemaVersion)
	CandidatePoolBuildStatusSingletonKey = MustVersionedStorePrefix("candidate_pool_build_status", CurrentStoreSchemaVersion)
	CurrentCandidatePoolSingletonKey     = MustVersionedStorePrefix("current_candidate_pool", CurrentStoreSchemaVersion)

	BuilderStateKey                      = MustVersionedStorePrefix("builder_state", CurrentStoreSchemaVersion)
	BuilderAdmissionStateKey             = MustVersionedStorePrefix("builder_admission", CurrentStoreSchemaVersion)
	CurrentBuilderSetStateKey            = MustVersionedStorePrefix("current_builder_set", CurrentStoreSchemaVersion)
	PendingBuilderSetReplacementStateKey = MustVersionedStorePrefix("pending_builder_set_replacement", CurrentStoreSchemaVersion)
	BuilderSetStateKey                   = MustVersionedStorePrefix("builder_set", CurrentStoreSchemaVersion)
	BuilderSetReplacementIndexKey        = MustVersionedStorePrefix("idx_builder_set_replacement", CurrentStoreSchemaVersion)
	BuilderSetByIDIndexKey               = MustVersionedStorePrefix("idx_builder_set_id", CurrentStoreSchemaVersion)
	BuilderSetByHeightIndexKey           = MustVersionedStorePrefix("idx_builder_set_height", CurrentStoreSchemaVersion)
	BuilderSetTaskRefKey                 = MustVersionedStorePrefix("builder_set_task_ref", CurrentStoreSchemaVersion)
	BuilderSetPruneIndexKey              = MustVersionedStorePrefix("builder_set_prune", CurrentStoreSchemaVersion)
	BuilderFaultKey                      = MustVersionedStorePrefix("builder_fault", CurrentStoreSchemaVersion)
	BuilderFaultPruneIndexKey            = MustVersionedStorePrefix("builder_fault_prune", CurrentStoreSchemaVersion)
	ServiceBondEffectiveIndexKey         = MustVersionedStorePrefix("service_bond_effective", CurrentStoreSchemaVersion)

	// There is no jail or tombstone prefix either: both are operator-global
	// counters on ServiceBondState (keeper_data_structure_contract.md §6.4).
	RoleFaultKey           = MustVersionedStorePrefix("role_fault", CurrentStoreSchemaVersion)
	RoleFaultPruneIndexKey = MustVersionedStorePrefix("role_fault_prune", CurrentStoreSchemaVersion)
	// RoleFaultByTaskIndexKey is the (task_id, fault_id) lookup direction §6.6's
	// fault_summary_hash needs. Without it the only way to read a task's fault
	// vector is a full RoleFault scan, which cannot run in a consensus path.
	RoleFaultByTaskIndexKey            = MustVersionedStorePrefix("role_fault_by_task", CurrentStoreSchemaVersion)
	SlashSummaryKey                    = MustVersionedStorePrefix("slash_summary", CurrentStoreSchemaVersion)
	TreasuryStateKey                   = MustVersionedStorePrefix("treasury_state", CurrentStoreSchemaVersion)
	TreasurySpendReceiptKey            = MustVersionedStorePrefix("treasury_spend_receipt", CurrentStoreSchemaVersion)
	TreasurySpendReceiptPruneIndexKey  = MustVersionedStorePrefix("treasury_spend_receipt_prune", CurrentStoreSchemaVersion)
	TreasurySpendProposalKey           = MustVersionedStorePrefix("treasury_spend_proposal", CurrentStoreSchemaVersion)
	TreasurySpendEpochKey              = MustVersionedStorePrefix("treasury_spend_epoch", CurrentStoreSchemaVersion)
	TreasurySpendRecipientEpochKey     = MustVersionedStorePrefix("treasury_spend_recipient_epoch", CurrentStoreSchemaVersion)
	TreasurySpendEpochCleanupCursorKey = MustVersionedStorePrefix("treasury_spend_epoch_cleanup_cursor", CurrentStoreSchemaVersion)
	TreasurySpendEpochCleanupIndexKey  = MustVersionedStorePrefix("treasury_spend_epoch_cleanup_index", CurrentStoreSchemaVersion)

	// BuilderRewardEpochKey and TreasuryEpochKey used to sit here. They were
	// registered with no SchemaBuilder and had zero readers or writers anywhere in
	// the repo, so they were the only two Hub prefixes outside the SDK's
	// overlap guard entirely. Deleted rather than versioned.
	RewardP30CutoffPointerKey        = MustVersionedStorePrefix("reward_p30_cutoff_pointer", CurrentStoreSchemaVersion)
	RewardEpochCursorKey             = MustVersionedStorePrefix("reward_epoch_cursor", CurrentStoreSchemaVersion)
	RewardEpochIndexKey              = MustVersionedStorePrefix("reward_epoch_index", CurrentStoreSchemaVersion)
	RewardEpochPruneIndexKey         = MustVersionedStorePrefix("reward_epoch_prune_index", CurrentStoreSchemaVersion)
	RewardEpochPruneCursorKey        = MustVersionedStorePrefix("reward_epoch_prune_cursor", CurrentStoreSchemaVersion)
	RewardEpochAuditKey              = MustVersionedStorePrefix("reward_epoch_audit", CurrentStoreSchemaVersion)
	EarningsKey                      = MustVersionedStorePrefix("econ_earnings_state", CurrentStoreSchemaVersion)
	ParameterBucketVersionKey        = MustVersionedStorePrefix("parameter_bucket_version", CurrentStoreSchemaVersion)
	ParameterBucketCurrentPointerKey = MustVersionedStorePrefix("parameter_bucket_current_pointer", CurrentStoreSchemaVersion)
	ParameterBucketPendingPointerKey = MustVersionedStorePrefix("parameter_bucket_pending_pointer", CurrentStoreSchemaVersion)
	ParameterBucketEffectiveIndexKey = MustVersionedStorePrefix("idx_parameter_bucket_effective", CurrentStoreSchemaVersion)
	ParameterBucketPruneIndexKey     = MustVersionedStorePrefix("idx_parameter_bucket_prune", CurrentStoreSchemaVersion)
	HardwareTierCompetitionEpochKey  = MustVersionedStorePrefix("hw_tier_epoch", CurrentStoreSchemaVersion)

	BeaconStateKey                   = MustVersionedStorePrefix("beacon_state", CurrentStoreSchemaVersion)
	BeaconPruneIndexKey              = MustVersionedStorePrefix("beacon_prune", CurrentStoreSchemaVersion)
	BeaconCheckpointStateKey         = MustVersionedStorePrefix("beacon_checkpoint", CurrentStoreSchemaVersion)
	BeaconCheckpointCursorKey        = MustVersionedStorePrefix("beacon_checkpoint_cursor", CurrentStoreSchemaVersion)
	BeaconConsumerRefStoreKey        = MustVersionedStorePrefix("beacon_consumer_ref", CurrentStoreSchemaVersion)
	FreezeSignalStateKey             = MustVersionedStorePrefix("freeze_signal_state", CurrentStoreSchemaVersion)
	FreezeSignalBuildCursorStoreKey  = MustVersionedStorePrefix("freeze_signal_build_cursor", CurrentStoreSchemaVersion)
	FreezeSignalByWindowIndexKey     = MustVersionedStorePrefix("freeze_signal_by_window", CurrentStoreSchemaVersion)
	FreezeSignalByProfileIndexKey    = MustVersionedStorePrefix("freeze_signal_by_profile", CurrentStoreSchemaVersion)
	FreezeRiskWindowScheduleIndexKey = MustVersionedStorePrefix("freeze_risk_window_schedule", CurrentStoreSchemaVersion)
	FreezeSignalDeadlineIndexKey     = MustVersionedStorePrefix("freeze_signal_deadline_index", CurrentStoreSchemaVersion)
	FreezeSignalPruneIndexKey        = MustVersionedStorePrefix("freeze_signal_prune", CurrentStoreSchemaVersion)
	EmergencyFreezeVoteStateKey      = MustVersionedStorePrefix("emergency_freeze_vote_state", CurrentStoreSchemaVersion)
)

// cross_chain_asset_bridge_protocol.md §3.2 forbids mirroring Hyperlane state; every prefix below
// holds TrueOpen's own guard, governance and supply-audit rows only. The upstream
// modules keep their own stores and remain their own owner.
var (
	ValidatorBridgeSignerKey   = MustVersionedStorePrefix("validator_bridge_signer", CurrentStoreSchemaVersion)
	BridgeRouteStateKey        = MustVersionedStorePrefix("bridge_route", CurrentStoreSchemaVersion)
	BridgeSignerSetStateKey    = MustVersionedStorePrefix("bridge_signer_set", CurrentStoreSchemaVersion)
	BridgeControlStateKey      = MustVersionedStorePrefix("bridge_control", CurrentStoreSchemaVersion)
	BridgeCutoverStateKey      = MustVersionedStorePrefix("bridge_cutover", CurrentStoreSchemaVersion)
	BridgeLimitStateKey        = MustVersionedStorePrefix("bridge_limit", CurrentStoreSchemaVersion)
	BridgePendingLimitStateKey = MustVersionedStorePrefix("bridge_pending_limit", CurrentStoreSchemaVersion)
	BridgeEpochUsageStateKey   = MustVersionedStorePrefix("bridge_epoch_usage", CurrentStoreSchemaVersion)
	BridgeEpochUsagePruneKey   = MustVersionedStorePrefix("bridge_epoch_usage_prune", CurrentStoreSchemaVersion)
	BridgeSupplyStateKey       = MustVersionedStorePrefix("bridge_supply", CurrentStoreSchemaVersion)
	BridgeBootstrapStateKey    = MustVersionedStorePrefix("bridge_bootstrap", CurrentStoreSchemaVersion)
)

// BridgeEpochUsagePruneKeyPair = (prune_epoch, reward_epoch) per
// keeper_data_structure_contract.md §6.6a. The due epoch leads so EndBlock can read
// "everything due by epoch E" as one bounded ascending range.
type BridgeEpochUsagePruneKeyPair = collections.Pair[uint64, uint64]

func NewBridgeEpochUsagePruneKey(pruneEpoch, rewardEpoch uint64) BridgeEpochUsagePruneKeyPair {
	return collections.Join(pruneEpoch, rewardEpoch)
}

type BeaconPruneKey = collections.Pair[uint64, uint64]

func NewBeaconPruneKey(pruneHeight, beaconHeight uint64) BeaconPruneKey {
	return collections.Join(pruneHeight, beaconHeight)
}

type BeaconConsumerRefKey = collections.Triple[uint64, uint32, string]

func NewBeaconConsumerRefKey(height uint64, kind BeaconConsumerKind, consumerID string) BeaconConsumerRefKey {
	return collections.Join3(height, uint32(kind), consumerID)
}

type FreezeSignalBuildCursorKey = collections.Pair[string, uint32]

func NewFreezeSignalBuildCursorKey(modelID string, profileVersion uint32) FreezeSignalBuildCursorKey {
	return collections.Join(modelID, profileVersion)
}

type FreezeSignalByWindowKey = collections.Triple[string, uint32, uint64]

func NewFreezeSignalByWindowKey(modelID string, profileVersion uint32, riskWindowID uint64) FreezeSignalByWindowKey {
	return collections.Join3(modelID, profileVersion, riskWindowID)
}

// freeze_signal_id is a raw Hash32. In this key it sits in the terminal position
// of the terminal pair, where collections.BytesKey already wrote the bytes with no
// length prefix -- so the encoding is byte-identical and existing page tokens for
// FreezeSignals stay valid. The change buys fail-closed width typing, not bytes.
type FreezeSignalWindowOrderKey = collections.Pair[uint64, shared.Hash32Key]
type FreezeSignalByProfileKey = collections.Quad[string, uint32, int32, FreezeSignalWindowOrderKey]

func NewFreezeSignalByProfileKey(modelID string, profileVersion uint32, signalStatus FreezeSignalStatus, riskWindowEndHeight uint64, signalID shared.Hash32Key) FreezeSignalByProfileKey {
	return collections.Join4(modelID, profileVersion, int32(signalStatus), collections.Join(riskWindowEndHeight, append(shared.Hash32Key(nil), signalID...)))
}

type BuilderSetByHeightKey = collections.Pair[uint64, uint64]

func NewBuilderSetByHeightKey(effectiveHeight, version uint64) BuilderSetByHeightKey {
	return collections.Join(effectiveHeight, version)
}

type BuilderSetTaskRefKeyPair = collections.Pair[shared.Hash32Key, uint64]

func NewBuilderSetTaskRefKey(taskID shared.Hash32Key, version uint64) BuilderSetTaskRefKeyPair {
	return collections.Join(taskID, version)
}

// BuilderSetPruneKeyTriple = (prune_epoch, builder_set_id, phase), the exact
// component order keeper_data_structure_contract.md registers (D-24). The previous key put a
// due *height* in K1; the schedule is denominated in retention *epochs*, so the
// height was a derived value baked into a consensus key and the index could not
// be read as "everything due by epoch E" without knowing epoch_length_blocks.
// The epoch -> height conversion now happens in exactly one place,
// firstDueBuilderSetPrune.
//
// builder_set_id is the term id. §6.5 gives a builder set one identity and the
// task-ref direction spells it as the decimal string of term_id
// (NewBuilderSetTaskRefKey); this index keeps the uint64 so that K1/K2 stay
// numerically ordered, which is what makes "oldest due first" a single
// ascending iteration.
//
// Fresh genesis: the old layout has no migration and no compatibility prefix.
// The index is derived state - never exported, rebuilt from the imported
// snapshots by rebuildBuilderSetPruneIndex - so an existing chain would in any
// case re-derive it at import rather than carry the old bytes forward.
type BuilderSetPruneKeyTriple = collections.Triple[uint64, uint64, uint32]

func NewBuilderSetPruneKey(pruneEpoch, version uint64, phase BuilderSetPrunePhase) BuilderSetPruneKeyTriple {
	return collections.Join3(pruneEpoch, version, uint32(phase))
}

type BuilderSetReplacementKeyPair = collections.Pair[uint64, uint64]

func NewBuilderSetReplacementKey(effectiveHeight, version uint64) BuilderSetReplacementKeyPair {
	return collections.Join(effectiveHeight, version)
}

type FreezeRiskWindowScheduleKey = collections.Triple[uint64, string, uint32]

func NewFreezeRiskWindowScheduleKey(nextWindowCloseHeight uint64, modelID string, profileVersion uint32) FreezeRiskWindowScheduleKey {
	return collections.Join3(nextWindowCloseHeight, modelID, profileVersion)
}

// Terminal freeze_signal_id: BytesKey already wrote it prefix-free, so this key's
// bytes do not change -- only its typing does.
type FreezeSignalDeadlineKey = collections.Pair[uint64, shared.Hash32Key]

func NewFreezeSignalDeadlineKey(voteDeadlineHeight uint64, freezeSignalID shared.Hash32Key) FreezeSignalDeadlineKey {
	return collections.Join(voteDeadlineHeight, append(shared.Hash32Key(nil), freezeSignalID...))
}

// Non-terminal freeze_signal_id: this one loses BytesKey's one-byte length prefix,
// so the encoded key changes. It carries no page token, only the prune sweep.
type FreezeSignalPruneKey = collections.Triple[uint64, shared.Hash32Key, int32]

func NewFreezeSignalPruneKey(pruneHeight uint64, freezeSignalID shared.Hash32Key, phase FreezeSignalPrunePhase) FreezeSignalPruneKey {
	return collections.Join3(pruneHeight, append(shared.Hash32Key(nil), freezeSignalID...), int32(phase))
}

// RewardBucketKeyPart is the leading reward_bucket component of
// HardwareTierEpochKey. It is a defined type rather than an alias so that the
// bucket-first competition key is a *different* Go type from the epoch-first
// RewardEpochCursorKeyPair. Both used to be collections.Pair[uint64, uint64],
// which let a cursor/audit key be handed to the competition map — and the
// reverse — without the compiler noticing, while the probe silently asked about
// a row that can never exist. Encoding delegates to the same generic big-endian
// uint64 key codec (RewardBucketKeyCodec), so no stored byte changes.
type RewardBucketKeyPart uint64

// RewardBucketKeyCodec encodes RewardBucketKeyPart exactly as
// collections.Uint64Key encodes uint64: the generic codec is instantiated on
// ~uint64, so the key bytes are byte-identical to the previous layout.
//
// KNOWN LIMITATION, dormant and non-consensus: uint64Key does not implement
// codec.HasSchemaCodec, so codec.KeySchemaCodec falls back to
// FallbackSchemaCodec, whose schema.KindForGoValue type-switch matches concrete
// uint64 but not a defined type — it returns InvalidKind, and the fallback then
// silently describes this component as StringKind carrying JSON instead of
// Uint64Kind. Encode/Decode are untouched, so no consensus byte moves, and
// nothing in this repo calls ModuleCodec/IndexingOptions, so the descriptor is
// unreachable today. The remedy, when the SDK state indexer is switched on, is
// to wrap this codec in a type that embeds codec.KeyCodec[RewardBucketKeyPart]
// and adds SchemaCodec() returning schema.Uint64Kind with uint64 conversions;
// pairKeyCodec.SchemaCodec delegates through codec.KeySchemaCodec, so that is
// enough to restore the descriptor. It is deliberately not done here: it would
// promote cosmossdk.io/schema from an indirect to a direct dependency for a
// field nothing can currently observe, and that PR has to take the dependency
// anyway.
var RewardBucketKeyCodec = codec.NewUint64Key[RewardBucketKeyPart]()

// HardwareTierEpochKey = (reward_bucket, epoch).
type HardwareTierEpochKey = collections.Pair[RewardBucketKeyPart, uint64]

func NewHardwareTierEpochKey(hardwareTier, epoch uint64) HardwareTierEpochKey {
	return collections.Join(RewardBucketKeyPart(hardwareTier), epoch)
}

func NewRewardCompetitionEpochKey(rewardBucket, epoch uint64) HardwareTierEpochKey {
	return NewHardwareTierEpochKey(rewardBucket, epoch)
}

type RewardEpochCursorKeyPair = collections.Pair[uint64, uint64]

func NewRewardEpochCursorKey(epoch, rewardBucket uint64) RewardEpochCursorKeyPair {
	return collections.Join(epoch, rewardBucket)
}

type RewardEpochIndexKeyTriple = collections.Triple[uint64, uint64, uint64]

func NewRewardEpochIndexKey(dueHeight, epoch, rewardBucket uint64) RewardEpochIndexKeyTriple {
	return collections.Join3(dueHeight, epoch, rewardBucket)
}

type RewardEpochPruneIndexKeyTriple = collections.Triple[uint64, uint64, uint64]

func NewRewardEpochPruneIndexKey(pruneEpoch, sourceEpoch, rewardBucket uint64) RewardEpochPruneIndexKeyTriple {
	return collections.Join3(pruneEpoch, sourceEpoch, rewardBucket)
}

type ParameterBucketVersionKeyTriple = collections.Triple[int32, string, uint64]

func NewParameterBucketVersionKey(bucketKind shared.BucketKind, bucketKey string, version uint64) ParameterBucketVersionKeyTriple {
	return collections.Join3(int32(bucketKind), bucketKey, version)
}

type ParameterBucketPointerKeyPair = collections.Pair[int32, string]

func NewParameterBucketPointerKey(bucketKind shared.BucketKind, bucketKey string) ParameterBucketPointerKeyPair {
	return collections.Join(int32(bucketKind), bucketKey)
}

type ParameterBucketEffectiveIndexKeyQuad = collections.Quad[uint64, int32, string, uint64]

func NewParameterBucketEffectiveIndexKey(effectiveHeight uint64, bucketKind shared.BucketKind, bucketKey string, version uint64) ParameterBucketEffectiveIndexKeyQuad {
	return collections.Join4(effectiveHeight, int32(bucketKind), bucketKey, version)
}

type ParameterBucketPruneIndexKeyQuad = collections.Quad[uint64, int32, string, uint64]

func NewParameterBucketPruneIndexKey(pruneHeight uint64, bucketKind shared.BucketKind, bucketKey string, version uint64) ParameterBucketPruneIndexKeyQuad {
	return collections.Join4(pruneHeight, int32(bucketKind), bucketKey, version)
}

func NewServiceBondKey(operatorAddress string) string {
	return operatorAddress
}

// Every unbonding_id, fault_id, responsibility_id and registration digest below is
// a raw Hash32; operator_address and builder_address stay Bech32 text, because a
// raw address codec changes the global Bech32 sort order and needs its own audit.
type UnbondingKeyPair = collections.Pair[string, shared.Hash32Key]

func NewUnbondingKey(operatorAddress string, unbondingID shared.Hash32Key) UnbondingKeyPair {
	return collections.Join(operatorAddress, unbondingID)
}

type UnbondingMaturityIndexKeyTriple = collections.Triple[uint64, string, shared.Hash32Key]

func NewUnbondingMaturityIndexKey(matureHeight uint64, operatorAddress string, unbondingID shared.Hash32Key) UnbondingMaturityIndexKeyTriple {
	return collections.Join3(matureHeight, operatorAddress, unbondingID)
}

type UnbondingByOperatorStatusKey = collections.Quad[string, int32, uint64, shared.Hash32Key]

func NewUnbondingByOperatorStatusKey(operatorAddress string, status UnbondingStatus, matureHeight uint64, unbondingID shared.Hash32Key) UnbondingByOperatorStatusKey {
	return collections.Join4(operatorAddress, int32(status), matureHeight, unbondingID)
}

type UnbondingReceiptPruneKey = collections.Pair[uint64, shared.Hash32Key]

func NewUnbondingReceiptPruneKey(pruneHeight uint64, unbondingID shared.Hash32Key) UnbondingReceiptPruneKey {
	return collections.Join(pruneHeight, unbondingID)
}

type ParticipantKeyPair = collections.Pair[int32, string]

func NewParticipantKey(participantType shared.ParticipantType, operatorAddress string) ParticipantKeyPair {
	return collections.Join(int32(participantType), operatorAddress)
}

type CurrentServiceAddressIndexKeyPair = collections.Pair[int32, string]

func NewCurrentServiceAddressIndexKey(participantType shared.ParticipantType, serviceAddress string) CurrentServiceAddressIndexKeyPair {
	return collections.Join(int32(participantType), serviceAddress)
}

// The three task-liability keyspaces carry a raw Hash32 task_id. operator_address
// stays Bech32 text: a raw address codec changes the global Bech32 sort order and
// needs its own iterator/cursor audit, so it is deliberately not folded in here.
type TaskLiabilityReservationKeyTriple = collections.Triple[shared.Hash32Key, int32, string]

func NewTaskLiabilityReservationKey(taskID shared.Hash32Key, duty shared.Duty, operatorAddress string) TaskLiabilityReservationKeyTriple {
	return collections.Join3(taskID, int32(duty), operatorAddress)
}

type ActiveLiabilityByOperatorKey = collections.Triple[string, shared.Hash32Key, int32]

func NewActiveLiabilityByOperatorKey(operatorAddress string, taskID shared.Hash32Key, duty shared.Duty) ActiveLiabilityByOperatorKey {
	return collections.Join3(operatorAddress, taskID, int32(duty))
}

type TaskLiabilityByTaskKey = collections.Triple[shared.Hash32Key, int32, string]

func NewTaskLiabilityByTaskKey(taskID shared.Hash32Key, duty shared.Duty, operatorAddress string) TaskLiabilityByTaskKey {
	return collections.Join3(taskID, int32(duty), operatorAddress)
}

// ServiceKeyResponsibilityKeyTriple = (participant_type, operator_address,
// responsibility_id). participant_type is int32(ParticipantType), matching every
// other participant-scoped Hub key (NewParticipantKey,
// NewCurrentServiceAddressIndexKey). It used to be the participant-type *name*,
// which put the string "CORTEX_NODE" into a consensus key even though §9.6b's
// frozen enum name is CORTEX - a spelling the contract does not define, and one
// only the Keeper's own name/parse pair agreed on (A-15a).
//
// This is not a genesis or wire change. ServiceKeyResponsibilityState already
// carries the typed ParticipantType, ExportGenesis exports values only, and
// InitGenesis rebuilds both the primary key and the by-task index from
// state.ParticipantType - so the name never crossed the genesis boundary and
// there is nothing to migrate.
type ServiceKeyResponsibilityKeyTriple = collections.Triple[int32, string, shared.Hash32Key]

func NewServiceKeyResponsibilityKey(participantType shared.ParticipantType, operatorAddress string, responsibilityID shared.Hash32Key) ServiceKeyResponsibilityKeyTriple {
	return collections.Join3(int32(participantType), operatorAddress, responsibilityID)
}

// ServiceKeyResponsibilityByTaskSuffix is byte-identical to the primary key so
// that the (session_id, task_id) index can hand its suffix straight back to
// NewServiceKeyResponsibilityKey.
type ServiceKeyResponsibilityByTaskSuffix = ServiceKeyResponsibilityKeyTriple

// session_id and task_id stay text here on purpose: ServiceKeyResponsibilityState
// still declares both as proto `string` (participant_identity.proto:85-86), so a
// raw key would have to decode them at every read and write -- adding a bridge
// instead of deleting one. They can follow once those two fields are retyped to
// bytes, which is a proto change and belongs with the P2 batch, not here.
// responsibility_id inside the suffix is already `bytes`, so it migrates.
type ServiceKeyResponsibilityByTaskKeyTriple = collections.Triple[string, string, ServiceKeyResponsibilityByTaskSuffix]

func NewServiceKeyResponsibilityByTaskKey(sessionID, taskID string, participantType shared.ParticipantType, operatorAddress string, responsibilityID shared.Hash32Key) ServiceKeyResponsibilityByTaskKeyTriple {
	return collections.Join3(sessionID, taskID, NewServiceKeyResponsibilityKey(participantType, operatorAddress, responsibilityID))
}

type ProfileCapabilityKeyTriple = collections.Triple[string, string, uint32]

func NewProfileCapabilityKey(operatorAddress, modelID string, profileVersion uint32) ProfileCapabilityKeyTriple {
	return collections.Join3(operatorAddress, modelID, profileVersion)
}

type ModelSupportKeyTriple = collections.Triple[string, string, uint32]

func NewModelSupportKey(operatorAddress, modelID string, profileVersion uint32) ModelSupportKeyTriple {
	return collections.Join3(operatorAddress, modelID, profileVersion)
}

type ModelSupportExpiryIndexKeyPair = collections.Pair[uint64, ModelSupportKeyTriple]

func NewModelSupportExpiryIndexKey(expireEpoch uint64, operatorAddress, modelID string, profileVersion uint32) ModelSupportExpiryIndexKeyPair {
	return collections.Join(expireEpoch, NewModelSupportKey(operatorAddress, modelID, profileVersion))
}

type ModelSupportPruneIndexKeyPair = collections.Pair[uint64, ModelSupportKeyTriple]

func NewModelSupportPruneIndexKey(pruneEpoch uint64, operatorAddress, modelID string, profileVersion uint32) ModelSupportPruneIndexKeyPair {
	return collections.Join(pruneEpoch, NewModelSupportKey(operatorAddress, modelID, profileVersion))
}

type ModelSupportByProfileIndexKeyTriple = collections.Triple[string, uint32, string]

func NewModelSupportByProfileIndexKey(modelID string, profileVersion uint32, operatorAddress string) ModelSupportByProfileIndexKeyTriple {
	return collections.Join3(modelID, profileVersion, operatorAddress)
}

type ModelSupportByOperatorIndexKeyTriple = collections.Triple[string, string, uint32]

func NewModelSupportByOperatorIndexKey(operatorAddress, modelID string, profileVersion uint32) ModelSupportByOperatorIndexKeyTriple {
	return collections.Join3(operatorAddress, modelID, profileVersion)
}

type DailySupportKey = collections.Pair[uint64, string]

func NewDailySupportKey(epoch uint64, operatorAddress string) DailySupportKey {
	return collections.Join(epoch, operatorAddress)
}

// DailySupportExpiryIndexKeyTriple is (expiry_epoch, operator_address,
// support_epoch), the exact component order frozen by
// keeper_data_structure_contract.md §6.1. The previous
// (expiry_epoch, support_epoch, operator_address) order made the index unusable
// for an operator-scoped bounded prefix scan.
type DailySupportExpiryIndexKeyTriple = collections.Triple[uint64, string, uint64]

func NewDailySupportExpiryIndexKey(expiryEpoch uint64, operatorAddress string, supportEpoch uint64) DailySupportExpiryIndexKeyTriple {
	return collections.Join3(expiryEpoch, operatorAddress, supportEpoch)
}

// ---- Global CandidatePool key types (keeper_data_structure_contract.md §3.2) ----
//
// Hash32 key components (snapshot_id, task_id) are lowercase 64-hex strings, the
// same wire form the rest of the Hub store already uses for Hash32 keys; the raw
// 32 bytes only exist inside the value rows and inside the §1.2 hash framing.
//
// CandidateSlotBindingKeyPair = (slot, slot_version). The binding is immutable
// identity history, so slot_version must stay in the key.
type CandidateSlotBindingKeyPair = collections.Pair[uint32, uint64]

func NewCandidateSlotBindingKey(slot uint32, slotVersion uint64) CandidateSlotBindingKeyPair {
	return collections.Join(slot, slotVersion)
}

// CandidatePoolSegmentKeyPair = (epoch, segment_index).
type CandidatePoolSegmentKeyPair = collections.Pair[uint64, uint32]

func NewCandidatePoolSegmentKey(epoch uint64, segmentIndex uint32) CandidatePoolSegmentKeyPair {
	return collections.Join(epoch, segmentIndex)
}

// CandidatePoolMemberKeyPair = (epoch, slot). Ascending slot order inside one
// epoch is exactly the §3.5 hash order, so a prefix scan is the canonical read.
type CandidatePoolMemberKeyPair = collections.Pair[uint64, uint32]

func NewCandidatePoolMemberKey(epoch uint64, slot uint32) CandidatePoolMemberKeyPair {
	return collections.Join(epoch, slot)
}

// CandidatePoolTaskRefKeyPair = (task_id, snapshot_id). §3.3 makes this row the
// idempotent per-task reference; the task_id-first order is what lets cleanup
// release a task's refs with a bounded prefix scan.
//
// Both components are raw Hash32. The bounded prefix scan is unaffected: lower
// hex and raw memcmp order identically (TestHash32RawAndLowerHexHaveIdenticalOrder),
// and both components were fixed width before the retype as well.
type CandidatePoolTaskRefKeyPair = collections.Pair[shared.Hash32Key, shared.Hash32Key]

func NewCandidatePoolTaskRefKey(taskID, snapshotID shared.Hash32Key) CandidatePoolTaskRefKeyPair {
	return collections.Join(taskID, snapshotID)
}

// CandidatePoolExpiryIndexKeyPair = (expires_height, snapshot_id).
type CandidatePoolExpiryIndexKeyPair = collections.Pair[uint64, shared.Hash32Key]

func NewCandidatePoolExpiryIndexKey(expiresHeight uint64, snapshotID shared.Hash32Key) CandidatePoolExpiryIndexKeyPair {
	return collections.Join(expiresHeight, snapshotID)
}

// CandidatePoolPruneIndexKeyTriple = (prune_height, snapshot_id, phase). §3.2
// puts the BODY/HEADER phase in the key so the two prune stages of one snapshot
// are separate due rows instead of a mutable status field.
type CandidatePoolPruneIndexKeyTriple = collections.Triple[uint64, shared.Hash32Key, int32]

func NewCandidatePoolPruneIndexKey(pruneHeight uint64, snapshotID shared.Hash32Key, phase CandidatePoolPrunePhase) CandidatePoolPruneIndexKeyTriple {
	return collections.Join3(pruneHeight, snapshotID, int32(phase))
}

// CandidateSlotBindingPruneIndexKeyTriple = (prune_epoch, slot, slot_version).
type CandidateSlotBindingPruneIndexKeyTriple = collections.Triple[uint64, uint32, uint64]

func NewCandidateSlotBindingPruneIndexKey(pruneEpoch uint64, slot uint32, slotVersion uint64) CandidateSlotBindingPruneIndexKeyTriple {
	return collections.Join3(pruneEpoch, slot, slotVersion)
}

type BuilderFaultKeyPair = collections.Pair[string, shared.Hash32Key]

func NewBuilderFaultKey(builderAddress string, faultID shared.Hash32Key) BuilderFaultKeyPair {
	return collections.Join(builderAddress, faultID)
}

type BuilderFaultPruneKeyTriple = collections.Triple[uint64, string, shared.Hash32Key]

func NewBuilderFaultPruneKey(pruneHeight uint64, builderAddress string, faultID shared.Hash32Key) BuilderFaultPruneKeyTriple {
	return collections.Join3(pruneHeight, builderAddress, faultID)
}

func NewRoleFaultKey(faultID shared.Hash32Key) shared.Hash32Key {
	return faultID
}

// source_id is a raw Hash32 -- either a role fault_id or a challenge_id. The
// TrimSpace this used to apply was a text-key defence that a fixed-width codec
// makes unrepresentable: a 32-byte key cannot carry surrounding whitespace.
type SlashSummaryKeyTriple = collections.Triple[int32, shared.Hash32Key, uint64]

func NewSlashSummaryKey(sourceKind SlashSourceKind, sourceID shared.Hash32Key, effectIndex uint64) SlashSummaryKeyTriple {
	return collections.Join3(int32(sourceKind), sourceID, effectIndex)
}

type RoleFaultPruneKey = collections.Pair[uint64, shared.Hash32Key]

func NewRoleFaultPruneKey(pruneHeight uint64, faultID shared.Hash32Key) RoleFaultPruneKey {
	return collections.Join(pruneHeight, faultID)
}

// RoleFaultByTaskKey is (task_id, fault_id), both raw Hash32. RoleFaultState is keyed by
// fault_id alone, so this is the only bounded way to read one task's fault
// vector; §6.6's fault_summary_hash is computed inside a settlement transition
// and may not scan the whole RoleFault map to find its own faults.
type RoleFaultByTaskKey = collections.Pair[shared.Hash32Key, shared.Hash32Key]

func NewRoleFaultByTaskKey(taskID, faultID shared.Hash32Key) RoleFaultByTaskKey {
	return collections.Join(taskID, faultID)
}

type TreasurySpendReceiptKeyPair = collections.Pair[uint64, uint32]

func NewTreasurySpendReceiptKey(proposalID uint64, itemIndex uint32) TreasurySpendReceiptKeyPair {
	return collections.Join(proposalID, itemIndex)
}

type TreasurySpendReceiptPruneKeyTriple = collections.Triple[uint64, uint64, uint32]

func NewTreasurySpendReceiptPruneKey(pruneHeight, proposalID uint64, itemIndex uint32) TreasurySpendReceiptPruneKeyTriple {
	return collections.Join3(pruneHeight, proposalID, itemIndex)
}

type TreasurySpendRecipientEpochKeyPair = collections.Pair[uint64, []byte]

func NewTreasurySpendRecipientEpochKey(rewardEpoch uint64, recipientAddress []byte) TreasurySpendRecipientEpochKeyPair {
	return collections.Join(rewardEpoch, append([]byte(nil), recipientAddress...))
}

type TreasurySpendEpochCleanupIndexKeyPair = collections.Pair[uint64, uint64]

func NewTreasurySpendEpochCleanupIndexKey(cleanupHeight, rewardEpoch uint64) TreasurySpendEpochCleanupIndexKeyPair {
	return collections.Join(cleanupHeight, rewardEpoch)
}

// freeze_signal_id is a raw Hash32 in the NON-terminal position, where
// collections.BytesKey wrote a one-byte length prefix. Dropping that prefix is a
// one-byte saving per row and, unlike FreezeSignalByProfileIndex above, it does
// change the encoded key -- so EmergencyFreezeVotes page tokens minted before this
// change are rejected. validator_consensus_address stays BytesKey: it is a 20-byte
// consensus address, not a digest.
type EmergencyFreezeVoteKey = collections.Pair[shared.Hash32Key, []byte]

func NewEmergencyFreezeVoteKey(freezeSignalID shared.Hash32Key, validatorConsensusAddress []byte) EmergencyFreezeVoteKey {
	return collections.Join(append(shared.Hash32Key(nil), freezeSignalID...), append([]byte(nil), validatorConsensusAddress...))
}

const (
	FreezeQuorumNumerator   = uint64(2)
	FreezeQuorumDenominator = uint64(3)
)

const ServiceBondEffectiveEpochDelay = uint64(1)

type ServiceBondEffectiveKeyPair = collections.Pair[uint64, string]

func NewServiceBondEffectiveKey(effectiveEpoch uint64, operatorAddress string) ServiceBondEffectiveKeyPair {
	return collections.Join(effectiveEpoch, strings.TrimSpace(operatorAddress))
}

const (
	BeaconSourcePlaceholderBlockHashV1 = "placeholder_blockhash_v1"
	BeaconSourceProposerVRFV1          = "proposer_vrf_v1"
)

const (
	ServiceBondRoleWorker   = "WORKER"
	ServiceBondRoleVerifier = "VERIFIER"
)

// Slash rates use basis points; the numerator is governed in ServiceParamsV1.
const ObjectiveForgerySlashDenom = uint64(10_000)

const (
	ServiceProviderStatusRegistered = "REGISTERED"
	ServiceProviderStatusActive     = "ACTIVE"
	ServiceProviderStatusStale      = "STALE"
	ServiceProviderStatusUnbonding  = "UNBONDING"
	ServiceProviderStatusExited     = "EXITED"
	ServiceProviderStatusTombstoned = "TOMBSTONED"
)

const (
	ServiceKeyStatusActive      = ServiceKeyStatus_SERVICE_KEY_STATUS_ACTIVE
	ServiceKeyStatusRevoked     = ServiceKeyStatus_SERVICE_KEY_STATUS_REVOKED
	TaskLiabilityStatusReserved = LiabilityStatus_LIABILITY_STATUS_RESERVED
	TaskLiabilityStatusReleased = LiabilityStatus_LIABILITY_STATUS_RELEASED
	TaskLiabilityStatusSlashed  = LiabilityStatus_LIABILITY_STATUS_SLASHED
)

const (
	ServiceBondStatusRegistered = ServiceBondStatus_SERVICE_BOND_STATUS_REGISTERED
	ServiceBondStatusActive     = ServiceBondStatus_SERVICE_BOND_STATUS_ACTIVE
	ServiceBondStatusJailed     = ServiceBondStatus_SERVICE_BOND_STATUS_JAILED
	ServiceBondStatusUnbonding  = ServiceBondStatus_SERVICE_BOND_STATUS_UNBONDING
	ServiceBondStatusExited     = ServiceBondStatus_SERVICE_BOND_STATUS_EXITED
	ServiceBondStatusTombstoned = ServiceBondStatus_SERVICE_BOND_STATUS_TOMBSTONED
)

const (
	UnbondingStatusOpen   = UnbondingStatus_UNBONDING_STATUS_OPEN
	UnbondingStatusMature = UnbondingStatus_UNBONDING_STATUS_MATURE
)

const (
	ModelSupportStatusDeclared = "DECLARED"
	ModelSupportStatusActive   = "ACTIVE"
	ModelSupportStatusInactive = "INACTIVE"
)

const (
	ModelSupportDeactivateBondBelowMin  = "BOND_BELOW_MIN"
	ModelSupportDeactivateSlashBelowMin = "SLASH_BELOW_MIN"
	ModelSupportDeactivateExpired       = "SUPPORT_EXPIRED"
	ModelSupportDeactivateTombstoned    = "TOMBSTONED"
	ModelSupportDeactivateFrozen        = "PROFILE_FROZEN"
)

const (
	ModelStatusUnspecified     = ModelProfileStatus_MODEL_PROFILE_STATUS_UNSPECIFIED
	ModelStatusRegistered      = ModelProfileStatus_MODEL_PROFILE_STATUS_REGISTERED
	ModelStatusActive          = ModelProfileStatus_MODEL_PROFILE_STATUS_ACTIVE
	ModelStatusFrozen          = ModelProfileStatus_MODEL_PROFILE_STATUS_FROZEN
	ModelStatusEmergencyFrozen = ModelProfileStatus_MODEL_PROFILE_STATUS_EMERGENCY_FROZEN
	ModelStatusDelisted        = ModelProfileStatus_MODEL_PROFILE_STATUS_DELISTED
)

const (
	ProfileStatusSourceUnspecified = ProfileStatusSource_PROFILE_STATUS_SOURCE_UNSPECIFIED
	ProfileStatusSourceAutoSupport = ProfileStatusSource_PROFILE_STATUS_SOURCE_AUTO_SUPPORT
	ProfileStatusSourceGovernance  = ProfileStatusSource_PROFILE_STATUS_SOURCE_GOVERNANCE
	ProfileStatusSourceEmergency   = ProfileStatusSource_PROFILE_STATUS_SOURCE_EMERGENCY
	ModelStatusSourceUnspecified   = ModelStatusSource_MODEL_STATUS_SOURCE_UNSPECIFIED
	ModelStatusSourceAutoProfile   = ModelStatusSource_MODEL_STATUS_SOURCE_AUTO_PROFILE
	ModelStatusSourceGovernance    = ModelStatusSource_MODEL_STATUS_SOURCE_GOVERNANCE
	ModelStatusSourceEmergency     = ModelStatusSource_MODEL_STATUS_SOURCE_EMERGENCY
)

const (
	ModelSupportActivationNone                  = ModelSupportActivationKind_MODEL_SUPPORT_ACTIVATION_KIND_NONE
	ModelSupportActivationP30OrderValue         = ModelSupportActivationKind_MODEL_SUPPORT_ACTIVATION_KIND_WORKER_P30_ORDER_VALUE
	ModelSupportActivationVerifierAssignedValid = ModelSupportActivationKind_MODEL_SUPPORT_ACTIVATION_KIND_VERIFIER_ASSIGNED_VALID
)

const (
	ModelRegistrationFeeMinMicroUSDC = uint64(1_000_000)
	TaskEpochLengthBlocks            = DefaultEpochLengthBlocks
)

// Three zero-reference constants were removed from the block above:
// MarkGateWindowEpochs, DefaultMarkRatePpm and DefaultTaskHardwareTier. Each
// duplicated a live governance/genesis parameter (Reward.MarkWindowEpochs,
// Reward.MarkRatePpmByBucket and the profile resource tier respectively) as a
// second, silently drifting copy of its default. The authoritative defaults live
// in x/hub/types/params.go; a constant that shadows a parameter is only ever
// one careless wiring away from splitting the value in two.

// DebugIntegrationRuleVersion remains an internal development-only tag until
// the settlement/reward runtime removes the legacy debug adapters. It is
// not a Hub parameter or public protocol field.
const DebugIntegrationRuleVersion = "DEBUG_KEEPER_INTEGRATION_V1"

// MaxSupportedProfilesPerOperator bounds every synchronous invalidation fan-out
// caused by bond, jail, tombstone, or provider status changes.
const MaxSupportedProfilesPerOperator = uint64(256)

// MaxProfilesPerModel also bounds synchronous model-wide invalidation and
// support-deactivation fan-out.
const MaxProfilesPerModel = uint32(256)

const (
	BuilderStageAssign           = "ASSIGN"
	BuilderStageOpenVerify       = "OPEN_VERIFY"
	BuilderStageSettle           = "SETTLE"
	BuilderSelectionProofVersion = "trueopen-builder-selection-v1"
)

const (
	RewardEligibilityStatusPendingEpochCheck   = "PENDING_EPOCH_CHECK"
	RewardEligibilityStatusEligible            = "ELIGIBLE"
	RewardEligibilityStatusIneligible          = "INELIGIBLE"
	RewardEpochPhaseHistogramOpen              = "HISTOGRAM_OPEN"
	RewardEpochPhaseCutoffDerived              = "CUTOFF_DERIVED"
	RewardEpochPhaseEligibilityRunning         = "ELIGIBILITY_RUNNING"
	RewardEpochPhaseRandomnessWait             = "RANDOMNESS_WAIT"
	RewardEpochPhaseMarkRunning                = "MARK_RUNNING"
	RewardEpochPhaseDone                       = "DONE"
	CompetitionBootstrapStatusBootstrap        = "BOOTSTRAP"
	CompetitionBootstrapStatusActive           = "ACTIVE"
	RewardClassService                         = "SERVICE"
	RewardClassBuilder                         = "BUILDER"
	RewardClassInfrastructure                  = "INFRASTRUCTURE"
	RewardCompetitionIdentityKindPayer         = "PAYER"
	RewardCompetitionIdentityKindWorker        = "WORKER"
	RewardEligibilityReasonPendingFinality     = "PENDING_FINALITY"
	RewardEligibilityReasonChallengeOpen       = "CHALLENGE_OPEN"
	RewardEligibilityReasonChallengeOverturned = "CHALLENGE_OVERTURNED"
	RewardEligibilityReasonFault               = "FAULT"
	RewardEligibilityReasonModelInactive       = "MODEL_INACTIVE"
	RewardEligibilityReasonProfileInactive     = "PROFILE_INACTIVE"
	RewardEligibilityReasonBelowTop10p         = "BELOW_TOP10P"
	RewardEligibilityReasonEligible            = "ELIGIBLE"
	RewardEligibilityReasonMissingAssignment   = "MISSING_ASSIGNMENT"
	RewardEligibilityReasonMissingSettlement   = "MISSING_SETTLEMENT"
)

const (
	EarningsPendingDeductionPendingCheck = "PENDING_CHECK"
	EarningsPendingDeductionCleared      = "CLEARED"
	EarningsPendingDeductionDeducted     = "DEDUCTED"
	EarningsPendingStatusPending         = "PENDING"
	EarningsPendingStatusMatured         = "MATURED"
	EarningsPendingStatusDeducted        = "DEDUCTED"
	EarningKindTaskFee                   = "TASK_FEE"
	ClaimClassAll                        = "ALL"
	ClaimClassTaskFee                    = "TASK_FEE"
	ClaimClassService                    = "SERVICE"
	ClaimClassBuilder                    = "BUILDER"
	ClaimClassInfrastructure             = "INFRASTRUCTURE"
)

// The custody Store rows spell these four statuses as proto enums, so the
// former ChallengeBondStatus*, ChallengeEffectPoolStatus*,
// ChallengeEffectReceiptStatusApplied and SettlementApplicationStatusApplied
// string constants are gone: §9.6b allows one numeric definition per closed
// enum, and a bare string beside the generated enum is a second one.
//
// What survives below is not Store state. Every constant here names a value of
// a `string` field on a shared payload — SettlementResult.finality_status
// and ChallengeEconomicReceipt.status cross the module boundary as text, and
// typing them is the wire PR's job, not this one's.
const (
	SettlementFinalityStatusPending    = "PENDING"
	SettlementFinalityStatusFinal      = "FINAL"
	SettlementFinalityStatusOverturned = "OVERTURNED"
	ChallengeEconomicStatusApplied     = "APPLIED"
)

const (
	FaultTypeWorkerInferTimeout   = "worker_infer_timeout"
	FaultTypeVerifierMiss         = "verifier_miss"
	FaultTypeCommitNoResult       = "commit_no_result"
	FaultTypeResultNoSettleReveal = "result_no_settle_reveal"
	FaultTypeValueOutlier         = "value_outlier"
	FaultTypeCommitRevealMismatch = "commit_reveal_mismatch"
	FaultTypeWorkerRevealTimeout  = "worker_reveal_timeout"
	FaultTypeObjectiveForgery     = "objective_forgery"
	FaultTypeRoundDivergence      = "round_divergence"
)

const (
	JailStatusNone       = ""
	JailStatusJailed     = "jailed"
	JailStatusTombstoned = "tombstoned"
)

const (
	SlashStatusNone    = ""
	SlashStatusApplied = "applied"
)

const (
	DefaultRewardCompetitionMinSample = uint64(1)
	DefaultP30BootstrapFloor          = uint64(0)
	PerformanceScoreDefaultPpm        = uint64(1_000_000)
	PerformanceScoreMethodVersionV1   = uint64(1)
	DefaultReferenceBucketKey         = "default"
	BucketSourceGenesis               = "GENESIS"
	BucketSourceGovernance            = "GOVERNANCE"
	BucketSourceDebugAuthority        = "DEBUG_AUTHORITY"
	MaxPricingBandBps                 = uint64(10_000)
	DebugEpochStatusClosed            = "CLOSED"
)

const (
	TreasuryInitialRatePpm = uint64(5000)
	TreasuryRateMinPpm     = uint64(1000)
	TreasuryRateMaxPpm     = uint64(20000)
	TreasuryPpmDenominator = uint64(1_000_000)
)

// ProfileStateKeyPair = (model_id, profile_version).
type ProfileStateKeyPair = collections.Pair[string, string]

func NewProfileStateKey(modelID string, profileVersion uint32) ProfileStateKeyPair {
	return collections.Join(modelID, strconv.FormatUint(uint64(profileVersion), 10))
}

// VRF key rotation state (§9.3a). History is keyed by the epoch a key became
// effective so a retired key stays addressable for audit; the activation index
// leads with the epoch so the epoch boundary reads one bounded ascending range.
var (
	VrfKeyStateKey           = MustVersionedStorePrefix("vrf_key", CurrentStoreSchemaVersion)
	VrfKeyHistoryStateKey    = MustVersionedStorePrefix("vrf_key_history", CurrentStoreSchemaVersion)
	VrfKeyActivationIndexKey = MustVersionedStorePrefix("vrf_key_activation", CurrentStoreSchemaVersion)
	VrfKeyPruneIndexKey      = MustVersionedStorePrefix("vrf_key_prune", CurrentStoreSchemaVersion)
)

type VrfKeyHistoryKeyPair = collections.Pair[string, uint64]

func NewVrfKeyHistoryKey(operatorAddress string, effectiveFromEpoch uint64) VrfKeyHistoryKeyPair {
	return collections.Join(operatorAddress, effectiveFromEpoch)
}

type VrfKeyActivationKeyPair = collections.Pair[uint64, string]

func NewVrfKeyActivationKey(activationEpoch uint64, operatorAddress string) VrfKeyActivationKeyPair {
	return collections.Join(activationEpoch, operatorAddress)
}

type VrfKeyPruneKeyTriple = collections.Triple[uint64, string, uint64]

func NewVrfKeyPruneKey(pruneEpoch uint64, operatorAddress string, effectiveFromEpoch uint64) VrfKeyPruneKeyTriple {
	return collections.Join3(pruneEpoch, operatorAddress, effectiveFromEpoch)
}
