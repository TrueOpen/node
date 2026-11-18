package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// epochHubKeeper is the Hub stub with an epoch length: replacing a params
// version archives the old body, and the archive's prune floor is an epoch
// boundary.
type epochHubKeeper struct{ stubHubKeeper }

func (h epochHubKeeper) GetHubParams(ctx sdk.Context) hubtypes.HubParamsSnapshot {
	params := h.stubHubKeeper.GetHubParams(ctx)
	params.EpochLengthBlocks = hubtypes.DefaultHubParams().Epoch.EpochLengthBlocks
	return params
}

// preAnchorParams is a params body as a chain recorded it before the anchor
// audit existed: none of the anchor parameters is present.
func preAnchorParams() tasktypes.TaskParamsV1 {
	p := tasktypes.DefaultTaskParams()
	v := &p.Verification
	v.AnchorRejectSlashBps, v.AnchorTimeoutSlashBps = 0, 0
	v.AnchorAuditMinRateBps, v.AnchorAuditRiskMarginBps = 0, 0
	v.AnchorOpenWindowBlocks, v.AnchorProofWindowBlocks, v.AnchorVerdictWindowBlocks = 0, 0, 0
	return p
}

func requireRecommendedAnchorParams(t *testing.T, p tasktypes.TaskParamsV1) {
	t.Helper()
	v := p.Verification
	require.Equal(t, tasktypes.RecommendedAnchorOpenWindowBlocks, v.AnchorOpenWindowBlocks)
	require.Equal(t, tasktypes.RecommendedAnchorProofWindowBlocks, v.AnchorProofWindowBlocks,
		"the upgrade must not install the historical proof window an honest loaded Worker misses")
	require.Equal(t, tasktypes.RecommendedAnchorVerdictWindowBlocks, v.AnchorVerdictWindowBlocks,
		"the upgrade must not install the historical verdict window that expires mid-verification")
	require.Equal(t, tasktypes.DefaultAnchorRejectSlashBPS, v.AnchorRejectSlashBps)
	require.Equal(t, tasktypes.DefaultAnchorTimeoutSlashBPS, v.AnchorTimeoutSlashBps)
	require.Equal(t, tasktypes.DefaultAnchorAuditMinRateBPS, v.AnchorAuditMinRateBps)
	require.Equal(t, tasktypes.DefaultAnchorAuditRiskMarginBPS, v.AnchorAuditRiskMarginBps)
	require.NoError(t, p.Validate())
}

// The anchor upgrade writes a new params version; that version carries the
// recommended windows, and the replaced pre-anchor body is archived unchanged
// with its recorded commitment.
func TestAnchorUpgradeWritesRecommendedWindows(t *testing.T) {
	f := initFixtureWithHub(t, epochHubKeeper{})
	chainID := sdk.UnwrapSDKContext(f.ctx).ChainID()
	old := preAnchorParams()
	oldHash, err := tasktypes.TaskParamsHashV1(chainID, 1, old)
	require.NoError(t, err)
	require.NoError(t, f.keeper.Params.Set(f.ctx, old))
	require.NoError(t, f.keeper.ParamsMeta.Set(f.ctx, tasktypes.TaskParamsMetaState{ParamsVersion: 1, ParamsHash: oldHash, UpdatedHeight: 1}))

	require.NoError(t, f.keeper.InitializeAnchorParams(f.ctx))

	got, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	requireRecommendedAnchorParams(t, got)
	want := old.WithAnchorDefaults()
	require.Equal(t, want, got, "nothing but the anchor parameters changes")
	meta, err := f.keeper.GetTaskParamsMeta(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), meta.ParamsVersion)
	newHash, err := tasktypes.TaskParamsHashV1(chainID, 2, got)
	require.NoError(t, err)
	require.Equal(t, newHash, meta.ParamsHash)
	archived, err := f.keeper.ParamsHistory.Get(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, old, archived.Params, "the replaced version keeps its recorded body")
	archivedHash, err := tasktypes.TaskParamsHashV1(chainID, 1, archived.Params)
	require.NoError(t, err)
	require.Equal(t, oldHash, archivedHash, "and its recorded commitment")

	// Running it again changes nothing.
	require.NoError(t, f.keeper.InitializeAnchorParams(f.ctx))
	again, err := f.keeper.GetTaskParamsMeta(f.ctx)
	require.NoError(t, err)
	require.Equal(t, meta, again)
}

// A body that already carries anchor windows was set by genesis or governance:
// the upgrade neither changes it nor writes a version.
func TestAnchorUpgradeKeepsChosenWindows(t *testing.T) {
	f := initFixture(t)
	chainID := sdk.UnwrapSDKContext(f.ctx).ChainID()
	chosen := tasktypes.DefaultTaskParams()
	v := &chosen.Verification
	v.AnchorOpenWindowBlocks, v.AnchorProofWindowBlocks, v.AnchorVerdictWindowBlocks = 100, 200, 300
	v.AnchorAuditMinRateBps = 7
	hash, err := tasktypes.TaskParamsHashV1(chainID, 5, chosen)
	require.NoError(t, err)
	meta := tasktypes.TaskParamsMetaState{ParamsVersion: 5, ParamsHash: hash, UpdatedHeight: 9}
	require.NoError(t, f.keeper.Params.Set(f.ctx, chosen))
	require.NoError(t, f.keeper.ParamsMeta.Set(f.ctx, meta))

	require.NoError(t, f.keeper.InitializeAnchorParams(f.ctx))

	got, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, chosen, got)
	after, err := f.keeper.GetTaskParamsMeta(f.ctx)
	require.NoError(t, err)
	require.Equal(t, meta, after)
}

// A new binary cold-starting from a genesis written before the anchor audit
// fills the recommended windows; a genesis that carries windows keeps them.
func TestAnchorColdStartWritesRecommendedWindows(t *testing.T) {
	f := initFixtureWithHub(t, epochHubKeeper{})
	genesis := tasktypes.DefaultGenesis()
	genesis.Params = preAnchorParams()
	require.NoError(t, genesis.Validate())
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	got, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	requireRecommendedAnchorParams(t, got)
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	requireRecommendedAnchorParams(t, exported.Params)

	g := initFixture(t)
	chosen := tasktypes.DefaultGenesis()
	v := &chosen.Params.Verification
	v.AnchorOpenWindowBlocks, v.AnchorProofWindowBlocks, v.AnchorVerdictWindowBlocks = 100, 200, 300
	require.NoError(t, g.keeper.InitGenesis(g.ctx, *chosen))
	kept, err := g.keeper.Params.Get(g.ctx)
	require.NoError(t, err)
	require.Equal(t, chosen.Params, kept)

	// The stock genesis itself starts on the recommended windows.
	h := initFixture(t)
	require.NoError(t, h.keeper.InitGenesis(h.ctx, *tasktypes.DefaultGenesis()))
	stock, err := h.keeper.Params.Get(h.ctx)
	require.NoError(t, err)
	requireRecommendedAnchorParams(t, stock)
}

// A pre-anchor genesis that carries live Sessions and Tasks cold-starts too:
// its rows stay bound to the body they were created under, which becomes a
// referenced history row, and the current version carries the recommended
// windows.
func TestAnchorColdStartKeepsLiveRowsOnTheirRecordedParams(t *testing.T) {
	f := initFixtureWithHub(t, epochHubKeeper{})
	chainID := sdk.UnwrapSDKContext(f.ctx).ChainID()
	genesis := taskGenesisV1(t, chainID)
	genesis.Params = preAnchorParams()
	require.NotEmpty(t, genesis.TaskCores)
	require.NoError(t, genesis.Validate())
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	got, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	requireRecommendedAnchorParams(t, got)
	bound, err := f.keeper.ParamsForTask(f.ctx, tasktypes.NewTaskKey(genesis.TaskCores[0].TaskId))
	require.NoError(t, err)
	require.Equal(t, genesis.Params, bound, "a Task imported from a pre-anchor genesis keeps its recorded body")
	require.NoError(t, f.keeper.EnsureParamsVersionInvariant(f.ctx))

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	requireRecommendedAnchorParams(t, exported.Params)
}
