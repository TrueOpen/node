package types

import (
	"encoding/binary"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const MaxCanonicalTokenIDsV1 = (shared.MaxCanonicalFieldBytesV1 - 4) / 4

// InputTokenIDsHashV1 preserves caller order and commits one raw field:
// u32_be(count) followed by count u32_be token IDs.
func InputTokenIDsHashV1(tokenIDs []uint32) ([32]byte, uint64, error) {
	return tokenIDsHashV1(shared.DomainInputTokenIDsV1, tokenIDs)
}

// GeneratedTokenIDsHashV1 uses the same raw encoding under its own domain.
func GeneratedTokenIDsHashV1(tokenIDs []uint32) ([32]byte, uint64, error) {
	return tokenIDsHashV1(shared.DomainGeneratedTokenIDsV1, tokenIDs)
}

func tokenIDsHashV1(domain string, tokenIDs []uint32) ([32]byte, uint64, error) {
	if uint64(len(tokenIDs)) > MaxCanonicalTokenIDsV1 {
		return [32]byte{}, 0, fmt.Errorf("token ID count %d exceeds %d", len(tokenIDs), MaxCanonicalTokenIDsV1)
	}
	rawSize := uint64(4) + uint64(len(tokenIDs))*4
	raw := make([]byte, int(rawSize))
	binary.BigEndian.PutUint32(raw[:4], uint32(len(tokenIDs)))
	for index, tokenID := range tokenIDs {
		binary.BigEndian.PutUint32(raw[4+index*4:], tokenID)
	}
	digest, err := canonicalTaskDigestV1(domain, raw)
	if err != nil {
		return [32]byte{}, 0, err
	}
	return digest, rawSize, nil
}
