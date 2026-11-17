package keeper

import (
	"bytes"
	"fmt"
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestBackloggedVerifierSelectionRetiresToItsDeadline(t *testing.T) {
	for _, beaconAvailable := range []bool{false, true} {
		t.Run(fmt.Sprint(beaconAvailable), func(t *testing.T) {
			ready := verifierSelectionReadyFixture(t)
			f, window := ready.f, ready.window
			hub := verifierAdmissionHubStub{membersBySlot: ready.membersBySlot, bindingsBySlot: ready.bindingsBySlot}
			if beaconAvailable {
				hub.selectionBeacon = hubtypes.BeaconSnapshot{Height: int64(window.SelectionRandomnessHeight), Randomness: bytes.Repeat([]byte{0x52}, types.Hash32Len)}
			}
			f.keeper.hubKeeper = hub
			selection := types.NewVerifyRoundIndexKey(window.SelectionRandomnessHeight, ready.taskKey, 1)
			present, err := f.keeper.VerifierSelectionRandomnessIndex.Has(f.ctx, selection)
			require.NoError(t, err)
			require.True(t, present)
			for height := window.AssignmentDeadlineHeight + 1; height <= window.AssignmentDeadlineHeight+3; height++ {
				f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)))
				_, err := f.keeper.processVerifierSelectionRandomnessIndex(f.ctx, height, 1)
				require.NoError(t, err, "a stale assignment window must not fail repeated blocks")
			}
			present, err = f.keeper.VerifierSelectionRandomnessIndex.Has(f.ctx, selection)
			require.NoError(t, err)
			require.False(t, present)
			present, err = f.keeper.VerifyOpenDeadlineIndex.Has(f.ctx, types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, ready.taskKey))
			require.NoError(t, err)
			require.True(t, present, "the frozen terminal deadline must remain authoritative")
			height := uint64(sdk.UnwrapSDKContext(f.ctx).BlockHeight())
			result, err := f.keeper.RunVerifierRound(f.ctx, window.TaskId, 1, height, 1)
			require.NoError(t, err)
			require.False(t, result.AssignmentCreated)
		})
	}
}
