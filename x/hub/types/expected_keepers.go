package types

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// BankKeeper is the concrete custody boundary required by Hub-owned rewards,
// bonds, challenge effects, emission and treasury accounts.
type BankKeeper interface {
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
	SendCoinsFromAccountToModule(ctx context.Context, from sdk.AccAddress, moduleName string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, moduleName string, to sdk.AccAddress, amt sdk.Coins) error
	SendCoinsFromModuleToModule(ctx context.Context, fromModule, toModule string, amt sdk.Coins) error
	// GetSupply is the authoritative left-hand side of I-BRIDGE-2
	// (cross_chain_asset_bridge_protocol.md §5.2). The bridge recomputes the identity
	// against the bank
	// rather than trusting its own cumulative counters, so a mint or burn that
	// escaped the decorated path shows up as a broken invariant instead of a
	// self-consistent lie.
	GetSupply(ctx context.Context, denom string) sdk.Coin
}

// AuthKeeper resolves account public keys for detached message signatures.
type AuthKeeper interface {
	GetAccount(context.Context, sdk.AccAddress) sdk.AccountI
}

// FreezeSignalTaskValidator exposes the Task-owned final-failure feed without
// allowing Hub to read Task collections directly. Every call visits at most
// Limit index rows and resumes strictly after LastIndexKey.
type FreezeSignalTaskValidator interface {
	ScanFreezeSignalFailures(context.Context, FreezeSignalFailureScanRequest) (FreezeSignalFailureScanResult, error)
}

// RoleFaultConsumerGate is the Task-owned finality authority used by the Hub
// RoleFault prune sweep. A true result means the task reached finality, every
// challenge/economic-effect consumer is closed, and the retained failure row
// matches this exact task/evidence scope.
type RoleFaultConsumerGate interface {
	RoleFaultConsumersClosed(context.Context, []byte, []byte) (bool, error)
}

type FreezeSignalFailureScanRequest struct {
	ModelID               string
	ProfileVersion        uint32
	RiskWindowStartHeight uint64
	RiskWindowEndHeight   uint64
	LastIndexKey          []byte
	Limit                 uint32
}

// FreezeFailureClass is the closed cross-module projection of the Task failure
// classes consumed by EmergencyFreeze. Values intentionally match the frozen
// task.v1.TaskFailureClass numbers.
type FreezeFailureClass uint32

const (
	FreezeFailureClassInsufficientVerifier  FreezeFailureClass = 2
	FreezeFailureClassMetricThresholdBreach FreezeFailureClass = 3
	FreezeFailureClassObjectiveFault        FreezeFailureClass = 4
	FreezeFailureClassSchemaFault           FreezeFailureClass = 5
	FreezeFailureClassWorkerEvidenceFault   FreezeFailureClass = 6
)

type FreezeSignalFailure struct {
	TaskID         []byte
	SettlementID   []byte
	FinalityHeight uint64
	FailureClass   FreezeFailureClass
	EvidenceDigest []byte
	IncludedInRoot bool
}

type FreezeSignalFailureScanResult struct {
	Failures     []FreezeSignalFailure
	LastIndexKey []byte
	Visited      uint32
	Done         bool
}

// ValidatorSnapshotProvider resolves immutable validator-set facts captured at
// freeze-signal OPEN time. The vote lookup starts from the validator operator
// signer and returns its historical consensus identity and voting power.
type ValidatorSnapshotProvider interface {
	HistoricalEntries(ctx context.Context) (uint32, error)
	CaptureValidatorSetSnapshot(ctx sdk.Context, height uint64) (ValidatorSnapshot, error)
	GetValidatorSnapshotMemberBySigner(ctx sdk.Context, snapshot ValidatorSnapshot, signerAddress string) (ValidatorSnapshotMember, bool, error)
}

type ValidatorSnapshot struct {
	Height           uint64
	ValidatorSetHash []byte
	TotalVotingPower uint64
}

type ValidatorSnapshotMember struct {
	ConsensusAddress []byte
	SignerAddress    string
	VotingPower      uint64
}

// TaskSafetyWindowProvider exposes the task-owned challenge windows required
// to validate the service-bond slashability horizon.
type TaskSafetyWindowProvider interface {
	GetTaskSafetyWindows(context.Context) (TaskSafetyWindows, error)
}

type TaskSafetyWindows struct {
	ChallengeResolveWindowBlocks uint64
	EvidenceResponseWindowBlocks uint64
}

type MsgServerDependencies struct {
	FreezeTaskValidator       FreezeSignalTaskValidator
	ValidatorSnapshotProvider ValidatorSnapshotProvider
	TaskSafetyWindowProvider  TaskSafetyWindowProvider
	RoleFaultConsumerGate     RoleFaultConsumerGate
}
