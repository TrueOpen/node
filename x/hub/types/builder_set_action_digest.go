package types

import (
	"bytes"
	"fmt"
	"math"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// ReplaceBuilderSetActionDigest is the TRUEOPEN_REPLACE_BUILDER_SET_V1 preimage that
// x/gov and the Hub recomputation must agree on (keeper_api_contract.md §9.6c).
//
// Unlike the five bridge action digests this one *is* persisted:
// BuilderSetPendingReplacementState.action_digest keeps it for the whole lead
// window so a re-executed proposal item can be recognised as the same action
// while its replacement is still pending. Anything that changes these bytes
// therefore turns an exact replay into a rejected conflict on a live chain.
//
// member_count is emitted as its own field even though the repeated frame
// carries its own REPEATED_V1 count. That duplication is what
// registry/v1/domains.json freezes; dropping it would shift effective_height
// into the position the repeated frame used to occupy, which no encoder-class
// check would notice.
func ReplaceBuilderSetActionDigest(chainID string, action ReplaceBuilderSetV1) ([32]byte, error) {
	if chainID == "" {
		return [32]byte{}, fmt.Errorf("chain_id must not be empty")
	}
	if action.ProposalId == 0 {
		return [32]byte{}, fmt.Errorf("proposal_id must be non-zero")
	}
	expectedSetHash, err := requireBytesLen("expected_current_set_hash", action.ExpectedCurrentSetHash, shared.Hash32KeySize)
	if err != nil {
		return [32]byte{}, err
	}
	setID, err := requireCanonicalNonEmpty("next_builder_set_id", action.NextBuilderSetId)
	if err != nil {
		return [32]byte{}, err
	}
	if action.EffectiveHeight == 0 {
		return [32]byte{}, fmt.Errorf("effective_height must be non-zero")
	}
	members, err := ReplaceBuilderSetMemberBytes(action.Members)
	if err != nil {
		return [32]byte{}, err
	}
	elements := make([]shared.CanonicalFieldV1, len(members))
	for index := range members {
		elements[index] = shared.RawCanonicalFieldV1(members[index])
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainReplaceBuilderSetV1)).
		Raw(
			[]byte(chainID), shared.Uint64BE(action.ProposalId),
			shared.Uint64BE(action.ExpectedCurrentVersion), expectedSetHash,
			[]byte(setID), shared.Uint32BE(uint32(len(members))),
		).
		Nested(shared.CanonicalRepeatedFieldsV1(elements)).
		Raw(shared.Uint64BE(action.EffectiveHeight)).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// ReplaceBuilderSetMemberBytes decodes members into the canonical codec bytes the
// preimage contributes, enforcing the §9.6c ordering rule while it goes: strictly
// ascending by address bytes, which makes uniqueness a consequence rather than a
// separate check. The Keeper reuses it so the digest and the snapshot it stores
// can never disagree about which bytes a member contributed.
func ReplaceBuilderSetMemberBytes(members []string) ([][]byte, error) {
	if len(members) == 0 {
		return nil, fmt.Errorf("members must not be empty")
	}
	if uint64(len(members)) > math.MaxUint32 {
		return nil, fmt.Errorf("members must have a uint32-sized count")
	}
	decoded := make([][]byte, len(members))
	for index, member := range members {
		// §1.4 rule 4 address encoding: every Address field in a governance action
		// preimage contributes its canonical codec bytes, never its Bech32 text.
		raw, err := bridgeAddressBytes(fmt.Sprintf("members[%d]", index), member)
		if err != nil {
			return nil, err
		}
		if index != 0 && bytes.Compare(decoded[index-1], raw) >= 0 {
			return nil, fmt.Errorf("members must be strictly ascending by address bytes and unique")
		}
		decoded[index] = raw
	}
	return decoded, nil
}
