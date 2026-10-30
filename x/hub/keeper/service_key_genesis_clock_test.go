package keeper_test

import (
	"fmt"
	"testing"

	shared "github.com/TrueOpen/node/x/shared/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestServiceKeyGenesisRejectsElapsedPendingAndFuturePrevious(t *testing.T) {
	f, operator, next := rotationFixture(t)
	requestBuilderRotation(t, f, operator, next)
	pending, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	for _, height := range []int64{240, 300} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			other := initFixture(t)
			other.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(other.ctx).WithBlockHeight(height))
			require.ErrorContains(t, other.keeper.InitGenesis(other.ctx, *pending), "genesis state clock")
		})
	}
	activateServiceRotationForTest(t, f, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, operator.Address)
	rotated, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	invalid := initFixture(t)
	invalid.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(invalid.ctx).WithBlockHeight(150))
	require.ErrorContains(t, invalid.keeper.InitGenesis(invalid.ctx, *rotated), "previous service key retirement")
	// The final committed EndBlock precedes the boundary whose key it installs.
	valid := initFixture(t)
	valid.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(valid.ctx).WithBlockHeight(240))
	require.NoError(t, valid.keeper.InitGenesis(valid.ctx, *rotated))
}
