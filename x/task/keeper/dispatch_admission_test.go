package keeper

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func dispatchEntryItem(entry *types.DispatchEntryV1) *types.DispatchRecordEntryV1 {
	return &types.DispatchRecordEntryV1{Entry: &types.DispatchRecordEntryV1_Dispatch{Dispatch: entry}}
}

func TestDispatchAcceptsRecoverableJailedWinner(t *testing.T) {
	f := newDispatchFixture(t)
	f.hub.bondStatus = hubtypes.ServiceBondStatusJailed
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[0].Status)
	record, err := f.keeper.readDispatchRecord(f.ctx, entry.Record.TaskId)
	require.NoError(t, err)
	require.Equal(t, entry.WinnerTicket.Target.OperatorAddress, record.Winner.OperatorAddress)
}

func TestRecordDispatchFreezesPrefundAndReplaysOnce(t *testing.T) {
	f := newDispatchFixture(t)
	signed := f.signedOrder(t, 0, f.delegateKey)
	entry := f.entry(t, signed, f.hub.builders[0])
	request := &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}}
	response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, request)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[0].Status)
	identity, err := dispatchEntryIdentity("dispatch-test", entry)
	require.NoError(t, err)
	require.Equal(t, identity[:], response.Results[0].ObjectId)
	stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, uint64(1), stream.NextExpectedSequence)
	require.Equal(t, shared.NewAmount(dispatchTestPrefund), stream.Balance)
	require.Equal(t, signed.Order.MaxFee, stream.FrozenTotal)
	require.Equal(t, signed.Order.MaxFee, stream.SessionKeySpent)
	require.Equal(t, dispatchTestPrefund, f.bank.balance(f.ctx, authtypes.NewModuleAddress(shared.TaskEscrowModuleName)), "recording freezes existing custody without another bank transfer")
	record, err := f.keeper.readDispatchRecord(f.ctx, entry.Record.TaskId)
	require.NoError(t, err)
	stored, err := f.keeper.DispatchRecord.Get(f.ctx, entry.Record.TaskId)
	require.NoError(t, err)
	require.Len(t, stored.BuilderAddress, 20)
	require.Len(t, stored.Winner.OperatorAddress, 20)
	require.Equal(t, record.DispatchTicketHash, entry.Record.DispatchTicketHash)
	core, err := f.keeper.TaskCore.Get(f.ctx, entry.Record.TaskId)
	require.NoError(t, err)
	require.Equal(t, types.TaskPhase_TASK_PHASE_INFER_PENDING, core.TaskPhase)
	selection, err := f.keeper.GetTaskBuilderSelection(f.ctx, entry.Record.TaskId)
	require.NoError(t, err)
	require.Equal(t, f.hub.builders, selection.SelectedTaskBuilders)
	events := len(sdk.UnwrapSDKContext(f.ctx).EventManager().Events())
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(550))
	again, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, request)
	require.NoError(t, err, "exact replay precedes mutable order expiry and Session authorization")
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, again.Results[0].Status)
	require.Equal(t, response.Results[0].ObjectId, again.Results[0].ObjectId)
	after, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, stream, after)
	require.Len(t, sdk.UnwrapSDKContext(f.ctx).EventManager().Events(), events)
}

func TestRecordDispatchIsolatesRejectedEntryAndLiabilityFailure(t *testing.T) {
	for _, liabilityFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "bad accept then valid replacement", true: "liability reservation rollback"}[liabilityFailure], func(t *testing.T) {
			f := newDispatchFixture(t)
			signed := f.signedOrder(t, 0, f.delegateKey)
			entry := f.entry(t, signed, f.hub.builders[0])
			request := &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}}
			want := types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ACCEPT_SIGNATURE_INVALID
			if liabilityFailure {
				f.hub.reserveFailure = true
				want = types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_LIABILITY_RESERVATION_FAILED
			} else {
				entry.Accept.ServiceSignature[0] ^= 1
				request.Entries = append(request.Entries, dispatchEntryItem(f.entry(t, signed, f.hub.builders[0])))
			}
			response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, request)
			require.NoError(t, err)
			require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_REJECTED, response.Results[0].Status)
			require.Equal(t, want, response.Results[0].RejectCode)
			stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
			require.NoError(t, err)
			if liabilityFailure {
				require.Zero(t, stream.NextExpectedSequence)
				require.Equal(t, shared.NewAmount(0), stream.FrozenTotal)
				require.Equal(t, shared.NewAmount(0), stream.SessionKeySpent)
				present, err := f.keeper.storeService.OpenKVStore(f.ctx).Has(append([]byte("dispatch-test/pool-ref/"), entry.Record.TaskId...))
				require.NoError(t, err)
				require.False(t, present)
			} else {
				require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[1].Status)
				require.Equal(t, uint64(1), stream.NextExpectedSequence)
			}
		})
	}
}

func TestRecordDispatchStorageFailureRollsBackEarlierItems(t *testing.T) {
	f := newDispatchFixture(t)
	first := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	second := f.entry(t, f.signedOrder(t, 1, f.delegateKey), f.hub.builders[0])
	f.hub.fatalBucketTask = bytes.Clone(second.Record.TaskId)
	_, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(first), dispatchEntryItem(second)}})
	require.ErrorContains(t, err, "mock parameter bucket store failure")
	stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Zero(t, stream.NextExpectedSequence)
	require.Equal(t, shared.NewAmount(0), stream.FrozenTotal)
	for _, entry := range []*types.DispatchEntryV1{first, second} {
		present, err := f.keeper.TaskCore.Has(f.ctx, entry.Record.TaskId)
		require.NoError(t, err)
		require.False(t, present)
		present, err = f.keeper.storeService.OpenKVStore(f.ctx).Has(append([]byte("dispatch-test/liability/"), entry.Record.TaskId...))
		require.NoError(t, err)
		require.False(t, present)
	}
}

func TestTakeoverRegistrationWaitsAndRecordingVoidsIt(t *testing.T) {
	for _, recordFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "matures without consuming sequence", true: "recording voids pending takeover"}[recordFirst], func(t *testing.T) {
			f := newDispatchFixture(t)
			signed := f.signedOrder(t, 0, f.delegateKey)
			request := &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[1], Entries: []*types.DispatchRecordEntryV1{{Entry: &types.DispatchRecordEntryV1_Takeover{Takeover: &types.TakeoverEntryV1{SignedOrder: &signed}}}}}
			response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, request)
			require.NoError(t, err)
			require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[0].Status)
			stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
			require.NoError(t, err)
			require.Equal(t, f.hub.builders[0], stream.BoundBuilder)
			require.Zero(t, stream.NextExpectedSequence)
			require.Equal(t, shared.NewAmount(0), stream.FrozenTotal)
			pending := stream.GetPendingTakeover()
			require.NotNil(t, pending)
			again, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, request)
			require.NoError(t, err)
			require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, again.Results[0].Status)
			if recordFirst {
				entry := f.entry(t, signed, f.hub.builders[0])
				accepted, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
				require.NoError(t, err)
				require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, accepted.Results[0].Status)
			} else {
				changed, err := f.keeper.applySessionPendingRow(f.ctx, sessionPendingTakeover, sessionPendingRow{height: pending.ActivationHeight, sessionID: f.sessionID}, pending.ActivationHeight-1)
				require.NoError(t, err)
				require.False(t, changed)
				f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(pending.ActivationHeight)))
				changed, err = f.keeper.applySessionPendingRow(f.ctx, sessionPendingTakeover, sessionPendingRow{height: pending.ActivationHeight, sessionID: f.sessionID}, pending.ActivationHeight)
				require.NoError(t, err)
				require.True(t, changed)
			}
			after, err := f.keeper.ReadStream(f.ctx, f.sessionID)
			require.NoError(t, err)
			require.Nil(t, after.GetPendingTakeover())
			if recordFirst {
				require.Equal(t, f.hub.builders[0], after.BoundBuilder)
				require.Equal(t, uint64(1), after.NextExpectedSequence)
			} else {
				require.Equal(t, f.hub.builders[1], after.BoundBuilder)
				require.Zero(t, after.NextExpectedSequence)
				require.Equal(t, pending.ActivationHeight, after.BoundChangeHeight)
			}
		})
	}
}
