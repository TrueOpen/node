package cmd

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	nodeapp "github.com/TrueOpen/node/app"
	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	taskkeeper "github.com/TrueOpen/node/x/task/keeper"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestLocalnetGenesisSeedBootsReadyForTasks(t *testing.T) {
	const chainID = "localnet-seed-integration"

	database := dbm.NewMemDB()
	t.Cleanup(func() { _ = database.Close() })
	options := simtestutil.AppOptionsMap{flags.FlagHome: t.TempDir()}
	application := nodeapp.New(
		log.NewNopLogger(),
		database,
		nil,
		true,
		options,
		baseapp.SetChainID(chainID),
	)

	validatorSet, err := simtestutil.CreateRandomValidatorSet()
	require.NoError(t, err)
	userKey := secp256k1.GenPrivKey()
	userAccount := authtypes.NewBaseAccount(userKey.PubKey().Address().Bytes(), userKey.PubKey(), 0, 0)
	userBalance := banktypes.Balance{
		Address: userAccount.GetAddress().String(),
		Coins: sdk.NewCoins(
			sdk.NewCoin(sdk.DefaultBondDenom, sdkmath.NewInt(100_000_000_000_000)),
		),
	}
	appState, err := simtestutil.GenesisStateWithValSet(
		application.AppCodec(),
		application.DefaultGenesis(),
		validatorSet,
		[]authtypes.GenesisAccount{userAccount},
		userBalance,
	)
	require.NoError(t, err)
	appStateJSON, err := json.Marshal(appState)
	require.NoError(t, err)
	genesisDocument, err := json.Marshal(map[string]any{
		"chain_id":       chainID,
		"initial_height": "1",
		"app_state":      json.RawMessage(appStateJSON),
	})
	require.NoError(t, err)

	seed, err := readGenesisSeed(filepath.Join("..", "..", "..", "config", "localnet_genesis_seed.json"))
	require.NoError(t, err)
	seededDocument, err := applyGenesisSeed(application.AppCodec(), genesisDocument, seed)
	require.NoError(t, err)
	var decoded struct {
		AppState json.RawMessage `json:"app_state"`
	}
	require.NoError(t, json.Unmarshal(seededDocument, &decoded))

	_, err = application.InitChain(&abci.RequestInitChain{
		ChainId:         chainID,
		ConsensusParams: simtestutil.DefaultConsensusParams,
		AppStateBytes:   decoded.AppState,
	})
	require.NoError(t, err)
	for height := int64(1); height <= 6; height++ {
		_, err = application.FinalizeBlock(&abci.RequestFinalizeBlock{
			Height:             height,
			Hash:               bytes.Repeat([]byte{byte(height)}, 32),
			NextValidatorsHash: validatorSet.Hash(),
		})
		require.NoError(t, err)
		_, err = application.Commit()
		require.NoError(t, err)
	}

	ctx := application.NewContextLegacy(true, cmtproto.Header{Height: 7})
	builderSet, err := application.HubKeeper.BuilderSet.Get(ctx, 1)
	require.NoError(t, err)
	require.Len(t, builderSet.ActiveBuilders, 3)
	require.Equal(t, uint64(1), builderSet.EffectiveHeight)

	modelSeed := seed.Models[0].Profile
	model, err := application.HubKeeper.Model.Get(ctx, modelSeed.ModelID)
	require.NoError(t, err)
	require.Equal(t, hubtypes.ModelStatusActive, model.Status)
	profile, err := application.HubKeeper.Profile.Get(
		ctx,
		hubtypes.NewProfileStateKey(modelSeed.ModelID, modelSeed.ProfileVersion),
	)
	require.NoError(t, err)
	require.Equal(t, hubtypes.ModelStatusActive, profile.Status)
	require.Equal(t, uint32(4), profile.ActiveSupporterCount)
	for _, cortex := range seed.CortexNodes {
		support, err := application.HubKeeper.ModelSupport.Get(
			ctx,
			hubtypes.NewModelSupportKey(cortex.OperatorAddress, modelSeed.ModelID, modelSeed.ProfileVersion),
		)
		require.NoError(t, err)
		require.True(t, support.DeclaredSupport)
		require.True(t, support.SupportActive)
	}
	hubQuery := hubkeeper.NewQueryServerImpl(application.HubKeeper)
	currentPool, err := hubQuery.CurrentCandidatePool(ctx, &hubtypes.QueryCurrentCandidatePoolRequest{})
	require.NoError(t, err)
	require.Equal(t, uint32(len(seed.CortexNodes)), currentPool.Snapshot.ActiveCount)
	members, err := hubQuery.CandidatePoolMembers(
		ctx, &hubtypes.QueryCandidatePoolMembersRequest{SnapshotId: currentPool.Snapshot.SnapshotId},
	)
	require.NoError(t, err)
	require.Len(t, members.Members, len(seed.CortexNodes))
	wantOperators := make(map[string]struct{}, len(seed.CortexNodes))
	for _, cortex := range seed.CortexNodes {
		wantOperators[cortex.OperatorAddress] = struct{}{}
	}
	for _, member := range members.Members {
		_, exists := wantOperators[member.OperatorAddress]
		require.Truef(t, exists, "unexpected candidate operator %s", member.OperatorAddress)
		delete(wantOperators, member.OperatorAddress)
	}
	require.Empty(t, wantOperators)

	session, err := taskkeeper.NewMsgServerImpl(application.TaskKeeper).CreateSession(
		ctx,
		&tasktypes.MsgCreateSession{SignerAddress: userAccount.GetAddress().String()},
	)
	require.NoError(t, err)
	// MsgCreateSessionResponse.status is the shared
	// hub.v1.MutationStatusV1, not a Task-local session status string.
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, session.Status)
	require.NotEmpty(t, session.SessionId)
}
