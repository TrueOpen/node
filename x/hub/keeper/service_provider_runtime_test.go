package keeper_test

import (
	"context"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestStakeServiceUsesConfiguredBusinessDenom(t *testing.T) {
	f := initFixture(t)
	genesis := types.DefaultGenesis()
	genesis.Params.Phase0.BusinessDenom = "uusdc"
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	identity := hubIdentity(t, 201)
	const chainID = "service-business-denom"
	ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(1))
	amount := testServiceBondMinInitial
	f.bank.add(identity.Address, "uusdc", amount+1)
	proof := serviceRegistrationProofBytes(
		chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, identity.Address, identity.PubKey,
	)
	_, err := keeper.NewMsgServerImpl(f.keeper).StakeService(ctx, &types.MsgStakeService{
		OperatorAddress: identity.Address,
		Action:          registerServiceAction(identity.PubKey, hubSign(t, identity, proof), amount),
	})
	require.NoError(t, err)

	_, err = f.keeper.StakeService(ctx, identity.Address, 1, 2, 0)
	require.NoError(t, err)
	moduleAddress := authtypes.NewModuleAddress(types.ServiceBondModuleName).String()
	require.Equal(t, amount+1, f.bank.balance(moduleAddress, "uusdc"))
	require.Zero(t, f.bank.balance(moduleAddress, "utrueopen"))
	require.NoError(t, f.keeper.EnsureServiceBondInvariant(ctx))
}

func TestCurrentServiceAddressInvariantRejectsCrossParticipantReuse(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	service := hubIdentity(t, 202)
	cortexOperator := hubAddress(t, 203)
	registerCortexNodeIdentityForTest(t, f, cortexOperator, service, testServiceBondMinInitial, 1, 0)

	builderOperator := hubAddress(t, 204)
	builder := types.BuilderState{
		SchemaVersion: 1, BuilderAddress: builderOperator,
		CurrentServiceAddress: service.Address, CurrentServicePubkey: service.PubKey,
		CurrentServiceKeyStatus: types.ServiceKeyStatusActive, ServiceAuthorizationNonce: 1,
		CurrentDescriptorVersion: 1, RegisteredHeight: 1,
	}
	require.NoError(t, builder.Validate())
	require.NoError(t, f.keeper.Builder.Set(f.ctx, builderOperator, builder))
	require.NoError(t, f.keeper.CurrentServiceAddressIndex.Set(
		f.ctx,
		types.NewCurrentServiceAddressIndexKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, service.Address),
		types.CurrentServiceAddressIndexState{OperatorAddress: builderOperator, ServiceAuthorizationNonce: 1},
	))
	require.ErrorContains(t, f.keeper.EnsureCurrentServiceAddressIndexInvariant(f.ctx), "is claimed by")
}

// TestTopUpServiceEnforcesBondFloorWhenReturningFromExit covers the A-12/A-20
// composition: UNBONDING still owns its identity and may cancel the exit only at
// the initial floor; EXITED has released that identity and must re-register with
// a new service-key proof, also at the initial floor.
//
// The test also pins the scope of the guard, which is the part that is a
// judgement call rather than a transcription of §10.0c step 3:
//
//   - a top-up that re-admits the node (EXITED *and* UNBONDING, i.e. the two
//     statuses whose flip to REGISTERED this branch performs) must satisfy the
//     floor. UNBONDING is included deliberately: pending_unbonding_total is
//     outside the candidate/reward weight per §10.0c step 5, so funds still in
//     flight cannot stand in for active_bond.
//   - a top-up that does *not* re-admit the node must stay ungated. A slash can
//     leave a still-REGISTERED bond below the floor, and §10.0c never requires a
//     slashed operator to restore the whole floor before it may add anything.
func TestTopUpServiceEnforcesBondFloorWhenReturningFromExit(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubIdentity(t, 201)
	serviceKey := hubIdentity(t, 202)
	server := keeper.NewMsgServerImpl(f.keeper)

	const registerHeight = uint64(100)
	registerCortexNodeIdentityForTest(t, f, operator.Address, serviceKey, testServiceBondMinInitial, registerHeight, 0)
	// The stake only counts from effective_bond_epoch onwards and an in-memory
	// fixture cannot let the chain advance, so the snapshot is pinned the way the
	// other bond fixtures do it. Without this the full unstake below has no
	// available bond to draw on.
	activateServiceBondForTest(t, f, operator.Address, 0)

	unstakeCtx := heightCtx(f, registerHeight)
	unstake, err := server.BeginServiceUnstake(unstakeCtx, &types.MsgBeginServiceUnstake{
		OperatorAddress: operator.Address, Amount: shared.NewAmount(testServiceBondMinInitial),
	})
	require.NoError(t, err)
	unbonding, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Zero(t, unbonding.ActiveBond)
	require.Equal(t, testServiceBondMinInitial, unbonding.PendingUnbondingTotal)
	require.Equal(t, types.ServiceBondStatusUnbonding, unbonding.Status)

	// (1) UNBONDING with the whole bond still in flight. The re-admission is
	// refused even though the operator's total exposure is unchanged, because the
	// unbonding queue does not count towards the live bond.
	f.bank.seedAccount(operator.Address, 1)
	_, err = server.StakeService(unstakeCtx, &types.MsgStakeService{
		OperatorAddress: operator.Address, Action: topUpServiceAction(1),
	})
	require.ErrorContains(t, err, "returning service bond must be at least")
	require.ErrorIs(t, err, types.ErrInsufficientServiceBond)
	stillUnbonding, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, types.ServiceBondStatusUnbonding, stillUnbonding.Status)
	require.Zero(t, stillUnbonding.ActiveBond)

	// (2) Finish the exit: mature, withdraw, status EXITED, active_bond == 0.
	withdrawHeight := unstake.MatureHeight
	withdrawCtx := heightCtx(f, withdrawHeight)
	withdrawn, err := server.WithdrawServiceUnbonded(withdrawCtx, &types.MsgWithdrawServiceUnbonded{
		OperatorAddress: operator.Address,
		Locator: types.UnbondingLocatorV1{Locator: &types.UnbondingLocatorV1_ById{
			ById: &shared.ByIDV1{Id: unstake.UnbondingId},
		}},
	})
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(testServiceBondMinInitial), withdrawn.WithdrawnAmount)
	exited, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, types.ServiceBondStatusExited, exited.Status)
	require.Zero(t, exited.ActiveBond)
	require.Zero(t, exited.PendingUnbondingTotal)
	_, err = f.keeper.GetCortexNodeState(f.ctx, operator.Address)
	require.Error(t, err, "EXITED must release the online cortex identity")
	oldIndexKey := types.NewCurrentServiceAddressIndexKey(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, serviceKey.Address)
	hasOldIndex, err := f.keeper.CurrentServiceAddressIndex.Has(f.ctx, oldIndexKey)
	require.NoError(t, err)
	require.False(t, hasOldIndex, "EXITED must release the old service address")
	reuseOperator := hubIdentity(t, 204)
	f.bank.seedAccount(reuseOperator.Address, testServiceBondMinInitial)
	chainID := sdk.UnwrapSDKContext(withdrawCtx).ChainID()
	reuseProof := serviceRegistrationProofBytes(chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, reuseOperator.Address, serviceKey.PubKey)
	_, err = server.StakeService(withdrawCtx, &types.MsgStakeService{
		OperatorAddress: reuseOperator.Address,
		Action:          registerServiceAction(serviceKey.PubKey, hubSign(t, serviceKey, reuseProof), testServiceBondMinInitial),
	})
	require.NoError(t, err, "a terminal operator must not burn its service address")

	// (3) Top-up no longer revives an EXITED bond because no current service key
	// exists. Re-registration is the returning node's first stake and applies the
	// §10.0c step 3 floor.
	f.bank.seedAccount(operator.Address, 1)
	_, err = server.StakeService(withdrawCtx, &types.MsgStakeService{
		OperatorAddress: operator.Address, Action: topUpServiceAction(1),
	})
	require.ErrorContains(t, err, "cortex node does not exist; use register")
	returnKey := hubIdentity(t, 203)
	returnProof := serviceRegistrationProofBytes(chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operator.Address, returnKey.PubKey)
	_, err = server.StakeService(withdrawCtx, &types.MsgStakeService{
		OperatorAddress: operator.Address,
		Action:          registerServiceAction(returnKey.PubKey, hubSign(t, returnKey, returnProof), 1),
	})
	require.ErrorContains(t, err, "initial service bond must be at least")
	require.ErrorIs(t, err, types.ErrInsufficientServiceBond)
	stillExited, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, types.ServiceBondStatusExited, stillExited.Status,
		"a rejected return must not leave the node live")
	require.Zero(t, stillExited.ActiveBond)

	// (4) A return that meets the floor is still accepted - the guard is a floor,
	// not a ban on returning (the data-structure contract).
	f.bank.seedAccount(operator.Address, testServiceBondMinInitial)
	returnProof = serviceRegistrationProofBytes(chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operator.Address, returnKey.PubKey)
	returned, err := server.StakeService(withdrawCtx, &types.MsgStakeService{
		OperatorAddress: operator.Address,
		Action:          registerServiceAction(returnKey.PubKey, hubSign(t, returnKey, returnProof), testServiceBondMinInitial),
	})
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(testServiceBondMinInitial), returned.ActiveBond)
	live, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, types.ServiceBondStatusRegistered, live.Status)

	// (5) A slash pushes the live bond below the floor without changing its status.
	// The next top-up is not a re-admission, so it must not be gated: keying the
	// guard on the status transition rather than on "active_bond is below the
	// floor" is what keeps this case working.
	slashHeight := withdrawHeight + 100
	_, err = f.keeper.ApplyServiceSlash(f.ctx, serviceSlashRequestForTest(
		operator.Address, shared.DutyWorker, "top-up-floor-scope", testServiceBondMinInitial-100_000, slashHeight,
	))
	require.NoError(t, err)
	slashed, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, uint64(100_000), slashed.ActiveBond)
	require.Equal(t, types.ServiceBondStatusRegistered, slashed.Status)

	f.bank.seedAccount(operator.Address, 1)
	subFloorTopUp, err := server.StakeService(heightCtx(f, slashHeight+1), &types.MsgStakeService{
		OperatorAddress: operator.Address, Action: topUpServiceAction(1),
	})
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(100_001), subFloorTopUp.ActiveBond)
}

// heightCtx derives a context at `height` from the fixture context. The msg
// server reads the block height off the context, and the fixture keeps one
// long-lived context, so each step needs its own view rather than a mutation of
// f.ctx. The EventManager is shared, so events accumulate across the derived
// contexts exactly as they do inside one block.
func heightCtx(f *fixture, height uint64) context.Context {
	return sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)))
}
