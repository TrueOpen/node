package keeper

import (
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestRecordedDoubleDispatchChallengeUsesFrozenTaskParameters(t *testing.T) {
	f := newDispatchFixture(t)
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	f.record(t, entry)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	var frozenPenalty shared.Amount
	for _, policy := range params.Dispatch.DispatchFaultPenaltyByKind {
		if policy.Kind == shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DOUBLE_DISPATCH {
			frozenPenalty = policy.Amount
			policy.Amount = shared.NewAmount(99_999)
		}
	}
	params.Dispatch.DispatchChallengeWindowBlocks = 1
	_, err = NewMsgServerImpl(f.keeper).UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{Authority: sdk.AccAddress(f.keeper.authority).String(), Params: params})
	require.NoError(t, err)
	second := *entry.Record.Rounds[0].Attempts[0]
	second.Rank = 1
	second.DispatchTicketHash = bytes32(0x94)
	entry.Record.Rounds[0].Attempts = append(entry.Record.Rounds[0].Attempts, &second)
	signDispatchTestMaterial(t, "task.v1.DispatchRecordV1", entry.Record, f.hub.keys[entry.Record.BuilderAddress], func(s []byte) { entry.Record.BuilderServiceSignature = s })
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(150))
	result, err := NewMsgServerImpl(f.keeper).ChallengeDispatchDoubleDispatch(f.ctx, &types.MsgChallengeDispatchDoubleDispatch{Order: entry.SignedOrder, Record: entry.Record, ChallengerAddress: f.owner})
	require.NoError(t, err)
	require.Equal(t, types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_UPHELD, result.Challenge.Status)
	raw, err := f.hub.store.OpenKVStore(f.ctx).Get(append([]byte("dispatch-test/fault/"), result.Challenge.GetFaultId()...))
	require.NoError(t, err)
	var fault hubtypes.DispatchFaultState
	require.NoError(t, fault.Unmarshal(raw))
	require.Equal(t, frozenPenalty, fault.PenaltyAmount)
}
