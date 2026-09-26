package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestServiceBondEffectiveIndexActivatesFreshCandidateSlot(t *testing.T) {
	f := initFixture(t)
	const chainID = "trueopen-service-effective-index"
	f.ctx = sdk.WrapSDKContext(
		sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(1),
	)
	genesis := types.DefaultGenesis()
	genesis.Params.Epoch.EpochLengthBlocks = 10
	genesis.Params.Epoch.DeltaWBlocks = 2
	genesis.Params.Epoch.DeltaMBlocks = 2
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	operator := hubIdentity(t, 215)
	serviceKey := hubIdentity(t, 216)
	registerCtx := sdk.WrapSDKContext(
		sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(1),
	)
	f.bank.seedAccount(operator.Address, testServiceBondMinInitial)
	proof := serviceRegistrationProofBytes(
		chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operator.Address, serviceKey.PubKey,
	)
	_, err := keeper.NewMsgServerImpl(f.keeper).StakeService(registerCtx, &types.MsgStakeService{
		OperatorAddress: operator.Address,
		Action: registerServiceAction(
			serviceKey.PubKey, hubSign(t, serviceKey, proof), testServiceBondMinInitial,
		),
	})
	require.NoError(t, err)

	bond, err := f.keeper.ReadServiceBondValue(registerCtx, types.NewServiceBondKey(operator.Address))
	require.NoError(t, err)
	require.Equal(t, uint64(1), bond.EffectiveBondEpoch)
	require.Zero(t, bond.EffectiveActiveBond)
	has, err := f.keeper.ServiceBondEffectiveIndex.Has(
		registerCtx, types.NewServiceBondEffectiveKey(bond.EffectiveBondEpoch, operator.Address),
	)
	require.NoError(t, err)
	require.True(t, has)
	reverse, err := f.keeper.ReadOperatorCandidateSlot(registerCtx, operator.Address)
	require.NoError(t, err)
	current, err := f.keeper.ReadCandidateSlotCurrent(registerCtx, reverse.Slot)
	require.NoError(t, err)
	require.Equal(t, uint64(1), current.AllocatedEpoch)
	exported, err := f.keeper.ExportGenesis(registerCtx)
	require.NoError(t, err)
	restarted := initFixture(t)
	restarted.ctx = sdk.WrapSDKContext(
		sdk.UnwrapSDKContext(restarted.ctx).WithChainID(chainID).WithBlockHeight(1),
	)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	require.NoError(t, restarted.keeper.EnsureServiceBondEffectiveIndexInvariant(restarted.ctx))
	has, err = restarted.keeper.ServiceBondEffectiveIndex.Has(
		restarted.ctx, types.NewServiceBondEffectiveKey(bond.EffectiveBondEpoch, operator.Address),
	)
	require.NoError(t, err)
	require.True(t, has)

	visited, err := f.keeper.ProcessServiceBondEffectiveActivations(registerCtx, 9, 1)
	require.NoError(t, err)
	require.Zero(t, visited)
	activationCtx := sdk.WrapSDKContext(
		sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(10),
	)
	visited, err = f.keeper.ProcessServiceBondEffectiveActivations(activationCtx, 10, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	bond, err = f.keeper.ReadServiceBondValue(activationCtx, types.NewServiceBondKey(operator.Address))
	require.NoError(t, err)
	require.Equal(t, bond.ActiveBond, bond.EffectiveActiveBond)
	require.Equal(t, testServiceBondMinInitial, bond.EffectiveActiveBond)
	reverse, err = f.keeper.ReadOperatorCandidateSlot(activationCtx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, operator.Address, reverse.OperatorAddress)
	has, err = f.keeper.ServiceBondEffectiveIndex.Has(
		activationCtx, types.NewServiceBondEffectiveKey(bond.EffectiveBondEpoch, operator.Address),
	)
	require.NoError(t, err)
	require.False(t, has)
	require.NoError(t, f.keeper.EnsureServiceBondEffectiveIndexInvariant(activationCtx))
}
