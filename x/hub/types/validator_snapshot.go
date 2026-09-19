package types

import (
	"bytes"
	"fmt"
	"math"
	"sort"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// CanonicalValidatorSnapshotMember is the byte-level member projection used by
// TRUEOPEN_VALIDATOR_SET_SNAPSHOT_V1. SignerAddress supports historical membership
// lookup but is deliberately not part of the frozen hash preimage.
type CanonicalValidatorSnapshotMember struct {
	ConsensusAddress []byte
	SignerAddress    string
	VotingPower      uint64
}

// CanonicalValidatorSetSnapshot implements the sole validator snapshot hash:
// H_FIELDS_V1(domain, chain_id, height, count, total_power,
// repeated(consensus_address, voting_power in consensus-address byte order)).
func CanonicalValidatorSetSnapshot(chainID string, height uint64, members []CanonicalValidatorSnapshotMember) ([]byte, uint64, error) {
	if chainID == "" || height == 0 || len(members) == 0 || len(members) > math.MaxUint32 {
		return nil, 0, fmt.Errorf("validator snapshot scope or member count is invalid")
	}
	ordered := append([]CanonicalValidatorSnapshotMember(nil), members...)
	sort.Slice(ordered, func(i, j int) bool {
		return bytes.Compare(ordered[i].ConsensusAddress, ordered[j].ConsensusAddress) < 0
	})
	memberFrames := make([]shared.CanonicalFrameV1, len(ordered))
	total := uint64(0)
	for index, member := range ordered {
		if len(member.ConsensusAddress) == 0 || member.SignerAddress == "" || member.VotingPower == 0 {
			return nil, 0, fmt.Errorf("validator snapshot member %d is incomplete", index)
		}
		if index > 0 && bytes.Equal(ordered[index-1].ConsensusAddress, member.ConsensusAddress) {
			return nil, 0, fmt.Errorf("duplicate validator consensus address")
		}
		if math.MaxUint64-total < member.VotingPower {
			return nil, 0, fmt.Errorf("validator snapshot total voting power overflow")
		}
		total += member.VotingPower
		memberFrames[index] = shared.FlatCanonicalFrameV1(
			member.ConsensusAddress,
			shared.Uint64BE(member.VotingPower),
		)
	}
	repeatedMembers := shared.CanonicalRepeatedFramesV1(memberFrames)
	if err := repeatedMembers.Err(); err != nil {
		return nil, 0, fmt.Errorf("validator snapshot members: %w", err)
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainValidatorSetSnapshotV1)).Raw(
		[]byte(chainID),
		shared.Uint64BE(height),
		shared.Uint32BE(uint32(len(ordered))),
		shared.Uint64BE(total),
	).Nested(repeatedMembers).Sum()
	if err != nil {
		return nil, 0, err
	}
	return digest, total, nil
}
