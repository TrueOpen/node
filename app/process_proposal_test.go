package app

// ProcessProposal handler tests.
//
// Uses a controllable fake keeper + fake operator lookup to exercise both
// the required and the pre-required halves of the policy (whose single source
// is the committed `BeaconParamsV1.vrf_required_from_height`, simulated by the
// fake's BeaconSentinelRequiredAtHeight) against the full matrix: valid /
// missing / corrupt / height-mismatched / source-tag-wrong / Keeper-rejected /
// operator-lookup-failure / no-active-VRF-key sentinels.

import (
	"bytes"
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

// realCryptoKeeper is a fake beaconKeeperReader that behaves like Keeper
// K3 for VRF verification: it uses the real ecvrf verifier and a
// caller-supplied VRF input. Duplicate detection is out-of-scope for these
// tests (that's Keeper's territory and tested there).
//
// vrfPubkey plays the on-chain VRF registry: the verification public key can
// only come from here, and the consensus public key is entirely absent from
// this path (the sampling protocol).
type realCryptoKeeper struct {
	input        []byte
	inputErr     error
	required     bool
	requiredErr  error
	vrfPubkey    []byte
	vrfKeyErr    error
	seenOperator string
}

func (r *realCryptoKeeper) BeaconVRFInputBytes(_ sdk.Context, _ uint64, _ []byte) ([]byte, error) {
	if r.inputErr != nil {
		return nil, r.inputErr
	}
	return r.input, nil
}

func (r *realCryptoKeeper) WriteBlockBeacon(_ sdk.Context, height uint64, _ []byte, sourceTag string) (hubtypes.BeaconState, error) {
	return hubtypes.BeaconState{Height: height, SourceTag: sourceTag}, nil
}

func (r *realCryptoKeeper) BeaconSentinelRequiredAtHeight(_ sdk.Context, _ uint64) (bool, error) {
	if r.requiredErr != nil {
		return false, r.requiredErr
	}
	return r.required, nil
}

func (r *realCryptoKeeper) ActiveVrfPubkeyForHeight(_ sdk.Context, operator string, _ uint64) ([]byte, error) {
	r.seenOperator = operator
	if r.vrfKeyErr != nil {
		return nil, r.vrfKeyErr
	}
	return r.vrfPubkey, nil
}

// ValidateBeaconCarrier mimics Keeper's real behavior but skips the
// duplicate-height store lookup (the fake has no store).
func (r *realCryptoKeeper) ValidateBeaconCarrier(
	ctx sdk.Context,
	carrier hubtypes.BeaconCarrier,
	proposer []byte,
	operator string,
	verifier hubkeeper.BeaconProofVerifier,
) (hubtypes.BeaconState, error) {
	if carrier.ProposerConsensusAddress != sdk.ConsAddress(proposer).String() {
		return hubtypes.BeaconState{}, errors.New("carrier proposer does not match ABCI proposer")
	}
	pubkey, err := r.ActiveVrfPubkeyForHeight(ctx, operator, carrier.Height)
	if err != nil {
		return hubtypes.BeaconState{}, err
	}
	proof, err := hex.DecodeString(carrier.ProofHex)
	if err != nil {
		return hubtypes.BeaconState{}, err
	}
	randomness, err := hex.DecodeString(carrier.RandomnessHex)
	if err != nil {
		return hubtypes.BeaconState{}, err
	}
	if err := verifier.VerifyBeaconProof(carrier.ProofCodec, pubkey, r.input, proof, randomness); err != nil {
		return hubtypes.BeaconState{}, err
	}
	return hubtypes.BeaconState{Height: carrier.Height, Verified: true}, nil
}

func (r *realCryptoKeeper) ValidateAndWriteVerifiedBeacon(
	ctx sdk.Context,
	carrier hubtypes.BeaconCarrier,
	proposer []byte,
	operator string,
	verifier hubkeeper.BeaconProofVerifier,
) (hubtypes.BeaconState, error) {
	return r.ValidateBeaconCarrier(ctx, carrier, proposer, operator, verifier)
}

// fakeOperatorLookup returns a fixed operator address no matter what proposer
// address is passed. Set lookupErr to simulate staking failures.
type fakeOperatorLookup struct {
	operator  string
	lookupErr error
	seenAddr  []byte
}

func (f *fakeOperatorLookup) GetProposerOperatorAddress(_ sdk.Context, addr []byte) (string, error) {
	f.seenAddr = addr
	return f.operator, f.lookupErr
}

// proposalTestOperator is the stable operator address staking resolves to in
// these tests.
const proposalTestOperator = "trueopen1testoperator"

func processProposalCtx() sdk.Context {
	return sdk.NewContext(nil, cmtproto.Header{Height: 1}, false, log.NewNopLogger()).
		WithContext(context.Background())
}

// makeValidCarrierBytes builds a well-formed sentinel signed by a fresh
// ed25519 key, returning the sentinel bytes and the corresponding pubkey.
func makeValidCarrierBytes(t *testing.T, height uint64, input []byte) (sentinel []byte, pubkey []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	proof, beta, err := nodeante.Prove(priv, input)
	require.NoError(t, err)
	carrier := &hubtypes.BeaconCarrier{
		Height:                   height,
		RandomnessHex:            hex.EncodeToString(beta),
		ProofHex:                 hex.EncodeToString(proof),
		ProposerConsensusAddress: sdk.ConsAddress(proposalTestProposer).String(),
		SourceTag:                hubtypes.BeaconSourceProposerVRFV1,
		ProofCodec:               nodeante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1,
	}
	sentinel, err = EncodeBeaconSentinel(carrier)
	require.NoError(t, err)
	return sentinel, pub
}

var proposalTestProposer = bytes.Repeat([]byte{0x42}, 20)

func TestProcessProposalAcceptsValidSentinel(t *testing.T) {
	input := []byte("vrf-in-42")
	sentinel, pubkey := makeValidCarrierBytes(t, 42, input)

	keeper := &realCryptoKeeper{input: input, required: true, vrfPubkey: pubkey}
	handler := newProcessProposalHandler(
		keeper,
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
		nil,
	)

	resp, err := handler(processProposalCtx(), &abci.RequestProcessProposal{
		Height:          42,
		Txs:             [][]byte{sentinel, []byte("business-tx")},
		ProposerAddress: proposalTestProposer,
	})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, resp.Status)
	require.Equal(t, proposalTestOperator, keeper.seenOperator,
		"the verification public key must be fetched from the on-chain registry using the operator staking resolved")
}

func TestProcessProposalRejectsMissingSentinelAtRequiredHeight(t *testing.T) {
	handler := newProcessProposalHandler(
		&realCryptoKeeper{input: []byte("x"), required: true},
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
		nil,
	)
	resp, err := handler(processProposalCtx(), &abci.RequestProcessProposal{
		Height: 42,
		Txs:    [][]byte{[]byte("just-a-business-tx")},
	})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, resp.Status,
		"a missing sentinel must be REJECTed once height >= vrf_required_from_height")
}

func TestProcessProposalAcceptsMissingSentinelBeforeRequiredHeight(t *testing.T) {
	handler := newProcessProposalHandler(
		&realCryptoKeeper{input: []byte("x"), required: false},
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
		nil,
	)
	resp, err := handler(processProposalCtx(), &abci.RequestProcessProposal{
		Height: 42,
		Txs:    [][]byte{[]byte("just-a-business-tx")},
	})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, resp.Status,
		"a pre-required height allows no sentinel, otherwise bootstrap cannot get off the ground")
}

// Without the committed parameter there is no way to tell whether a sentinel is
// mandatory at this height; letting it through would mean producing a block
// under an unknown policy, so REJECT is the only option.
func TestProcessProposalRejectsWhenRequiredPolicyUnreadable(t *testing.T) {
	handler := newProcessProposalHandler(
		&realCryptoKeeper{input: []byte("x"), requiredErr: errors.New("params not loaded")},
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
		nil,
	)
	resp, err := handler(processProposalCtx(), &abci.RequestProcessProposal{
		Height: 42,
		Txs:    [][]byte{[]byte("just-a-business-tx")},
	})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, resp.Status)
}

func TestProcessProposalDoesNotDecodeOrReorderBusinessTransactions(t *testing.T) {
	decoder := func([]byte) (sdk.Tx, error) {
		panic("business transaction decoder must not be used for proposal ordering")
	}
	innerCalled := false
	handler := newProcessProposalHandler(
		&realCryptoKeeper{},
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		decoder,
		func(_ sdk.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
			innerCalled = true
			require.Equal(t, [][]byte{[]byte("low"), []byte("high")}, req.Txs)
			return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_ACCEPT}, nil
		},
	)

	resp, err := handler(processProposalCtx(), &abci.RequestProcessProposal{
		Height: 1,
		Txs:    [][]byte{[]byte("low"), []byte("high")},
	})
	require.NoError(t, err)
	require.True(t, innerCalled)
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, resp.Status)
}

func TestProcessProposalRejectsCorruptSentinel(t *testing.T) {
	// Sentinel prefix + garbage body → looks like sentinel but decodes bad.
	corrupt := append([]byte(BeaconSentinelMagicPrefix), 0xff, 0xff, 0xff, 0xff)

	for _, required := range []bool{true, false} {
		handler := newProcessProposalHandler(
			&realCryptoKeeper{input: []byte("x"), required: required},
			&fakeOperatorLookup{operator: proposalTestOperator},
			nodeante.NewVRFVerifier(),
			nil,
			nil,
		)
		resp, err := handler(processProposalCtx(), &abci.RequestProcessProposal{
			Height: 42,
			Txs:    [][]byte{corrupt},
		})
		require.NoError(t, err)
		require.Equal(t, abci.ResponseProcessProposal_REJECT, resp.Status,
			"a corrupt sentinel must be REJECTed on both sides of the required boundary (required=%v)", required)
	}
}

func TestProcessProposalRejectsHeightMismatch(t *testing.T) {
	input := []byte("in")
	sentinel, pubkey := makeValidCarrierBytes(t, 100 /* carrier says height 100 */, input)

	handler := newProcessProposalHandler(
		&realCryptoKeeper{input: input, required: true, vrfPubkey: pubkey},
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
		nil,
	)

	resp, err := handler(processProposalCtx(), &abci.RequestProcessProposal{
		Height: 42, /* but req says 42 */
		Txs:    [][]byte{sentinel},
	})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, resp.Status)
}

func TestProcessProposalRejectsWrongSourceTag(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	input := []byte("in")
	proof, beta, err := nodeante.Prove(priv, input)
	require.NoError(t, err)
	carrier := &hubtypes.BeaconCarrier{
		Height:                   42,
		RandomnessHex:            hex.EncodeToString(beta),
		ProofHex:                 hex.EncodeToString(proof),
		ProposerConsensusAddress: "trueopenvalcons1x",
		SourceTag:                hubtypes.BeaconSourcePlaceholderBlockHashV1, // wrong
		ProofCodec:               nodeante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1,
	}
	sentinel, err := EncodeBeaconSentinel(carrier)
	require.NoError(t, err)

	handler := newProcessProposalHandler(
		&realCryptoKeeper{input: input, required: true, vrfPubkey: pub},
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
		nil,
	)
	resp, err := handler(processProposalCtx(), &abci.RequestProcessProposal{
		Height: 42, Txs: [][]byte{sentinel},
	})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, resp.Status,
		"only proposer_vrf_v1 source_tag is acceptable in ProcessProposal")
}

func TestProcessProposalRejectsOperatorLookupFailure(t *testing.T) {
	input := []byte("in")
	sentinel, _ := makeValidCarrierBytes(t, 42, input)

	handler := newProcessProposalHandler(
		&realCryptoKeeper{input: input, required: true},
		&fakeOperatorLookup{lookupErr: errors.New("proposer not in validator set")},
		nodeante.NewVRFVerifier(),
		nil,
		nil,
	)

	resp, err := handler(processProposalCtx(), &abci.RequestProcessProposal{
		Height: 42, Txs: [][]byte{sentinel}, ProposerAddress: bytes.Repeat([]byte{0xAA}, 20),
	})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, resp.Status,
		"a proposal signed by an unknown proposer must be rejected")
}

// the sampling protocol: with no active VRF public key,
// REJECT — do not fall back to the consensus public key and do not look up
// historical keys. This is the core assertion of the DOC-021 fix.
func TestProcessProposalRejectsWhenProposerHasNoActiveVrfKey(t *testing.T) {
	input := []byte("in")
	sentinel, _ := makeValidCarrierBytes(t, 42, input)

	handler := newProcessProposalHandler(
		&realCryptoKeeper{
			input:     input,
			required:  true,
			vrfKeyErr: hubkeeper.ErrNoActiveVrfKey,
		},
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
		nil,
	)
	resp, err := handler(processProposalCtx(), &abci.RequestProcessProposal{
		Height: 42, Txs: [][]byte{sentinel}, ProposerAddress: proposalTestProposer,
	})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, resp.Status,
		"a proposer with no registered VRF public key must not slip through on its consensus public key")
}

func TestProcessProposalRejectsForgedProof(t *testing.T) {
	input := []byte("in")
	pubA, _ := makeValidCarrierBytes(t, 42, input) // pub for the correct signer
	// Now build a carrier signed by a DIFFERENT (attacker) key but claiming
	// to be pubA's producer. Real VRF verify must reject.
	_, privB, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	forgedProof, forgedBeta, err := nodeante.Prove(privB, input)
	require.NoError(t, err)
	forged := &hubtypes.BeaconCarrier{
		Height:                   42,
		RandomnessHex:            hex.EncodeToString(forgedBeta),
		ProofHex:                 hex.EncodeToString(forgedProof),
		ProposerConsensusAddress: sdk.ConsAddress(proposalTestProposer).String(),
		SourceTag:                hubtypes.BeaconSourceProposerVRFV1,
		ProofCodec:               nodeante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1,
	}
	sentinel, err := EncodeBeaconSentinel(forged)
	require.NoError(t, err)

	handler := newProcessProposalHandler(
		// the on-chain registry says this operator's active public key is A
		&realCryptoKeeper{input: input, required: true, vrfPubkey: pubA},
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
		nil,
	)
	resp, err := handler(processProposalCtx(), &abci.RequestProcessProposal{
		Height: 42, Txs: [][]byte{sentinel}, ProposerAddress: proposalTestProposer,
	})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, resp.Status,
		"a proof signed by a foreign key must NOT verify under the registered VRF pubkey")
}

func TestProcessProposalChainsToInnerHandler(t *testing.T) {
	input := []byte("in")
	sentinel, pubkey := makeValidCarrierBytes(t, 42, input)

	var innerRan bool
	inner := func(_ sdk.Context, _ *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
		innerRan = true
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_ACCEPT}, nil
	}

	handler := newProcessProposalHandler(
		&realCryptoKeeper{input: input, required: true, vrfPubkey: pubkey},
		&fakeOperatorLookup{operator: proposalTestOperator},
		nodeante.NewVRFVerifier(),
		nil,
		inner,
	)
	_, err := handler(processProposalCtx(), &abci.RequestProcessProposal{
		Height: 42, Txs: [][]byte{sentinel}, ProposerAddress: proposalTestProposer,
	})
	require.NoError(t, err)
	require.True(t, innerRan, "inner handler must run after sentinel check passes")
}

// Contract §1.4:404 — exactly one sentinel and only at index 0; the magic
// appearing anywhere else REJECTs the whole block.
// Only a proposer can get such a payload into a block (an externally
// submitted one dies in CheckTx decode), so this is the proposer-misbehaviour
// guard.
func TestProcessProposalRejectsSentinelMagicOutsideIndexZero(t *testing.T) {
	input := []byte("vrf-in-42")
	sentinel, pubkey := makeValidCarrierBytes(t, 42, input)

	for _, tc := range []struct {
		name string
		txs  [][]byte
	}{
		{"valid sentinel plus stray copy", [][]byte{sentinel, []byte("business-tx"), sentinel}},
		{"stray magic only, no sentinel at index 0", [][]byte{[]byte("business-tx"), sentinel}},
		{"bare magic prefix at index 1", [][]byte{sentinel, []byte(BeaconSentinelMagicPrefix)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, required := range []bool{true, false} {
				handler := newProcessProposalHandler(
					&realCryptoKeeper{input: input, required: required, vrfPubkey: pubkey},
					&fakeOperatorLookup{operator: proposalTestOperator},
					nodeante.NewVRFVerifier(),
					nil,
					func(_ sdk.Context, _ *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
						t.Fatal("inner handler must not run for a proposal carrying a stray sentinel magic")
						return nil, nil
					},
				)
				resp, err := handler(processProposalCtx(), &abci.RequestProcessProposal{
					Height:          42,
					Txs:             tc.txs,
					ProposerAddress: proposalTestProposer,
				})
				require.NoError(t, err)
				require.Equal(t, abci.ResponseProcessProposal_REJECT, resp.Status, "required=%v", required)
			}
		})
	}
}
