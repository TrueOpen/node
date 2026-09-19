package types

import (
	"bytes"
	"fmt"
	"math"
	"unicode/utf8"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	ParameterBucketSchemaVersionV1 = uint32(1)
	MaxParameterBucketKeyBytes     = 256
	DefaultParameterBucketKey      = "default"
	DefaultParameterBucketTimeout  = uint64(100_000)
)

func ValidateParameterBucketKey(bucketKey string) error {
	if bucketKey == "" || len(bucketKey) > MaxParameterBucketKeyBytes || !utf8.ValidString(bucketKey) {
		return fmt.Errorf("bucket_key must be 1..%d bytes", MaxParameterBucketKeyBytes)
	}
	for i := range bucketKey {
		if bucketKey[i] < 0x21 || bucketKey[i] > 0x7e {
			return fmt.Errorf("bucket_key must be canonical printable ASCII without whitespace")
		}
	}
	return nil
}

func IsParameterBucketKind(kind shared.BucketKind) bool {
	return kind == shared.BucketKind_BUCKET_KIND_TIMEOUT
}

func CanonicalTimeoutBucketEntries(entries []TimeoutBucketEntryV1) ([][]byte, uint32, error) {
	frames, encoded, err := canonicalTimeoutBucketEntryFrames(entries)
	if err != nil {
		return nil, 0, err
	}
	bytes, err := canonicalFrameBytesV1(frames)
	return bytes, encoded, err
}

func canonicalTimeoutBucketEntryFrames(entries []TimeoutBucketEntryV1) ([]shared.CanonicalFrameV1, uint32, error) {
	frames := make([]shared.CanonicalFrameV1, len(entries))
	var previous uint64
	for i := range entries {
		if i > 0 && entries[i].UpperWorkUnitsInclusive <= previous {
			return nil, 0, fmt.Errorf("timeout bucket upper bounds must be strictly increasing")
		}
		if entries[i].TimeoutBlocks == 0 {
			return nil, 0, fmt.Errorf("timeout bucket entry %d timeout_blocks must be non-zero", i)
		}
		frames[i] = shared.FlatCanonicalFrameV1(
			shared.Uint64BE(entries[i].UpperWorkUnitsInclusive),
			shared.Uint64BE(entries[i].TimeoutBlocks),
		)
		previous = entries[i].UpperWorkUnitsInclusive
	}
	bytes, err := canonicalFrameBytesV1(frames)
	if err != nil {
		return nil, 0, err
	}
	encoded, err := canonicalRepeatedSize(bytes)
	return frames, encoded, err
}

func canonicalFrameBytesV1(frames []shared.CanonicalFrameV1) ([][]byte, error) {
	encoded := make([][]byte, len(frames))
	for index, frame := range frames {
		value, err := frame.Bytes()
		if err != nil {
			return nil, fmt.Errorf("canonical frame %d: %w", index, err)
		}
		encoded[index] = value
	}
	return encoded, nil
}

func canonicalRepeatedSize(frames [][]byte) (uint32, error) {
	if len(frames) > shared.MaxCanonicalRepeatedElementsV1 {
		return 0, fmt.Errorf("canonical bucket entries exceed %d elements", shared.MaxCanonicalRepeatedElementsV1)
	}
	size := uint64(12) // length-prefixed repeated count
	for _, frame := range frames {
		if math.MaxUint64-size < uint64(8+len(frame)) {
			return 0, fmt.Errorf("canonical bucket entries size overflows uint64")
		}
		size += uint64(8 + len(frame))
	}
	if size > math.MaxUint32 {
		return 0, fmt.Errorf("canonical bucket entries exceed uint32 encoded size")
	}
	return uint32(size), nil
}

func ParameterBucketContentHash(chainID string, state ParameterBucketVersionState) ([]byte, error) {
	if !utf8.ValidString(chainID) {
		return nil, fmt.Errorf("chain_id must be valid UTF-8")
	}
	if err := ValidateParameterBucketKey(state.BucketKey); err != nil {
		return nil, err
	}
	if !IsParameterBucketKind(state.BucketKind) || state.Version == 0 || state.SchemaVersion != ParameterBucketSchemaVersionV1 {
		return nil, fmt.Errorf("parameter bucket kind, version, and schema_version are required")
	}
	frames, _, err := canonicalTimeoutBucketEntryFrames(state.TimeoutEntries.Entries)
	if err != nil {
		return nil, err
	}
	repeatedEntries := shared.CanonicalRepeatedFramesV1(frames)
	if err := repeatedEntries.Err(); err != nil {
		return nil, err
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainParameterBucketV1)).Raw(
		[]byte(chainID), shared.EnumBE(uint32(state.BucketKind)), []byte(state.BucketKey),
		shared.Uint64BE(state.Version), shared.Uint64BE(state.EffectiveHeight), shared.Uint32BE(uint32(len(frames))),
	).Nested(repeatedEntries).Sum()
}

func ValidateParameterBucketVersionStructure(state ParameterBucketVersionState, params ParameterBucketParamsV1) error {
	if !IsParameterBucketKind(state.BucketKind) {
		return fmt.Errorf("parameter bucket kind is invalid")
	}
	if err := ValidateParameterBucketKey(state.BucketKey); err != nil {
		return err
	}
	if state.Version == 0 || state.SchemaVersion != ParameterBucketSchemaVersionV1 || state.EffectiveHeight == 0 || state.CreatedHeight == 0 {
		return fmt.Errorf("parameter bucket version metadata is invalid")
	}
	entries := state.TimeoutEntries.Entries
	_, encoded, err := CanonicalTimeoutBucketEntries(entries)
	if err != nil {
		return err
	}
	if len(entries) == 0 || uint64(len(entries)) > uint64(params.MaxParameterBucketEntries) ||
		entries[len(entries)-1].UpperWorkUnitsInclusive != math.MaxUint64 {
		return fmt.Errorf("timeout bucket entries do not cover the configured bounded domain")
	}
	if state.EntryCount != uint32(len(entries)) || state.EncodedSizeBytes != encoded {
		return fmt.Errorf("parameter bucket entry_count or encoded_size_bytes mismatch")
	}
	if uint64(encoded) > params.MaxParameterBucketUpdateBytes {
		return fmt.Errorf("parameter bucket canonical entries exceed the configured byte cap")
	}
	if len(state.ContentHash) != 0 && len(state.ContentHash) != shared.Hash32KeySize {
		return fmt.Errorf("parameter bucket content_hash must be Hash32")
	}
	return nil
}

func ValidateParameterBucketVersion(state ParameterBucketVersionState, params ParameterBucketParamsV1, chainID string) error {
	if err := ValidateParameterBucketVersionStructure(state, params); err != nil {
		return err
	}
	hash, err := ParameterBucketContentHash(chainID, state)
	if err != nil {
		return err
	}
	if !bytes.Equal(hash, state.ContentHash) {
		return fmt.Errorf("parameter bucket content_hash mismatch")
	}
	return nil
}

func DefaultParameterBucketGenesis() ([]ParameterBucketVersionState, []ParameterBucketCurrentPointerState) {
	entries := []TimeoutBucketEntryV1{{
		UpperWorkUnitsInclusive: math.MaxUint64,
		TimeoutBlocks:           DefaultParameterBucketTimeout,
	}}
	_, encodedSize, _ := CanonicalTimeoutBucketEntries(entries)
	version := ParameterBucketVersionState{
		BucketKind: shared.BucketKind_BUCKET_KIND_TIMEOUT,
		BucketKey:  DefaultParameterBucketKey, Version: 1, SchemaVersion: ParameterBucketSchemaVersionV1,
		EffectiveHeight: 1, TimeoutEntries: TimeoutBucketEntriesV1{Entries: entries},
		EntryCount: 1, EncodedSizeBytes: encodedSize, CreatedHeight: 1,
	}
	pointer := ParameterBucketCurrentPointerState{
		BucketKind: shared.BucketKind_BUCKET_KIND_TIMEOUT,
		BucketKey:  DefaultParameterBucketKey, CurrentVersion: 1,
	}
	return []ParameterBucketVersionState{version}, []ParameterBucketCurrentPointerState{pointer}
}
