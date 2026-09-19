package keeper

import (
	"context"
	"crypto/sha256"
	"testing"

	storetypes "cosmossdk.io/store/types"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type internalFixture struct {
	ctx    context.Context
	keeper Keeper
}

func ensureInternalFixtureChainID(f *internalFixture) {
	sdkCtx := sdk.UnwrapSDKContext(f.ctx)
	if sdkCtx.ChainID() == "" {
		f.ctx = sdk.WrapSDKContext(sdkCtx.WithChainID("trueopen-test-1"))
	}
}

type internalStubHubKeeper struct{ types.HubKeeper }

func (internalStubHubKeeper) AcquireBeaconConsumerRef(context.Context, uint64, hubtypes.BeaconConsumerKind, string) error {
	return nil
}

func (internalStubHubKeeper) ReleaseBeaconConsumerRef(context.Context, uint64, hubtypes.BeaconConsumerKind, string) error {
	return nil
}

func (internalStubHubKeeper) IsFreezeFailureWindowProtected(context.Context, string, uint32, uint64) (bool, error) {
	return false, nil
}

func (internalStubHubKeeper) GetRoleScoringSnapshot(sdk.Context, sdk.AccAddress, string) (hubtypes.RoleScoringSnapshot, error) {
	return hubtypes.RoleScoringSnapshot{PerformanceScorePpm: hubtypes.PerformanceScoreDefaultPpm, PerformanceVersion: hubtypes.PerformanceScoreMethodVersionV1}, nil
}

func (internalStubHubKeeper) GetBeaconForDomain(sdk.Context, string, int64) (hubtypes.BeaconSnapshot, bool) {
	return hubtypes.BeaconSnapshot{}, false
}

func (internalStubHubKeeper) GetCandidatePoolLayout(context.Context, []byte) (uint32, uint32, uint32, bool) {
	return 8, 1, 1, true
}

// Four tests were deleted by the earlier schema stub pass because the
// production functions they covered are gone. What must come back with them:
//
//  1. TestValidateInferReceiptFactsAllowsOptionalBatchLogRoot covered
//     validateInferReceiptFacts: every required infer-receipt field is canonical
//     lowercase 64-hex, batch_log_root is the only optional one, and a non-hex
//     output_hash is rejected with "output_hash must be canonical lowercase sha256
//     hex". Six of those fields left the wire and the rest became raw Hash32 bytes,
//     so verification coverage must re-assert the byte-length form (types.Hash32Len) plus non-zero
//     output_size_bytes against MsgSubmitInferReceipt. The frozen preimage itself
//     stays covered by x/task/types (TestInferReceiptSigningDigestReadsEveryPreimageField
//     + signature_test.go), which is green.
//  2. TestValidateSweepCompatibilityReasonRejectsIgnoredValue covered
//     validateSweepCompatibilityReason: MsgSweepExpiredTask.reason had to be empty so
//     no caller could pick the failure class by hand. MsgSweepExpiredTask is deleted;
//     MsgSweepDeadline coverage must re-assert that the Keeper derives the failure
//     class from accepted deadline state and takes no caller-supplied reason.
//  3. TestNormalizeUserChallengeKind covered normalizeUserChallengeKind: empty
//     defaults to USER_REVALIDATION and a leading space is rejected. The whole
//     challenge Msg surface is 0 in V1 (K-BLOCK-03/04), so no public path exists.
//  4. TestFrozenVerifyDeadlineHeightUsesAssignmentBucketVersion covered
//     frozenVerifyDeadlineHeight: the verify deadline is derived from the timeout
//     bucket *version frozen on the assignment*, and a version that does not exist
//     is an invariant break. TimeoutBucket is deleted from this module (the bodies
//     live in x/hub and Task keeps only TaskBucketRef), so cross-module tests must assert
//     the same "frozen version, not current version" property across the Hub boundary.

func (internalStubHubKeeper) GetCortexNode(_ sdk.Context, address sdk.AccAddress) (hubtypes.CortexNodeSnapshot, bool) {
	return hubtypes.CortexNodeSnapshot{
		OperatorAddress:  address.String(),
		ServiceKeyStatus: hubtypes.ServiceKeyStatusActive,
	}, true
}

func (internalStubHubKeeper) GetServiceBond(_ sdk.Context, address sdk.AccAddress, _ uint64) (hubtypes.ServiceBondSnapshot, bool) {
	return hubtypes.ServiceBondSnapshot{
		OperatorAddress:       address.String(),
		Status:                hubtypes.ServiceBondStatusActive,
		ActiveBond:            1_000_000,
		AvailableBond:         1_000_000,
		RequiredTaskLiability: 30_000,
		BondVersion:           1,
	}, true
}

func (internalStubHubKeeper) ReleaseTaskLiabilities(context.Context, string, string, uint64) error {
	return nil
}

func (internalStubHubKeeper) ApplyTaskRoleFault(_ context.Context, fact hubtypes.TaskRoleFaultFact) (hubtypes.RoleFaultState, error) {
	return stubAppliedRoleFault(fact), nil
}

// stubAppliedRoleFault mirrors the shape the real Hub returns: a deterministic
// Hash32 fault_id over the identity fields, so a stubbed §6.6 fault vector is
// stable, unique per (task, operator, fault_type) and non-zero.
func stubAppliedRoleFault(fact hubtypes.TaskRoleFaultFact) hubtypes.RoleFaultState {
	faultID := sha256.Sum256(append(append([]byte(nil), fact.TaskID...),
		[]byte(fact.OperatorAddress+"|"+fact.FaultType+"|"+fact.Duty.String())...))
	return hubtypes.RoleFaultState{
		FaultId: faultID[:], TaskId: append([]byte(nil), fact.TaskID...),
		OperatorAddress: fact.OperatorAddress, Duty: fact.Duty,
		FaultClass:           hubtypes.FaultKind_FAULT_KIND_TIMEOUT,
		ClassificationSource: fact.ClassificationSource,
		EvidenceDigest:       append([]byte(nil), fact.EvidenceDigest...),
		RecordedHeight:       fact.Height,
		Status:               hubtypes.RoleFaultStatus_ROLE_FAULT_STATUS_CONFIRMED,
	}
}

func (internalStubHubKeeper) DeleteOneClosedTaskLiability(context.Context, string) (bool, error) {
	return false, nil
}

func (internalStubHubKeeper) DeleteFinalizedSettlementReceipts(context.Context, string, string, string) error {
	return nil
}

func (internalStubHubKeeper) GetCurrentServiceKey(_ context.Context, participantType, operatorAddress string) (hubtypes.CurrentServiceKeySnapshot, error) {
	return hubtypes.CurrentServiceKeySnapshot{ParticipantType: participantType, OperatorAddress: operatorAddress, ServiceAddress: operatorAddress, AuthorizationNonce: 1, Status: hubtypes.ServiceKeyStatusActive}, nil
}

func (internalStubHubKeeper) GetBuilderObjectiveEvidenceCurrentBinding(_ context.Context, operatorAddress string) (hubtypes.CurrentServiceKeySnapshot, error) {
	return hubtypes.CurrentServiceKeySnapshot{ParticipantType: shared.ParticipantTypeBuilder, OperatorAddress: operatorAddress, ServiceAddress: operatorAddress, AuthorizationNonce: 1, Status: hubtypes.ServiceKeyStatusActive}, nil
}

func (internalStubHubKeeper) GetBuilderEvidenceProofKey(_ context.Context, operatorAddress string, authorizationNonce uint64) (hubtypes.CurrentServiceKeySnapshot, error) {
	return hubtypes.CurrentServiceKeySnapshot{ParticipantType: shared.ParticipantTypeBuilder, OperatorAddress: operatorAddress, AuthorizationNonce: authorizationNonce, Status: hubtypes.ServiceKeyStatusActive}, nil
}

func (internalStubHubKeeper) ApplyBuilderObjectiveEvidence(_ context.Context, fact shared.BuilderObjectiveEvidenceFactV2) (shared.BuilderObjectiveEvidenceReceiptV2, error) {
	return shared.BuilderObjectiveEvidenceReceiptV2{EvidenceId: append([]byte(nil), fact.CanonicalEvidenceDigest...), Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

func (internalStubHubKeeper) AcquireBusObjectiveEvidenceResponsibility(_ context.Context, locator shared.BusObjectiveEvidenceResponsibilityV1) (shared.BusObjectiveEvidenceResponsibilityReceiptV1, error) {
	return stubBusObjectiveEvidenceResponsibilityReceipt(locator, true), nil
}

func (internalStubHubKeeper) ReleaseBusObjectiveEvidenceResponsibility(_ context.Context, locator shared.BusObjectiveEvidenceResponsibilityV1) (shared.BusObjectiveEvidenceResponsibilityReceiptV1, error) {
	return stubBusObjectiveEvidenceResponsibilityReceipt(locator, false), nil
}

func stubBusObjectiveEvidenceResponsibilityReceipt(locator shared.BusObjectiveEvidenceResponsibilityV1, active bool) shared.BusObjectiveEvidenceResponsibilityReceiptV1 {
	id := sha256.Sum256(append(append(append([]byte(nil), locator.SessionId...), locator.TaskId...), []byte(locator.BuilderOperator)...))
	return shared.BusObjectiveEvidenceResponsibilityReceiptV1{
		ResponsibilityId: id[:], BuilderOperator: locator.BuilderOperator,
		TaskId: append([]byte(nil), locator.TaskId...), ServiceAuthorizationNonce: locator.ServiceAuthorizationNonce,
		Active: active, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}
}

func (internalStubHubKeeper) ReserveServiceKeyResponsibility(context.Context, hubtypes.ServiceKeyResponsibilityState) error {
	return nil
}

func (internalStubHubKeeper) ReleaseServiceKeyResponsibility(context.Context, string, string, string) error {
	return nil
}

func (internalStubHubKeeper) ReleaseServiceKeyResponsibilities(context.Context, string, string) error {
	return nil
}

func (internalStubHubKeeper) AcquirePendingStageDuty(context.Context, string) error { return nil }
func (internalStubHubKeeper) ReleasePendingStageDuty(context.Context, string) error { return nil }

func (internalStubHubKeeper) VerifyCurrentCortexServiceDigest(context.Context, string, []byte, []byte, uint64) error {
	return nil
}

func (internalStubHubKeeper) GetProfileState(sdk.Context, string, uint32) (hubtypes.ProfileStateSnapshot, bool) {
	return hubtypes.ProfileStateSnapshot{
		Status:                    hubtypes.ModelStatusActive,
		ChallengeOpenWindowBlocks: hubtypes.ProfileChallengeOpenWindowMinBlocks,
	}, true
}

func (internalStubHubKeeper) GetHubParams(sdk.Context) hubtypes.HubParamsSnapshot {
	params := hubtypes.DefaultHubParams()
	return hubtypes.HubParamsSnapshot{
		EpochLengthBlocks:                     params.Epoch.EpochLengthBlocks,
		ServiceUnbondingPeriodBlocks:          params.Service.ServiceUnbondingPeriodBlocks,
		UnbondingSlashSafetyMarginBlocks:      params.Service.UnbondingSlashSafetyMarginBlocks,
		DeltaWBlocks:                          params.Epoch.DeltaWBlocks,
		OpenVerifyBuilderProposalWindowBlocks: params.Builder.OpenVerifyBuilderProposalWindowBlocks,
		// BuildAssignmentCandidateSet bounds the pool body with the registered
		// candidate_slot_hard_capacity, so the stub has to project it too or every
		// pool looks like a count/body mismatch.
		CandidateSlotHardCapacity:         params.CandidatePool.CandidateSlotHardCapacity,
		BuildersPerTask:                   params.Builder.BuildersPerTask,
		FreezeFailureIndexRetentionBlocks: params.Freeze.FreezeFailureIndexRetentionBlocks,
		MaxQueryPageTokenBytes:            params.QueryEvent.MaxQueryPageTokenBytes,
		MaxQueryPageLimit:                 params.QueryEvent.MaxQueryPageLimit,
		MaxQueryResponseBytes:             params.QueryEvent.MaxQueryResponseBytes,
		MaxEndblockVisitedItemsTotal:      params.QueryEvent.MaxEndblockVisitedItemsTotal,
	}
}

func (internalStubHubKeeper) GetEndBlockBudget(context.Context, uint64) (hubtypes.EndBlockBudgetSnapshot, error) {
	return hubtypes.EndBlockBudgetSnapshot{RemainingItems: 10_000, RemainingBytes: 1 << 20}, nil
}

func (internalStubHubKeeper) ConsumeEndBlockBudget(context.Context, uint64, uint64, uint64) error {
	return nil
}

func initInternalFixture(t *testing.T) *internalFixture {
	return initInternalFixtureWithHub(t, internalStubHubKeeper{})
}

func initInternalFixtureWithHub(t *testing.T, hub types.HubKeeper) *internalFixture {
	t.Helper()

	encCfg := moduletestutil.MakeTestEncodingConfig()
	addressCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	storeService := runtime.NewKVStoreService(storeKey)
	transientKey := storetypes.NewTransientStoreKey("transient_internal_test")
	transientStoreService := runtime.NewTransientStoreService(transientKey)
	ctx := testutil.DefaultContextWithDB(t, storeKey, transientKey).Ctx
	authority := authtypes.NewModuleAddress(types.GovModuleName)

	k := NewKeeper(storeService, transientStoreService, encCfg.Codec, addressCodec, authority, taskInternalAuthKeeper{}, taskInternalBankKeeper{}, hub)
	require.NoError(t, k.Params.Set(ctx, types.DefaultGenesis().Params))
	return &internalFixture{ctx: ctx, keeper: k}
}

// Three stream/order tests were removed by the earlier schema stub pass. Their
// successors live in session_runtime_test.go against the current wire
// (TestSetAndReplaceStreamStateMaintainLifecycleIndexes ->
// TestSessionLifecycleStaleRowConsumesVisitedBudget /
// TestSessionLifecycleClosesAndSchedulesHistoryPrune,
// TestCloseStreamPendingTaskUpdatesCountHeightAndLifecycleIndex and
// TestOrderSequenceConsumeAndFinalize ->
// TestTaskBudgetIsSoleLedgerAndFullRefundIsIdempotent). The deleted versions
// built StreamState/OrderSequenceState with string session_id / task_id and the
// deleted TaskAssignment alias, which Ruling 23 removed.
