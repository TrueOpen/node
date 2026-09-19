package types

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

const (
	merkleLeafPrefix  = "TRUEOPEN_MERKLE_LEAF_V1"
	merkleNodePrefix  = "TRUEOPEN_MERKLE_NODE_V1"
	merkleEmptyPrefix = "TRUEOPEN_MERKLE_EMPTY_V1"
	merkleHashBytes   = sha256.Size
)

// MerkleRootV1 computes the repository-wide binary Merkle tree. Leaf order is
// owned by the caller; this helper never sorts, deduplicates, or pads leaves.
func MerkleRootV1(domain string, leaves [][]byte) ([]byte, error) {
	if domain == "" {
		return nil, fmt.Errorf("merkle domain is required")
	}
	domainFrame, err := treeDomainFrame("merkle", domain)
	if err != nil {
		return nil, err
	}
	if len(leaves) == 0 {
		return treeHash([]byte(merkleEmptyPrefix), domainFrame), nil
	}

	if len(leaves) > MaxCanonicalRepeatedElementsV1 {
		return nil, fmt.Errorf("merkle leaf count %d exceeds limit %d", len(leaves), MaxCanonicalRepeatedElementsV1)
	}

	level := make([][]byte, len(leaves))
	for i, leaf := range leaves {
		if len(leaf) != merkleHashBytes {
			return nil, fmt.Errorf("merkle leaf %d must be %d bytes, got %d", i, merkleHashBytes, len(leaf))
		}
		level[i] = treeHash([]byte(merkleLeafPrefix), domainFrame, leaf)
	}
	for len(level) > 1 {
		next := make([][]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 == len(level) {
				next = append(next, level[i])
				continue
			}
			next = append(next, treeHash([]byte(merkleNodePrefix), domainFrame, level[i], level[i+1]))
		}
		level = next
	}
	return level[0], nil
}

// treeDomainFrame builds u32_be(len(domain)) || domain for the two canonical
// tree framings.
//
// The UTF-8 check mirrors PayloadFrameV1: the two framings prefix a domain the
// same way, so accepting a byte string here that H_V1 would reject would leave
// two different notions of "a domain" in one repository. It is a rejection-only
// branch -- the one production caller passes a DomainRegistryV1 ASCII literal --
// and no reachable input changes shape because of it.
func treeDomainFrame(kind, domain string) ([]byte, error) {
	if err := validateCanonicalDomainV1(domain); err != nil {
		return nil, fmt.Errorf("%s %w", kind, err)
	}
	domainBytes := []byte(domain)
	framed := make([]byte, 4+len(domainBytes))
	binary.BigEndian.PutUint32(framed[:4], uint32(len(domainBytes)))
	copy(framed[4:], domainBytes)
	return framed, nil
}

func treeHash(parts ...[]byte) []byte {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write(part)
	}
	return h.Sum(nil)
}
