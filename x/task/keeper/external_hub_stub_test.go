package keeper_test

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	types "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

type stubHubKeeper struct{ tasktypes.HubKeeper }

func (stubHubKeeper) AcquireBeaconConsumerRef(context.Context, uint64, types.BeaconConsumerKind, string) error {
	return nil
}

func (stubHubKeeper) ReleaseBeaconConsumerRef(context.Context, uint64, types.BeaconConsumerKind, string) error {
	return nil
}

func (stubHubKeeper) GetCandidatePoolLayout(context.Context, []byte) (uint32, uint32, uint32, bool) {
	return 8, 1, 1, true
}

func (stubHubKeeper) GetCandidatePoolSnapshot(_ sdk.Context, snapshotID []byte) (types.CandidatePoolSnapshotState, bool) {
	return types.CandidatePoolSnapshotState{
		SnapshotId: append([]byte(nil), snapshotID...),
		PoolHash:   repeatByte(0x44),
	}, true
}

func (stubHubKeeper) HasCandidatePoolTaskRef(context.Context, []byte, []byte) (bool, error) {
	return true, nil
}

func (stubHubKeeper) GetNodeJailStatus(sdk.Context, sdk.AccAddress, string) types.JailStatus {
	return ""
}
func (stubHubKeeper) GetNodeTombstone(sdk.Context, sdk.AccAddress) bool { return false }
func (stubHubKeeper) GetCortexNode(sdk.Context, sdk.AccAddress) (types.CortexNodeSnapshot, bool) {
	return types.CortexNodeSnapshot{}, false
}
func (stubHubKeeper) GetProfileCapability(sdk.Context, sdk.AccAddress, string, uint32) (types.ProfileCapabilitySnapshot, bool) {
	return types.ProfileCapabilitySnapshot{}, false
}
func (stubHubKeeper) GetModelSupport(sdk.Context, sdk.AccAddress, string, uint32) (types.ModelSupportSnapshot, bool) {
	return types.ModelSupportSnapshot{}, false
}
func (stubHubKeeper) GetModelStatus(sdk.Context, string) types.ModelStatus {
	return types.ModelStatusUnspecified
}
func (stubHubKeeper) GetProfileState(sdk.Context, string, uint32) (types.ProfileStateSnapshot, bool) {
	return types.ProfileStateSnapshot{}, false
}
func (stubHubKeeper) IsProfileFrozen(sdk.Context, string, uint32) bool { return false }
func (stubHubKeeper) IsEmergencyFrozen(sdk.Context) bool               { return false }
func (stubHubKeeper) GetHubParams(sdk.Context) types.HubParamsSnapshot {
	params := types.DefaultHubParams()
	return types.HubParamsSnapshot{
		DeltaWBlocks:                          params.Epoch.DeltaWBlocks,
		ServiceUnbondingPeriodBlocks:          params.Service.ServiceUnbondingPeriodBlocks,
		UnbondingSlashSafetyMarginBlocks:      params.Service.UnbondingSlashSafetyMarginBlocks,
		CandidateSlotHardCapacity:             params.CandidatePool.CandidateSlotHardCapacity,
		OpenVerifyBuilderProposalWindowBlocks: params.Builder.OpenVerifyBuilderProposalWindowBlocks,
		MaxQueryPageTokenBytes:                params.QueryEvent.MaxQueryPageTokenBytes,
		MaxQueryPageLimit:                     params.QueryEvent.MaxQueryPageLimit,
		MaxQueryResponseBytes:                 params.QueryEvent.MaxQueryResponseBytes,
		MaxEndblockVisitedItemsTotal:          params.QueryEvent.MaxEndblockVisitedItemsTotal,
	}
}
func (stubHubKeeper) GetEndBlockBudget(context.Context, uint64) (types.EndBlockBudgetSnapshot, error) {
	return types.EndBlockBudgetSnapshot{RemainingItems: 10_000, RemainingBytes: 1 << 20}, nil
}
func (stubHubKeeper) ConsumeEndBlockBudget(context.Context, uint64, uint64, uint64) error {
	return nil
}
func (stubHubKeeper) GetServiceBond(sdk.Context, sdk.AccAddress, uint64) (types.ServiceBondSnapshot, bool) {
	return types.ServiceBondSnapshot{}, false
}
func (stubHubKeeper) GetRoleScoringSnapshot(sdk.Context, sdk.AccAddress, string) (types.RoleScoringSnapshot, error) {
	return types.RoleScoringSnapshot{PerformanceScorePpm: types.PerformanceScoreDefaultPpm, PerformanceVersion: types.PerformanceScoreMethodVersionV1}, nil
}
func (stubHubKeeper) ReleaseTaskLiabilities(context.Context, string, string, uint64) error {
	return nil
}

func (stubHubKeeper) DeleteOneClosedTaskLiability(context.Context, string) (bool, error) {
	return false, nil
}
func (stubHubKeeper) GetCurrentServiceKey(_ context.Context, participantType, operatorAddress string) (types.CurrentServiceKeySnapshot, error) {
	return types.CurrentServiceKeySnapshot{ParticipantType: participantType, OperatorAddress: operatorAddress, ServiceAddress: operatorAddress, Status: types.ServiceKeyStatusActive}, nil
}
func (stubHubKeeper) GetBuilderEvidenceProofKey(_ context.Context, operatorAddress string, authorizationNonce uint64) (types.CurrentServiceKeySnapshot, error) {
	return types.CurrentServiceKeySnapshot{ParticipantType: shared.ParticipantTypeBuilder, OperatorAddress: operatorAddress, AuthorizationNonce: authorizationNonce, Status: types.ServiceKeyStatusActive}, nil
}
func (stubHubKeeper) ApplyBuilderObjectiveEvidence(_ context.Context, fact shared.BuilderObjectiveEvidenceFactV2) (shared.BuilderObjectiveEvidenceReceiptV2, error) {
	return shared.BuilderObjectiveEvidenceReceiptV2{EvidenceId: append([]byte(nil), fact.CanonicalEvidenceDigest...), Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}
func (stubHubKeeper) ReserveServiceKeyResponsibility(context.Context, types.ServiceKeyResponsibilityState) error {
	return nil
}
func (stubHubKeeper) ReleaseServiceKeyResponsibility(context.Context, string, string, string) error {
	return nil
}
func (stubHubKeeper) ReleaseServiceKeyResponsibilities(context.Context, string, string) error {
	return nil
}
func (stubHubKeeper) VerifyCurrentCortexServiceDigest(context.Context, string, []byte, []byte, uint64) error {
	return nil
}
func (stubHubKeeper) HasBuilderSetTaskRef(context.Context, []byte, string) (bool, error) {
	return true, nil
}
func (stubHubKeeper) GetBuilderSetByID(_ sdk.Context, builderSetID string) (types.BuilderSetState, error) {
	return types.BuilderSetState{BuilderSetId: builderSetID, BuilderSetHash: repeatByte(0x77)}, nil
}
func (stubHubKeeper) GetBeaconForDomain(sdk.Context, string, int64) (types.BeaconSnapshot, bool) {
	return types.BeaconSnapshot{}, false
}

// taskKeyOf names the raw 32-byte task store key since X-16. It is deliberately
// not called hexTaskKey any more: the result is a []byte digest, so it must never
// reach a %s verb or be used as a Go map key without an explicit hex rendering.
func taskKeyOf(taskID []byte) tasktypes.TaskKey { return tasktypes.NewTaskKey(taskID) }
