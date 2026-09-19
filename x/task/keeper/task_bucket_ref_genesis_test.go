package keeper_test

import (
	"context"
	"fmt"
	"math"
	"testing"

	storetypes "cosmossdk.io/store/types"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/keeper"
	module "github.com/TrueOpen/node/x/task/module"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

type taskBucketGenesisHubStub struct {
	stubHubKeeper
	refCount      uint64
	missingBucket bool
}

func (s taskBucketGenesisHubStub) GetParameterBucketVersion(ctx context.Context, kind shared.BucketKind, key string, version uint64) (hubtypes.ParameterBucketVersionState, error) {
	if s.missingBucket {
		return hubtypes.ParameterBucketVersionState{}, fmt.Errorf("parameter bucket not found")
	}
	state := hubtypes.ParameterBucketVersionState{
		BucketKind: kind, BucketKey: key, Version: version,
		SchemaVersion: hubtypes.ParameterBucketSchemaVersionV1, EffectiveHeight: 1, TaskRefCount: s.refCount,
	}
	// v0.3 removed the ReferenceBucket variant; TIMEOUT is the only bucket kind
	// and its entries are a plain field rather than a oneof.
	entries := []hubtypes.TimeoutBucketEntryV1{{UpperWorkUnitsInclusive: math.MaxUint64, TimeoutBlocks: 40}}
	_, state.EncodedSizeBytes, _ = hubtypes.CanonicalTimeoutBucketEntries(entries)
	state.EntryCount = 1
	state.TimeoutEntries = hubtypes.TimeoutBucketEntriesV1{Entries: entries}
	hash, err := hubtypes.ParameterBucketContentHash(sdk.UnwrapSDKContext(ctx).ChainID(), state)
	if err != nil {
		return hubtypes.ParameterBucketVersionState{}, err
	}
	state.ContentHash = hash
	return state, nil
}

func initTaskBucketGenesisFixture(t *testing.T, hub tasktypes.HubKeeper) *fixture {
	t.Helper()
	encCfg := moduletestutil.MakeTestEncodingConfig(module.AppModule{})
	addressCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	storeKey := storetypes.NewKVStoreKey(tasktypes.StoreKey)
	storeService := runtime.NewKVStoreService(storeKey)
	transientKey := storetypes.NewTransientStoreKey("transient_task_bucket_test")
	transientStoreService := runtime.NewTransientStoreService(transientKey)
	ctx := testutil.DefaultContextWithDB(t, storeKey, transientKey).Ctx
	authority := authtypes.NewModuleAddress(tasktypes.GovModuleName)
	k := keeper.NewKeeper(storeService, transientStoreService, encCfg.Codec, addressCodec, authority, taskExternalAuthKeeper{}, taskExternalBankKeeper{}, hub)
	return &fixture{ctx: sdk.WrapSDKContext(ctx.WithChainID("trueopen-test-1")), keeper: k}
}

func taskGenesisWithBucketRefs(t *testing.T, chainID string) *tasktypes.GenesisState {
	t.Helper()
	genesis := taskGenesisV1(t, chainID)
	taskID := genesis.TaskCores[0].TaskId
	// v0.3 removed the REFERENCE bucket kind; TIMEOUT is the only one left.
	genesis.TaskBucketRefs = []tasktypes.TaskBucketRefState{
		{TaskId: taskID, BucketKind: shared.BucketKind_BUCKET_KIND_TIMEOUT, BucketKey: hubtypes.DefaultParameterBucketKey, Version: 1, AcquiredHeight: 5},
	}
	return genesis
}

func TestInitGenesisValidatesTaskBucketRefsAgainstHubCounts(t *testing.T) {
	valid := initTaskBucketGenesisFixture(t, taskBucketGenesisHubStub{refCount: 1})
	require.NoError(t, valid.keeper.InitGenesis(valid.ctx, *taskGenesisWithBucketRefs(t, genesisChainID(valid))))

	timeoutDrift := initTaskBucketGenesisFixture(t, taskBucketGenesisHubStub{refCount: 1})
	timeoutDriftGenesis := taskGenesisWithBucketRefs(t, genesisChainID(timeoutDrift))
	timeoutDriftGenesis.TaskAssignments[0].InferTimeoutBlocks++
	timeoutDriftGenesis.TaskAssignments[0].InferDeadlineHeight++
	err := timeoutDrift.keeper.InitGenesis(timeoutDrift.ctx, *timeoutDriftGenesis)
	require.ErrorContains(t, err, "infer timeout does not match the retained bucket")

	drifted := initTaskBucketGenesisFixture(t, taskBucketGenesisHubStub{refCount: 2})
	err = drifted.keeper.InitGenesis(drifted.ctx, *taskGenesisWithBucketRefs(t, genesisChainID(drifted)))
	require.ErrorContains(t, err, "task_ref_count=2, imported Task refs=1")

	missing := initTaskBucketGenesisFixture(t, taskBucketGenesisHubStub{refCount: 1, missingBucket: true})
	err = missing.keeper.InitGenesis(missing.ctx, *taskGenesisWithBucketRefs(t, genesisChainID(missing)))
	require.ErrorContains(t, err, "missing Hub parameter bucket version")

	duplicate := initTaskBucketGenesisFixture(t, taskBucketGenesisHubStub{refCount: 2})
	duplicateGenesis := taskGenesisWithBucketRefs(t, genesisChainID(duplicate))
	duplicateGenesis.TaskBucketRefs = append(duplicateGenesis.TaskBucketRefs, duplicateGenesis.TaskBucketRefs[0])
	err = duplicate.keeper.InitGenesis(duplicate.ctx, *duplicateGenesis)
	require.ErrorContains(t, err, "duplicate task_bucket_ref")
}
