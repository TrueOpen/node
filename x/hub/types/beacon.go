package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	shared "github.com/TrueOpen/node/x/shared/types"
)

type BeaconConsumerKind uint32

const (
	BeaconConsumerKindAssignment        BeaconConsumerKind = 1
	BeaconConsumerKindVerifierWindow    BeaconConsumerKind = 2
	BeaconConsumerKindVerifierSelection BeaconConsumerKind = 3
	BeaconConsumerKindRewardMark        BeaconConsumerKind = 4
	BeaconConsumerKindChallengeSampling BeaconConsumerKind = 5
	MaxBeaconConsumerIDBytes                               = 128
)

// ValidateBeaconState validates the persisted beacon row shared by runtime
// writes and genesis import. requireVerified excludes the dev-only placeholder
// source from consumers that require proposer VRF randomness.
func ValidateBeaconState(state BeaconState, requireVerified bool) error {
	if state.Height == 0 {
		return fmt.Errorf("height must be > 0")
	}
	if _, err := decodeHashHex("randomness_hex", state.RandomnessHex); err != nil {
		return err
	}
	switch state.SourceTag {
	case BeaconSourcePlaceholderBlockHashV1:
		if requireVerified {
			return fmt.Errorf("dev-only beacon source is not allowed")
		}
		if state.GetXProofDigest() != nil || state.ProposerConsensusAddress != "" || state.Verified {
			return fmt.Errorf("placeholder beacon must not carry proof/proposer/verified fields")
		}
	case BeaconSourceProposerVRFV1:
		if !state.Verified {
			return fmt.Errorf("proposer-VRF beacon must be verified")
		}
		if len(state.GetProofDigest()) != sha256.Size {
			return fmt.Errorf("proof_digest must be 32 bytes")
		}
		if strings.TrimSpace(state.ProposerConsensusAddress) == "" {
			return fmt.Errorf("proposer_consensus_address is required")
		}
		if strings.TrimSpace(state.ProposerConsensusAddress) != state.ProposerConsensusAddress {
			return fmt.Errorf("proposer_consensus_address must be canonical without surrounding whitespace")
		}
	default:
		return fmt.Errorf("unknown source_tag %q", state.SourceTag)
	}
	return nil
}

func ValidateBeaconConsumerRef(height uint64, kind BeaconConsumerKind, consumerID string) error {
	if height == 0 {
		return fmt.Errorf("beacon consumer height must be > 0")
	}
	switch kind {
	case BeaconConsumerKindAssignment,
		BeaconConsumerKindVerifierWindow,
		BeaconConsumerKindVerifierSelection,
		BeaconConsumerKindRewardMark,
		BeaconConsumerKindChallengeSampling:
	default:
		return fmt.Errorf("beacon consumer kind is invalid")
	}
	if consumerID == "" || consumerID != strings.TrimSpace(consumerID) || len(consumerID) > MaxBeaconConsumerIDBytes || strings.IndexByte(consumerID, 0) >= 0 {
		return fmt.Errorf("beacon consumer id is not canonical or exceeds %d bytes", MaxBeaconConsumerIDBytes)
	}
	return nil
}

func ValidateBeaconCheckpointState(state BeaconCheckpointState, interval uint64) error {
	if interval == 0 || state.StartHeight == 0 || state.BeaconCount != interval || len(state.CheckpointRoot) != sha256.Size {
		return fmt.Errorf("beacon checkpoint shape is invalid")
	}
	expectedStart, overflow := checkpointStartHeight(state.CheckpointIndex, interval)
	if overflow || expectedStart != state.StartHeight || interval-1 > ^uint64(0)-state.StartHeight || state.EndHeight != state.StartHeight+interval-1 {
		return fmt.Errorf("beacon checkpoint interval is invalid")
	}
	return nil
}

func ValidateBeaconCheckpointCursorState(state BeaconCheckpointCursorState, interval uint64) error {
	if interval == 0 || state.StartHeight == 0 || state.BeaconCount == 0 || state.BeaconCount >= interval || len(state.RollingRoot) != sha256.Size {
		return fmt.Errorf("beacon checkpoint cursor shape is invalid")
	}
	expectedStart, overflow := checkpointStartHeight(state.CheckpointIndex, interval)
	if overflow || expectedStart != state.StartHeight || state.BeaconCount-1 > ^uint64(0)-state.StartHeight || state.LastHeight != state.StartHeight+state.BeaconCount-1 {
		return fmt.Errorf("beacon checkpoint cursor interval is invalid")
	}
	return nil
}

func BeaconCheckpointStep(
	checkpointIndex, startHeight, currentHeight uint64,
	previousRoot, randomness, proofDigest []byte,
	proposerConsensusAddress string,
) ([]byte, error) {
	if currentHeight < startHeight || len(previousRoot) != sha256.Size || len(randomness) != sha256.Size {
		return nil, fmt.Errorf("beacon checkpoint input is invalid")
	}
	if len(proofDigest) == 0 {
		proofDigest = make([]byte, sha256.Size)
	} else if len(proofDigest) != sha256.Size {
		return nil, fmt.Errorf("beacon checkpoint proof digest must be empty or 32 bytes")
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainBeaconCheckpointV1)).Raw(
		shared.Uint64BE(checkpointIndex), shared.Uint64BE(startHeight), shared.Uint64BE(currentHeight),
		previousRoot, randomness, proofDigest, []byte(proposerConsensusAddress),
	).Sum()
}

func BeaconCheckpointRoot(rows []BeaconState, checkpointIndex, interval uint64) ([]byte, error) {
	start, overflow := checkpointStartHeight(checkpointIndex, interval)
	if overflow || uint64(len(rows)) != interval {
		return nil, fmt.Errorf("beacon checkpoint rows do not fill one interval")
	}
	root := make([]byte, sha256.Size)
	for i, row := range rows {
		expectedHeight := start + uint64(i)
		if row.Height != expectedHeight {
			return nil, fmt.Errorf("beacon checkpoint row height %d does not match %d", row.Height, expectedHeight)
		}
		if err := ValidateBeaconState(row, false); err != nil {
			return nil, err
		}
		randomness, _ := hex.DecodeString(row.RandomnessHex)
		next, err := BeaconCheckpointStep(checkpointIndex, start, row.Height, root, randomness, row.GetProofDigest(), row.ProposerConsensusAddress)
		if err != nil {
			return nil, err
		}
		root = next
	}
	return root, nil
}

func BeaconCheckpointMatches(state BeaconCheckpointState, rows []BeaconState, interval uint64) error {
	if err := ValidateBeaconCheckpointState(state, interval); err != nil {
		return err
	}
	root, err := BeaconCheckpointRoot(rows, state.CheckpointIndex, interval)
	if err != nil {
		return err
	}
	if !bytes.Equal(root, state.CheckpointRoot) {
		return fmt.Errorf("beacon checkpoint root mismatch")
	}
	return nil
}

func checkpointStartHeight(index, interval uint64) (uint64, bool) {
	if interval == 0 || index > (^uint64(0)-1)/interval {
		return 0, true
	}
	return index*interval + 1, false
}

func decodeHashHex(fieldName, value string) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%s must be hex: %w", fieldName, err)
	}
	if len(decoded) != sha256.Size {
		return nil, fmt.Errorf("%s must be 32 bytes", fieldName)
	}
	if value != strings.ToLower(value) {
		return nil, fmt.Errorf("%s must use lower-case hex", fieldName)
	}
	return decoded, nil
}
