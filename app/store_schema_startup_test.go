package app

import (
	"bytes"
	"encoding/hex"
	"testing"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	taskkeeper "github.com/TrueOpen/node/x/task/keeper"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// buildSplitGenesis builds a default genesis plus one funded account. It used to
// live in migration_replay_integration_test.go, which was deleted together with
// the Task StateVersion / StoreMigrations wire (fresh genesis, node_context.md
// §1.2 has no migration surface to replay).
func buildSplitGenesis(t *testing.T, app *App) ([]byte, []byte) {
	t.Helper()
	valSet, err := simtestutil.CreateRandomValidatorSet()
	require.NoError(t, err)
	priv := secp256k1.GenPrivKey()
	account := authtypes.NewBaseAccount(priv.PubKey().Address().Bytes(), priv.PubKey(), 0, 0)
	balances := []banktypes.Balance{{
		Address: account.GetAddress().String(),
		Coins:   sdk.NewCoins(sdk.NewCoin(sdk.DefaultBondDenom, sdkmath.NewInt(100_000_000_000_000))),
	}}
	genesis, err := simtestutil.GenesisStateWithValSet(app.AppCodec(), app.DefaultGenesis(), valSet, []authtypes.GenesisAccount{account}, balances...)
	require.NoError(t, err)
	raw, err := cmtjson.MarshalIndent(genesis, "", " ")
	require.NoError(t, err)
	return raw, valSet.Hash()
}

// seedBuilderDutySelection commits the minimum consistent state that puts one
// task past the Builder duty selection: the Task-owned selection plus its
// InferReceipt, the Hub BuilderSet reference that an unreleased selection must
// own, and the three Hub OPEN_VERIFY_BUILDER responsibilities the selection
// authorizes. Every digest is derived the way the production keeper derives it,
// including TRUEOPEN_SELECTED_TASK_BUILDERS_V1 over ctx.ChainID().
func seedBuilderDutySelection(t *testing.T, application *App, ctx sdk.Context) {
	t.Helper()
	const (
		builderSetID             = "builder-set-restart"
		builderSetVersion uint64 = 1
		receiptHeight            = uint64(7)
	)
	taskID := bytes.Repeat([]byte{0x91}, tasktypes.Hash32Len)
	sessionID := bytes.Repeat([]byte{0x92}, tasktypes.Hash32Len)
	builderSetHash := bytes.Repeat([]byte{0x93}, tasktypes.Hash32Len)
	builders := []string{
		sdk.AccAddress(bytes.Repeat([]byte{0x94}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0x95}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0x96}, 20)).String(),
	}
	worker := sdk.AccAddress(bytes.Repeat([]byte{0x97}, 20)).String()
	taskKey := tasktypes.TaskKey(taskID)
	// Hub spells both scope ids as hex text; the responsibility id hashes that
	// spelling, so the same strings feed the rows and the id producer.
	sessionHex, taskHex := hex.EncodeToString(sessionID), hex.EncodeToString(taskID)

	selectedHash, err := tasktypes.SelectedTaskBuildersHash(
		ctx.ChainID(), taskID, builderSetID, builderSetHash, builders,
	)
	require.NoError(t, err)
	require.NoError(t, application.TaskKeeper.TaskCore.Set(ctx, taskKey, tasktypes.TaskCoreState{
		TaskId: taskID, SessionId: sessionID,
		TaskPhase: tasktypes.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED,
	}))
	require.NoError(t, application.TaskKeeper.TaskBuilderSelection.Set(ctx, taskKey, tasktypes.TaskBuilderSelectionState{
		TaskId: taskID, BuilderSetId: builderSetID, BuilderSetHash: builderSetHash,
		SelectedTaskBuilders: builders, SelectedTaskBuilderCount: uint32(len(builders)),
		SelectedTaskBuildersHash: selectedHash, CreatedHeight: receiptHeight,
		BodyStatus: shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE,
	}))
	require.NoError(t, application.TaskKeeper.InferReceipt.Set(ctx, taskKey, tasktypes.InferReceiptState{
		TaskId: taskID, WinnerWorker: worker, ReceiptHeight: receiptHeight,
	}))
	require.NoError(t, application.HubKeeper.BuilderSet.Set(ctx, builderSetVersion, hubtypes.BuilderSetState{
		BuilderSetVersion: builderSetVersion,
		BuilderSetId:      builderSetID,
		BuilderSetHash:    builderSetHash,
	}))
	require.NoError(t, application.HubKeeper.BuilderSetTaskRef.Set(ctx,
		// v0.3 keys and stores the builder set by numeric version, not by the
		// legacy term-scoped string id.
		hubtypes.NewBuilderSetTaskRefKey(taskID, builderSetVersion),
		hubtypes.BuilderSetTaskRefState{
			TaskId: taskID, BuilderSetVersion: builderSetVersion, AcquiredHeight: receiptHeight,
		},
	))
	workerResponsibilityID, err := taskkeeper.WorkerOutputEvidenceResponsibilityID(sessionHex, taskHex, worker)
	require.NoError(t, err)
	require.NoError(t, application.HubKeeper.ServiceKeyResponsibility.Set(ctx,
		hubtypes.NewServiceKeyResponsibilityKey(
			shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, worker, workerResponsibilityID,
		),
		hubtypes.ServiceKeyResponsibilityState{
			ParticipantType:    shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
			OperatorAddress:    worker,
			ResponsibilityId:   workerResponsibilityID,
			ResponsibilityKind: hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE,
			SessionId:          sessionHex, TaskId: taskHex, CreatedHeight: receiptHeight,
			ServiceAuthorizationNonce: 1,
		},
	))

	kind := hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_OPEN_VERIFY_BUILDER
	for _, builder := range builders {
		responsibilityID, err := taskkeeper.BuilderStageResponsibilityID(sessionHex, taskHex, kind, builder)
		require.NoError(t, err)
		require.NoError(t, application.HubKeeper.ServiceKeyResponsibility.Set(ctx,
			hubtypes.NewServiceKeyResponsibilityKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, builder, responsibilityID),
			hubtypes.ServiceKeyResponsibilityState{
				ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
				OperatorAddress: builder, ResponsibilityId: responsibilityID,
				ResponsibilityKind: kind, SessionId: sessionHex, TaskId: taskHex,
				CreatedHeight: receiptHeight,
			},
		))
		seedBusObjectiveEvidenceResponsibility(t, application, ctx, sessionID, taskID, builder, receiptHeight)
	}
}

// seedBusObjectiveEvidenceResponsibility adds the Hub side an ACTIVE Builder
// selection also owns: the Builder's objective-evidence binding plus the one BUS
// responsibility row (and its ByTask index) that binding authorizes. Without it
// ensureBusObjectiveEvidenceResponsibilities rejects the selection before the
// startup path ever reaches the chain-id-bound commitment this test is about.
func seedBusObjectiveEvidenceResponsibility(
	t *testing.T, application *App, ctx sdk.Context, sessionID, taskID []byte, builder string, createdHeight uint64,
) {
	t.Helper()
	const authorizationNonce = uint64(9)
	require.NoError(t, application.HubKeeper.Builder.Set(ctx, builder, hubtypes.BuilderState{
		BuilderAddress: builder, ServiceAuthorizationNonce: authorizationNonce,
		CurrentServiceKeyStatus: hubtypes.ServiceKeyStatusActive, PendingEvidenceSubmissionCount: 1,
	}))
	responsibilityID, err := application.HubKeeper.BusObjectiveEvidenceResponsibilityID(
		shared.BusObjectiveEvidenceResponsibilityV1{
			SchemaVersion: 1, BuilderOperator: builder, SessionId: sessionID, TaskId: taskID,
			ServiceAuthorizationNonce: authorizationNonce,
		},
	)
	require.NoError(t, err)
	state := hubtypes.ServiceKeyResponsibilityState{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
		OperatorAddress: builder, ResponsibilityId: responsibilityID,
		ResponsibilityKind: hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE,
		SessionId:          hex.EncodeToString(sessionID), TaskId: hex.EncodeToString(taskID),
		CreatedHeight: createdHeight, ServiceAuthorizationNonce: authorizationNonce,
	}
	require.NoError(t, application.HubKeeper.ServiceKeyResponsibility.Set(ctx,
		hubtypes.NewServiceKeyResponsibilityKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, builder, responsibilityID),
		state,
	))
	require.NoError(t, application.HubKeeper.ServiceKeyResponsibilityByTaskIndex.Set(ctx,
		hubtypes.NewServiceKeyResponsibilityByTaskKey(
			state.SessionId, state.TaskId, state.ParticipantType, state.OperatorAddress, state.ResponsibilityId,
		),
	))
}

// A committed Builder duty selection has to survive a restart. The startup
// invariant recomputes TRUEOPEN_SELECTED_TASK_BUILDERS_V1, and that preimage is
// bound to the chain-id, so the context EnsureLoadedStoreSchemas builds for
// itself must carry the chain-id the commitment was produced under. It used to
// build a bare cmtproto.Header, which left ctx.ChainID() empty and made
// SelectedTaskBuildersHash refuse to derive anything at all -- so every restart
// of a node holding one active selection panicked with "Builder duty selection
// commitment is invalid" against state that was in fact consistent.
func TestAppStartupAcceptsCommittedBuilderDutySelection(t *testing.T) {
	db := dbm.NewMemDB()
	t.Cleanup(func() { _ = db.Close() })

	source := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID(SimAppChainID))
	raw, validatorHash := buildSplitGenesis(t, source)
	initChainAndCommit(t, source, raw, validatorHash)

	ctx := source.NewUncachedContext(false, cmtproto.Header{
		Height: source.LastBlockHeight(), ChainID: SimAppChainID,
	})
	seedBuilderDutySelection(t, source, ctx)
	commit := source.CommitMultiStore().Commit()

	restarted := New(log.NewNopLogger(), db, nil, false, smokeAppOptions(), baseapp.SetChainID(SimAppChainID))
	require.NoError(t, restarted.LoadHeight(commit.Version))
	require.NoError(t, restarted.EnsureLoadedStoreSchemas())

	// The same database must also come up through the real startup path, which
	// panics rather than returning the error.
	require.NotPanics(t, func() {
		_ = New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID(SimAppChainID))
	})
}

func TestAppStartupRejectsNonEmptyDatabaseWithoutCurrentSchema(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		removeSchema  func(*App, sdk.Context) error
		errorContains string
	}{
		// Fresh genesis deleted both StateVersion and
		// StoreMigrations, so Keeper.EnsureCurrentStoreSchema now only asserts
		// that the module was initialized at all. Removing TaskParams is the
		// only remaining way to present an uninitialized Task store; restore a
		// real schema-version case if a migration surface is ever reintroduced.
		{
			name: "task",
			removeSchema: func(app *App, ctx sdk.Context) error {
				return app.TaskKeeper.Params.Remove(sdk.WrapSDKContext(ctx))
			},
			errorContains: "incompatible task store",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db := dbm.NewMemDB()
			t.Cleanup(func() { _ = db.Close() })

			source := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID(SimAppChainID))
			raw, validatorHash := buildSplitGenesis(t, source)
			initChainAndCommit(t, source, raw, validatorHash)

			ctx := source.NewUncachedContext(false, cmtproto.Header{Height: source.LastBlockHeight()})
			require.NoError(t, testCase.removeSchema(source, ctx))
			corruptCommit := source.CommitMultiStore().Commit()

			exportApp := New(log.NewNopLogger(), db, nil, false, smokeAppOptions(), baseapp.SetChainID(SimAppChainID))
			require.NoError(t, exportApp.LoadHeight(corruptCommit.Version))
			exportErr := exportApp.EnsureLoadedStoreSchemas()
			require.ErrorContains(t, exportErr, testCase.errorContains)
			require.ErrorContains(t, exportErr, "delete/reset the node database")

			var panicValue any
			func() {
				defer func() { panicValue = recover() }()
				_ = New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID(SimAppChainID))
			}()
			require.NotNil(t, panicValue)
			startupErr, ok := panicValue.(error)
			require.True(t, ok, "startup panic must be an error, got %T", panicValue)
			require.ErrorContains(t, startupErr, testCase.errorContains)
			require.ErrorContains(t, startupErr, "delete/reset the node database")
		})
	}
}
