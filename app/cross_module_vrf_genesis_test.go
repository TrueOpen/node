package app

import (
	"bytes"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

func TestGenesisValidatorVrfKeyCoverage(t *testing.T) {
	t.Run("required mode rejects a missing key", func(t *testing.T) {
		application, ctx, _ := genesisVrfCoverageFixture(t, true)
		require.ErrorContains(t, application.EnsureGenesisValidatorVrfKeyCoverage(ctx), "has no active VRF key")
	})

	t.Run("required mode rejects a non-genesis active epoch", func(t *testing.T) {
		application, ctx, operator := genesisVrfCoverageFixture(t, true)
		setGenesisVrfKey(t, application, ctx, operator, 1, false)
		require.ErrorContains(t, application.EnsureGenesisValidatorVrfKeyCoverage(ctx), "active_from_epoch must be 0")
	})

	t.Run("required mode rejects a pending key", func(t *testing.T) {
		application, ctx, operator := genesisVrfCoverageFixture(t, true)
		setGenesisVrfKey(t, application, ctx, operator, 0, true)
		require.ErrorContains(t, application.EnsureGenesisValidatorVrfKeyCoverage(ctx), "must not carry pending fields")
	})

	t.Run("required mode accepts one canonical active key per validator", func(t *testing.T) {
		application, ctx, operator := genesisVrfCoverageFixture(t, true)
		setGenesisVrfKey(t, application, ctx, operator, 0, false)
		require.NoError(t, application.EnsureGenesisValidatorVrfKeyCoverage(ctx))
	})

	t.Run("disabled mode does not require a key", func(t *testing.T) {
		application, ctx, _ := genesisVrfCoverageFixture(t, false)
		require.NoError(t, application.EnsureGenesisValidatorVrfKeyCoverage(ctx))
	})
}

func TestFreshGenesisVrfCoverageBoundary(t *testing.T) {
	require.True(t, requiresFreshGenesisVrfKeyCoverage(nil))
	require.True(t, requiresFreshGenesisVrfKeyCoverage(&abci.RequestInitChain{InitialHeight: 0}))
	require.True(t, requiresFreshGenesisVrfKeyCoverage(&abci.RequestInitChain{InitialHeight: 1}))
	require.False(t, requiresFreshGenesisVrfKeyCoverage(&abci.RequestInitChain{InitialHeight: 2}))
}

func genesisVrfCoverageFixture(t *testing.T, required bool) (*App, sdk.Context, string) {
	t.Helper()
	application := bootAppMinimal(t)
	ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
	params, err := application.HubKeeper.Params.Get(ctx)
	require.NoError(t, err)
	if required {
		params.Beacon.VrfRequiredFromHeight = 1
	} else {
		params.Beacon.VrfRequiredFromHeight = 0
	}
	require.NoError(t, application.HubKeeper.Params.Set(ctx, params))

	var operator string
	validatorCount := 0
	err = application.StakingKeeper.IterateValidators(ctx, func(_ int64, validator stakingtypes.ValidatorI) bool {
		operatorBytes, decodeErr := application.StakingKeeper.ValidatorAddressCodec().StringToBytes(validator.GetOperator())
		require.NoError(t, decodeErr)
		operator = sdk.AccAddress(operatorBytes).String()
		validatorCount++
		return false
	})
	require.NoError(t, err)
	require.Equal(t, 1, validatorCount)
	return application, ctx, operator
}

func setGenesisVrfKey(t *testing.T, application *App, ctx sdk.Context, operator string, activeFromEpoch uint64, pending bool) {
	t.Helper()
	state := hubtypes.VrfKeyState{
		OperatorAddress:       operator,
		ActiveVrfPubkey:       bytes.Repeat([]byte{0x41}, hubtypes.VrfPubkeyLen),
		ActiveFromEpoch:       activeFromEpoch,
		VrfAuthorizationNonce: 1,
	}
	if pending {
		state.XPendingVrfPubkey = &hubtypes.VrfKeyState_PendingVrfPubkey{
			PendingVrfPubkey: bytes.Repeat([]byte{0x42}, hubtypes.VrfPubkeyLen),
		}
		state.XPendingFromEpoch = &hubtypes.VrfKeyState_PendingFromEpoch{PendingFromEpoch: 1}
	}
	require.NoError(t, application.HubKeeper.VrfKey.Set(ctx, operator, state))
}
