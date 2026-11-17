package keeper_test

import (
	"testing"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestEpochWeightInputsIncludeChangesBeforeReferenceAndFreezeAfterIt(t *testing.T) {
	f := initCandidatePoolFixture(t)
	genesis := types.DefaultGenesis()
	genesis.Params.Epoch.EpochLengthBlocks = 10
	genesis.Params.Epoch.DeltaWBlocks = 1
	genesis.Params.Epoch.DeltaMBlocks = 1
	genesis.Params.CandidatePool.CandidateSlotHardCapacity = 8
	genesis.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
	genesis.Params.CandidatePool.CandidatePoolBuildLeadBlocks = 4
	genesis.Params.CandidatePool.MaxCandidatePoolBuildMembersPerBlock = 8
	identity := hubIdentity(t, 200)
	genesis.CortexNodes = []types.CortexNodeState{{SchemaVersion: 1, OperatorAddress: identity.Address, CurrentServiceAddress: identity.Address, CurrentServicePubkey: identity.PubKey, ServiceAuthorizationNonce: 1, ServiceKeyStatus: types.ServiceKeyStatusActive, RegisteredHeight: 1, UpdatedHeight: 1}}
	genesis.ServiceBonds = []types.ServiceBondState{{OperatorAddress: identity.Address, ActiveBond: 2 * testServiceBondMinInitial, EffectiveActiveBond: 2 * testServiceBondMinInitial, BondVersion: 1, Status: types.ServiceBondStatusActive, LastStakeHeight: 1}}
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	registerTestModelProfile(t, f, "reference-weights", 1, testServiceBondMinInitial, 1)
	modelID := testModelID(candidatePoolTestChainID, hubAddress(t, 250), "reference-weights")
	_, err := f.keeper.DeclareModelSupport(f.ctx, identity.Address, modelID, true, true, 1)
	require.NoError(t, err)
	_, err = f.keeper.ProcessDirtyCandidatePools(f.ctx, 1, 8)
	require.NoError(t, err)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(2))
	_, err = f.keeper.ProcessCandidatePoolExpiries(f.ctx, 2, 1)
	require.NoError(t, err)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(6))
	_, err = f.keeper.ProcessDirtyCandidatePools(f.ctx, 6, 8)
	require.NoError(t, err)
	reverse, err := f.keeper.ReadOperatorCandidateSlot(f.ctx, identity.Address)
	require.NoError(t, err)
	before, err := f.keeper.ReadCandidatePoolMember(f.ctx, types.NewCandidatePoolMemberKey(1, reverse.Slot))
	require.NoError(t, err)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(9))
	registerTestModelProfile(t, f, "reference-weights", 2, testServiceBondMinInitial, 9)
	// No support, bond or operator mutation follows registration. The new
	// profile still belongs to the reference snapshot of every supporter.
	withProfile, err := f.keeper.ReadCandidatePoolMember(f.ctx, types.NewCandidatePoolMemberKey(1, reverse.Slot))
	require.NoError(t, err)
	require.Len(t, withProfile.WeightInputs.Profiles, 2)
	require.Equal(t, uint32(2), withProfile.WeightInputs.Profiles[1].ProfileVersion)
	f.bank.seedAccount(identity.Address, 100)
	_, err = f.keeper.StakeService(f.ctx, identity.Address, 100, 9, 0)
	require.NoError(t, err)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10))
	_, err = f.keeper.ProcessCandidatePoolExpiries(f.ctx, 10, 2)
	require.NoError(t, err)
	frozen, err := f.keeper.ReadCandidatePoolMember(f.ctx, types.NewCandidatePoolMemberKey(1, reverse.Slot))
	require.NoError(t, err)
	amount, err := shared.ParseAmount(before.WeightInputs.ActiveBond)
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(amount+100), frozen.WeightInputs.ActiveBond, "prebuilding must not freeze stake before the authoritative epoch reference")
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(11))
	registerTestModelProfile(t, f, "reference-weights", 3, testServiceBondMinInitial, 11)
	f.bank.seedAccount(identity.Address, 200)
	_, err = f.keeper.StakeService(f.ctx, identity.Address, 200, 11, 1)
	require.NoError(t, err)
	after, err := f.keeper.ReadCandidatePoolMember(f.ctx, types.NewCandidatePoolMemberKey(1, reverse.Slot))
	require.NoError(t, err)
	require.Equal(t, frozen.WeightInputs, after.WeightInputs, "post-reference facts must not rewrite historical draw inputs")
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	restored := initCandidatePoolFixture(t)
	restored.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restored.ctx).WithBlockHeight(12))
	require.NoError(t, restored.keeper.InitGenesis(restored.ctx, *exported))
	recovered, err := restored.keeper.ReadCandidatePoolMember(restored.ctx, types.NewCandidatePoolMemberKey(1, reverse.Slot))
	require.NoError(t, err)
	require.Equal(t, frozen.WeightInputs, recovered.WeightInputs)
}

func TestCandidateBuildCannotCaptureFactsAfterEpochReference(t *testing.T) {
	f := initCandidatePoolFixture(t)
	genesis := types.DefaultGenesis()
	genesis.Params.Epoch.EpochLengthBlocks = 10
	genesis.Params.Epoch.DeltaWBlocks = 1
	genesis.Params.Epoch.DeltaMBlocks = 1
	genesis.Params.CandidatePool.CandidateSlotHardCapacity = 8
	genesis.Params.CandidatePool.CandidateBitmapSegmentBytes = 1
	genesis.Params.CandidatePool.CandidatePoolBuildLeadBlocks = 4
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	_, err := f.keeper.ProcessDirtyCandidatePools(f.ctx, 1, 8)
	require.NoError(t, err)
	_, err = f.keeper.ProcessCandidatePoolExpiries(f.ctx, 2, 1)
	require.NoError(t, err)
	for _, height := range []uint64{9, 10, 11, 12} {
		f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)))
		_, err = f.keeper.ProcessDirtyCandidatePools(f.ctx, height, 1)
		require.NoError(t, err)
	}
	status, err := f.keeper.CandidatePoolBuildStatus.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, types.CandidatePoolBuildFailureReason_CANDIDATE_POOL_BUILD_FAILURE_REASON_NO_EFFECTIVE_WINDOW, status.FailureReason)
	rows, err := f.keeper.CandidatePoolSnapshot.Iterate(f.ctx, nil)
	require.NoError(t, err)
	defer rows.Close()
	for ; rows.Valid(); rows.Next() {
		row, err := rows.Value()
		require.NoError(t, err)
		require.NotEqual(t, uint64(1), row.Epoch, "missing reference facts cannot be replaced by later live state")
	}
}
