package app

// PrepareProposal handler that injects the Beacon sentinel.
//
// Data flow (see §2):
//
//   1. Confirm the local VRF public key is the on-chain active key for this
//      proposer in the epoch this height belongs to.
//   2. Compute canonical VRF input for req.Height via
//      TrueOpenKeeper.BeaconVRFInputBytes.
//   3. Ask the local ProposerSigner (backed by config/vrf_key.json) to
//      produce (proof, randomness) via Prove.
//   4. Assemble a BeaconCarrier proto.
//   5. Encode as sentinel (magic || proto bytes).
//   6. Prepend to Txs[0]. Shrink the max-tx-bytes budget by sentinel size
//      before delegating to the inner PrepareProposal handler for the
//      remaining business tx selection.
//
// The signature uses the separately registered VRF private key, not the
// consensus private key (the sampling protocol /).
// The consensus private key can stay in tmkms / an HSM.
//
// Failure policy:
//   - If ANY step above errors, return the inner handler's proposal without
//     a sentinel. This does not silently degrade the chain: at a required
//     height ProcessProposal REJECTs the sentinel-less proposal and the next
//     proposer takes over; at a pre-required height a missing sentinel was
//     allowed in the first place.
//   - Signer errors are logged at warning level but not returned; the
//     alternative is halting all block production if the key file blips.

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtcrypto "github.com/cometbft/cometbft/crypto"
	sdk "github.com/cosmos/cosmos-sdk/types"

	nodeante "github.com/TrueOpen/node/app/ante"
	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// ProposerSigner is the narrow contract the PrepareProposal handler needs
// from the holder of the independent VRF hot key. The default implementation
// reads config/vrf_key.json; it never reads the consensus private key.
//
// Verify never sits behind this interface — ProcessProposal / PreBlocker use
// the deterministic nodeante.VRFVerifier which does not need private
// material.
type ProposerSigner interface {
	// Prove generates an ECVRF (proof, randomness) pair for the given
	// canonical VRF input using the local VRF private key.
	Prove(input []byte) (proof, randomness []byte, err error)
	// VrfPubKey returns the local 32-byte VRF public key. PrepareProposal
	// compares it against the chain's registered active key before injecting
	// anything.
	VrfPubKey() ([]byte, error)
}

// ErrProposerSignerUnavailable is returned by the handler when no signer is
// wired. Not fatal — the handler falls back to a no-sentinel proposal.
var ErrProposerSignerUnavailable = errors.New("proposer signer unavailable")

// beaconKeeperReader is the narrow view of the trueopen Keeper the beacon
// hooks (PrepareProposal / ProcessProposal / PreBlocker) share. Kept narrow
// so unit tests can supply a stub without pulling the whole module.
type beaconKeeperReader interface {
	BeaconVRFInputBytes(ctx sdk.Context, height uint64, proposerConsensusAddressRaw []byte) ([]byte, error)
	WriteBlockBeacon(ctx sdk.Context, height uint64, blockHash []byte, sourceTag string) (hubtypes.BeaconState, error)
	// BeaconSentinelRequiredAtHeight is the single source of the consensus
	// policy; it reads the committed
	// BeaconParamsV1.vrf_required_from_height.
	BeaconSentinelRequiredAtHeight(ctx sdk.Context, height uint64) (bool, error)
	// ActiveVrfPubkeyForHeight is PrepareProposal's self-check: the public key
	// matching the local VRF private key must already be the on-chain active
	// key for that epoch, otherwise the injected sentinel is rejected by the
	// node's own ProcessProposal and a single-validator chain halts outright.
	ActiveVrfPubkeyForHeight(ctx sdk.Context, operator string, height uint64) ([]byte, error)
	ValidateBeaconCarrier(
		ctx sdk.Context,
		carrier hubtypes.BeaconCarrier,
		proposerConsensusAddressRaw []byte,
		proposerOperatorAddress string,
		verifier hubkeeper.BeaconProofVerifier,
	) (hubtypes.BeaconState, error)
	ValidateAndWriteVerifiedBeacon(
		ctx sdk.Context,
		carrier hubtypes.BeaconCarrier,
		proposerConsensusAddressRaw []byte,
		proposerOperatorAddress string,
		verifier hubkeeper.BeaconProofVerifier,
	) (hubtypes.BeaconState, error)
}

// beaconKeeperAdapter adapts hubkeeper.Keeper (which takes context.Context)
// to the sdk.Context-taking interface above. Keeps the handler code sdk-typed.
type beaconKeeperAdapter struct {
	inner hubkeeper.Keeper
}

func (a beaconKeeperAdapter) BeaconVRFInputBytes(ctx sdk.Context, height uint64, proposerConsensusAddressRaw []byte) ([]byte, error) {
	return a.inner.BeaconVRFInputBytes(ctx, height, proposerConsensusAddressRaw)
}

func (a beaconKeeperAdapter) WriteBlockBeacon(ctx sdk.Context, height uint64, blockHash []byte, sourceTag string) (hubtypes.BeaconState, error) {
	return a.inner.WriteBlockBeacon(ctx, height, blockHash, sourceTag)
}

func (a beaconKeeperAdapter) BeaconSentinelRequiredAtHeight(ctx sdk.Context, height uint64) (bool, error) {
	return a.inner.BeaconSentinelRequiredAtHeight(ctx, height)
}

func (a beaconKeeperAdapter) ActiveVrfPubkeyForHeight(ctx sdk.Context, operator string, height uint64) ([]byte, error) {
	return a.inner.ActiveVrfPubkeyForHeight(ctx, operator, height)
}

func (a beaconKeeperAdapter) ValidateBeaconCarrier(
	ctx sdk.Context,
	carrier hubtypes.BeaconCarrier,
	proposerConsensusAddressRaw []byte,
	proposerOperatorAddress string,
	verifier hubkeeper.BeaconProofVerifier,
) (hubtypes.BeaconState, error) {
	return a.inner.ValidateBeaconCarrier(ctx, carrier, proposerConsensusAddressRaw, proposerOperatorAddress, verifier)
}

func (a beaconKeeperAdapter) ValidateAndWriteVerifiedBeacon(
	ctx sdk.Context,
	carrier hubtypes.BeaconCarrier,
	proposerConsensusAddressRaw []byte,
	proposerOperatorAddress string,
	verifier hubkeeper.BeaconProofVerifier,
) (hubtypes.BeaconState, error) {
	return a.inner.ValidateAndWriteVerifiedBeacon(ctx, carrier, proposerConsensusAddressRaw, proposerOperatorAddress, verifier)
}

// NewPrepareProposalHandler builds a sdk.PrepareProposalHandler that prepends
// a beacon sentinel and then delegates to innerHandler for the raw tx
// selection. Pass nil signer to keep this a no-op wrapper (useful during
// bootstrap before priv_validator is loaded).
//
// The txDecoder argument remains part of the app hook signature but is unused.
// The app-level order_value descending sort is intentionally absent. §10.1:2763
// scopes order_value to "Builder/PrepareProposal congestion ordering and audit
// events" and the contract registers no mempool tier: the deleted
// implementation let a P1 bucket unconditionally outrank P0 and ordered
// zero-valued buckets by proposer-local arrival, which is exploitable. When a
// congestion policy is registered, decode here and sort by exactly the
// keeper-derived order_value (§5.13 min(max_fee, checked_add(infer_fee_cap,
// verify_fee_cap))), not by a second app-side formula.
func NewPrepareProposalHandler(
	keeper hubkeeper.Keeper,
	staking ProposerOperatorLookup,
	signer ProposerSigner,
	txDecoder sdk.TxDecoder,
	innerHandler sdk.PrepareProposalHandler,
) sdk.PrepareProposalHandler {
	return newPrepareProposalHandler(beaconKeeperAdapter{inner: keeper}, staking, signer, txDecoder, innerHandler)
}

// newPrepareProposalHandler is the test-friendly form used by unit tests.
func newPrepareProposalHandler(
	keeper beaconKeeperReader,
	staking ProposerOperatorLookup,
	signer ProposerSigner,
	_ sdk.TxDecoder,
	innerHandler sdk.PrepareProposalHandler,
) sdk.PrepareProposalHandler {
	return func(ctx sdk.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
		sentinel, sentinelErr := buildBeaconSentinel(ctx, keeper, staking, signer, req)

		if sentinel == nil {
			// Bootstrapping / dev / KMS transient failure: skip sentinel.
			// Log via SDK ctx so operators can grep for it.
			if sentinelErr != nil {
				ctx.Logger().Warn(
					"beacon sentinel not injected; PrepareProposal will emit a proposal without sentinel",
					"height", req.Height,
					"error", sentinelErr.Error(),
				)
			}
			inner, err := innerHandler(ctx, req)
			if err != nil {
				return nil, err
			}
			return inner, nil
		}

		// Shrink the budget by the sentinel size so inner handler
		// respects the modified cap.
		reservedReq := *req
		reservedReq.MaxTxBytes = req.MaxTxBytes - int64(len(sentinel))
		if reservedReq.MaxTxBytes < 0 {
			// Sentinel alone exceeds MaxTxBytes; refuse silently and let
			// ProcessProposal fail the block (extreme edge case).
			ctx.Logger().Error(
				"beacon sentinel exceeds MaxTxBytes budget; emitting proposal without sentinel",
				"sentinel_bytes", len(sentinel),
				"max_tx_bytes", req.MaxTxBytes,
			)
			inner, err := innerHandler(ctx, req)
			if err != nil {
				return nil, err
			}
			return inner, nil
		}

		inner, err := innerHandler(ctx, &reservedReq)
		if err != nil {
			return nil, err
		}
		out := &abci.ResponsePrepareProposal{
			Txs: make([][]byte, 0, len(inner.Txs)+1),
		}
		out.Txs = append(out.Txs, sentinel)
		out.Txs = append(out.Txs, inner.Txs...)
		return out, nil
	}
}

// buildBeaconSentinel is the pure "compute the sentinel bytes" step. Split
// out for testability. Returns (bytes, nil) on success; (nil, err) on any
// failure including "signer unavailable" so caller can decide policy.
func buildBeaconSentinel(
	ctx sdk.Context,
	keeper beaconKeeperReader,
	staking ProposerOperatorLookup,
	signer ProposerSigner,
	req *abci.RequestPrepareProposal,
) ([]byte, error) {
	if signer == nil {
		return nil, ErrProposerSignerUnavailable
	}
	if req == nil || req.Height <= 0 {
		return nil, fmt.Errorf("invalid RequestPrepareProposal: %+v", req)
	}

	canonicalConsAddr, err := canonicalABCIProposerAddress(req.ProposerAddress)
	if err != nil {
		return nil, err
	}

	// Self-check: the local VRF public key must be exactly the on-chain active
	// key for this operator in the epoch this height belongs to. This settles
	// three things at once:
	//   1. Halt prevention — injecting a sentinel that the node's own
	//      ProcessProposal would reject means a single-validator chain is stuck
	//      at that height forever.
	//   2. Confirming "I really am the proposer of this block" — the lookup is
	//      on the operator behind req.ProposerAddress, not on the local
	//      identity, so nothing is injected when someone else's key does not
	//      match.
	//   3. Removing the dependency on the consensus private key entirely — the
	//      old implementation ran this self-check off FilePV's consensus
	//      address.
	localVrfPubkey, err := signer.VrfPubKey()
	if err != nil {
		return nil, fmt.Errorf("local VRF public key: %w", err)
	}
	operator, err := staking.GetProposerOperatorAddress(ctx, req.ProposerAddress)
	if err != nil {
		return nil, fmt.Errorf("resolve proposer operator address: %w", err)
	}
	chainVrfPubkey, err := keeper.ActiveVrfPubkeyForHeight(ctx, operator, uint64(req.Height))
	if err != nil {
		return nil, fmt.Errorf("resolve registered VRF public key for %s: %w", operator, err)
	}
	if !bytes.Equal(localVrfPubkey, chainVrfPubkey) {
		return nil, fmt.Errorf(
			"local VRF public key does not match the key registered for %s at height %d",
			operator, req.Height,
		)
	}

	input, err := keeper.BeaconVRFInputBytes(ctx, uint64(req.Height), req.ProposerAddress)
	if err != nil {
		return nil, fmt.Errorf("compute VRF input for height %d: %w", req.Height, err)
	}

	proof, randomness, err := signer.Prove(input)
	if err != nil {
		return nil, fmt.Errorf("proposer signer prove: %w", err)
	}

	carrier := &hubtypes.BeaconCarrier{
		Height:                   uint64(req.Height),
		RandomnessHex:            hex.EncodeToString(randomness),
		ProofHex:                 hex.EncodeToString(proof),
		ProposerConsensusAddress: canonicalConsAddr,
		SourceTag:                hubtypes.BeaconSourceProposerVRFV1,
		ProofCodec:               nodeante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1,
	}

	return EncodeBeaconSentinel(carrier)
}

func canonicalABCIProposerAddress(raw []byte) (string, error) {
	if len(raw) != cmtcrypto.AddressSize {
		return "", fmt.Errorf("ABCI proposer address must be %d bytes, got %d", cmtcrypto.AddressSize, len(raw))
	}
	return sdk.ConsAddress(raw).String(), nil
}
