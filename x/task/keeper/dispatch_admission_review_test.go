package keeper

import (
	"strconv"
	"testing"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	"github.com/stretchr/testify/require"
)

func TestDispatchWinnerMinimumUsesUnreservedBond(t *testing.T) {
	for _, available := range []uint64{0, 999_999, 1_000_000} {
		t.Run(strconv.FormatUint(available, 10), func(t *testing.T) {
			f := newDispatchFixture(t)
			f.hub.availableBond = &available
			entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
			response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
			require.NoError(t, err)
			if available >= 1_000_000 {
				require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[0].Status)
				return
			}
			require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_REJECTED, response.Results[0].Status)
			state, err := f.keeper.DispatchWinnerIneligible.Get(f.ctx, entry.Accept.DispatchTicketHash)
			require.NoError(t, err)
			require.Equal(t, int32(types.DispatchWinnerIneligibleReasonV1_DISPATCH_WINNER_INELIGIBLE_REASON_V1_BOND_INSUFFICIENT), state.Reason)
			stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
			require.NoError(t, err)
			require.Zero(t, stream.NextExpectedSequence)
			require.Equal(t, shared.NewAmount(0), stream.FrozenTotal)
		})
	}
}

func TestSignedBadOrderCostsRejectOnlyTheirBatchEntry(t *testing.T) {
	f := newDispatchFixture(t)
	good := f.signedOrder(t, 0, f.delegateKey)
	bad := good.Order
	bad.TxFeeReserve = bad.MaxFee
	invalid := f.entry(t, signDispatchOrder(t, bad, f.delegateKey), f.hub.builders[0])
	valid := f.entry(t, good, f.hub.builders[0])
	response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(invalid), dispatchEntryItem(valid)}})
	require.NoError(t, err)
	require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ORDER_AMOUNT_INVALID, response.Results[0].RejectCode)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_REJECTED, response.Results[0].Status)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[1].Status)
	stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, uint64(1), stream.NextExpectedSequence)
	require.Equal(t, good.Order.MaxFee, stream.FrozenTotal)
}
