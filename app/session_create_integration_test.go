package app

// App-level CreateSession coverage. The cross-phase integration scenario is
// maintained in x/task/keeper/integration_flow_test.go, where temporary
// Cortex/Nexus adapters can be explicit without weakening app wiring tests.
//
// Coverage:
//   - MsgCreateSession succeeds under real depinject-wired app.
//   - Generated session_id and nonce are deterministic and increment
//     across successive calls by the same signer.
//   - StreamState / SessionNonce all persist with fields matching the
//     response.
//   - Independent signers do not collide (each has its own nonce
//     namespace).
//
// MsgCreateSessionResponse no longer carries owner or
// next_session_nonce; the wire is (session_id, session_nonce, status). The
// owner / next-nonce assertions below now read the persisted SessionNonceState
// and StreamState instead of the response. Re-check them against the final
// msg_server_session.go semantics once T-1 lands.

import (
	"bytes"
	"cosmossdk.io/collections"
	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"testing"

	"cosmossdk.io/log"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	taskkeeper "github.com/TrueOpen/node/x/task/keeper"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// buildMinimalGenesis boots a validator + delegator only. No module
// account funding; the test does not exercise bank transfers.
func buildMinimalGenesis(t *testing.T, app *App, additionalAccounts ...authtypes.GenesisAccount) ([]byte, []byte) {
	t.Helper()

	valSet, err := simtestutil.CreateRandomValidatorSet()
	require.NoError(t, err)

	delegPriv := secp256k1.GenPrivKey()
	delegAcc := authtypes.NewBaseAccount(delegPriv.PubKey().Address().Bytes(), delegPriv.PubKey(), 0, 0)
	accs := append([]authtypes.GenesisAccount{delegAcc}, additionalAccounts...)
	balances := []banktypes.Balance{{
		Address: delegAcc.GetAddress().String(),
		Coins:   sdk.NewCoins(sdk.NewCoin(sdk.DefaultBondDenom, sdkmath.NewInt(100_000_000_000_000))),
	}}

	for _, account := range additionalAccounts {
		balances = append(balances, banktypes.Balance{Address: account.GetAddress().String(), Coins: sdk.NewCoins(sdk.NewInt64Coin(hubtypes.DefaultHubParams().Phase0.BusinessDenom, 100_000_000))})
	}

	genesisState, err := simtestutil.GenesisStateWithValSet(app.AppCodec(), app.DefaultGenesis(), valSet, accs, balances...)
	require.NoError(t, err)

	stateBytes, err := cmtjson.MarshalIndent(genesisState, "", " ")
	require.NoError(t, err)
	return stateBytes, valSet.Hash()
}

// makeSigner returns a bech32 trueopen signer address from a fresh
// secp256k1 keypair, wrapped as a GenesisAccount for genesis inclusion.
func makeSigner(t *testing.T, accNum uint64) (sdk.AccAddress, authtypes.GenesisAccount) {
	t.Helper()
	priv := (&ethsecp256k1.PrivKey{Key: secp256k1.GenPrivKey().Bytes()})
	addr := sdk.AccAddress(priv.PubKey().Address())
	return addr, authtypes.NewBaseAccount(addr, priv.PubKey(), accNum, 0)
}

func TestCreateSessionIncrementsNonceAcrossCalls(t *testing.T) {
	signerAddr, signerAcc := makeSigner(t, 1)

	db := dbm.NewMemDB()
	defer db.Close()
	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(),
		baseapp.SetChainID(SimAppChainID))
	stateBytes, valHash := buildMinimalGenesis(t, app, signerAcc)
	initChainAndCommit(t, app, stateBytes, valHash)

	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1, ChainID: SimAppChainID})
	initialBuilder := seedSessionBuilder(t, app, ctx)
	ms := taskkeeper.NewMsgServerImpl(app.TaskKeeper)

	// First call: nonce starts at 0.
	resp1, err := ms.CreateSession(ctx, &tasktypes.MsgCreateSession{SignerAddress: signerAddr.String(), InitialBuilder: initialBuilder, InitialDeposit: shared.NewAmount(1_000_000)})
	require.NoError(t, err)
	require.NotEmpty(t, resp1.SessionId)
	require.Equal(t, uint64(0), resp1.SessionNonce, "first session nonce must be 0")

	// Second call: nonce = 1.
	resp2, err := ms.CreateSession(ctx, &tasktypes.MsgCreateSession{SignerAddress: signerAddr.String(), InitialBuilder: initialBuilder, InitialDeposit: shared.NewAmount(1_000_000)})
	require.NoError(t, err)
	require.Equal(t, uint64(1), resp2.SessionNonce)
	require.NotEqual(t, resp1.SessionId, resp2.SessionId,
		"different nonce must derive different session_id — otherwise SessionNonce collision")

	// Third call: nonce = 2.
	resp3, err := ms.CreateSession(ctx, &tasktypes.MsgCreateSession{SignerAddress: signerAddr.String(), InitialBuilder: initialBuilder, InitialDeposit: shared.NewAmount(1_000_000)})
	require.NoError(t, err)
	require.Equal(t, uint64(2), resp3.SessionNonce)

	// SessionNonce state persists next_session_nonce.
	nonceState, err := app.TaskKeeper.ReadSessionNonce(ctx, signerAddr.String())
	require.NoError(t, err)
	require.Equal(t, uint64(3), nonceState.NextSessionNonce,
		"persisted next_session_nonce must equal one past the last issued nonce")

	// StreamState for each session persists with owner + active status.
	for i, resp := range []*tasktypes.MsgCreateSessionResponse{resp1, resp2, resp3} {
		stream, err := app.TaskKeeper.ReadStream(ctx, resp.SessionId)
		require.NoErrorf(t, err, "session %d state must persist", i)
		require.Equal(t, resp.SessionId, stream.SessionId)
		require.Equal(t, signerAddr.String(), stream.OwnerUserAddress)
		require.Equal(t, tasktypes.SessionStatus_SESSION_STATUS_ACTIVE, stream.Status)
	}
}

func TestCreateSessionIsolatesSignerNamespaces(t *testing.T) {
	// Two independent signers each starting from nonce 0. Their
	// SessionNonce namespaces must not interfere.
	signerA, accA := makeSigner(t, 1)
	signerB, accB := makeSigner(t, 2)

	db := dbm.NewMemDB()
	defer db.Close()
	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(),
		baseapp.SetChainID(SimAppChainID))
	stateBytes, valHash := buildMinimalGenesis(t, app, accA, accB)
	initChainAndCommit(t, app, stateBytes, valHash)

	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1, ChainID: SimAppChainID})
	initialBuilder := seedSessionBuilder(t, app, ctx)
	ms := taskkeeper.NewMsgServerImpl(app.TaskKeeper)

	// A creates two sessions.
	_, err := ms.CreateSession(ctx, &tasktypes.MsgCreateSession{SignerAddress: signerA.String(), InitialBuilder: initialBuilder, InitialDeposit: shared.NewAmount(1_000_000)})
	require.NoError(t, err)
	respA2, err := ms.CreateSession(ctx, &tasktypes.MsgCreateSession{SignerAddress: signerA.String(), InitialBuilder: initialBuilder, InitialDeposit: shared.NewAmount(1_000_000)})
	require.NoError(t, err)
	require.Equal(t, uint64(1), respA2.SessionNonce)

	// B's first session must still be nonce = 0 (independent namespace).
	respB1, err := ms.CreateSession(ctx, &tasktypes.MsgCreateSession{SignerAddress: signerB.String(), InitialBuilder: initialBuilder, InitialDeposit: shared.NewAmount(1_000_000)})
	require.NoError(t, err)
	require.Equal(t, uint64(0), respB1.SessionNonce,
		"different signers must not share SessionNonce; got B nonce %d, want 0", respB1.SessionNonce)
	require.NotEqual(t, respA2.SessionId, respB1.SessionId,
		"different signers must derive different session_id from the same nonce")
}

func TestCreateSessionRejectsMalformedSigner(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()
	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(),
		baseapp.SetChainID(SimAppChainID))
	stateBytes, valHash := buildMinimalGenesis(t, app)
	initChainAndCommit(t, app, stateBytes, valHash)

	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1, ChainID: SimAppChainID})
	ms := taskkeeper.NewMsgServerImpl(app.TaskKeeper)

	// Non-bech32 signer must be rejected by the address codec check.
	_, err := ms.CreateSession(ctx, &tasktypes.MsgCreateSession{SignerAddress: "not-a-bech32-address"})
	require.Error(t, err)

	// Nil msg is also rejected.
	_, err = ms.CreateSession(ctx, nil)
	require.Error(t, err)
}

func seedSessionBuilder(t *testing.T, application *App, ctx sdk.Context) string {
	t.Helper()
	operator := sdk.AccAddress(bytes.Repeat([]byte{0x71}, 20)).String()
	key := (&ethsecp256k1.PrivKey{Key: secp256k1.GenPrivKey().Bytes()}).PubKey()
	require.NoError(t, application.HubKeeper.StoreBuilder(ctx, operator, hubtypes.BuilderState{SchemaVersion: 1, BuilderAddress: operator, CurrentServiceAddress: sdk.AccAddress(key.Address()).String(), CurrentServicePubkey: key.Bytes(), CurrentServiceKeyStatus: hubtypes.ServiceKeyStatusActive, ServiceAuthorizationNonce: 1, CurrentDescriptorVersion: 1, RegisteredHeight: uint64(ctx.BlockHeight())}))
	admission, err := application.HubKeeper.BuilderAdmission.Get(ctx, operator)
	require.ErrorIs(t, err, collections.ErrNotFound)
	admission.BuilderAddress = bytes.Repeat([]byte{0x71}, 20)
	admission.Status = int32(hubtypes.BuilderStatus_BUILDER_STATUS_ADMITTED)
	admission.CurrentBuilderSetVersion = 1
	admission.UpdatedHeight = uint64(ctx.BlockHeight())
	require.NoError(t, application.HubKeeper.BuilderAdmission.Set(ctx, operator, admission))
	members, err := hubkeeper.BuilderSetMembersHash([][]byte{bytes.Repeat([]byte{0x71}, 20)})
	require.NoError(t, err)
	hash, err := hubkeeper.BuilderSetHash(ctx.ChainID(), 1, "session-test", uint64(ctx.BlockHeight()), 1, members)
	require.NoError(t, err)
	set := hubtypes.BuilderSetState{BuilderSetVersion: 1, BuilderSetId: "session-test", EffectiveHeight: uint64(ctx.BlockHeight()), ActiveBuilderCount: 1, ActiveBuilders: []string{operator}, BuilderSetHash: hash, BuilderSetMembersHash: members, BodyStatus: shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE}
	require.NoError(t, application.HubKeeper.StoreBuilderSet(ctx, set))
	require.NoError(t, application.HubKeeper.BuilderSetByHeightIndex.Set(ctx, hubtypes.NewBuilderSetByHeightKey(set.EffectiveHeight, 1), set.BuilderSetId))
	require.NoError(t, application.HubKeeper.CurrentBuilderSet.Set(ctx, hubtypes.CurrentBuilderSetState{Mode: hubtypes.BuilderSetModeV1_BUILDER_SET_MODE_V1_GOVERNED_FIXED_V1, BuilderSetVersion: 1, BuilderSetId: set.BuilderSetId, BuilderSetHash: hash, BuilderSetMembersHash: members, EffectiveHeight: set.EffectiveHeight}))
	return operator
}
