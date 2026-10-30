package keeper

import (
	"context"
	"errors"
	"testing"

	"cosmossdk.io/collections"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	"github.com/TrueOpen/wire/dispatch"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

type unavailableDispatchProofHub struct {
	*dispatchTestHub
	operator string
	err      error
}

func (h unavailableDispatchProofHub) GetDispatchServiceProofKey(ctx context.Context, role shared.ParticipantType, operator string) (hubtypes.CurrentServiceKeySnapshot, error) {
	if operator == h.operator {
		return hubtypes.CurrentServiceKeySnapshot{}, h.err
	}
	return h.dispatchTestHub.GetDispatchServiceProofKey(ctx, role, operator)
}

func TestSkipMaturityWithoutProofAuthorityDoesNotHaltOrPunish(t *testing.T) {
	storageFailure := errors.New("injected proof storage failure")
	for _, proofError := range []error{collections.ErrNotFound, dispatch.ErrProofKeyUnavailable, storageFailure} {
		t.Run(proofError.Error(), func(t *testing.T) {
			f := newDispatchFixture(t)
			entry, reject := f.rejectedDispatchEntry(t)
			f.record(t, entry)
			f.keeper.hubKeeper = unavailableDispatchProofHub{dispatchTestHub: f.hub, operator: reject.Member.OperatorAddress, err: proofError}
			opened, err := NewMsgServerImpl(f.keeper).ChallengeDispatchSkip(f.ctx, &types.MsgChallengeDispatchSkip{TaskId: entry.Record.TaskId, AttemptRank: 0, ChallengerAddress: f.owner})
			require.NoError(t, err)
			height := opened.Challenge.GetProofDeadlineHeight() + 1
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)))
			err = f.keeper.EndBlocker(f.ctx)
			row, readErr := f.keeper.readDispatchChallenge(f.ctx, opened.Challenge.ChallengeId)
			require.NoError(t, readErr)
			if errors.Is(proofError, storageFailure) {
				require.ErrorIs(t, err, storageFailure)
				require.True(t, dispatchChallengePending(row.Status))
				return
			}
			require.NoError(t, err)
			require.Equal(t, types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_EXPIRED, row.Status)
			require.Empty(t, row.GetFaultId())
			pending, err := f.keeper.pendingDispatchCount(f.ctx, row.TaskId)
			require.NoError(t, err)
			require.Zero(t, pending)
			require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height + 1)))
			require.NoError(t, f.keeper.EndBlocker(f.ctx))
		})
	}
}
