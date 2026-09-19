package keeper

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"cosmossdk.io/collections"
	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
)

func (k Keeper) getBeaconStoreAtHeight(ctx context.Context, height uint64) (internaltypes.BeaconStoreState, error) {
	stored, err := k.Beacon.Get(ctx, height)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return internaltypes.BeaconStoreState{}, fmt.Errorf("beacon height %d not found: %w", height, collections.ErrNotFound)
		}
		return internaltypes.BeaconStoreState{}, err
	}
	if stored.Height != height {
		return internaltypes.BeaconStoreState{}, fmt.Errorf("beacon key height %d does not match state height %d", height, stored.Height)
	}
	if err := validateBeaconStoreState(stored, false); err != nil {
		return internaltypes.BeaconStoreState{}, fmt.Errorf("beacon height %d: %w", height, err)
	}
	return stored, nil
}

func beaconStateToStore(state types.BeaconState) (internaltypes.BeaconStoreState, error) {
	if err := types.ValidateBeaconState(state, false); err != nil {
		return internaltypes.BeaconStoreState{}, err
	}
	if len(state.RandomnessHex) != 64 || state.RandomnessHex != strings.ToLower(state.RandomnessHex) {
		return internaltypes.BeaconStoreState{}, fmt.Errorf("beacon randomness_hex must be 32-byte lower-case hex")
	}
	randomness, err := hex.DecodeString(state.RandomnessHex)
	if err != nil {
		return internaltypes.BeaconStoreState{}, fmt.Errorf("decode beacon randomness: %w", err)
	}
	stored := internaltypes.BeaconStoreState{
		Height: state.Height, Randomness: append([]byte(nil), randomness...), SourceTag: state.SourceTag,
		ProofDigest: append([]byte(nil), state.GetProofDigest()...), ProposerConsensusAddress: state.ProposerConsensusAddress,
		Verified: state.Verified, BlockHash: append([]byte(nil), state.BlockHash...),
	}
	if err := validateBeaconStoreState(stored, false); err != nil {
		return internaltypes.BeaconStoreState{}, err
	}
	return stored, nil
}

func beaconStoreToState(stored internaltypes.BeaconStoreState) (types.BeaconState, error) {
	if err := validateBeaconStoreState(stored, false); err != nil {
		return types.BeaconState{}, err
	}
	return beaconStorePublicProjection(stored), nil
}

func beaconStorePublicProjection(stored internaltypes.BeaconStoreState) types.BeaconState {
	state := types.BeaconState{
		Height: stored.Height, RandomnessHex: hex.EncodeToString(stored.Randomness), SourceTag: stored.SourceTag,
		ProposerConsensusAddress: stored.ProposerConsensusAddress,
		Verified:                 stored.Verified,
		BlockHash:                append([]byte(nil), stored.BlockHash...),
	}
	if len(stored.ProofDigest) != 0 {
		state.XProofDigest = &types.BeaconState_ProofDigest{ProofDigest: append([]byte(nil), stored.ProofDigest...)}
	}
	return state
}

func validateBeaconStoreState(stored internaltypes.BeaconStoreState, requireVerified bool) error {
	if stored.Height == 0 {
		return fmt.Errorf("height must be > 0")
	}
	if len(stored.Randomness) != 32 {
		return fmt.Errorf("stored beacon randomness must be 32 raw bytes")
	}
	if len(stored.BlockHash) != 32 {
		return fmt.Errorf("stored beacon block_hash must be 32 raw bytes")
	}
	switch stored.SourceTag {
	case types.BeaconSourcePlaceholderBlockHashV1:
		if requireVerified {
			return fmt.Errorf("dev-only beacon source is not allowed")
		}
		if len(stored.ProofDigest) != 0 || stored.ProposerConsensusAddress != "" || stored.Verified {
			return fmt.Errorf("placeholder beacon must not carry proof/proposer/verified fields")
		}
	case types.BeaconSourceProposerVRFV1:
		if !stored.Verified {
			return fmt.Errorf("proposer-VRF beacon must be verified")
		}
		if len(stored.ProofDigest) != 32 {
			return fmt.Errorf("proof_digest must be 32 bytes")
		}
		if strings.TrimSpace(stored.ProposerConsensusAddress) == "" {
			return fmt.Errorf("proposer_consensus_address is required")
		}
		if strings.TrimSpace(stored.ProposerConsensusAddress) != stored.ProposerConsensusAddress {
			return fmt.Errorf("proposer_consensus_address must be canonical without surrounding whitespace")
		}
	default:
		return fmt.Errorf("unknown source_tag %q", stored.SourceTag)
	}
	return nil
}
