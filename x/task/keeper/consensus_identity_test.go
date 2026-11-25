package keeper

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

type failingStakingStub struct {
	anchorStakingStub
	err error
}

func (s failingStakingStub) GetValidator(context.Context, sdk.ValAddress) (stakingtypes.Validator, error) {
	return stakingtypes.Validator{}, s.err
}

func TestIsConsensusIdentity(t *testing.T) {
	f := newAnchorFixture(t, 1_000_000, 64)
	servicePub := secp256k1.GenPrivKeyFromSecret([]byte("panel-service-key")).PubKey().(*secp256k1.PubKey)
	consensusPub := secp256k1.GenPrivKeyFromSecret([]byte("validator-consensus-key")).PubKey().(*secp256k1.PubKey)
	staking := f.staking
	staking.consensus = map[string]bool{string(sdk.ConsAddress(consensusPub.Address())): true}
	f.keeper.stakingKeeper = staking

	outsider := sdk.AccAddress(bytes.Repeat([]byte{0x31}, 20))
	outsiderService := sdk.AccAddress(bytes.Repeat([]byte{0x32}, 20))
	validatorAccount, err := sdk.AccAddressFromBech32(f.valAcct[0])
	require.NoError(t, err)

	check := func(operator, service sdk.AccAddress, pubkey []byte) bool {
		t.Helper()
		found, err := f.keeper.IsConsensusIdentity(f.ctx, operator, service, pubkey)
		require.NoError(t, err)
		return found
	}

	require.False(t, check(outsider, outsiderService, servicePub.Key), "an unrelated node is not a block producer")
	require.False(t, check(outsider, nil, nil), "a node without a service binding is judged on its operator account")
	require.True(t, check(validatorAccount, outsiderService, servicePub.Key), "a validator's operator account")
	require.True(t, check(outsider, validatorAccount, servicePub.Key), "a validator's operator account used as service address")
	require.True(t, check(outsider, outsiderService, consensusPub.Key), "a validator's consensus key used as service key")

	// A registered audit vote address is a block-producer identity, as operator
	// or as service address, and stops being one when the registration is cleared.
	hot := sdk.AccAddress(bytes.Repeat([]byte{0x33}, 20))
	_, err = f.server.RegisterAnchorVoteKey(f.at(5), &types.MsgRegisterAnchorVoteKey{
		ValidatorOperatorAddress: f.valoper[1], VoteAddress: hot.String(), SubmitterAddress: f.valAcct[1],
	})
	require.NoError(t, err)
	require.True(t, check(hot, outsiderService, servicePub.Key))
	require.True(t, check(outsider, hot, servicePub.Key))
	require.False(t, check(outsider, outsiderService, servicePub.Key))
	_, err = f.server.RegisterAnchorVoteKey(f.at(6), &types.MsgRegisterAnchorVoteKey{
		ValidatorOperatorAddress: f.valoper[1], SubmitterAddress: f.valAcct[1],
	})
	require.NoError(t, err)
	require.False(t, check(hot, outsiderService, servicePub.Key))

	// A validator that is no longer bonded is not a block producer.
	unbonded := staking
	unbonded.unbonded = true
	f.keeper.stakingKeeper = unbonded
	require.False(t, check(validatorAccount, outsiderService, servicePub.Key))
	require.False(t, check(outsider, outsiderService, consensusPub.Key))
	f.keeper.stakingKeeper = staking

	_, err = f.keeper.IsConsensusIdentity(f.ctx, nil, nil, servicePub.Key)
	require.Error(t, err, "an identity with no account cannot be cleared")
}

// The lookup never answers "not a block producer" when it could not look.
func TestIsConsensusIdentityFailsClosed(t *testing.T) {
	f := newAnchorFixture(t, 1_000_000, 64)
	outsider := sdk.AccAddress(bytes.Repeat([]byte{0x31}, 20))

	broken := f.keeper
	broken.stakingKeeper = failingStakingStub{anchorStakingStub: f.staking, err: errors.New("store unavailable")}
	_, err := broken.IsConsensusIdentity(f.ctx, outsider, nil, nil)
	require.ErrorContains(t, err, "store unavailable")

	unbound := f.keeper
	unbound.stakingKeeper = nil
	_, err = unbound.IsConsensusIdentity(f.ctx, outsider, nil, nil)
	require.Error(t, err)
}
