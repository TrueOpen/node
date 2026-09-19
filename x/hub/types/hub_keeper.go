package types

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
)

type JailStatus string
type ModelStatus = ModelProfileStatus

type ProfileStateSnapshot struct {
	ModelID                   string
	ProfileVersion            uint32
	Status                    ModelProfileStatus
	TaskTypes                 []shared.TaskType
	ResourceTier              uint64
	MinStake                  uint64
	ChallengeOpenWindowBlocks uint64
	ActiveSupporterCount      uint64
	ActiveSupportStake        uint64
	StatusSource              ProfileStatusSource
	ExecutionSnapshot         shared.ProfileExecutionSnapshot
	ExecutionSnapshotHash     []byte
	PricingProfile            shared.PricingProfile
	RefPrice                  uint64
}

type HubParamsSnapshot struct {
	EpochLengthBlocks                     uint64
	DeltaWBlocks                          uint64
	ServiceUnbondingPeriodBlocks          uint64
	UnbondingSlashSafetyMarginBlocks      uint64
	ObjectiveForgerySlashBps              uint32
	CandidateSlotHardCapacity             uint32
	BuildersPerTask                       uint32
	AssignmentBuilderProposalWindowBlocks uint64
	OpenVerifyBuilderProposalWindowBlocks uint64
	SettlementBuilderGraceBlocks          uint64
	FreezeFailureIndexRetentionBlocks     uint64
	MaxQueryPageTokenBytes                uint32
	MaxQueryPageLimit                     uint32
	MaxQueryResponseBytes                 uint64
	MaxEndblockVisitedItemsTotal          uint32
	BusinessDenom                         string
	EVMChainID                            uint64
}

type EndBlockBudgetSnapshot struct {
	Height         uint64
	RemainingItems uint64
	RemainingBytes uint64
}

type CortexNodeSnapshot struct {
	OperatorAddress           string
	ServiceAuthorizationNonce uint64
	ServiceKeyStatus          ServiceKeyStatus
	PendingStageDutyCount     uint32
}

type ProfileCapabilitySnapshot struct {
	OperatorAddress        string
	ModelID                string
	ProfileVersion         uint32
	InferenceCapability    bool
	VerificationCapability bool
	CapabilityVersion      uint64
}

type ModelSupportSnapshot struct {
	OperatorAddress              string
	ModelID                      string
	ProfileVersion               uint32
	DeclaredSupport              bool
	SupportActive                bool
	ActivationKind               ModelSupportActivationKind
	FirstActivationDuty          shared.Duty
	FirstSupportTaskID           []byte
	FirstSupportOrderValue       uint64
	P30CutoffEpoch               uint64
	P30Bootstrap                 bool
	SupportFreshUntilEpoch       uint64
	ActiveSupportStakeSnapshot   uint64
	EligibleSupportStakeSnapshot uint64
	SupportVersion               uint64
}

type ServiceBondSnapshot struct {
	OperatorAddress       string
	Status                ServiceBondStatus
	Amount                sdk.Coin
	ActiveBond            uint64
	EffectiveActiveBond   uint64
	AvailableBond         uint64
	RequiredTaskLiability uint64
	ReservedLiability     uint64
	BondVersion           uint64
	EffectiveEpoch        uint64
	PendingUnbonding      uint64
}

type CurrentServiceKeySnapshot struct {
	ParticipantType    string
	OperatorAddress    string
	ServiceAddress     string
	ServicePubkey      string
	AuthorizationNonce uint64
	UpdatedHeight      uint64
	Status             ServiceKeyStatus
}

type RoleScoringSnapshot struct {
	PerformanceScorePpm uint64
	PerformanceVersion  uint64
	JailCount           uint64
}

type FrozenFactLiabilityRequest struct {
	TaskID                    []byte
	CandidatePoolSnapshotID   []byte
	OperatorAddress           string
	Duty                      shared.Duty
	OrderValue                uint64
	Slot                      uint32
	SlotVersion               uint64
	RequiredTaskLiability     uint64
	ActiveBondSnapshot        uint64
	AvailableBondSnapshot     uint64
	MinStakeSnapshot          uint64
	BondVersionSnapshot       uint64
	CapabilityVersionSnapshot uint64
	SupportVersionSnapshot    uint64
	ModelID                   string
	ProfileVersion            uint32
	Height                    uint64
}

// TaskLiabilityReservationSnapshot is the read-only economic basis exposed to
// Task when it freezes a round-2 slash plan. It deliberately omits Hub store
// keys, versions and candidate metadata.
type TaskLiabilityReservationSnapshot struct {
	TaskID          []byte
	OperatorAddress string
	Duty            shared.Duty
	ReservedAmount  uint64
	Reserved        bool
}

// TaskSupportCompletionFact is the internal Task-to-Hub proof that one
// protocol-assigned duty reached fault-free finality. Hub verifies the matching
// RELEASED liability and derives the support mutation, P30 cutoff and events.
// RewardBucket remains explicit because the protocol does not freeze a numeric
// resource-tier-to-bucket mapping.
type TaskSupportCompletionFact struct {
	TaskID          []byte
	OperatorAddress string
	ModelID         string
	ProfileVersion  uint32
	Duty            shared.Duty
	RewardBucket    RewardBucket
	OrderValue      uint64
	Height          uint64
}

type TaskRoleFaultFact struct {
	SessionID            []byte
	TaskID               []byte
	OperatorAddress      string
	Duty                 shared.Duty
	FaultType            string
	ClassificationSource shared.FailureClassificationSource
	EvidenceDigest       []byte
	SlashBps             uint32
	Height               uint64
}

// RoundEconomicEffectApplyRequest is the Task-owned frozen effect plus the
// minimum Hub execution scope. Task persists the effect and cursor; Hub owns
// only service slashing and custody moves involving the challenge-effect pool.
type RoundEconomicEffectApplyRequest struct {
	SessionID         []byte
	TaskID            []byte
	RoundID           []byte
	VerifyRound       uint32
	Duty              shared.Duty
	Effect            shared.RoundEconomicEffectV1
	EffectPoolBalance shared.Amount
	EvidenceDigest    []byte
	Height            uint64
}

// RoundEconomicEffectApplyReceipt reports the actual custody result. Pool
// credit/debit are mutually exclusive and EffectPoolBalance is the resulting
// cursor balance for an APPLIED mutation. A NOOP replay has zero pool delta and
// echoes the caller's already-persisted balance.
type RoundEconomicEffectApplyReceipt struct {
	EffectIndex       uint32
	EffectKind        shared.RoundEconomicEffectKindV1
	RequestedAmount   shared.Amount
	AppliedAmount     shared.Amount
	UnfilledAmount    shared.Amount
	EffectPoolCredit  shared.Amount
	EffectPoolDebit   shared.Amount
	EffectPoolBalance shared.Amount
	SlashSummaryID    []byte
	Status            shared.MutationStatusV1
}

type BeaconSnapshot struct {
	Height      int64
	Randomness  []byte
	SourceTag   string
	ProofDigest []byte
}

type TaskEarningsCredit struct {
	Beneficiary string
	Amount      shared.Amount
}

// HubKeeper is the single Task-to-Hub owner boundary for the fresh v0.3 state.
type HubKeeper interface {
	GetNodeJailStatus(ctx sdk.Context, addr sdk.AccAddress, duty string) JailStatus
	GetNodeTombstone(ctx sdk.Context, addr sdk.AccAddress) bool
	GetCortexNode(ctx sdk.Context, addr sdk.AccAddress) (CortexNodeSnapshot, bool)
	GetProfileCapability(ctx sdk.Context, provider sdk.AccAddress, modelID string, profileVersion uint32) (ProfileCapabilitySnapshot, bool)
	GetModelSupport(ctx sdk.Context, provider sdk.AccAddress, modelID string, profileVersion uint32) (ModelSupportSnapshot, bool)

	GetCandidatePoolSnapshot(ctx sdk.Context, snapshotID []byte) (CandidatePoolSnapshotState, bool)
	CurrentActiveCandidatePool(ctx context.Context) (CandidatePoolSnapshotState, error)
	ResolveCandidatePoolMember(ctx context.Context, snapshotID []byte, slot uint32) (CandidatePoolMemberState, CandidateSlotBindingState, error)
	CandidatePoolSegmentBitmap(ctx context.Context, snapshotID []byte, segmentIndex uint32) ([]byte, error)
	GetCandidatePoolLayout(ctx context.Context, snapshotID []byte) (slotCapacity, segmentBytes, segmentCount uint32, ok bool)
	HasCandidatePoolTaskRef(ctx context.Context, taskID, snapshotID []byte) (bool, error)
	AcquireCandidatePoolTaskRef(ctx context.Context, taskID, snapshotID []byte, height uint64) (bool, error)
	ReleaseCandidatePoolTaskRef(ctx context.Context, taskID, snapshotID []byte, height uint64) (bool, error)

	GetModelStatus(ctx sdk.Context, modelID string) ModelStatus
	GetProfileState(ctx sdk.Context, modelID string, profileVersion uint32) (ProfileStateSnapshot, bool)
	IsProfileFrozen(ctx sdk.Context, modelID string, profileVersion uint32) bool
	IsFreezeFailureWindowProtected(ctx context.Context, modelID string, profileVersion uint32, finalityHeight uint64) (bool, error)
	GetHubParams(ctx sdk.Context) HubParamsSnapshot
	GetEndBlockBudget(ctx context.Context, height uint64) (EndBlockBudgetSnapshot, error)
	ConsumeEndBlockBudget(ctx context.Context, height, visitedItems, serializedBytes uint64) error

	GetServiceBond(ctx sdk.Context, cortexNode sdk.AccAddress, orderValue uint64) (ServiceBondSnapshot, bool)
	GetRoleScoringSnapshot(ctx sdk.Context, roleAddress sdk.AccAddress, role string) (RoleScoringSnapshot, error)
	ReserveTaskLiabilityFromFrozenFact(ctx context.Context, req FrozenFactLiabilityRequest) (TaskLiabilityReservationState, error)
	GetTaskLiabilityReservation(ctx context.Context, taskID []byte, duty shared.Duty, operatorAddress string) (TaskLiabilityReservationSnapshot, error)
	RecordTaskSupportCompletion(ctx context.Context, fact TaskSupportCompletionFact) error
	ApplyTaskRoleFault(ctx context.Context, fact TaskRoleFaultFact) (RoleFaultState, error)
	ApplyWorkerObjectiveEvidence(ctx context.Context, fact shared.WorkerObjectiveEvidenceFactV1) (RoleFaultState, error)
	ApplyRoundEconomicEffect(ctx context.Context, req RoundEconomicEffectApplyRequest) (RoundEconomicEffectApplyReceipt, error)
	ReleaseTaskLiabilities(ctx context.Context, sessionID, taskID string, height uint64) error
	DeleteOneClosedTaskLiability(ctx context.Context, taskID string) (bool, error)

	GetCurrentServiceKey(ctx context.Context, participantType, operatorAddress string) (CurrentServiceKeySnapshot, error)
	GetBuilderEvidenceProofKey(ctx context.Context, operatorAddress string, authorizationNonce uint64) (CurrentServiceKeySnapshot, error)
	GetWorkerEvidenceProofKey(ctx context.Context, sessionIDHex, taskIDHex, operatorAddress string) (CurrentServiceKeySnapshot, error)
	GetBuilderObjectiveEvidenceCurrentBinding(ctx context.Context, operatorAddress string) (CurrentServiceKeySnapshot, error)
	ReserveServiceKeyResponsibility(ctx context.Context, responsibility ServiceKeyResponsibilityState) error
	ReleaseServiceKeyResponsibility(ctx context.Context, participantType, operatorAddress, responsibilityID string) error
	ReleaseServiceKeyResponsibilities(ctx context.Context, sessionID, taskID string) error
	AcquireBusObjectiveEvidenceResponsibility(ctx context.Context, locator shared.BusObjectiveEvidenceResponsibilityV1) (shared.BusObjectiveEvidenceResponsibilityReceiptV1, error)
	ReleaseBusObjectiveEvidenceResponsibility(ctx context.Context, locator shared.BusObjectiveEvidenceResponsibilityV1) (shared.BusObjectiveEvidenceResponsibilityReceiptV1, error)
	AcquirePendingStageDuty(ctx context.Context, operatorAddress string) error
	ReleasePendingStageDuty(ctx context.Context, operatorAddress string) error
	VerifyCurrentCortexServiceDigest(ctx context.Context, operatorAddress string, signature, signingDigest []byte, height uint64) error

	GetBuilderSetForHeight(ctx sdk.Context, height uint64) (BuilderSetState, error)
	GetBuilderSetByID(ctx sdk.Context, builderSetID string) (BuilderSetState, error)
	HasBuilderSetTaskRef(ctx context.Context, taskID []byte, builderSetID string) (bool, error)
	AcquireBuilderSetTaskRef(ctx context.Context, taskID []byte, builderSetID string, builderSetHash []byte, height uint64) (bool, error)
	ReleaseBuilderSetTaskRef(ctx context.Context, taskID []byte, builderSetID string, builderSetHash []byte, height uint64) (bool, error)
	GetBlockAnchorHash(ctx context.Context, height uint64) ([]byte, error)

	GetBeaconForDomain(ctx sdk.Context, domain string, height int64) (BeaconSnapshot, bool)
	AcquireBeaconConsumerRef(ctx context.Context, height uint64, kind BeaconConsumerKind, consumerID string) error
	ReleaseBeaconConsumerRef(ctx context.Context, height uint64, kind BeaconConsumerKind, consumerID string) error
	ResolveEffectiveParameterBucket(ctx context.Context, kind shared.BucketKind, bucketKey string, height uint64) (ParameterBucketVersionState, error)
	GetParameterBucketVersion(ctx context.Context, kind shared.BucketKind, bucketKey string, version uint64) (ParameterBucketVersionState, error)
	AcquireParameterBucketTaskRef(ctx context.Context, kind shared.BucketKind, bucketKey string, version, height uint64) error
	ReleaseParameterBucketTaskRef(ctx context.Context, kind shared.BucketKind, bucketKey string, version, height uint64) error

	CreditTaskSettlementEarnings(ctx context.Context, sessionID, taskID []byte, credits []TaskEarningsCredit, height uint64) error
	CreditTaskMaintenance(ctx context.Context, settlementID []byte, amount shared.Amount, height uint64) error
	RecordProfilePriceSample(ctx context.Context, sample shared.ProfilePriceSampleV1) error
	ApplyBuilderObjectiveEvidence(ctx context.Context, fact shared.BuilderObjectiveEvidenceFactV2) (shared.BuilderObjectiveEvidenceReceiptV2, error)
}
