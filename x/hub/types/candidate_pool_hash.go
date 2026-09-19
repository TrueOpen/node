package types

import (
	"crypto/sha256"
	"fmt"
	"math"
	"math/bits"
	"unicode/utf8"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// CandidatePoolMemberRef is the canonical identity projection used by the
// CandidatePool segment and member-set commitments. OperatorBytes are address
// codec bytes, never Bech32 presentation text.
type CandidatePoolMemberRef struct {
	Slot          uint32
	SlotVersion   uint64
	OperatorBytes []byte
	BindingHash   []byte
}

// CandidatePoolSegmentRef is one fixed-width bitmap segment in canonical
// segment-index order. Callers must materialize omitted all-zero segments.
type CandidatePoolSegmentRef struct {
	SegmentIndex uint32
	Bitmap       []byte
}

func CandidateSlotBindingHash(slot uint32, slotVersion uint64, operatorBytes []byte, allocatedEpoch uint64) ([]byte, error) {
	if slotVersion == 0 {
		return nil, fmt.Errorf("slot_version must be greater than zero")
	}
	if err := validateCandidateOperatorBytes(operatorBytes); err != nil {
		return nil, err
	}
	return candidatePoolDigest(shared.DomainCandidateSlotBindingV1,
		shared.Uint32BE(slot), shared.Uint64BE(slotVersion), operatorBytes, shared.Uint64BE(allocatedEpoch))
}

func CandidatePoolSegmentHash(targetEpoch uint64, segmentIndex uint32, segmentBitmap []byte, activeMemberCount uint32, members []CandidatePoolMemberRef) ([]byte, error) {
	if len(segmentBitmap) == 0 {
		return nil, fmt.Errorf("segment bitmap must not be empty")
	}
	if uint64(len(members)) != uint64(activeMemberCount) {
		return nil, fmt.Errorf("active_member_count %d does not match %d members", activeMemberCount, len(members))
	}
	if uint64(len(members)) > math.MaxUint32 {
		return nil, fmt.Errorf("member count overflows uint32")
	}
	setBits := 0
	for _, value := range segmentBitmap {
		setBits += bits.OnesCount8(value)
	}
	if uint64(setBits) != uint64(activeMemberCount) {
		return nil, fmt.Errorf("segment bitmap has %d active bits, want %d", setBits, activeMemberCount)
	}

	segmentBits := uint64(len(segmentBitmap)) * 8
	segmentStart := uint64(segmentIndex) * segmentBits
	elements := make([]shared.CanonicalFrameV1, 0, len(members))
	builder := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainCandidatePoolSegmentV1)).Raw(
		shared.Uint64BE(targetEpoch), shared.Uint32BE(segmentIndex), segmentBitmap, shared.Uint32BE(activeMemberCount),
	)
	for i, member := range members {
		if err := validateCandidateMemberRef(member, i, members); err != nil {
			return nil, err
		}
		slot := uint64(member.Slot)
		if slot < segmentStart || slot >= segmentStart+segmentBits {
			return nil, fmt.Errorf("member slot %d is outside segment %d", member.Slot, segmentIndex)
		}
		bit := slot - segmentStart
		if segmentBitmap[bit/8]&(byte(1)<<uint(bit%8)) == 0 {
			return nil, fmt.Errorf("member slot %d is not active in segment bitmap", member.Slot)
		}
		elements = append(elements, shared.FlatCanonicalFrameV1(
			shared.Uint32BE(member.Slot), shared.Uint64BE(member.SlotVersion), member.BindingHash,
		))
	}
	return builder.Nested(shared.CanonicalRepeatedFramesV1(elements)).Sum()
}

func CandidateActiveBitmapHash(slotCapacity uint32, segmentCount uint32, segments []CandidatePoolSegmentRef) ([]byte, error) {
	if slotCapacity == 0 || segmentCount == 0 {
		return nil, fmt.Errorf("slot_capacity and segment_count must be greater than zero")
	}
	if uint64(len(segments)) != uint64(segmentCount) {
		return nil, fmt.Errorf("segment_count %d does not match %d segments", segmentCount, len(segments))
	}
	segmentBytes := len(segments[0].Bitmap)
	if segmentBytes == 0 {
		return nil, fmt.Errorf("segment bitmap must not be empty")
	}
	segmentBits := uint64(segmentBytes) * 8
	wantSegments := (uint64(slotCapacity) + segmentBits - 1) / segmentBits
	if wantSegments != uint64(segmentCount) {
		return nil, fmt.Errorf("segment_count %d does not match slot capacity geometry %d", segmentCount, wantSegments)
	}

	builder := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainCandidateActiveBitmapV1)).Raw(
		shared.Uint32BE(slotCapacity), shared.Uint32BE(segmentCount),
	)
	elements := make([]shared.CanonicalFrameV1, 0, len(segments))
	for i, segment := range segments {
		if segment.SegmentIndex != uint32(i) {
			return nil, fmt.Errorf("segments must be contiguous and ascending: index %d has segment_index %d", i, segment.SegmentIndex)
		}
		if len(segment.Bitmap) != segmentBytes {
			return nil, fmt.Errorf("segment %d bitmap length %d does not match %d", i, len(segment.Bitmap), segmentBytes)
		}
		elements = append(elements, shared.FlatCanonicalFrameV1(shared.Uint32BE(segment.SegmentIndex), segment.Bitmap))
	}
	usedInLast := uint64(slotCapacity) % segmentBits
	if usedInLast != 0 {
		last := segments[len(segments)-1].Bitmap
		for bit := usedInLast; bit < segmentBits; bit++ {
			if last[bit/8]&(byte(1)<<uint(bit%8)) != 0 {
				return nil, fmt.Errorf("last segment has non-zero trailing bit %d", bit)
			}
		}
	}
	return builder.Nested(shared.CanonicalRepeatedFramesV1(elements)).Sum()
}

func CandidateMemberSetHash(activeCount uint32, members []CandidatePoolMemberRef) ([]byte, error) {
	if uint64(len(members)) != uint64(activeCount) {
		return nil, fmt.Errorf("active_count %d does not match %d members", activeCount, len(members))
	}
	elements := make([]shared.CanonicalFrameV1, 0, len(members))
	builder := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainCandidateMemberSetV1)).Raw(shared.Uint32BE(activeCount))
	for i, member := range members {
		if err := validateCandidateMemberRef(member, i, members); err != nil {
			return nil, err
		}
		elements = append(elements, shared.FlatCanonicalFrameV1(
			shared.Uint32BE(member.Slot), shared.Uint64BE(member.SlotVersion), member.OperatorBytes, member.BindingHash,
		))
	}
	return builder.Nested(shared.CanonicalRepeatedFramesV1(elements)).Sum()
}

func CandidatePoolHash(chainID string, epoch uint64, slotCapacity uint32, effectiveHeight, expiresHeight uint64, activeCount uint32, activeBitmapHash, memberSetHash []byte) ([]byte, error) {
	if chainID == "" || !utf8.ValidString(chainID) {
		return nil, fmt.Errorf("chain_id must be non-empty strict UTF-8")
	}
	if slotCapacity == 0 || activeCount > slotCapacity {
		return nil, fmt.Errorf("active_count must not exceed positive slot_capacity")
	}
	if effectiveHeight >= expiresHeight {
		return nil, fmt.Errorf("effective_height must be less than expires_height")
	}
	if err := validateCandidateHash32("active_bitmap_hash", activeBitmapHash); err != nil {
		return nil, err
	}
	if err := validateCandidateHash32("member_set_hash", memberSetHash); err != nil {
		return nil, err
	}
	return candidatePoolDigest(shared.DomainGlobalCandidatePoolV1,
		[]byte(chainID), shared.Uint64BE(epoch), shared.Uint32BE(slotCapacity),
		shared.Uint64BE(effectiveHeight), shared.Uint64BE(expiresHeight), shared.Uint32BE(activeCount),
		activeBitmapHash, memberSetHash,
	)
}

func CandidatePoolSnapshotID(chainID string, epoch uint64, candidatePoolHash []byte) ([]byte, error) {
	if chainID == "" || !utf8.ValidString(chainID) {
		return nil, fmt.Errorf("chain_id must be non-empty strict UTF-8")
	}
	if err := validateCandidateHash32("candidate_pool_hash", candidatePoolHash); err != nil {
		return nil, err
	}
	return candidatePoolDigest(shared.DomainGlobalCandidatePoolSnapshotIDV1,
		[]byte(chainID), shared.Uint64BE(epoch), candidatePoolHash)
}

func CandidateMembershipBinding(chainID string, snapshotID, poolHash []byte, slot uint32, slotVersion uint64, operatorBytes []byte) ([]byte, error) {
	if chainID == "" || !utf8.ValidString(chainID) {
		return nil, fmt.Errorf("chain_id must be non-empty strict UTF-8")
	}
	if err := validateCandidateHash32("candidate_pool_snapshot_id", snapshotID); err != nil {
		return nil, err
	}
	if err := validateCandidateHash32("candidate_pool_hash", poolHash); err != nil {
		return nil, err
	}
	if slotVersion == 0 {
		return nil, fmt.Errorf("slot_version must be greater than zero")
	}
	if err := validateCandidateOperatorBytes(operatorBytes); err != nil {
		return nil, err
	}
	return candidatePoolDigest(shared.DomainCandidateMembershipV1,
		[]byte(chainID), snapshotID, poolHash, shared.Uint32BE(slot), shared.Uint64BE(slotVersion), operatorBytes)
}

func validateCandidateMemberRef(member CandidatePoolMemberRef, index int, members []CandidatePoolMemberRef) error {
	if index > 0 && members[index-1].Slot >= member.Slot {
		return fmt.Errorf("members must be strictly ascending by slot with no duplicates")
	}
	if member.SlotVersion == 0 {
		return fmt.Errorf("member slot_version must be greater than zero")
	}
	if err := validateCandidateOperatorBytes(member.OperatorBytes); err != nil {
		return err
	}
	return validateCandidateHash32("binding_hash", member.BindingHash)
}

func validateCandidateOperatorBytes(operatorBytes []byte) error {
	if len(operatorBytes) == 0 || len(operatorBytes) > 255 {
		return fmt.Errorf("operator_address bytes length must be in 1..255")
	}
	return nil
}

func validateCandidateHash32(field string, value []byte) error {
	if len(value) != sha256.Size {
		return fmt.Errorf("%s must be exactly %d raw bytes", field, sha256.Size)
	}
	return nil
}

// candidatePoolDigest is the one flat-hash shape shared by the four candidate
// pool identities below. domainKey is a parameter rather than a field because
// those four differ only in their domain; every caller passes a frozen
// DomainRegistryV1 constant, and MustDomain plus the builder's own domain check
// keep a runtime-assembled string from reaching a digest.
func candidatePoolDigest(domainKey string, fields ...[]byte) ([]byte, error) {
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(domainKey)).Raw(fields...).Sum()
}
