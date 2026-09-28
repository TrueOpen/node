package keeper_test

import (
	"bytes"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

func TestModelSupportDeactivationResumesAcrossBlocksAndGenesis(t *testing.T) {
	f := initFixture(t)
	genesis := hubGenesisWithIndexes()
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	modelID := append([]byte(nil), genesis.Models[0].ModelId...)
	operators := []string{genesis.CortexNodes[0].OperatorAddress}
	for _, seed := range []struct {
		addressFill  byte
		identityFill byte
	}{{0x11, 0x31}, {0x22, 0x32}} {
		operator := hubAddress(t, seed.addressFill)
		registerSupportOperatorForTest(t, f, operator, hubIdentity(t, seed.identityFill))
		_, err := f.keeper.DeclareModelSupport(f.ctx, operator, modelID, true, true, 10)
		require.NoError(t, err)
		operators = append(operators, operator)
	}

	model, err := f.keeper.SetModelStatus(f.ctx, modelID, types.ModelStatusFrozen, types.GovernanceReason_GOVERNANCE_REASON_FREEZE, 10)
	require.NoError(t, err)
	require.Equal(t, types.ModelStatusFrozen, model.Status)
	require.Equal(t, types.ModelStatusFrozen, f.keeper.GetModelStatus(sdk.UnwrapSDKContext(f.ctx), modelID))
	initial, err := f.keeper.GetModelSupportState(f.ctx, operators[0], modelID)
	require.NoError(t, err)
	require.True(t, initial.SupportActive, "the model status gates eligibility before stored rows converge")

	visited, consumed, err := f.keeper.ProcessModelSupportDeactivations(f.ctx, 10, 1, 1<<20)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	require.Positive(t, consumed)
	cursor, err := f.keeper.ModelSupportDeactivateCursor.Get(f.ctx, modelID)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cursor.VisitedCount)
	require.Equal(t, uint64(1), cursor.DeactivatedCount)
	require.NotEmpty(t, cursor.LastOperatorAddress)
	_, err = f.keeper.SetModelStatus(f.ctx, modelID, types.ModelStatusRegistered, types.GovernanceReason_GOVERNANCE_REASON_UNFREEZE, 11)
	require.ErrorContains(t, err, "deactivation must finish")
	visited, consumed, err = f.keeper.ProcessModelSupportDeactivations(f.ctx, 11, 100, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited, "the byte budget must stop after one visited row")
	require.Positive(t, consumed)
	cursor, err = f.keeper.ModelSupportDeactivateCursor.Get(f.ctx, modelID)
	require.NoError(t, err)
	require.Equal(t, uint64(2), cursor.VisitedCount)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Len(t, exported.ModelSupportDeactivateCursors, 1)
	require.True(t, bytes.Equal(modelID, exported.ModelSupportDeactivateCursors[0].ModelId))
	for _, tc := range []struct {
		name   string
		mutate func(*types.GenesisState)
	}{
		{"count exceeds visits", func(state *types.GenesisState) {
			state.ModelSupportDeactivateCursors[0].DeactivatedCount = state.ModelSupportDeactivateCursors[0].VisitedCount + 1
		}},
		{"missing resume address", func(state *types.GenesisState) {
			state.ModelSupportDeactivateCursors[0].LastOperatorAddress = ""
		}},
		{"duplicate cursor", func(state *types.GenesisState) {
			state.ModelSupportDeactivateCursors = append(state.ModelSupportDeactivateCursors, state.ModelSupportDeactivateCursors[0])
		}},
		{"recheck overlap", func(state *types.GenesisState) {
			state.ModelSupportRecheckCursors = append(state.ModelSupportRecheckCursors, types.ModelSupportRecheckCursorState{ModelId: modelID, EffectiveHeight: 1})
		}},
		{"model not frozen or delisted", func(state *types.GenesisState) {
			state.Models = append([]types.ModelState(nil), exported.Models...)
			state.Models[0].Status = types.ModelStatusRegistered
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := *exported
			invalid.ModelSupportDeactivateCursors = append([]types.ModelSupportDeactivateCursorState(nil), exported.ModelSupportDeactivateCursors...)
			tc.mutate(&invalid)
			require.Error(t, invalid.Validate())
		})
	}
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	restored, err := restarted.keeper.ModelSupportDeactivateCursor.Get(restarted.ctx, modelID)
	require.NoError(t, err)
	require.Equal(t, cursor, restored)

	for height := uint64(11); height < 20; height++ {
		has, err := restarted.keeper.ModelSupportDeactivateCursor.Has(restarted.ctx, modelID)
		require.NoError(t, err)
		if !has {
			break
		}
		visited, _, err := restarted.keeper.ProcessModelSupportDeactivations(restarted.ctx, height, 1, 1<<20)
		require.NoError(t, err)
		require.Equal(t, uint64(1), visited)
	}
	_, err = restarted.keeper.ModelSupportDeactivateCursor.Get(restarted.ctx, modelID)
	require.ErrorIs(t, err, collections.ErrNotFound)
	for _, operator := range operators {
		support, err := restarted.keeper.GetModelSupportState(restarted.ctx, operator, modelID)
		require.NoError(t, err)
		require.False(t, support.DeclaredSupport)
		require.False(t, support.SupportActive)
		require.Zero(t, support.ActiveSupportStakeSnapshot)
	}
	finalModel, err := restarted.keeper.GetModel(restarted.ctx, modelID)
	require.NoError(t, err)
	require.Zero(t, finalModel.ActiveSupporterCount)
	require.Zero(t, finalModel.ActiveSupportStake)
	_, err = restarted.keeper.SetModelStatus(restarted.ctx, modelID, types.ModelStatusRegistered, types.GovernanceReason_GOVERNANCE_REASON_UNFREEZE, 20)
	require.NoError(t, err)
}

// TestFreezeClearsInFlightRecheckInsteadOfRejecting exercises the rule that a
// governance status change into FROZEN/DELISTED wins over a
// not-yet-effective threshold recheck. It must clear the pending recheck index
// entry, the model's pending fields and any in-flight recheck cursor, then
// enqueue the deactivation sweep, rather than reject the status change.
func TestFreezeClearsInFlightRecheckInsteadOfRejecting(t *testing.T) {
	f := initFixture(t)
	genesis := hubGenesisWithIndexes()
	modelID := append([]byte(nil), genesis.Models[0].ModelId...)
	const pendingEffectiveHeight = 50
	genesis.Models[0].PendingSupportMinStake = genesis.Models[0].SupportMinStake + 1
	genesis.Models[0].PendingEffectiveHeight = pendingEffectiveHeight
	genesis.ModelSupportRecheckCursors = append(genesis.ModelSupportRecheckCursors, types.ModelSupportRecheckCursorState{
		ModelId: modelID, EffectiveHeight: pendingEffectiveHeight, MinStakeLowered: false,
	})
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	has, err := f.keeper.ModelSupportRecheckIndex.Has(f.ctx, types.NewModelSupportRecheckIndexKey(pendingEffectiveHeight, modelID))
	require.NoError(t, err)
	require.True(t, has, "genesis import must rebuild the recheck index from PendingEffectiveHeight")
	has, err = f.keeper.ModelSupportRecheckCursor.Has(f.ctx, modelID)
	require.NoError(t, err)
	require.True(t, has)

	model, err := f.keeper.SetModelStatus(f.ctx, modelID, types.ModelStatusFrozen, types.GovernanceReason_GOVERNANCE_REASON_FREEZE, 10)
	require.NoError(t, err, "a governance freeze must clear a conflicting recheck rather than reject")
	require.Zero(t, model.PendingSupportMinStake)
	require.Zero(t, model.PendingEffectiveHeight)

	has, err = f.keeper.ModelSupportRecheckIndex.Has(f.ctx, types.NewModelSupportRecheckIndexKey(pendingEffectiveHeight, modelID))
	require.NoError(t, err)
	require.False(t, has, "the stale recheck index row must be removed")
	has, err = f.keeper.ModelSupportRecheckCursor.Has(f.ctx, modelID)
	require.NoError(t, err)
	require.False(t, has, "the in-flight recheck cursor must be removed")
	has, err = f.keeper.ModelSupportDeactivateCursor.Has(f.ctx, modelID)
	require.NoError(t, err)
	require.True(t, has, "the model has support rows, so a deactivation cursor must be enqueued")
}

// TestFreezeSkipsDeactivateCursorWithNoSupportRows covers the case where a
// model has no ModelSupportState row at all: the status change
// skips creating the cursor and completes the status write directly.
func TestFreezeSkipsDeactivateCursorWithNoSupportRows(t *testing.T) {
	f := initFixture(t)
	genesis := hubGenesisWithIndexes()
	modelID := append([]byte(nil), genesis.Models[0].ModelId...)
	genesis.ModelSupports = nil
	genesis.ModelCapabilities = nil
	genesis.Models[0].ActiveSupporterCount = 0
	genesis.Models[0].ActiveSupportStake = 0
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	_, err := f.keeper.SetModelStatus(f.ctx, modelID, types.ModelStatusFrozen, types.GovernanceReason_GOVERNANCE_REASON_FREEZE, 10)
	require.NoError(t, err)

	has, err := f.keeper.ModelSupportDeactivateCursor.Has(f.ctx, modelID)
	require.NoError(t, err)
	require.False(t, has, "a model with no support rows must not get a deactivation cursor")
}

// TestModelSupportRecheckPrioritizesJailOverThresholdReason covers the
// interleaving of a JAIL event with an in-flight min-stake recheck cursor:
// reweightModelSupport must attribute the row's suspension to JAIL rather than
// the recheck's own threshold reason, since a jailed operator's ineligibility
// is not conditional on the threshold that triggered the recheck.
func TestModelSupportRecheckPrioritizesJailOverThresholdReason(t *testing.T) {
	f := initFixture(t)
	genesis := hubGenesisWithIndexesForChain(testHubChainID)
	modelID := append([]byte(nil), genesis.Models[0].ModelId...)
	operator := genesis.ModelSupports[0].OperatorAddress
	genesis.ModelSupportRecheckCursors = append(genesis.ModelSupportRecheckCursors, types.ModelSupportRecheckCursorState{
		ModelId: modelID, EffectiveHeight: 5, MinStakeLowered: false,
	})
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	// The operator is jailed after genesis import, interleaving with the
	// already in-flight recheck cursor above.
	bond, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)
	bond.Status = types.ServiceBondStatusJailed
	bond.JailCount = 1
	require.NoError(t, f.keeper.WriteServiceBondValue(f.ctx, operator, bond))

	_, _, err = f.keeper.ProcessModelSupportRechecks(f.ctx, 5, 100, 1<<20)
	require.NoError(t, err)

	support, err := f.keeper.GetModelSupportState(f.ctx, operator, modelID)
	require.NoError(t, err)
	require.False(t, support.SupportActive)
	require.Equal(t, types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_JAIL, support.SuspendReason,
		"a jailed operator's suspension reason must not be overwritten by the recheck's own threshold reason")
}
