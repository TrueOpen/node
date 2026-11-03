package keeper

import (
	"testing"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestUpdateTaskParamsWritesVersionHashAndStatus(t *testing.T) {
	f := initInternalFixture(t)
	authority, err := f.keeper.addressCodec.BytesToString(f.keeper.authority)
	require.NoError(t, err)
	next, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	next.Generation.TopKMax++

	response, err := NewMsgServerImpl(f.keeper).UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{
		ExpectedVersion: 0,
		Params:          next,
		Authority:       authority,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), response.NewVersion)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Status)
	expectedHash, err := types.TaskParamsHashV1(sdk.UnwrapSDKContext(f.ctx).ChainID(), 1, next)
	require.NoError(t, err)
	require.Equal(t, expectedHash, response.ParamsHash)

	meta, err := f.keeper.GetTaskParamsMeta(f.ctx)
	require.NoError(t, err)
	require.Equal(t, response.NewVersion, meta.ParamsVersion)
	require.Equal(t, response.ParamsHash, meta.ParamsHash)
	stored, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, next, stored)
}

func TestUpdateTaskParamsRejectsStaleVersionWithoutWrites(t *testing.T) {
	f := initInternalFixture(t)
	authority, err := f.keeper.addressCodec.BytesToString(f.keeper.authority)
	require.NoError(t, err)
	before, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	next := before
	next.Batch.MaxBatchCommitItems++

	_, err = NewMsgServerImpl(f.keeper).UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{
		ExpectedVersion: 1,
		Params:          next,
		Authority:       authority,
	})
	require.ErrorIs(t, err, types.ErrTaskParamsVersionMismatch)
	after, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestUpdateTaskParamsRejectsEpochSummaryBudgetChanges(t *testing.T) {
	f := initInternalFixture(t)
	authority, err := f.keeper.addressCodec.BytesToString(f.keeper.authority)
	require.NoError(t, err)
	before, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	next := before
	next.Cleanup.MaxEpochTaskSummaryBytes++

	_, err = NewMsgServerImpl(f.keeper).UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{
		ExpectedVersion: 0,
		Params:          next,
		Authority:       authority,
	})
	require.ErrorIs(t, err, types.ErrTaskParamsRuntimeImmutable)
	after, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestUpdateTaskParamsRejectsGenesisOnlyWeightGeometry(t *testing.T) {
	f := initInternalFixture(t)
	authority, err := f.keeper.addressCodec.BytesToString(f.keeper.authority)
	require.NoError(t, err)
	next, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	next.Weights.SelectedVerifierCount++

	_, err = NewMsgServerImpl(f.keeper).UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{
		ExpectedVersion: 0,
		Params:          next,
		Authority:       authority,
	})
	require.Error(t, err)
}

func TestGetTaskSafetyWindowsUsesActivePhaseOneClocks(t *testing.T) {
	f := initInternalFixture(t)
	windows, err := f.keeper.GetTaskSafetyWindows(f.ctx)
	require.NoError(t, err)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, windows.ChallengeResolveWindowBlocks, params.Dispatch.RecordDeadlineBlocks+params.Dispatch.DispatchCompensationClaimWindowBlocks+params.Dispatch.DispatchCompensationRebuttalBlocks)
	require.Equal(t, params.Evidence.VerifierValueEvidenceWindowBlocks, windows.EvidenceResponseWindowBlocks)
	require.Equal(t, params.Deadlines.VerifyOpenDeadlineBlocks, windows.VerifyOpenDeadlineBlocks)
}

func TestTaskSafetyClockRetainsReferencedParameterVersions(t *testing.T) {
	f := initInternalFixture(t)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.Deadlines.VerifyOpenDeadlineBlocks = 400
	params.Dispatch.MaxOrderLifetimeBlocks = 160
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	taskID := taskKeyFromByte(0x93)
	require.NoError(t, f.keeper.PinTaskParams(f.ctx, taskID))
	params.Deadlines.VerifyOpenDeadlineBlocks = 500
	params.Dispatch.MaxOrderLifetimeBlocks = 120
	_, err = NewMsgServerImpl(f.keeper).UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{Authority: sdk.AccAddress(f.keeper.authority).String(), Params: params})
	require.NoError(t, err)
	windows, err := f.keeper.GetTaskSafetyWindows(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(400), windows.VerifyOpenDeadlineBlocks)
	require.Equal(t, uint64(161), windows.MinimumBuilderSetLeadBlocks)
	require.NoError(t, f.keeper.UnpinTaskParams(f.ctx, taskID))
	windows, err = f.keeper.GetTaskSafetyWindows(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(500), windows.VerifyOpenDeadlineBlocks)
	require.Equal(t, uint64(121), windows.MinimumBuilderSetLeadBlocks)
}
