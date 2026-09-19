package types

import (
	"encoding/binary"
	"fmt"
	"math"
)

const (
	mmrLeafPrefix  = "TRUEOPEN_MMR_LEAF_V1"
	mmrNodePrefix  = "TRUEOPEN_MMR_NODE_V1"
	mmrEmptyPrefix = "TRUEOPEN_MMR_EMPTY_V1"
)

type mmrPeakV1 struct {
	height uint8
	hash   []byte
}

// MMRAccumulatorV1 incrementally commits an ordered, append-only byte stream.
// Its peak shape is derived solely from LeafCount; callers cannot choose or
// reorder peaks.
type MMRAccumulatorV1 struct {
	domainFrame []byte
	peaks       []mmrPeakV1
	leafCount   uint64
}

func NewMMRAccumulatorV1(domain string) (*MMRAccumulatorV1, error) {
	domainFrame, err := treeDomainFrame("mmr", domain)
	if err != nil {
		return nil, err
	}
	return &MMRAccumulatorV1{domainFrame: domainFrame, peaks: make([]mmrPeakV1, 0, 64)}, nil
}

// LeafCount returns the number of leaves already appended.
func (a *MMRAccumulatorV1) LeafCount() uint64 {
	if a == nil {
		return 0
	}
	return a.leafCount
}

// Append adds the next zero-based leaf. Equal-height peaks merge from left to
// right until the peak heights are strictly decreasing again.
func (a *MMRAccumulatorV1) Append(leaf []byte) error {
	if a == nil || len(a.domainFrame) == 0 {
		return fmt.Errorf("mmr accumulator is not initialized")
	}
	if uint64(len(leaf)) > MaxCanonicalFieldBytesV1 {
		return fmt.Errorf("mmr leaf exceeds %d bytes", MaxCanonicalFieldBytesV1)
	}
	if a.leafCount == math.MaxUint64 {
		return fmt.Errorf("mmr leaf count overflows uint64")
	}

	current := mmrPeakV1{
		hash: mmrLeafHash(a.domainFrame, a.leafCount, leaf),
	}
	for len(a.peaks) != 0 && a.peaks[len(a.peaks)-1].height == current.height {
		left := a.peaks[len(a.peaks)-1]
		a.peaks = a.peaks[:len(a.peaks)-1]
		current.hash = mmrNodeHash(a.domainFrame, left.hash, current.hash)
		current.height++
	}
	a.peaks = append(a.peaks, current)
	a.leafCount++
	return nil
}

// Root folds peaks from right to left. The empty root is domain-separated and
// one leaf returns that leaf hash directly, without an extra node layer.
func (a *MMRAccumulatorV1) Root() ([]byte, error) {
	if a == nil || len(a.domainFrame) == 0 {
		return nil, fmt.Errorf("mmr accumulator is not initialized")
	}
	if len(a.peaks) == 0 {
		return mmrEmptyHash(a.domainFrame), nil
	}
	root := append([]byte(nil), a.peaks[len(a.peaks)-1].hash...)
	for index := len(a.peaks) - 2; index >= 0; index-- {
		root = mmrNodeHash(a.domainFrame, a.peaks[index].hash, root)
	}
	return root, nil
}

// MMRRootV1 computes the frozen MMR_ROOT_V1 primitive without sorting,
// deduplicating, padding or copying the final leaf.
func MMRRootV1(domain string, leaves [][]byte) ([]byte, error) {
	accumulator, err := NewMMRAccumulatorV1(domain)
	if err != nil {
		return nil, err
	}
	for _, leaf := range leaves {
		if err := accumulator.Append(leaf); err != nil {
			return nil, err
		}
	}
	return accumulator.Root()
}

// MMRLeafV1 hashes one variable-length leaf at its zero-based index.
func MMRLeafV1(domain string, index uint64, leaf []byte) ([]byte, error) {
	domainFrame, err := treeDomainFrame("mmr", domain)
	if err != nil {
		return nil, err
	}
	if uint64(len(leaf)) > MaxCanonicalFieldBytesV1 {
		return nil, fmt.Errorf("mmr leaf exceeds %d bytes", MaxCanonicalFieldBytesV1)
	}
	return mmrLeafHash(domainFrame, index, leaf), nil
}

// MMRNodeV1 hashes two ordered Hash32 children.
func MMRNodeV1(domain string, left, right []byte) ([]byte, error) {
	domainFrame, err := treeDomainFrame("mmr", domain)
	if err != nil {
		return nil, err
	}
	if len(left) != merkleHashBytes || len(right) != merkleHashBytes {
		return nil, fmt.Errorf("mmr node children must both be %d bytes", merkleHashBytes)
	}
	return mmrNodeHash(domainFrame, left, right), nil
}

// MMREmptyV1 returns the domain-separated root of an empty MMR.
func MMREmptyV1(domain string) ([]byte, error) {
	domainFrame, err := treeDomainFrame("mmr", domain)
	if err != nil {
		return nil, err
	}
	return mmrEmptyHash(domainFrame), nil
}

func mmrLeafHash(domainFrame []byte, index uint64, leaf []byte) []byte {
	var indexBytes, lengthBytes [8]byte
	binary.BigEndian.PutUint64(indexBytes[:], index)
	binary.BigEndian.PutUint64(lengthBytes[:], uint64(len(leaf)))
	return treeHash([]byte(mmrLeafPrefix), domainFrame, indexBytes[:], lengthBytes[:], leaf)
}

func mmrNodeHash(domainFrame, left, right []byte) []byte {
	return treeHash([]byte(mmrNodePrefix), domainFrame, left, right)
}

func mmrEmptyHash(domainFrame []byte) []byte {
	return treeHash([]byte(mmrEmptyPrefix), domainFrame)
}
