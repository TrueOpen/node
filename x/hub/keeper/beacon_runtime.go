package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"

	"cosmossdk.io/collections"
	cmtcrypto "github.com/cometbft/cometbft/crypto"
	sdk "github.com/cosmos/cosmos-sdk/types"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

type BeaconProofVerifier interface {
	VerifyBeaconProof(codec string, pubkey []byte, input []byte, proof []byte, randomness []byte) error
}

type BeaconProofVerifierFunc func(codec string, pubkey []byte, input []byte, proof []byte, randomness []byte) error

func (fn BeaconProofVerifierFunc) VerifyBeaconProof(codec string, pubkey []byte, input []byte, proof []byte, randomness []byte) error {
	return fn(codec, pubkey, input, proof, randomness)
}

func (k Keeper) WriteBlockBeacon(ctx context.Context, height uint64, blockHash []byte, sourceTag string) (types.BeaconState, error) {
	if sourceTag != types.BeaconSourcePlaceholderBlockHashV1 {
		return types.BeaconState{}, fmt.Errorf("dev beacon writer cannot write source_tag %q", sourceTag)
	}
	if height == 0 {
		return types.BeaconState{}, fmt.Errorf("height must be > 0")
	}
	if len(blockHash) != sha256.Size {
		return types.BeaconState{}, fmt.Errorf("block_hash must be 32 bytes")
	}
	if existing, err := k.Beacon.Get(ctx, height); err == nil {
		if existing.Height != height {
			return types.BeaconState{}, fmt.Errorf("beacon key height %d does not match state height %d", height, existing.Height)
		}
		public, err := beaconStoreToState(existing)
		if err != nil {
			return types.BeaconState{}, fmt.Errorf("existing beacon height %d is invalid: %w", height, err)
		}
		if len(existing.BlockHash) != sha256.Size || !bytes.Equal(existing.BlockHash, blockHash) {
			return types.BeaconState{}, fmt.Errorf("beacon height %d already records a different block hash", height)
		}
		return public, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return types.BeaconState{}, err
	}
	prior, err := k.placeholderPriorBeaconBytes(ctx, height)
	if err != nil {
		return types.BeaconState{}, err
	}
	digest, err := combineBeacon(prior, height, blockHash)
	if err != nil {
		return types.BeaconState{}, err
	}
	stored := internaltypes.BeaconStoreState{
		Height:     height,
		Randomness: append([]byte(nil), digest...),
		SourceTag:  sourceTag,
		Verified:   false,
		BlockHash:  append([]byte(nil), blockHash...),
	}
	if err := k.persistBeaconStoreState(ctx, stored); err != nil {
		return types.BeaconState{}, err
	}
	return beaconStoreToState(stored)
}

// ValidateAndWriteVerifiedBeacon validates the carrier and persists it.
//
// proposerOperatorAddress is the proposer's stable operator account address
// (ADR-0004); the verification public key is obtained by indexing VrfKeyState with
// it -- not the consensus public key, see vrf_beacon_key.go for the reasoning.
func (k Keeper) ValidateAndWriteVerifiedBeacon(ctx context.Context, carrier types.BeaconCarrier, proposerConsensusAddressRaw []byte, proposerOperatorAddress string, verifier BeaconProofVerifier) (types.BeaconState, error) {
	stored, err := k.validateBeaconCarrierStore(ctx, carrier, proposerConsensusAddressRaw, proposerOperatorAddress, verifier)
	if err != nil {
		return types.BeaconState{}, err
	}
	if err := k.persistBeaconStoreState(ctx, stored); err != nil {
		return types.BeaconState{}, err
	}
	return beaconStoreToState(stored)
}

// ValidateBeaconCarrier only validates and does not persist, for use by
// ProcessProposal.
func (k Keeper) ValidateBeaconCarrier(ctx context.Context, carrier types.BeaconCarrier, proposerConsensusAddressRaw []byte, proposerOperatorAddress string, verifier BeaconProofVerifier) (types.BeaconState, error) {
	stored, err := k.validateBeaconCarrierStore(ctx, carrier, proposerConsensusAddressRaw, proposerOperatorAddress, verifier)
	if err != nil {
		return types.BeaconState{}, err
	}
	return beaconStoreToState(stored)
}

func (k Keeper) validateBeaconCarrierStore(ctx context.Context, carrier types.BeaconCarrier, proposerConsensusAddressRaw []byte, proposerOperatorAddress string, verifier BeaconProofVerifier) (internaltypes.BeaconStoreState, error) {
	if carrier.Height == 0 {
		return internaltypes.BeaconStoreState{}, fmt.Errorf("height must be > 0")
	}
	if carrier.SourceTag != types.BeaconSourceProposerVRFV1 {
		return internaltypes.BeaconStoreState{}, fmt.Errorf("source_tag must be %s", types.BeaconSourceProposerVRFV1)
	}
	canonicalProposerAddress, err := canonicalConsensusAddress(proposerConsensusAddressRaw)
	if err != nil {
		return internaltypes.BeaconStoreState{}, err
	}
	if carrier.ProposerConsensusAddress != canonicalProposerAddress {
		return internaltypes.BeaconStoreState{}, fmt.Errorf(
			"proposer_consensus_address %q does not match ABCI proposer %q",
			carrier.ProposerConsensusAddress, canonicalProposerAddress,
		)
	}
	if strings.TrimSpace(carrier.ProofCodec) == "" {
		return internaltypes.BeaconStoreState{}, fmt.Errorf("proof_codec is required")
	}
	if strings.TrimSpace(proposerOperatorAddress) == "" {
		return internaltypes.BeaconStoreState{}, fmt.Errorf("proposer operator address is required")
	}
	if verifier == nil {
		return internaltypes.BeaconStoreState{}, fmt.Errorf("beacon proof verifier is required")
	}
	blockHash, err := blockHashFromSDKContext(ctx, carrier.Height)
	if err != nil {
		return internaltypes.BeaconStoreState{}, err
	}
	if exists, err := k.Beacon.Has(ctx, carrier.Height); err != nil {
		return internaltypes.BeaconStoreState{}, err
	} else if exists {
		return internaltypes.BeaconStoreState{}, fmt.Errorf("duplicate beacon at height %d", carrier.Height)
	}
	randomness, err := decodeBeaconHex("randomness_hex", carrier.RandomnessHex)
	if err != nil {
		return internaltypes.BeaconStoreState{}, err
	}
	proof, err := decodeNonEmptyHex("proof_hex", carrier.ProofHex)
	if err != nil {
		return internaltypes.BeaconStoreState{}, err
	}
	input, err := k.BeaconVRFInputBytes(ctx, carrier.Height, proposerConsensusAddressRaw)
	if err != nil {
		return internaltypes.BeaconStoreState{}, err
	}
	// The verification public key comes from the on-chain VRF registry, indexed by
	// the stable operator, taking the active public key of the epoch the height
	// belongs to. No active public key means REJECT, with no fallback to the
	// consensus public key (randomness protocol §3.1 admission).
	vrfPubkey, err := k.ActiveVrfPubkeyForHeight(ctx, proposerOperatorAddress, carrier.Height)
	if err != nil {
		return internaltypes.BeaconStoreState{}, fmt.Errorf("resolve beacon verification key: %w", err)
	}
	if err := verifier.VerifyBeaconProof(carrier.ProofCodec, vrfPubkey, input, proof, randomness); err != nil {
		return internaltypes.BeaconStoreState{}, fmt.Errorf("invalid beacon proof: %w", err)
	}
	proofDigest := sha256.Sum256(proof)
	stored := internaltypes.BeaconStoreState{
		Height:                   carrier.Height,
		Randomness:               append([]byte(nil), randomness...),
		SourceTag:                carrier.SourceTag,
		ProofDigest:              proofDigest[:],
		ProposerConsensusAddress: canonicalProposerAddress,
		Verified:                 true,
		BlockHash:                blockHash,
	}
	if err := validateBeaconStoreState(stored, true); err != nil {
		return internaltypes.BeaconStoreState{}, err
	}
	return stored, nil
}

// BeaconVRFInputBytes derives the exact 32-byte ECVRF alpha bound to this
// chain, height, prior verified randomness and ABCI proposer.
func (k Keeper) BeaconVRFInputBytes(ctx context.Context, height uint64, proposerConsensusAddressRaw []byte) ([]byte, error) {
	if height == 0 {
		return nil, fmt.Errorf("height must be > 0")
	}
	if _, err := canonicalConsensusAddress(proposerConsensusAddressRaw); err != nil {
		return nil, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	chainID := sdkCtx.ChainID()
	if strings.TrimSpace(chainID) == "" || strings.TrimSpace(chainID) != chainID {
		return nil, fmt.Errorf("chain_id must be non-empty canonical text")
	}
	prior, err := k.priorBeaconBytes(ctx, height)
	if err != nil {
		return nil, err
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainBeaconVRFInputV1)).Raw(
		[]byte(chainID), shared.Uint64BE(height), prior, proposerConsensusAddressRaw,
	).Sum()
}

func canonicalConsensusAddress(proposerConsensusAddressRaw []byte) (string, error) {
	if len(proposerConsensusAddressRaw) != cmtcrypto.AddressSize {
		return "", fmt.Errorf(
			"ABCI proposer consensus address must be %d bytes, got %d",
			cmtcrypto.AddressSize, len(proposerConsensusAddressRaw),
		)
	}
	return sdk.ConsAddress(proposerConsensusAddressRaw).String(), nil
}

func (k Keeper) GetBeaconAtHeight(ctx context.Context, height uint64) (types.BeaconState, error) {
	stored, err := k.getBeaconStoreAtHeight(ctx, height)
	if err != nil {
		return types.BeaconState{}, err
	}
	return beaconStoreToState(stored)
}

// GetBlockAnchorHash returns the block hash persisted by the trusted ABCI
// beacon write path. It never accepts or derives an anchor from task input.
func (k Keeper) GetBlockAnchorHash(ctx context.Context, height uint64) ([]byte, error) {
	if height == 0 {
		return nil, fmt.Errorf("height must be > 0")
	}
	state, err := k.getBeaconStoreAtHeight(ctx, height)
	if err != nil {
		return nil, err
	}
	if state.Height != height {
		return nil, fmt.Errorf("beacon key height %d does not match state height %d", height, state.Height)
	}
	return append([]byte(nil), state.BlockHash...), nil
}

func blockHashFromSDKContext(ctx context.Context, height uint64) ([]byte, error) {
	sdkCtx, ok := ctx.(sdk.Context)
	if !ok {
		return nil, fmt.Errorf("beacon write requires sdk context")
	}
	if sdkCtx.BlockHeight() <= 0 || uint64(sdkCtx.BlockHeight()) != height {
		return nil, fmt.Errorf("beacon height %d does not match sdk block height %d", height, sdkCtx.BlockHeight())
	}
	blockHash := sdkCtx.HeaderHash()
	if len(blockHash) != sha256.Size {
		return nil, fmt.Errorf("sdk block header hash must be 32 bytes")
	}
	return blockHash, nil
}

func (k Keeper) AggregateBeacon(ctx context.Context, startHeight, count uint64) ([]byte, error) {
	return k.aggregateBeacon(ctx, startHeight, count, false)
}

func (k Keeper) AggregateBeaconAllowDevOnly(ctx context.Context, startHeight, count uint64) ([]byte, error) {
	return k.aggregateBeacon(ctx, startHeight, count, true)
}

func (k Keeper) aggregateBeacon(ctx context.Context, startHeight, count uint64, allowDevOnly bool) ([]byte, error) {
	if count == 0 {
		return nil, fmt.Errorf("count must be > 0")
	}
	if count > math.MaxUint32 {
		return nil, fmt.Errorf("count must fit uint32")
	}
	endHeight, err := checkedAdd(startHeight, count)
	if err != nil {
		return nil, fmt.Errorf("aggregation window overflows uint64: %w", err)
	}
	randomness := make([][]byte, 0, count)
	for h := startHeight; h < endHeight; h++ {
		entry, err := k.getBeaconStoreAtHeight(ctx, h)
		if err != nil {
			return nil, err
		}
		if err := validateBeaconStoreState(entry, !allowDevOnly); err != nil {
			return nil, fmt.Errorf("height %d: %w", h, err)
		}
		randomness = append(randomness, entry.Randomness)
	}
	return beaconAggregateDigest(startHeight, uint32(count), randomness)
}

// beaconAggregateDigest is the TRUEOPEN_BEACON_AGGREGATE_V1 preimage, separated from
// the window scan above so that it can be called with an explicit randomness list.
//
// Collecting the rows is a store concern; the field order is consensus. While the
// two lived in one method body the only way to reach the preimage was to stand up a
// keeper with a populated beacon store, which is why no test ever fed this domain a
// list of its own choosing and why a reordering of start_height against beacon_count
// - or of two randomness entries - had nothing to fail.
func beaconAggregateDigest(startHeight uint64, beaconCount uint32, randomness [][]byte) ([]byte, error) {
	if uint64(beaconCount) != uint64(len(randomness)) {
		return nil, fmt.Errorf("beacon_count %d does not match %d randomness entries", beaconCount, len(randomness))
	}
	randomnessFields := make([]shared.CanonicalFieldV1, len(randomness))
	for index := range randomness {
		randomnessFields[index] = shared.RawCanonicalFieldV1(randomness[index])
	}
	repeatedRandomness := shared.CanonicalRepeatedFieldsV1(randomnessFields)
	if err := repeatedRandomness.Err(); err != nil {
		return nil, fmt.Errorf("beacon aggregate randomness: %w", err)
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainBeaconAggregateV1)).Raw(
		shared.Uint64BE(startHeight),
		shared.Uint32BE(beaconCount),
	).Nested(repeatedRandomness).Sum()
}

func decodeBeaconHex(fieldName, value string) ([]byte, error) {
	bytes, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%s must be hex: %w", fieldName, err)
	}
	if len(bytes) != sha256.Size {
		return nil, fmt.Errorf("%s must be 32 bytes", fieldName)
	}
	if value != strings.ToLower(value) {
		return nil, fmt.Errorf("%s must use lower-case hex", fieldName)
	}
	return bytes, nil
}

func decodeNonEmptyHex(fieldName, value string) ([]byte, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("%s is required", fieldName)
	}
	// The production carrier contract remains blocked by K-17, but the current
	// development carrier must still be allocation-bounded before hex decoding.
	const maxTransientBeaconProofBytes = 4096
	if len(value) > maxTransientBeaconProofBytes*2 {
		return nil, fmt.Errorf("%s exceeds %d bytes", fieldName, maxTransientBeaconProofBytes)
	}
	bytes, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%s must be hex: %w", fieldName, err)
	}
	if len(bytes) == 0 {
		return nil, fmt.Errorf("%s must not be empty", fieldName)
	}
	if value != strings.ToLower(value) {
		return nil, fmt.Errorf("%s must use lower-case hex", fieldName)
	}
	return bytes, nil
}

func (k Keeper) priorBeaconBytes(ctx context.Context, height uint64) ([]byte, error) {
	if height == 0 || height == 1 {
		return make([]byte, sha256.Size), nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("load Hub params for beacon VRF activation: %w", err)
	}
	if params.Beacon.VrfRequiredFromHeight != 0 && height == params.Beacon.VrfRequiredFromHeight {
		return make([]byte, sha256.Size), nil
	}
	prior, err := k.getBeaconStoreAtHeight(ctx, height-1)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, fmt.Errorf("beacon height %d not found", height-1)
		}
		return nil, err
	}
	if err := validateBeaconStoreState(prior, true); err != nil {
		return nil, fmt.Errorf("height %d: %w", height-1, err)
	}
	return append([]byte(nil), prior.Randomness...), nil
}

func (k Keeper) placeholderPriorBeaconBytes(ctx context.Context, height uint64) ([]byte, error) {
	if height == 0 || height == 1 {
		return make([]byte, sha256.Size), nil
	}
	prior, err := k.getBeaconStoreAtHeight(ctx, height-1)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, fmt.Errorf("beacon height %d not found", height-1)
		}
		return nil, err
	}
	if err := validateBeaconStoreState(prior, false); err != nil {
		return nil, fmt.Errorf("height %d: %w", height-1, err)
	}
	return append([]byte(nil), prior.Randomness...), nil
}

func combineBeacon(prior []byte, height uint64, blockHash []byte) ([]byte, error) {
	if len(prior) != sha256.Size || len(blockHash) != sha256.Size {
		return nil, fmt.Errorf("placeholder prior randomness and block_hash must be 32 bytes")
	}
	if height == 0 {
		return nil, fmt.Errorf("placeholder height must be > 0")
	}
	preimage, err := shared.FlatCanonicalFrameV1(prior, shared.Uint64BE(height), blockHash).Bytes()
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(preimage)
	return digest[:], nil
}
