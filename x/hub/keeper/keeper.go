package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// Keeper owns global hub state.
type Keeper struct {
	storeService          corestore.KVStoreService
	transientStoreService corestore.TransientStoreService
	cdc                   codec.Codec
	addressCodec          address.Codec
	authority             []byte
	authKeeper            types.AuthKeeper
	bankKeeper            types.BankKeeper

	freezeTaskValidator           types.FreezeSignalTaskValidator
	validatorSnapshots            types.ValidatorSnapshotProvider
	roleFaultConsumers            types.RoleFaultConsumerGate
	governanceActionReplayChecker *governanceActionReplayCheckerHolder
	bridgeUpstream                *bridgeUpstreamHolder
	vrfPoPVerifier                VrfPoPVerifier

	Schema                       collections.Schema
	Params                       collections.Item[types.HubParamsV2]
	ParamsMeta                   collections.Item[types.HubParamsMetaState]
	EndBlockBudgetHeight         collections.Item[uint64]
	EndBlockBudgetRemainingItems collections.Item[uint64]
	EndBlockBudgetRemainingBytes collections.Item[uint64]

	Model                               collections.Map[string, types.ModelState]
	Profile                             collections.Map[types.ProfileStateKeyPair, types.ProfileState]
	RegistrationReceipt                 collections.Map[shared.Hash32Key, types.RegistrationReceipt]
	CortexNode                          collections.Map[string, internaltypes.CortexNodeStoreState]
	ServiceBond                         collections.Map[string, internaltypes.ServiceBondStoreState]
	Unbonding                           collections.Map[types.UnbondingKeyPair, internaltypes.UnbondingStoreState]
	UnbondingMaturityIndex              collections.KeySet[types.UnbondingMaturityIndexKeyTriple]
	UnbondingByOperatorStatusIndex      collections.KeySet[types.UnbondingByOperatorStatusKey]
	UnbondingReceipt                    collections.Map[shared.Hash32Key, internaltypes.UnbondingReceiptStoreState]
	UnbondingReceiptPruneIndex          collections.KeySet[types.UnbondingReceiptPruneKey]
	ServiceDescriptor                   collections.Map[types.ParticipantKeyPair, internaltypes.ServiceDescriptorStoreState]
	CurrentServiceAddressIndex          collections.Map[types.CurrentServiceAddressIndexKeyPair, internaltypes.CurrentServiceAddressIndexStoreState]
	TaskLiabilityReservation            collections.Map[types.TaskLiabilityReservationKeyTriple, internaltypes.TaskLiabilityStoreState]
	ActiveLiabilityByOperatorIndex      collections.KeySet[types.ActiveLiabilityByOperatorKey]
	TaskLiabilityByTaskIndex            collections.KeySet[types.TaskLiabilityByTaskKey]
	ServiceKeyResponsibility            collections.Map[types.ServiceKeyResponsibilityKeyTriple, internaltypes.ServiceKeyResponsibilityStoreState]
	ServiceKeyResponsibilityByTaskIndex collections.KeySet[types.ServiceKeyResponsibilityByTaskKeyTriple]
	ProfileCapability                   collections.Map[types.ProfileCapabilityKeyTriple, types.ProfileCapabilityState]
	ModelSupport                        collections.Map[types.ModelSupportKeyTriple, types.ModelSupportState]
	ModelSupportExpiryIndex             collections.KeySet[types.ModelSupportExpiryIndexKeyPair]
	ModelSupportByProfileIndex          collections.KeySet[types.ModelSupportByProfileIndexKeyTriple]
	ModelSupportByOperatorIndex         collections.KeySet[types.ModelSupportByOperatorIndexKeyTriple]
	ModelSupportPruneIndex              collections.KeySet[types.ModelSupportPruneIndexKeyPair]
	DailySupport                        collections.Map[types.DailySupportKey, types.DailySupportState]
	DailySupportExpiryIndex             collections.KeySet[types.DailySupportExpiryIndexKeyTriple]
	SupportDeactivateCursor             collections.Map[types.ProfileStateKeyPair, types.SupportDeactivateCursorState]

	// ---- Global epoch stable-slot CandidatePool (the data-structure contract) ----
	//
	// One pool per epoch shared by every profile and duty. The PR #88
	// per-(model, profile_version, duty) collections
	// (CandidatePoolCurrent/Dirty/Overflow plus the inline-candidate-vector
	// snapshot) are deleted, not migrated.
	//
	// OperatorCandidateSlot is derived state: §3.3 line "OperatorCandidateSlotState
	// is the only reverse index of CandidateSlotCurrentState; Genesis rebuilds it from
	// the primary table and checks (operator,slot,slot_version) in both directions".
	// It must therefore NOT be exported in
	// GenesisState (hub/v1/genesis.proto lists it under the "Derived state is
	// NOT exported" block) and InitGenesis must rebuild it from
	// CandidateSlotCurrent while checking both directions: exactly one reverse row
	// per ALLOCATED/RETIRING slot, none for FREE slots.
	//
	// Every CandidatePool index (expiry, prune, slot-binding prune) is likewise
	// derived and rebuilt from the primaries at import.
	CandidatePoolSnapshot      collections.Map[shared.Hash32Key, types.CandidatePoolSnapshotState]
	CandidatePoolActiveSegment collections.Map[types.CandidatePoolSegmentKeyPair, types.CandidatePoolActiveSegmentState]
	CandidatePoolMember        collections.Map[types.CandidatePoolMemberKeyPair, internaltypes.CandidatePoolMemberStoreState]
	CandidateSlotCurrent       collections.Map[uint32, internaltypes.CandidateSlotCurrentStoreState]
	CandidateSlotBinding       collections.Map[types.CandidateSlotBindingKeyPair, internaltypes.CandidateSlotBindingStoreState]
	OperatorCandidateSlot      collections.Map[string, internaltypes.OperatorCandidateSlotStoreState]
	CandidatePoolBuildCursor   collections.Map[uint64, types.CandidatePoolBuildCursorState]
	CandidatePoolTaskRef       collections.Map[types.CandidatePoolTaskRefKeyPair, types.CandidatePoolTaskRefState]

	CandidatePoolExpiryIndex       collections.KeySet[types.CandidatePoolExpiryIndexKeyPair]
	CandidatePoolPruneIndex        collections.KeySet[types.CandidatePoolPruneIndexKeyTriple]
	CandidateSlotBindingPruneIndex collections.KeySet[types.CandidateSlotBindingPruneIndexKeyTriple]

	// Two singletons (§3.2): the build diagnosis a new build overwrites, and the
	// pointer to the unique ACTIVE snapshot.
	CandidatePoolBuildStatus collections.Item[types.CandidatePoolBuildStatusState]
	CurrentCandidatePool     collections.Item[types.CurrentCandidatePoolState]

	Builder                      collections.Map[string, internaltypes.BuilderStoreState]
	BuilderAdmission             collections.Map[string, internaltypes.BuilderAdmissionStoreState]
	CurrentBuilderSet            collections.Item[types.CurrentBuilderSetState]
	PendingBuilderSetReplacement collections.Item[internaltypes.BuilderSetPendingReplacementStoreState]
	BuilderSet                   collections.Map[uint64, internaltypes.BuilderSetStoreState]
	BuilderSetReplacementIndex   collections.Map[types.BuilderSetReplacementKeyPair, uint64]
	BuilderSetByIDIndex          collections.Map[string, uint64]
	BuilderSetByHeightIndex      collections.Map[types.BuilderSetByHeightKey, string]
	BuilderSetTaskRef            collections.Map[types.BuilderSetTaskRefKeyPair, types.BuilderSetTaskRefState]
	BuilderSetPruneIndex         collections.KeySet[types.BuilderSetPruneKeyTriple]
	BuilderFault                 collections.Map[types.BuilderFaultKeyPair, internaltypes.BuilderFaultStoreState]
	BuilderFaultPruneIndex       collections.KeySet[types.BuilderFaultPruneKeyTriple]
	ServiceBondEffectiveIndex    collections.KeySet[types.ServiceBondEffectiveKeyPair]

	RoleFault           collections.Map[shared.Hash32Key, internaltypes.RoleFaultStoreState]
	RoleFaultPruneIndex collections.KeySet[types.RoleFaultPruneKey]
	// RoleFaultByTaskIndex is derived from RoleFault and is never exported: it is
	// rebuilt from the imported primaries at InitGenesis, exactly like the other
	// by-task index directions.
	RoleFaultByTaskIndex            collections.KeySet[types.RoleFaultByTaskKey]
	SlashSummary                    collections.Map[types.SlashSummaryKeyTriple, internaltypes.SlashSummaryStoreState]
	Treasury                        collections.Item[types.TreasuryState]
	TreasurySpendReceipt            collections.Map[types.TreasurySpendReceiptKeyPair, internaltypes.TreasurySpendReceiptStoreState]
	TreasurySpendReceiptPruneIndex  collections.KeySet[types.TreasurySpendReceiptPruneKeyTriple]
	TreasurySpendProposal           collections.Map[uint64, types.TreasurySpendProposalState]
	TreasurySpendEpoch              collections.Map[uint64, types.TreasurySpendEpochState]
	TreasurySpendRecipientEpoch     collections.Map[types.TreasurySpendRecipientEpochKeyPair, internaltypes.TreasurySpendRecipientEpochStoreState]
	TreasurySpendEpochCleanupCursor collections.Map[uint64, internaltypes.TreasurySpendEpochCleanupCursorStoreState]
	TreasurySpendEpochCleanupIndex  collections.KeySet[types.TreasurySpendEpochCleanupIndexKeyPair]

	ParameterBucketVersion        collections.Map[types.ParameterBucketVersionKeyTriple, types.ParameterBucketVersionState]
	ParameterBucketCurrentPointer collections.Map[types.ParameterBucketPointerKeyPair, types.ParameterBucketCurrentPointerState]
	ParameterBucketPendingPointer collections.Map[types.ParameterBucketPointerKeyPair, types.ParameterBucketPendingPointerState]
	ParameterBucketEffectiveIndex collections.KeySet[types.ParameterBucketEffectiveIndexKeyQuad]
	ParameterBucketPruneIndex     collections.KeySet[types.ParameterBucketPruneIndexKeyQuad]

	RewardCompetitionEpoch collections.Map[types.HardwareTierEpochKey, types.RewardCompetitionEpochState]
	RewardP30CutoffPointer collections.Map[uint64, types.RewardP30CutoffPointerState]
	RewardEpochCursor      collections.Map[types.RewardEpochCursorKeyPair, types.RewardEpochCursorState]
	RewardEpochIndex       collections.KeySet[types.RewardEpochIndexKeyTriple]
	RewardEpochPruneIndex  collections.KeySet[types.RewardEpochPruneIndexKeyTriple]
	RewardEpochPruneCursor collections.Map[types.RewardEpochCursorKeyPair, types.RewardEpochPruneCursorState]
	RewardEpochAudit       collections.Map[types.RewardEpochCursorKeyPair, types.RewardEpochAuditState]
	Earnings               collections.Map[string, internaltypes.EarningsStoreState]

	Beacon                        collections.Map[uint64, internaltypes.BeaconStoreState]
	BeaconPruneIndex              collections.KeySet[types.BeaconPruneKey]
	BeaconCheckpoint              collections.Map[uint64, types.BeaconCheckpointState]
	BeaconCheckpointCursor        collections.Item[types.BeaconCheckpointCursorState]
	BeaconConsumerRef             collections.KeySet[types.BeaconConsumerRefKey]
	FreezeSignalState             collections.Map[shared.Hash32Key, types.FreezeSignalState]
	FreezeSignalBuildCursor       collections.Map[types.FreezeSignalBuildCursorKey, types.FreezeSignalBuildCursorState]
	FreezeSignalByWindow          collections.Map[types.FreezeSignalByWindowKey, types.FreezeSignalByWindowIndex]
	FreezeSignalByProfileIndex    collections.KeySet[types.FreezeSignalByProfileKey]
	FreezeRiskWindowScheduleIndex collections.KeySet[types.FreezeRiskWindowScheduleKey]
	FreezeSignalDeadlineIndex     collections.KeySet[types.FreezeSignalDeadlineKey]
	FreezeSignalPruneIndex        collections.KeySet[types.FreezeSignalPruneKey]
	EmergencyFreezeVoteState      collections.Map[types.EmergencyFreezeVoteKey, types.EmergencyFreezeVoteState]

	// Bridge rows are TrueOpen's own guard/governance/audit state. Hyperlane's
	// Mailbox, ISM, token and delivered-message state stay in the upstream
	// modules; the bridge protocol forbid a second
	// copy here, so there
	// is deliberately no delivered-message or nonce table below.
	VrfKey                collections.Map[string, internaltypes.VrfKeyStoreState]
	VrfKeyHistory         collections.Map[types.VrfKeyHistoryKeyPair, internaltypes.VrfKeyHistoryStoreState]
	VrfKeyActivationIndex collections.KeySet[types.VrfKeyActivationKeyPair]
	VrfKeyPruneIndex      collections.KeySet[types.VrfKeyPruneKeyTriple]

	ValidatorBridgeSigner collections.Map[string, internaltypes.ValidatorBridgeSignerStoreState]
	BridgeRoute           collections.Item[types.BridgeRouteState]
	BridgeSignerSet       collections.Item[types.BridgeSignerSetState]
	BridgeControl         collections.Item[types.BridgeControlState]
	BridgeCutover         collections.Item[types.BridgeCutoverState]
	BridgeLimit           collections.Item[types.BridgeLimitState]
	BridgePendingLimit    collections.Item[types.BridgePendingLimitState]
	BridgeEpochUsage      collections.Map[uint64, types.BridgeEpochUsageState]
	BridgeEpochUsagePrune collections.KeySet[types.BridgeEpochUsagePruneKeyPair]
	BridgeSupply          collections.Item[types.BridgeSupplyState]
	BridgeBootstrap       collections.Item[internaltypes.BridgeBootstrapStoreState]
}

func NewKeeper(
	storeService corestore.KVStoreService,
	transientStoreService corestore.TransientStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,
	authKeeper types.AuthKeeper,
	bankKeeper types.BankKeeper,
) Keeper {
	if _, err := addressCodec.BytesToString(authority); err != nil {
		panic(fmt.Sprintf("invalid %s authority address %s: %s", types.ModuleName, authority, err))
	}
	if authKeeper == nil {
		panic("hub auth keeper is required")
	}
	if bankKeeper == nil {
		panic("hub bank keeper is required")
	}
	if transientStoreService == nil {
		panic("hub transient store service is required")
	}

	sb := collections.NewSchemaBuilder(storeService)
	profileKeyCodec := collections.PairKeyCodec(collections.StringKey, collections.StringKey)
	pairUint64KeyCodec := collections.PairKeyCodec(collections.Uint64Key, collections.Uint64Key)
	// The competition map is keyed (reward_bucket, epoch) while the cursor, prune
	// cursor and audit maps are keyed (epoch, reward_bucket). Giving the leading
	// component its own type keeps the two layouts from being interchangeable at
	// compile time; the encoded bytes are identical to pairUint64KeyCodec.
	rewardCompetitionEpochKeyCodec := collections.PairKeyCodec(types.RewardBucketKeyCodec, collections.Uint64Key)
	treasurySpendReceiptKeyCodec := collections.PairKeyCodec(collections.Uint64Key, collections.Uint32Key)
	treasurySpendPruneKeyCodec := collections.TripleKeyCodec(collections.Uint64Key, collections.Uint64Key, collections.Uint32Key)
	freezeCursorKeyCodec := collections.PairKeyCodec(collections.StringKey, collections.Uint32Key)
	freezeWindowKeyCodec := collections.TripleKeyCodec(collections.StringKey, collections.Uint32Key, collections.Uint64Key)
	freezeSignalOrderKeyCodec := collections.PairKeyCodec(collections.Uint64Key, shared.Hash32KeyCodec)
	freezeProfileKeyCodec := collections.QuadKeyCodec(collections.StringKey, collections.Uint32Key, collections.Int32Key, freezeSignalOrderKeyCodec)
	freezeScheduleKeyCodec := collections.TripleKeyCodec(collections.Uint64Key, collections.StringKey, collections.Uint32Key)
	freezeDeadlineKeyCodec := collections.PairKeyCodec(collections.Uint64Key, shared.Hash32KeyCodec)
	freezePruneKeyCodec := collections.TripleKeyCodec(collections.Uint64Key, shared.Hash32KeyCodec, collections.Int32Key)
	// freeze_signal_id is non-terminal here, so replacing BytesKey drops the one-byte
	// length prefix and changes the encoded key. validator_consensus_address stays
	// BytesKey: it is a 20-byte consensus address, not a digest.
	freezeVoteKeyCodec := collections.PairKeyCodec(shared.Hash32KeyCodec, collections.BytesKey)
	rewardEpochIndexKeyCodec := collections.TripleKeyCodec(collections.Uint64Key, collections.Uint64Key, collections.Uint64Key)
	unbondingMaturityCodec := collections.TripleKeyCodec(collections.Uint64Key, collections.StringKey, shared.Hash32KeyCodec)
	participantKeyCodec := collections.PairKeyCodec(collections.Int32Key, collections.StringKey)
	profileSupportKeyCodec := collections.TripleKeyCodec(collections.StringKey, collections.StringKey, collections.Uint32Key)
	supportByProfileKeyCodec := collections.TripleKeyCodec(collections.StringKey, collections.Uint32Key, collections.StringKey)
	liabilityKeyCodec := collections.TripleKeyCodec(shared.Hash32KeyCodec, collections.Int32Key, collections.StringKey)
	activeLiabilityByOperatorKeyCodec := collections.TripleKeyCodec(collections.StringKey, shared.Hash32KeyCodec, collections.Int32Key)
	// BuilderSetTaskRef used to borrow profileKeyCodec purely because both were
	// (string, string). profileKeyCodec still serves Profile, Unbonding,
	// SupportDeactivateCursor and BuilderFault, none of which lead with a Hash32,
	// so the shape coincidence ends here and the codec is its own.
	builderSetTaskRefKeyCodec := collections.PairKeyCodec(shared.Hash32KeyCodec, collections.Uint64Key)
	// Unbonding and BuilderFault leave profileKeyCodec for the same reason
	// BuilderSetTaskRef did: the (string, string) shape was a coincidence, and their
	// second component is a Hash32 while Profile and SupportDeactivateCursor keep a
	// model_id there.
	addressHash32KeyCodec := collections.PairKeyCodec(collections.StringKey, shared.Hash32KeyCodec)
	beaconConsumerRefKeyCodec := collections.TripleKeyCodec(collections.Uint64Key, collections.Uint32Key, collections.StringKey)
	roleFaultPruneKeyCodec := collections.PairKeyCodec(collections.Uint64Key, shared.Hash32KeyCodec)
	roleFaultByTaskKeyCodec := collections.PairKeyCodec(shared.Hash32KeyCodec, shared.Hash32KeyCodec)
	slashSummaryKeyCodec := collections.TripleKeyCodec(collections.Int32Key, shared.Hash32KeyCodec, collections.Uint64Key)
	// (int32(ParticipantType), operator_address, responsibility_id hex). The same
	// codec is reused as the suffix of the by-task index so the two directions
	// cannot drift apart (A-15a).
	serviceKeyResponsibilityKeyCodec := collections.TripleKeyCodec(collections.Int32Key, collections.StringKey, shared.Hash32KeyCodec)

	// Global CandidatePool key codecs (the data-structure contract). slot is uint32
	// and slot_version uint64, so the binding key is not a string pair; Hash32 key
	// components (snapshot_id, task_id) are lowercase 64-hex strings.
	candidateSlotBindingKeyCodec := collections.PairKeyCodec(collections.Uint32Key, collections.Uint64Key)
	candidatePoolEpochSlotKeyCodec := collections.PairKeyCodec(collections.Uint64Key, collections.Uint32Key)
	candidatePoolTaskRefKeyCodec := collections.PairKeyCodec(shared.Hash32KeyCodec, shared.Hash32KeyCodec)
	candidatePoolExpiryKeyCodec := collections.PairKeyCodec(collections.Uint64Key, shared.Hash32KeyCodec)
	candidatePoolPruneKeyCodec := collections.TripleKeyCodec(collections.Uint64Key, shared.Hash32KeyCodec, collections.Int32Key)
	candidateSlotBindingPruneKeyCodec := collections.TripleKeyCodec(collections.Uint64Key, collections.Uint32Key, collections.Uint64Key)
	parameterBucketVersionKeyCodec := collections.TripleKeyCodec(collections.Int32Key, collections.StringKey, collections.Uint64Key)
	parameterBucketPointerKeyCodec := collections.PairKeyCodec(collections.Int32Key, collections.StringKey)
	parameterBucketScheduleKeyCodec := collections.QuadKeyCodec(collections.Uint64Key, collections.Int32Key, collections.StringKey, collections.Uint64Key)

	k := Keeper{
		storeService: storeService, transientStoreService: transientStoreService,
		cdc:          cdc,
		addressCodec: addressCodec,
		authority:    authority,
		authKeeper:   authKeeper,
		bankKeeper:   bankKeeper,

		Params:                       collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.HubParamsV2](cdc)),
		ParamsMeta:                   collections.NewItem(sb, types.ParamsMetaKey, "params_meta", codec.CollValue[types.HubParamsMetaState](cdc)),
		EndBlockBudgetHeight:         collections.NewItem(sb, types.EndBlockBudgetHeightKey, "endblock_budget_height", collections.Uint64Value),
		EndBlockBudgetRemainingItems: collections.NewItem(sb, types.EndBlockBudgetRemainingItemsKey, "endblock_budget_remaining_items", collections.Uint64Value),
		EndBlockBudgetRemainingBytes: collections.NewItem(sb, types.EndBlockBudgetRemainingBytesKey, "endblock_budget_remaining_bytes", collections.Uint64Value),

		Model:                               collections.NewMap(sb, types.ModelStateKey, "model_state", collections.StringKey, codec.CollValue[types.ModelState](cdc)),
		Profile:                             collections.NewMap(sb, types.ProfileStateKey, "profile_state", profileKeyCodec, codec.CollValue[types.ProfileState](cdc)),
		RegistrationReceipt:                 collections.NewMap(sb, types.RegistrationReceiptKey, "registration_receipt", shared.Hash32KeyCodec, codec.CollValue[types.RegistrationReceipt](cdc)),
		CortexNode:                          collections.NewMap(sb, types.CortexNodeKey, "cortex_node", collections.StringKey, codec.CollValue[internaltypes.CortexNodeStoreState](cdc)),
		ServiceBond:                         collections.NewMap(sb, types.ServiceBondKey, "service_bond", collections.StringKey, codec.CollValue[internaltypes.ServiceBondStoreState](cdc)),
		Unbonding:                           collections.NewMap(sb, types.UnbondingKey, "unbonding", addressHash32KeyCodec, codec.CollValue[internaltypes.UnbondingStoreState](cdc)),
		UnbondingMaturityIndex:              collections.NewKeySet(sb, types.UnbondingMaturityIndexKey, "unbonding_maturity_index", unbondingMaturityCodec),
		UnbondingByOperatorStatusIndex:      collections.NewKeySet(sb, types.UnbondingByOperatorStatusIndexKey, "unbonding_by_operator_status", collections.QuadKeyCodec(collections.StringKey, collections.Int32Key, collections.Uint64Key, shared.Hash32KeyCodec)),
		UnbondingReceipt:                    collections.NewMap(sb, types.UnbondingReceiptKey, "unbonding_receipt", shared.Hash32KeyCodec, codec.CollValue[internaltypes.UnbondingReceiptStoreState](cdc)),
		UnbondingReceiptPruneIndex:          collections.NewKeySet(sb, types.UnbondingReceiptPruneIndexKey, "unbonding_receipt_prune", collections.PairKeyCodec(collections.Uint64Key, shared.Hash32KeyCodec)),
		ServiceDescriptor:                   collections.NewMap(sb, types.ServiceDescriptorKey, "service_descriptor", participantKeyCodec, codec.CollValue[internaltypes.ServiceDescriptorStoreState](cdc)),
		CurrentServiceAddressIndex:          collections.NewMap(sb, types.CurrentServiceAddressIndexKey, "current_service_address", participantKeyCodec, codec.CollValue[internaltypes.CurrentServiceAddressIndexStoreState](cdc)),
		TaskLiabilityReservation:            collections.NewMap(sb, types.TaskLiabilityReservationKey, "task_liability", liabilityKeyCodec, codec.CollValue[internaltypes.TaskLiabilityStoreState](cdc)),
		ActiveLiabilityByOperatorIndex:      collections.NewKeySet(sb, types.ActiveLiabilityByOperatorIndexKey, "active_liability_by_operator", activeLiabilityByOperatorKeyCodec),
		TaskLiabilityByTaskIndex:            collections.NewKeySet(sb, types.TaskLiabilityByTaskIndexKey, "task_liability_by_task", liabilityKeyCodec),
		ServiceKeyResponsibility:            collections.NewMap(sb, types.ServiceKeyResponsibilityKey, "service_key_responsibility", serviceKeyResponsibilityKeyCodec, codec.CollValue[internaltypes.ServiceKeyResponsibilityStoreState](cdc)),
		ServiceKeyResponsibilityByTaskIndex: collections.NewKeySet(sb, types.ServiceKeyResponsibilityByTaskIndexKey, "service_key_responsibility_by_task", collections.TripleKeyCodec(collections.StringKey, collections.StringKey, serviceKeyResponsibilityKeyCodec)),
		ProfileCapability:                   collections.NewMap(sb, types.ProfileCapabilityKey, "profile_capability", profileSupportKeyCodec, codec.CollValue[types.ProfileCapabilityState](cdc)),
		ModelSupport:                        collections.NewMap(sb, types.ModelSupportKey, "model_support", profileSupportKeyCodec, codec.CollValue[types.ModelSupportState](cdc)),
		ModelSupportExpiryIndex:             collections.NewKeySet(sb, types.ModelSupportExpiryIndexKey, "model_support_expiry", collections.PairKeyCodec(collections.Uint64Key, profileSupportKeyCodec)),
		ModelSupportPruneIndex:              collections.NewKeySet(sb, types.ModelSupportPruneIndexKey, "model_support_prune", collections.PairKeyCodec(collections.Uint64Key, profileSupportKeyCodec)),
		ModelSupportByProfileIndex:          collections.NewKeySet(sb, types.ModelSupportByProfileIndexKey, "model_support_by_profile", supportByProfileKeyCodec),
		ModelSupportByOperatorIndex:         collections.NewKeySet(sb, types.ModelSupportByOperatorIndexKey, "model_support_by_operator", profileSupportKeyCodec),
		DailySupport:                        collections.NewMap(sb, types.DailySupportStateKey, "daily_support", collections.PairKeyCodec(collections.Uint64Key, collections.StringKey), codec.CollValue[types.DailySupportState](cdc)),
		DailySupportExpiryIndex:             collections.NewKeySet(sb, types.DailySupportExpiryIndexKey, "daily_support_expiry", collections.TripleKeyCodec(collections.Uint64Key, collections.StringKey, collections.Uint64Key)),
		SupportDeactivateCursor:             collections.NewMap(sb, types.SupportDeactivateCursorKey, "support_deactivate_cursor", profileKeyCodec, codec.CollValue[types.SupportDeactivateCursorState](cdc)),
		CandidatePoolSnapshot:               collections.NewMap(sb, types.CandidatePoolSnapshotKey, "candidate_pool_snapshot", shared.Hash32KeyCodec, codec.CollValue[types.CandidatePoolSnapshotState](cdc)),
		CandidatePoolActiveSegment:          collections.NewMap(sb, types.CandidatePoolActiveSegmentKey, "candidate_pool_active_segment", candidatePoolEpochSlotKeyCodec, codec.CollValue[types.CandidatePoolActiveSegmentState](cdc)),
		CandidatePoolMember:                 collections.NewMap(sb, types.CandidatePoolMemberKey, "candidate_pool_member", candidatePoolEpochSlotKeyCodec, codec.CollValue[internaltypes.CandidatePoolMemberStoreState](cdc)),
		CandidateSlotCurrent:                collections.NewMap(sb, types.CandidateSlotCurrentKey, "candidate_slot_current", collections.Uint32Key, codec.CollValue[internaltypes.CandidateSlotCurrentStoreState](cdc)),
		CandidateSlotBinding:                collections.NewMap(sb, types.CandidateSlotBindingKey, "candidate_slot_binding", candidateSlotBindingKeyCodec, codec.CollValue[internaltypes.CandidateSlotBindingStoreState](cdc)),
		OperatorCandidateSlot:               collections.NewMap(sb, types.OperatorCandidateSlotKey, "operator_candidate_slot", collections.StringKey, codec.CollValue[internaltypes.OperatorCandidateSlotStoreState](cdc)),
		CandidatePoolBuildCursor:            collections.NewMap(sb, types.CandidatePoolBuildCursorKey, "candidate_pool_build_cursor", collections.Uint64Key, codec.CollValue[types.CandidatePoolBuildCursorState](cdc)),
		CandidatePoolTaskRef:                collections.NewMap(sb, types.CandidatePoolTaskRefKey, "candidate_pool_task_ref", candidatePoolTaskRefKeyCodec, codec.CollValue[types.CandidatePoolTaskRefState](cdc)),
		CandidatePoolExpiryIndex:            collections.NewKeySet(sb, types.CandidatePoolExpiryIndexKey, "candidate_pool_expiry", candidatePoolExpiryKeyCodec),
		CandidatePoolPruneIndex:             collections.NewKeySet(sb, types.CandidatePoolPruneIndexKey, "candidate_pool_prune", candidatePoolPruneKeyCodec),
		CandidateSlotBindingPruneIndex:      collections.NewKeySet(sb, types.CandidateSlotBindingPruneIndexKey, "candidate_slot_binding_prune", candidateSlotBindingPruneKeyCodec),
		CandidatePoolBuildStatus:            collections.NewItem(sb, types.CandidatePoolBuildStatusSingletonKey, "candidate_pool_build_status", codec.CollValue[types.CandidatePoolBuildStatusState](cdc)),
		CurrentCandidatePool:                collections.NewItem(sb, types.CurrentCandidatePoolSingletonKey, "current_candidate_pool", codec.CollValue[types.CurrentCandidatePoolState](cdc)),
		Builder:                             collections.NewMap(sb, types.BuilderStateKey, "builder_state", collections.StringKey, codec.CollValue[internaltypes.BuilderStoreState](cdc)),
		BuilderAdmission:                    collections.NewMap(sb, types.BuilderAdmissionStateKey, "builder_admission", collections.StringKey, codec.CollValue[internaltypes.BuilderAdmissionStoreState](cdc)),
		CurrentBuilderSet:                   collections.NewItem(sb, types.CurrentBuilderSetStateKey, "current_builder_set", codec.CollValue[types.CurrentBuilderSetState](cdc)),
		PendingBuilderSetReplacement:        collections.NewItem(sb, types.PendingBuilderSetReplacementStateKey, "pending_builder_set_replacement", codec.CollValue[internaltypes.BuilderSetPendingReplacementStoreState](cdc)),
		BuilderSet:                          collections.NewMap(sb, types.BuilderSetStateKey, "builder_set", collections.Uint64Key, codec.CollValue[internaltypes.BuilderSetStoreState](cdc)),
		BuilderSetReplacementIndex:          collections.NewMap(sb, types.BuilderSetReplacementIndexKey, "builder_set_replacement", pairUint64KeyCodec, collections.Uint64Value),
		BuilderSetByIDIndex:                 collections.NewMap(sb, types.BuilderSetByIDIndexKey, "builder_set_by_id", collections.StringKey, collections.Uint64Value),
		BuilderSetByHeightIndex:             collections.NewMap(sb, types.BuilderSetByHeightIndexKey, "builder_set_by_height", pairUint64KeyCodec, collections.StringValue),
		BuilderSetTaskRef:                   collections.NewMap(sb, types.BuilderSetTaskRefKey, "builder_set_task_ref", builderSetTaskRefKeyCodec, codec.CollValue[types.BuilderSetTaskRefState](cdc)),
		// (prune_epoch, builder_set_id, phase) - see BuilderSetPruneKeyTriple.
		BuilderSetPruneIndex:      collections.NewKeySet(sb, types.BuilderSetPruneIndexKey, "builder_set_prune", collections.TripleKeyCodec(collections.Uint64Key, collections.Uint64Key, collections.Uint32Key)),
		BuilderFault:              collections.NewMap(sb, types.BuilderFaultKey, "builder_fault", addressHash32KeyCodec, codec.CollValue[internaltypes.BuilderFaultStoreState](cdc)),
		BuilderFaultPruneIndex:    collections.NewKeySet(sb, types.BuilderFaultPruneIndexKey, "builder_fault_prune", collections.TripleKeyCodec(collections.Uint64Key, collections.StringKey, shared.Hash32KeyCodec)),
		ServiceBondEffectiveIndex: collections.NewKeySet(sb, types.ServiceBondEffectiveIndexKey, "service_bond_effective", collections.PairKeyCodec(collections.Uint64Key, collections.StringKey)),

		RoleFault:                       collections.NewMap(sb, types.RoleFaultKey, "role_fault", shared.Hash32KeyCodec, codec.CollValue[internaltypes.RoleFaultStoreState](cdc)),
		RoleFaultPruneIndex:             collections.NewKeySet(sb, types.RoleFaultPruneIndexKey, "role_fault_prune", roleFaultPruneKeyCodec),
		RoleFaultByTaskIndex:            collections.NewKeySet(sb, types.RoleFaultByTaskIndexKey, "role_fault_by_task", roleFaultByTaskKeyCodec),
		SlashSummary:                    collections.NewMap(sb, types.SlashSummaryKey, "slash_summary", slashSummaryKeyCodec, codec.CollValue[internaltypes.SlashSummaryStoreState](cdc)),
		Treasury:                        collections.NewItem(sb, types.TreasuryStateKey, "treasury_state", codec.CollValue[types.TreasuryState](cdc)),
		TreasurySpendReceipt:            collections.NewMap(sb, types.TreasurySpendReceiptKey, "treasury_spend_receipt", treasurySpendReceiptKeyCodec, codec.CollValue[internaltypes.TreasurySpendReceiptStoreState](cdc)),
		TreasurySpendReceiptPruneIndex:  collections.NewKeySet(sb, types.TreasurySpendReceiptPruneIndexKey, "treasury_spend_receipt_prune", treasurySpendPruneKeyCodec),
		TreasurySpendProposal:           collections.NewMap(sb, types.TreasurySpendProposalKey, "treasury_spend_proposal", collections.Uint64Key, codec.CollValue[types.TreasurySpendProposalState](cdc)),
		TreasurySpendEpoch:              collections.NewMap(sb, types.TreasurySpendEpochKey, "treasury_spend_epoch", collections.Uint64Key, codec.CollValue[types.TreasurySpendEpochState](cdc)),
		TreasurySpendRecipientEpoch:     collections.NewMap(sb, types.TreasurySpendRecipientEpochKey, "treasury_spend_recipient_epoch", collections.PairKeyCodec(collections.Uint64Key, collections.BytesKey), codec.CollValue[internaltypes.TreasurySpendRecipientEpochStoreState](cdc)),
		TreasurySpendEpochCleanupCursor: collections.NewMap(sb, types.TreasurySpendEpochCleanupCursorKey, "treasury_spend_epoch_cleanup_cursor", collections.Uint64Key, codec.CollValue[internaltypes.TreasurySpendEpochCleanupCursorStoreState](cdc)),
		TreasurySpendEpochCleanupIndex:  collections.NewKeySet(sb, types.TreasurySpendEpochCleanupIndexKey, "treasury_spend_epoch_cleanup_index", pairUint64KeyCodec),

		ParameterBucketVersion:        collections.NewMap(sb, types.ParameterBucketVersionKey, "parameter_bucket_version", parameterBucketVersionKeyCodec, codec.CollValue[types.ParameterBucketVersionState](cdc)),
		ParameterBucketCurrentPointer: collections.NewMap(sb, types.ParameterBucketCurrentPointerKey, "parameter_bucket_current_pointer", parameterBucketPointerKeyCodec, codec.CollValue[types.ParameterBucketCurrentPointerState](cdc)),
		ParameterBucketPendingPointer: collections.NewMap(sb, types.ParameterBucketPendingPointerKey, "parameter_bucket_pending_pointer", parameterBucketPointerKeyCodec, codec.CollValue[types.ParameterBucketPendingPointerState](cdc)),
		ParameterBucketEffectiveIndex: collections.NewKeySet(sb, types.ParameterBucketEffectiveIndexKey, "parameter_bucket_effective_index", parameterBucketScheduleKeyCodec),
		ParameterBucketPruneIndex:     collections.NewKeySet(sb, types.ParameterBucketPruneIndexKey, "parameter_bucket_prune_index", parameterBucketScheduleKeyCodec),

		RewardCompetitionEpoch: collections.NewMap(sb, types.HardwareTierCompetitionEpochKey, "reward_competition_epoch", rewardCompetitionEpochKeyCodec, codec.CollValue[types.RewardCompetitionEpochState](cdc)),
		RewardP30CutoffPointer: collections.NewMap(sb, types.RewardP30CutoffPointerKey, "reward_p30_cutoff_pointer", collections.Uint64Key, codec.CollValue[types.RewardP30CutoffPointerState](cdc)),
		RewardEpochCursor:      collections.NewMap(sb, types.RewardEpochCursorKey, "reward_epoch_cursor", pairUint64KeyCodec, codec.CollValue[types.RewardEpochCursorState](cdc)),
		RewardEpochIndex:       collections.NewKeySet(sb, types.RewardEpochIndexKey, "reward_epoch_index", rewardEpochIndexKeyCodec),
		RewardEpochPruneIndex:  collections.NewKeySet(sb, types.RewardEpochPruneIndexKey, "reward_epoch_prune_index", rewardEpochIndexKeyCodec),
		RewardEpochPruneCursor: collections.NewMap(sb, types.RewardEpochPruneCursorKey, "reward_epoch_prune_cursor", pairUint64KeyCodec, codec.CollValue[types.RewardEpochPruneCursorState](cdc)),
		RewardEpochAudit:       collections.NewMap(sb, types.RewardEpochAuditKey, "reward_epoch_audit", pairUint64KeyCodec, codec.CollValue[types.RewardEpochAuditState](cdc)),
		Earnings:               collections.NewMap(sb, types.EarningsKey, "earnings", collections.StringKey, codec.CollValue[internaltypes.EarningsStoreState](cdc)),

		Beacon:                        collections.NewMap(sb, types.BeaconStateKey, "beacon_state", collections.Uint64Key, codec.CollValue[internaltypes.BeaconStoreState](cdc)),
		BeaconPruneIndex:              collections.NewKeySet(sb, types.BeaconPruneIndexKey, "beacon_prune", pairUint64KeyCodec),
		BeaconCheckpoint:              collections.NewMap(sb, types.BeaconCheckpointStateKey, "beacon_checkpoint", collections.Uint64Key, codec.CollValue[types.BeaconCheckpointState](cdc)),
		BeaconCheckpointCursor:        collections.NewItem(sb, types.BeaconCheckpointCursorKey, "beacon_checkpoint_cursor", codec.CollValue[types.BeaconCheckpointCursorState](cdc)),
		BeaconConsumerRef:             collections.NewKeySet(sb, types.BeaconConsumerRefStoreKey, "beacon_consumer_ref", beaconConsumerRefKeyCodec),
		FreezeSignalState:             collections.NewMap(sb, types.FreezeSignalStateKey, "freeze_signal_state", shared.Hash32KeyCodec, codec.CollValue[types.FreezeSignalState](cdc)),
		FreezeSignalBuildCursor:       collections.NewMap(sb, types.FreezeSignalBuildCursorStoreKey, "freeze_signal_build_cursor", freezeCursorKeyCodec, codec.CollValue[types.FreezeSignalBuildCursorState](cdc)),
		FreezeSignalByWindow:          collections.NewMap(sb, types.FreezeSignalByWindowIndexKey, "freeze_signal_by_window", freezeWindowKeyCodec, codec.CollValue[types.FreezeSignalByWindowIndex](cdc)),
		FreezeSignalByProfileIndex:    collections.NewKeySet(sb, types.FreezeSignalByProfileIndexKey, "freeze_signal_by_profile", freezeProfileKeyCodec),
		FreezeRiskWindowScheduleIndex: collections.NewKeySet(sb, types.FreezeRiskWindowScheduleIndexKey, "freeze_risk_window_schedule", freezeScheduleKeyCodec),
		FreezeSignalDeadlineIndex:     collections.NewKeySet(sb, types.FreezeSignalDeadlineIndexKey, "freeze_signal_deadline_index", freezeDeadlineKeyCodec),
		FreezeSignalPruneIndex:        collections.NewKeySet(sb, types.FreezeSignalPruneIndexKey, "freeze_signal_prune", freezePruneKeyCodec),
		EmergencyFreezeVoteState:      collections.NewMap(sb, types.EmergencyFreezeVoteStateKey, "emergency_freeze_vote_state", freezeVoteKeyCodec, codec.CollValue[types.EmergencyFreezeVoteState](cdc)),
		bridgeUpstream:                &bridgeUpstreamHolder{},
		governanceActionReplayChecker: &governanceActionReplayCheckerHolder{},

		VrfKey:                collections.NewMap(sb, types.VrfKeyStateKey, "vrf_key", collections.StringKey, codec.CollValue[internaltypes.VrfKeyStoreState](cdc)),
		VrfKeyHistory:         collections.NewMap(sb, types.VrfKeyHistoryStateKey, "vrf_key_history", collections.PairKeyCodec(collections.StringKey, collections.Uint64Key), codec.CollValue[internaltypes.VrfKeyHistoryStoreState](cdc)),
		VrfKeyActivationIndex: collections.NewKeySet(sb, types.VrfKeyActivationIndexKey, "vrf_key_activation", collections.PairKeyCodec(collections.Uint64Key, collections.StringKey)),
		VrfKeyPruneIndex:      collections.NewKeySet(sb, types.VrfKeyPruneIndexKey, "vrf_key_prune", collections.TripleKeyCodec(collections.Uint64Key, collections.StringKey, collections.Uint64Key)),

		ValidatorBridgeSigner: collections.NewMap(sb, types.ValidatorBridgeSignerKey, "validator_bridge_signer", collections.StringKey, codec.CollValue[internaltypes.ValidatorBridgeSignerStoreState](cdc)),
		BridgeRoute:           collections.NewItem(sb, types.BridgeRouteStateKey, "bridge_route", codec.CollValue[types.BridgeRouteState](cdc)),
		BridgeSignerSet:       collections.NewItem(sb, types.BridgeSignerSetStateKey, "bridge_signer_set", codec.CollValue[types.BridgeSignerSetState](cdc)),
		BridgeControl:         collections.NewItem(sb, types.BridgeControlStateKey, "bridge_control", codec.CollValue[types.BridgeControlState](cdc)),
		BridgeCutover:         collections.NewItem(sb, types.BridgeCutoverStateKey, "bridge_cutover", codec.CollValue[types.BridgeCutoverState](cdc)),
		BridgeLimit:           collections.NewItem(sb, types.BridgeLimitStateKey, "bridge_limit", codec.CollValue[types.BridgeLimitState](cdc)),
		BridgePendingLimit:    collections.NewItem(sb, types.BridgePendingLimitStateKey, "bridge_pending_limit", codec.CollValue[types.BridgePendingLimitState](cdc)),
		BridgeEpochUsage:      collections.NewMap(sb, types.BridgeEpochUsageStateKey, "bridge_epoch_usage", collections.Uint64Key, codec.CollValue[types.BridgeEpochUsageState](cdc)),
		BridgeEpochUsagePrune: collections.NewKeySet(sb, types.BridgeEpochUsagePruneKey, "bridge_epoch_usage_prune", pairUint64KeyCodec),
		BridgeSupply:          collections.NewItem(sb, types.BridgeSupplyStateKey, "bridge_supply", codec.CollValue[types.BridgeSupplyState](cdc)),
		BridgeBootstrap:       collections.NewItem(sb, types.BridgeBootstrapStateKey, "bridge_bootstrap", codec.CollValue[internaltypes.BridgeBootstrapStoreState](cdc)),
	}

	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

// WithFreezeRuntimeDependencies returns the module-owned Keeper copy used by
// Msg and EndBlock execution. Task keeps the dependency-free HubKeeper copy;
// its read-only failure-retention gate does not need these adapters.
func (k Keeper) WithFreezeRuntimeDependencies(taskValidator types.FreezeSignalTaskValidator, snapshots types.ValidatorSnapshotProvider) Keeper {
	k.freezeTaskValidator = taskValidator
	k.validatorSnapshots = snapshots
	return k
}

// WithRuntimeDependencies returns the module-owned Keeper copy used by Msg and
// EndBlock execution. The dependency-free keeper remains the Task-facing Hub
// adapter, avoiding a construction cycle.
func (k Keeper) WithRuntimeDependencies(taskValidator types.FreezeSignalTaskValidator, snapshots types.ValidatorSnapshotProvider, roleFaultConsumers types.RoleFaultConsumerGate) Keeper {
	k = k.WithFreezeRuntimeDependencies(taskValidator, snapshots)
	k.roleFaultConsumers = roleFaultConsumers
	return k
}

// GetAuthority returns the hub module authority.
func (k Keeper) GetAuthority() []byte {
	return k.authority
}
