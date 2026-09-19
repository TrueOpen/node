package app

// PreBlocker tests.
//
// Covered properties:
//   - Happy path: PreBlocker successfully verifies + persists (via fake
//     keeper) and chains to inner PreBlocker.
//   - Missing sentinel at a pre-required height: a placeholder is written and
//     the inner handler runs as usual.
//   - Missing sentinel at a required height: PANIC — ProcessProposal should
//     have rejected it, and the same section of the protocol forbids writing a
//     placeholder in that range, so a hard stop beats a silent downgrade.
//   - Corrupt sentinel at a required height: PANIC for the same reason.
//   - Policy unreadable: bubbles up as an ordinary error (not a panic, because
//     the block may well have deserved rejection in the first place).
//   - Staking lookup failure: bubbles up as a non-panic error.
//   - Keeper persist failure: bubbles up as a non-panic error.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"

	"cosmossdk.io/log"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	nodeante "github.com/TrueOpen/node/app/ante"
	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// persistingKeeper wraps realCryptoKeeper with a controllable persist path.
type persistingKeeper struct {
	realCryptoKeeper
	persistErr           error
	persisted            *hubtypes.BeaconCarrier
	placeholderPersisted *hubtypes.BeaconState
}

func (p *persistingKeeper) WriteBlockBeacon(_ sdk.Context, height uint64, _ []byte, sourceTag string) (hubtypes.BeaconState, error) {
	state := hubtypes.BeaconState{Height: height, SourceTag: sourceTag}
	p.placeholderPersisted = &state
	return state, nil
}

func (p *persistingKeeper) ValidateAndWriteVerifiedBeacon(
	ctx sdk.Context,
	carrier hubtypes.BeaconCarrier,
	proposer []byte,
	operator string,
	verifier hubkeeper.BeaconProofVerifier,
) (hubtypes.BeaconState, error) {
	if p.persistErr != nil {
		return hubtypes.BeaconState{}, p.persistErr
	}
	if _, err := p.realCryptoKeeper.ValidateBeaconCarrier(ctx, carrier, proposer, operator, verifier); err != nil {
		return hubtypes.BeaconState{}, err
	}
	p.persisted = &carrier
	return hubtypes.BeaconState{Height: carrier.Height, Verified: true}, nil
}

func preBlockerCtx() sdk.Context {
	return sdk.NewContext(nil, cmtproto.Header{Height: 1}, false, log.NewNopLogger()).
		WithContext(context.Background())
}

func makeSentinelWithSigner(t *testing.T, height uint64, input []byte) (sentinel []byte, pubkey []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	proof, beta, err := nodeante.Prove(priv, input)
	require.NoError(t, err)
	sentinel, err = EncodeBeaconSentinel(&hubtypes.BeaconCarrier{
		Height:                   height,
		RandomnessHex:            hex.EncodeToString(beta),
		ProofHex:                 hex.EncodeToString(proof),
		ProposerConsensusAddress: sdk.ConsAddress(proposalTestProposer).String(),
		SourceTag:                hubtypes.BeaconSourceProposerVRFV1,
		ProofCodec:               nodeante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1,
	})
	require.NoError(t, err)
	return sentinel, pub
}

func TestPreBlockerPersistsValidBeacon(t *testing.T) {
	input := []byte("in")
	sentinel, pubkey := makeSentinelWithSigner(t, 42, input)
	keeper := &persistingKeeper{realCryptoKeeper: realCryptoKeeper{input: input, required: true, vrfPubkey: pubkey}}

	var innerRan bool
	inner := func(_ sdk.Context, _ *abci.RequestFinalizeBlock) (*sdk.ResponsePreBlock, error) {
		innerRan = true
		return &sdk.ResponsePreBlock{}, nil
	}
	handler := newBeaconPreBlocker(
		keeper,
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		inner,
	)

	_, err := handler(preBlockerCtx(), &abci.RequestFinalizeBlock{
		Height: 42, Txs: [][]byte{sentinel}, ProposerAddress: proposalTestProposer,
	})
	require.NoError(t, err)
	require.True(t, innerRan)
	require.NotNil(t, keeper.persisted, "keeper must have received the carrier")
	require.Equal(t, uint64(42), keeper.persisted.Height)
	require.Equal(t, proposalTestOperator, keeper.seenOperator)
}

func TestPreBlockerWritesPlaceholderBeforeRequiredHeight(t *testing.T) {
	keeper := &persistingKeeper{realCryptoKeeper: realCryptoKeeper{input: []byte("x"), required: false}}
	handler := newBeaconPreBlocker(
		keeper,
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
	)
	_, err := handler(preBlockerCtx(), &abci.RequestFinalizeBlock{
		Height: 42, Txs: [][]byte{[]byte("business-tx-only")},
	})
	require.NoError(t, err)
	require.NotNil(t, keeper.placeholderPersisted)
	require.Equal(t, uint64(42), keeper.placeholderPersisted.Height)
}

func TestPreBlockerPanicsOnMissingSentinelAtRequiredHeight(t *testing.T) {
	keeper := &persistingKeeper{realCryptoKeeper: realCryptoKeeper{input: []byte("x"), required: true}}
	handler := newBeaconPreBlocker(
		keeper,
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
	)
	require.Panics(t, func() {
		_, _ = handler(preBlockerCtx(), &abci.RequestFinalizeBlock{
			Height: 42, Txs: [][]byte{[]byte("no-sentinel")},
		})
	}, "a missing sentinel at a required height means ProcessProposal let through a block it should have rejected; the protocol forbids writing a placeholder in that range, so a hard stop is the only option")
	require.Nil(t, keeper.placeholderPersisted,
		"writing a placeholder is forbidden in the required range (randomness_and_sampling_protocol.md §3)")
}

func TestPreBlockerSurfacesUnreadableRequiredPolicy(t *testing.T) {
	handler := newBeaconPreBlocker(
		&persistingKeeper{realCryptoKeeper: realCryptoKeeper{
			input:       []byte("x"),
			requiredErr: errors.New("params not loaded"),
		}},
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
	)
	_, err := handler(preBlockerCtx(), &abci.RequestFinalizeBlock{
		Height: 42, Txs: [][]byte{[]byte("no-sentinel")},
	})
	require.Error(t, err, "when the policy cannot be read we must neither write a placeholder nor panic; hand it to the caller")
}

func TestPreBlockerPanicsOnCorruptSentinel(t *testing.T) {
	corrupt := append([]byte(BeaconSentinelMagicPrefix), 0xff, 0xff, 0xff, 0xff)
	handler := newBeaconPreBlocker(
		&persistingKeeper{realCryptoKeeper: realCryptoKeeper{input: []byte("x"), required: true}},
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
	)
	require.Panics(t, func() {
		_, _ = handler(preBlockerCtx(), &abci.RequestFinalizeBlock{
			Height: 42, Txs: [][]byte{corrupt},
		})
	}, "corrupt sentinel after ProcessProposal accepted is a broken invariant")
}

func TestPreBlockerBubblesUpOperatorLookupError(t *testing.T) {
	input := []byte("in")
	sentinel, _ := makeSentinelWithSigner(t, 42, input)
	handler := newBeaconPreBlocker(
		&persistingKeeper{realCryptoKeeper: realCryptoKeeper{input: input, required: true}},
		&fakeOperatorLookup{lookupErr: errors.New("proposer left validator set")},
		nodeante.NewVRFVerifier(),
		nil,
	)
	_, err := handler(preBlockerCtx(), &abci.RequestFinalizeBlock{
		Height: 42, Txs: [][]byte{sentinel}, ProposerAddress: proposalTestProposer,
	})
	require.Error(t, err, "operator lookup failure must surface as PreBlocker error, not panic")
}

func TestPreBlockerBubblesUpKeeperPersistError(t *testing.T) {
	input := []byte("in")
	sentinel, pubkey := makeSentinelWithSigner(t, 42, input)
	handler := newBeaconPreBlocker(
		&persistingKeeper{
			realCryptoKeeper: realCryptoKeeper{input: input, required: true, vrfPubkey: pubkey},
			persistErr:       errors.New("collections write failed"),
		},
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
	)
	_, err := handler(preBlockerCtx(), &abci.RequestFinalizeBlock{
		Height: 42, Txs: [][]byte{sentinel}, ProposerAddress: proposalTestProposer,
	})
	require.Error(t, err, "keeper persist failure must surface as PreBlocker error")
}
