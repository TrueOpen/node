package types

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
)

const (
	BatchMutationAppliedV1 = uint32(1)
	BatchMutationNoopV1    = uint32(2)
)

// BatchResultDigestItemV1 is the language-neutral digest projection of one
// dense BatchItemResultV1. item_index is positional and is therefore not hashed
// a second time.
type BatchResultDigestItemV1 struct {
	ObjectID []byte
	Status   uint32
}

// BatchResultDigestV1 is the sole TRUEOPEN_BATCH_RESULT_V1 implementation shared
// by Hub and Task batch handlers.
func BatchResultDigestV1(chainID, kind string, items []BatchResultDigestItemV1) ([]byte, error) {
	if strings.TrimSpace(chainID) == "" || strings.TrimSpace(chainID) != chainID {
		return nil, fmt.Errorf("chain_id must be non-empty and canonical")
	}
	if !IsBatchResultKind(kind) {
		return nil, fmt.Errorf("unknown batch result kind %q", kind)
	}
	if len(items) == 0 || len(items) > math.MaxUint32 {
		return nil, fmt.Errorf("batch result item count is out of range")
	}
	elements := make([]CanonicalFrameV1, 0, len(items))
	for index, item := range items {
		if len(item.ObjectID) != sha256.Size || bytes.Equal(item.ObjectID, make([]byte, sha256.Size)) {
			return nil, fmt.Errorf("batch result object_id at item %d must be a non-zero Hash32", index)
		}
		if item.Status != BatchMutationAppliedV1 && item.Status != BatchMutationNoopV1 {
			return nil, fmt.Errorf("batch result status at item %d is not APPLIED or NOOP", index)
		}
		elements = append(elements, FlatCanonicalFrameV1(item.ObjectID, EnumBE(item.Status)))
	}
	return NewCanonicalHashBuilderV1(MustDomain(DomainBatchResultV1)).Raw(
		[]byte(chainID), []byte(kind), Uint32BE(uint32(len(items))),
	).Nested(CanonicalRepeatedFramesV1(elements)).Sum()
}
