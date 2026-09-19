package app

// timeout/sweep/claim failure-path e2e.
//
// The plan asks for at least one failure-path e2e among
// timeout/sweep/claim/fault. The happy-path settle lifecycle is gated
// behind the K12 fixture migration (see session_create_integration_test.go),
// but the failure paths that reject at the FIRST message do not need any
// of that lifecycle scaffolding — they exercise the app-wired MsgServer
// plus address codec plus keeper reject logic end-to-end and assert the
// store is left untouched.
//
// This file installs:
//   - the claim failure path (MsgClaimEarnings): a malformed signer is
//     rejected with ErrInvalidUserAddress and mutates no earnings state.
//
// These are the real user-triggered failure branches an operator hits
// when they fat-finger an address, so they are worth pinning at app level
// even before the full settle lifecycle lands.
//
// The sweep failure path is deleted. MsgSweepExpiredTask no
// longer exists; §5.9 sweeping is MsgSweepDeadline over the typed
// DeadlineKindV1 indexes, and TaskStatus / TaskSettlementState are both gone
// (TaskCore carries the stage union now). The three deleted cases were:
// missing task rejected with ErrTaskNotFound leaving no settlement/status row,
// malformed submitter, and empty session_id / task_id scope. Re-install them
// against MsgSweepDeadline once T-1's msg_server_sweep_deadline.go lands.

import (
	"testing"

	"cosmossdk.io/log"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// bootAppWithSigner boots a minimal validator+delegator app that also
// funds the given signer as a genesis account, returning the running app
// and a live checkState context.
func bootAppWithSigner(t *testing.T, signerAcc authtypes.GenesisAccount) (*App, sdk.Context) {
	t.Helper()
	db := dbm.NewMemDB()
	t.Cleanup(func() { _ = db.Close() })

	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(),
		baseapp.SetChainID(SimAppChainID))
	stateBytes, valHash := buildMinimalGenesis(t, app, signerAcc)
	initChainAndCommit(t, app, stateBytes, valHash)

	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1})
	return app, ctx
}

func TestClaimEarningsRejectsMalformedSigner(t *testing.T) {
	_, signerAcc := makeSigner(t, 1)
	app, ctx := bootAppWithSigner(t, signerAcc)
	// ClaimEarnings is a hub-owned message; earnings state lives in the
	// hub keeper.
	ms := hubkeeper.NewMsgServerImpl(app.HubKeeper)

	_, err := ms.ClaimEarnings(ctx, &hubtypes.MsgClaimEarnings{
		SignerAddress: "not-a-bech32-address",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, hubtypes.ErrInvalidUserAddress)

	// State invariance: the malformed claim must not create an earnings
	// row under the bogus key.
	has, err := app.HubKeeper.Earnings.Has(ctx, "not-a-bech32-address")
	require.NoError(t, err)
	require.False(t, has, "rejected claim must not create an earnings row")
}
