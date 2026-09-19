package app

// PrepareProposal handler unit tests.
//
// Uses a fake ProposerSigner + fake keeper (both narrow interfaces) so the
// handler can be exercised without CometBFT + vrf_key.json setup.
//
// Covered properties:
//   - Happy path: handler prepends a well-formed beacon sentinel at Txs[0]
//     and forwards the rest of business Txs behind it.
//   - MaxTxBytes accounting: inner handler receives MaxTxBytes reduced by
//     sentinel size, so producer cannot silently exceed the mempool cap.
//   - Fallback A — nil signer: no sentinel; inner handler runs unmodified.
//   - Fallback B — signer error: no sentinel + warning logged; inner
//     handler runs unmodified.
//   - Fallback C — keeper VRF input error: no sentinel; graceful.
//   - Fallback D — sentinel > MaxTxBytes: no sentinel; error logged.
//   - Fallback E — the local VRF public key does not match the registered
//     active key on chain, or the operator cannot be found: no injection. This
//     is the halt-prevention guard rail: injecting a sentinel the node's own
//     ProcessProposal would reject leaves a single-validator chain stuck at
//     that height forever.
//   - Determinism: identical (input, signer) produces identical sentinel
//     bytes across two invocations (Beacon replay replay).

import (
	"bytes"
	"context"
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

// prepareTestVrfPubkey is the "active VRF public key registered on chain" in
// these tests; the local signer holds it by default, so the self-check passes.
var prepareTestVrfPubkey = bytes.Repeat([]byte{0x7E}, hubtypes.VrfPubkeyLen)

// fakeSigner returns fixed proof / randomness bytes so we can assert on the
// sentinel's contents without pulling real ed25519 into the test.
type fakeSigner struct {
	proof      []byte
	randomness []byte
	vrfPubkey  []byte
	proveErr   error
	pubkeyErr  error
}

func (f *fakeSigner) Prove(_ []byte) ([]byte, []byte, error) {
	if f.proveErr != nil {
		return nil, nil, f.proveErr
	}
	return f.proof, f.randomness, nil
}

func (f *fakeSigner) VrfPubKey() ([]byte, error) {
	if f.pubkeyErr != nil {
		return nil, f.pubkeyErr
	}
	return f.vrfPubkey, nil
}

// newFakeSigner builds a signer whose VRF public key matches what the fake
// keeper reports as registered.
func newFakeSigner(proof, randomness []byte) *fakeSigner {
	return &fakeSigner{proof: proof, randomness: randomness, vrfPubkey: prepareTestVrfPubkey}
}

// fakeBeaconKeeper mocks the narrow beaconKeeperReader interface. It records
// the height it was asked about so tests can assert on call args. Since
// PrepareProposal never calls ValidateBeaconCarrier or
// ValidateAndWriteVerifiedBeacon, those methods panic — invoking them in a
// PrepareProposal test would signal a code smell.
type fakeBeaconKeeper struct {
	input        []byte
	inputErr     error
	vrfPubkey    []byte
	vrfKeyErr    error
	lastHeight   uint64
	lastProposer []byte
}

func (f *fakeBeaconKeeper) BeaconVRFInputBytes(_ sdk.Context, height uint64, proposer []byte) ([]byte, error) {
	f.lastHeight = height
	f.lastProposer = append([]byte(nil), proposer...)
	if f.inputErr != nil {
		return nil, f.inputErr
	}
	return f.input, nil
}

func (f *fakeBeaconKeeper) WriteBlockBeacon(sdk.Context, uint64, []byte, string) (hubtypes.BeaconState, error) {
	panic("unexpected WriteBlockBeacon call")
}

func (f *fakeBeaconKeeper) BeaconSentinelRequiredAtHeight(sdk.Context, uint64) (bool, error) {
	panic("PrepareProposal test should not read the required-height policy")
}

func (f *fakeBeaconKeeper) ActiveVrfPubkeyForHeight(_ sdk.Context, _ string, _ uint64) ([]byte, error) {
	if f.vrfKeyErr != nil {
		return nil, f.vrfKeyErr
	}
	if f.vrfPubkey != nil {
		return f.vrfPubkey, nil
	}
	return prepareTestVrfPubkey, nil
}

func (f *fakeBeaconKeeper) ValidateBeaconCarrier(
	_ sdk.Context,
	_ hubtypes.BeaconCarrier,
	_ []byte,
	_ string,
	_ hubkeeper.BeaconProofVerifier,
) (hubtypes.BeaconState, error) {
	panic("PrepareProposal test should not call ValidateBeaconCarrier")
}

func (f *fakeBeaconKeeper) ValidateAndWriteVerifiedBeacon(
	_ sdk.Context,
	_ hubtypes.BeaconCarrier,
	_ []byte,
	_ string,
	_ hubkeeper.BeaconProofVerifier,
) (hubtypes.BeaconState, error) {
	panic("PrepareProposal test should not call ValidateAndWriteVerifiedBeacon")
}

// echoInner is a trivial inner PrepareProposal handler that echoes the input
// Txs back. Enough to assert composition semantics.
func echoInner(_ sdk.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
	return &abci.ResponsePrepareProposal{Txs: req.Txs}, nil
}

func testCtx() sdk.Context {
	return sdk.NewContext(nil, cmtproto.Header{Height: 1}, false, log.NewNopLogger()).
		WithContext(context.Background())
}

func prepareTestLookup() *fakeOperatorLookup {
	return &fakeOperatorLookup{operator: proposalTestOperator}
}

func TestPrepareProposalHandlerHappyPath(t *testing.T) {
	proposer := bytes.Repeat([]byte{0x42}, 20)
	// 32-byte randomness reduces to 32 bytes; 80-byte proof is realistic.
	signer := newFakeSigner(bytes.Repeat([]byte{0xAA}, 80), bytes.Repeat([]byte{0xBB}, 32))
	keeper := &fakeBeaconKeeper{input: []byte("vrf-input-42")}
	staking := prepareTestLookup()

	handler := newPrepareProposalHandler(keeper, staking, signer, nil, echoInner)

	businessTx := []byte("business-tx-payload")
	req := &abci.RequestPrepareProposal{
		Height:          42,
		MaxTxBytes:      100_000,
		Txs:             [][]byte{businessTx},
		ProposerAddress: proposer,
	}

	resp, err := handler(testCtx(), req)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(resp.Txs), 2, "sentinel + business tx expected")

	require.True(t, IsBeaconSentinel(resp.Txs[0]), "Txs[0] must be a beacon sentinel")
	require.Equal(t, businessTx, resp.Txs[1], "business tx must follow at Txs[1]")

	// Sentinel decode must produce a carrier with height 42 and our fake
	// signer's outputs bit-identical.
	carrier, err := DecodeBeaconSentinel(resp.Txs[0])
	require.NoError(t, err)
	require.Equal(t, uint64(42), carrier.Height)
	require.Equal(t, sdk.ConsAddress(proposer).String(), carrier.ProposerConsensusAddress)
	require.Equal(t, nodeante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1, carrier.ProofCodec)
	require.Len(t, carrier.RandomnessHex, 64, "32-byte β becomes 64-char hex")
	require.Len(t, carrier.ProofHex, 160, "80-byte π becomes 160-char hex")

	// Keeper must have been asked for the correct height.
	require.Equal(t, uint64(42), keeper.lastHeight)
	require.Equal(t, proposer, keeper.lastProposer)
	require.Equal(t, proposer, staking.seenAddr,
		"the self-check must look up the operator behind req.ProposerAddress, not the local identity")
}

func TestPrepareProposalHandlerShrinksMaxTxBytesBudget(t *testing.T) {
	proposer := bytes.Repeat([]byte{0x43}, 20)
	signer := newFakeSigner(bytes.Repeat([]byte{0x01}, 80), bytes.Repeat([]byte{0x02}, 32))
	keeper := &fakeBeaconKeeper{input: []byte("in")}
	staking := prepareTestLookup()

	// spyInner captures what MaxTxBytes the inner handler was asked for.
	var innerSeenMaxTxBytes int64
	spyInner := func(_ sdk.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
		innerSeenMaxTxBytes = req.MaxTxBytes
		return &abci.ResponsePrepareProposal{Txs: req.Txs}, nil
	}
	handler := newPrepareProposalHandler(keeper, staking, signer, nil, spyInner)

	origMax := int64(50_000)
	_, err := handler(testCtx(), &abci.RequestPrepareProposal{
		Height:          7,
		MaxTxBytes:      origMax,
		Txs:             [][]byte{[]byte("t1")},
		ProposerAddress: proposer,
	})
	require.NoError(t, err)
	require.Less(t, innerSeenMaxTxBytes, origMax,
		"inner handler must see a reduced MaxTxBytes so producer cannot exceed the cap by size(sentinel)")
	// The reduction must be exactly the sentinel size.
	// We recompute what the sentinel would be to check.
	sentinel, err := buildBeaconSentinel(testCtx(), keeper, staking, signer, &abci.RequestPrepareProposal{Height: 7, ProposerAddress: proposer})
	require.NoError(t, err)
	require.Equal(t, origMax-int64(len(sentinel)), innerSeenMaxTxBytes)
}

func TestPrepareProposalHandlerNilSignerFallsThrough(t *testing.T) {
	// No signer wired (a full node, or vrf_key.json not generated yet). The
	// handler must still produce a proposal as usual; ProcessProposal decides
	// whether the block can be accepted, per the committed policy.
	keeper := &fakeBeaconKeeper{input: []byte("in")}
	handler := newPrepareProposalHandler(keeper, prepareTestLookup(), nil, nil, echoInner)

	resp, err := handler(testCtx(), &abci.RequestPrepareProposal{
		Height:     9,
		MaxTxBytes: 10_000,
		Txs:        [][]byte{[]byte("only-tx")},
	})
	require.NoError(t, err)
	require.Len(t, resp.Txs, 1)
	require.False(t, IsBeaconSentinel(resp.Txs[0]))
}

func TestPrepareProposalHandlerSignerErrorFallsThrough(t *testing.T) {
	proposer := bytes.Repeat([]byte{0x44}, 20)
	signer := newFakeSigner(nil, nil)
	signer.proveErr = errors.New("vrf key file unreadable")
	keeper := &fakeBeaconKeeper{input: []byte("in")}
	handler := newPrepareProposalHandler(keeper, prepareTestLookup(), signer, nil, echoInner)

	resp, err := handler(testCtx(), &abci.RequestPrepareProposal{
		Height:          9,
		MaxTxBytes:      10_000,
		Txs:             [][]byte{[]byte("only-tx")},
		ProposerAddress: proposer,
	})
	require.NoError(t, err, "signer error must not fail the proposal outright")
	require.Len(t, resp.Txs, 1)
	require.False(t, IsBeaconSentinel(resp.Txs[0]))
}

func TestPrepareProposalHandlerKeeperErrorFallsThrough(t *testing.T) {
	proposer := bytes.Repeat([]byte{0x45}, 20)
	signer := newFakeSigner(bytes.Repeat([]byte{0x01}, 80), bytes.Repeat([]byte{0x02}, 32))
	keeper := &fakeBeaconKeeper{inputErr: errors.New("cannot read prior beacon")}
	handler := newPrepareProposalHandler(keeper, prepareTestLookup(), signer, nil, echoInner)

	resp, err := handler(testCtx(), &abci.RequestPrepareProposal{
		Height:          9,
		MaxTxBytes:      10_000,
		Txs:             [][]byte{[]byte("t1")},
		ProposerAddress: proposer,
	})
	require.NoError(t, err)
	require.False(t, IsBeaconSentinel(resp.Txs[0]))
}

func TestPrepareProposalHandlerSentinelExceedsBudget(t *testing.T) {
	proposer := bytes.Repeat([]byte{0x46}, 20)
	signer := newFakeSigner(bytes.Repeat([]byte{0x01}, 80), bytes.Repeat([]byte{0x02}, 32))
	keeper := &fakeBeaconKeeper{input: []byte("in")}
	handler := newPrepareProposalHandler(keeper, prepareTestLookup(), signer, nil, echoInner)

	// MaxTxBytes = 1 forces sentinel-larger-than-budget branch.
	resp, err := handler(testCtx(), &abci.RequestPrepareProposal{
		Height:          9,
		MaxTxBytes:      1,
		Txs:             [][]byte{[]byte("t1")},
		ProposerAddress: proposer,
	})
	require.NoError(t, err, "budget exhaustion must not fail the proposal")
	require.False(t, IsBeaconSentinel(resp.Txs[0]), "no sentinel emitted when it would violate MaxTxBytes")
}

// Halt-prevention guard rail: if the active key registered on chain is not the
// local one, never inject — the injected sentinel would be rejected by this
// node's own ProcessProposal, and a single-validator chain would be stuck at
// this height forever.
func TestPrepareProposalHandlerRefusesWhenLocalKeyIsNotTheRegisteredOne(t *testing.T) {
	proposer := bytes.Repeat([]byte{0x47}, 20)
	signer := newFakeSigner(bytes.Repeat([]byte{0x01}, 80), bytes.Repeat([]byte{0x02}, 32))
	keeper := &fakeBeaconKeeper{
		input:     []byte("in"),
		vrfPubkey: bytes.Repeat([]byte{0x11}, hubtypes.VrfPubkeyLen), // a different key on chain
	}
	handler := newPrepareProposalHandler(keeper, prepareTestLookup(), signer, nil, echoInner)

	resp, err := handler(testCtx(), &abci.RequestPrepareProposal{
		Height:          9,
		MaxTxBytes:      10_000,
		Txs:             [][]byte{[]byte("t1")},
		ProposerAddress: proposer,
	})
	require.NoError(t, err)
	require.False(t, IsBeaconSentinel(resp.Txs[0]),
		"no sentinel may be injected when the local VRF public key differs from the one registered on chain")
}

func TestPrepareProposalHandlerRefusesWhenOperatorHasNoActiveVrfKey(t *testing.T) {
	proposer := bytes.Repeat([]byte{0x48}, 20)
	signer := newFakeSigner(bytes.Repeat([]byte{0x01}, 80), bytes.Repeat([]byte{0x02}, 32))
	keeper := &fakeBeaconKeeper{input: []byte("in"), vrfKeyErr: hubkeeper.ErrNoActiveVrfKey}
	handler := newPrepareProposalHandler(keeper, prepareTestLookup(), signer, nil, echoInner)

	resp, err := handler(testCtx(), &abci.RequestPrepareProposal{
		Height:          9,
		MaxTxBytes:      10_000,
		Txs:             [][]byte{[]byte("t1")},
		ProposerAddress: proposer,
	})
	require.NoError(t, err)
	require.False(t, IsBeaconSentinel(resp.Txs[0]))
}

func TestPrepareProposalHandlerRefusesWhenOperatorLookupFails(t *testing.T) {
	proposer := bytes.Repeat([]byte{0x49}, 20)
	signer := newFakeSigner(bytes.Repeat([]byte{0x01}, 80), bytes.Repeat([]byte{0x02}, 32))
	keeper := &fakeBeaconKeeper{input: []byte("in")}
	handler := newPrepareProposalHandler(
		keeper,
		&fakeOperatorLookup{lookupErr: errors.New("not a validator")},
		signer, nil, echoInner,
	)

	resp, err := handler(testCtx(), &abci.RequestPrepareProposal{
		Height:          9,
		MaxTxBytes:      10_000,
		Txs:             [][]byte{[]byte("t1")},
		ProposerAddress: proposer,
	})
	require.NoError(t, err)
	require.False(t, IsBeaconSentinel(resp.Txs[0]))
}

func TestPrepareProposalHandlerIsDeterministic(t *testing.T) {
	proposer := bytes.Repeat([]byte{0x4A}, 20)
	// Same inputs -> same sentinel bytes. Prereq for Beacon replay 3-validator replay.
	signer := newFakeSigner(bytes.Repeat([]byte{0x33}, 80), bytes.Repeat([]byte{0x44}, 32))
	keeper := &fakeBeaconKeeper{input: []byte("in")}
	handler := newPrepareProposalHandler(keeper, prepareTestLookup(), signer, nil, echoInner)

	req := &abci.RequestPrepareProposal{Height: 100, MaxTxBytes: 100_000, Txs: [][]byte{[]byte("x")}, ProposerAddress: proposer}
	r1, err := handler(testCtx(), req)
	require.NoError(t, err)
	r2, err := handler(testCtx(), req)
	require.NoError(t, err)
	require.True(t, bytes.Equal(r1.Txs[0], r2.Txs[0]),
		"identical (signer, keeper, req) must produce identical sentinel bytes")
}
