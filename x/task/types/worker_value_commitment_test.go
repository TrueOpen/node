package types

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// This file binds the published TRUEOPEN_WORKER_VALUE_COMMITMENT_V3 vector.
//
// It used to publish the producer's inputs and its two outputs as a flat JSON object.
// That pinned the digest against one input, which is real coverage, but it left the
// domain invisible to the repository-wide differential: the file never spelled the
// domain literal and never published a preimage, so nothing checked the fourteen
// framed fields against DomainRegistryV1's recorded order. Seven of those fields are
// bare Hash32 and three are Uint64BE, and a swap inside either group is exactly the
// change a fixture that only re-derives its own outputs cannot see.
//
// The vector is still the same one frozen preimage, not a second copy of it: the
// binding below feeds the vector's own fields to WorkerValueCommitment and demands the
// pinned digest back, and the digest is byte for byte the one the flat file carried.
const workerValueCommitmentFixturePath = "testdata/worker_value_commitment_v3.json"

const workerValueCommitmentFixtureSchema = "trueopen.task.worker_value_commitment.v3"

const workerValueCommitmentRegenEnv = "TRUEOPEN_REGEN_FIXTURES"

type workerValueCommitmentFixtureV1 struct {
	Schema  string                          `json:"schema"`
	Source  string                          `json:"source"`
	Notes   []string                        `json:"notes"`
	Vectors []workerValueCommitmentVectorV1 `json:"vectors"`
}

type workerValueCommitmentVectorV1 struct {
	Name              string                         `json:"name"`
	Domain            string                         `json:"domain"`
	Framing           string                         `json:"framing"`
	ContractSection   string                         `json:"contract_section"`
	Producer          string                         `json:"producer"`
	Fields            []workerValueCommitmentFieldV1 `json:"fields"`
	PreimageSizeBytes uint64                         `json:"preimage_size_bytes"`
	PreimageHex       string                         `json:"preimage_hex"`
	DigestHex         string                         `json:"digest_hex"`
	// ExpectedEncodedSizeBytes is the producer's second return value.
	ExpectedEncodedSizeBytes uint64 `json:"expected_encoded_size_bytes"`
}

type workerValueCommitmentFieldV1 struct {
	Name   string  `json:"name"`
	Type   string  `json:"type"`
	Hex    string  `json:"hex,omitempty"`
	UTF8   *string `json:"utf8,omitempty"`
	Value  *uint64 `json:"value,omitempty"`
	Enum   string  `json:"enum,omitempty"`
	Bech32 string  `json:"bech32,omitempty"`
}

func (f workerValueCommitmentFieldV1) encode(t *testing.T) []byte {
	t.Helper()
	switch f.Type {
	case "bytes":
		return workerValueCommitmentHex(t, f.Name, f.Hex)
	case "address":
		raw := workerValueCommitmentHex(t, f.Name, f.Hex)
		require.Equal(t, hex.EncodeToString(raw), hex.EncodeToString(f.addressBytes(t)),
			"address field %q: the Bech32 text must decode to the recorded address codec bytes", f.Name)
		return raw
	case "string":
		require.NotNil(t, f.UTF8, "field %q requires utf8", f.Name)
		return []byte(*f.UTF8)
	case "uint32":
		require.NotNil(t, f.Value, "field %q requires value", f.Name)
		require.LessOrEqual(t, *f.Value, uint64(^uint32(0)), "field %q overflows uint32", f.Name)
		return shared.Uint32BE(uint32(*f.Value))
	case "uint64":
		require.NotNil(t, f.Value, "field %q requires value", f.Name)
		return shared.Uint64BE(*f.Value)
	case "enum":
		require.NotNil(t, f.Value, "field %q requires value", f.Name)
		require.NotEmpty(t, f.Enum, "enum field %q must record the symbolic name", f.Name)
		require.LessOrEqual(t, *f.Value, uint64(^uint32(0)), "field %q overflows the enum encoding", f.Name)
		return shared.EnumBE(uint32(*f.Value))
	default:
		t.Fatalf("unknown fixture field type %q on field %q", f.Type, f.Name)
		return nil
	}
}

// addressBytes decodes and re-encodes the Bech32 column so a malformed or
// non-canonical address text fails here rather than silently framing whatever the hex
// column happens to hold.
func (f workerValueCommitmentFieldV1) addressBytes(t *testing.T) []byte {
	t.Helper()
	require.NotEmpty(t, f.Bech32, "address field %q must record the Bech32 text", f.Name)
	hrp, decoded, err := bech32.DecodeAndConvert(f.Bech32)
	require.NoError(t, err, "address field %q must be a decodable Bech32 address", f.Name)
	reencoded, err := bech32.ConvertAndEncode(hrp, decoded)
	require.NoError(t, err)
	require.Equal(t, f.Bech32, reencoded, "address field %q must be canonical Bech32", f.Name)
	return decoded
}

func (v workerValueCommitmentVectorV1) encodeFields(t *testing.T) [][]byte {
	t.Helper()
	encoded := make([][]byte, 0, len(v.Fields))
	for _, field := range v.Fields {
		encoded = append(encoded, field.encode(t))
	}
	return encoded
}

func (v workerValueCommitmentVectorV1) preimage(t *testing.T, fields [][]byte) []byte {
	t.Helper()
	return shared.CanonicalFrameBytes(append([][]byte{[]byte(v.Domain)}, fields...)...)
}

func (v workerValueCommitmentVectorV1) digest(t *testing.T, fields [][]byte) []byte {
	t.Helper()
	require.Equal(t, shared.FramingHFieldsV1.String(), v.Framing)
	return shared.CanonicalHashBytes(v.Domain, fields...)
}

// TestWorkerValueCommitmentGoldenVector pins the ordered preimage and the digest and
// checks the registry row that owns the domain. This is the language-independent half.
func TestWorkerValueCommitmentGoldenVector(t *testing.T) {
	vector := workerValueCommitmentVector(t)

	spec, registered := shared.DomainSpecFor(vector.Domain)
	require.True(t, registered, "%s must be present in DomainRegistryV1", vector.Domain)
	require.Equal(t, vector.Framing, spec.Framing.String())
	require.Equal(t, vector.Domain, shared.MustDomain(vector.Domain))
	require.NotEmpty(t, vector.ContractSection,
		"the vector must cite its contract authority")
	require.Len(t, vector.Fields, len(spec.Fields),
		"the vector must frame exactly the fields the registry row records")
	for index, field := range vector.Fields {
		require.Equal(t, spec.Fields[index], field.Name,
			"field %d must carry the registry row's own name", index)
	}

	fields := vector.encodeFields(t)
	preimage := vector.preimage(t, fields)
	require.Equal(t, vector.PreimageSizeBytes, uint64(len(preimage)))
	require.Equal(t, vector.PreimageHex, hex.EncodeToString(preimage))
	require.Equal(t, vector.DigestHex, hex.EncodeToString(vector.digest(t, fields)))
	require.Len(t, vector.DigestHex, 64)
}

// TestWorkerValueCommitmentRejectsEveryFieldBitFlip is the per-field tamper gate:
// flipping any single bit of any framed field has to move the digest, which is also
// what proves every field the vector declares actually reaches the hash.
func TestWorkerValueCommitmentRejectsEveryFieldBitFlip(t *testing.T) {
	vector := workerValueCommitmentVector(t)
	base := vector.encodeFields(t)
	baseDigest := hex.EncodeToString(vector.digest(t, base))
	require.Equal(t, vector.DigestHex, baseDigest)

	flipped := 0
	for index, field := range vector.Fields {
		require.NotEmpty(t, base[index],
			"field %d (%s) frames as zero bytes and cannot be tamper-checked", index, field.Name)
		for byteIndex := range base[index] {
			for bit := 0; bit < 8; bit++ {
				mutated := make([][]byte, len(base))
				for i := range base {
					mutated[i] = bytes.Clone(base[i])
				}
				mutated[index][byteIndex] ^= 1 << uint(bit)
				require.NotEqual(t, baseDigest, hex.EncodeToString(vector.digest(t, mutated)),
					"field %d (%s) byte %d bit %d did not change the digest", index, field.Name, byteIndex, bit)
				flipped++
			}
		}
	}
	require.Positive(t, flipped)
}

// TestWorkerValueCommitmentGolden is the value binding: the vector's own typed inputs
// go into the real producer and the producer's own output is compared with the pinned
// digest. Nothing here re-assembles a preimage, so a producer that reordered two of its
// seven Hash32 arguments fails even though the encoder sequence is unchanged.
func TestWorkerValueCommitmentGolden(t *testing.T) {
	vector := workerValueCommitmentVector(t)
	value := workerValueCommitmentFromVector(t, vector)

	hash, encodedSize, err := WorkerValueCommitment(value)
	require.NoError(t, err)
	require.Equal(t, vector.ExpectedEncodedSizeBytes, encodedSize)
	require.Equal(t, vector.DigestHex, hex.EncodeToString(hash[:]))

	mutations := []func(*WorkerValueCommitmentV3){
		func(v *WorkerValueCommitmentV3) { v.ChainId += "-changed" },
		func(v *WorkerValueCommitmentV3) { v.TaskId[0]++ },
		func(v *WorkerValueCommitmentV3) { v.AcceptedTaskHash[0]++ },
		func(v *WorkerValueCommitmentV3) { v.EvidenceSchemaHash[0]++ },
		func(v *WorkerValueCommitmentV3) { v.WorkerValueRoot[0]++ },
		func(v *WorkerValueCommitmentV3) { v.WorkerValuesEncodedSizeBytes++ },
	}
	for index, mutate := range mutations {
		changed := workerValueCommitmentFromVector(t, vector)
		mutate(&changed)
		changedHash, _, err := WorkerValueCommitment(changed)
		require.NoError(t, err, "mutation %d", index)
		require.NotEqual(t, hash, changedHash, "mutation %d", index)
	}
}

func TestWorkerValueCommitmentRejectsInvalidSchemaAndSizes(t *testing.T) {
	vector := workerValueCommitmentVector(t)

	value := workerValueCommitmentFromVector(t, vector)
	value.SchemaVersion = 2
	_, _, err := WorkerValueCommitment(value)
	require.ErrorContains(t, err, "schema_version")

	value = workerValueCommitmentFromVector(t, vector)
	value.WorkerValuesEncodedSizeBytes = math.MaxUint64
	_, _, err = WorkerValueCommitment(value)
	require.ErrorContains(t, err, "worker_values_encoded_size_bytes")

	value = workerValueCommitmentFromVector(t, vector)
	value.WorkerValuesEncodedSizeBytes = 0
	_, _, err = WorkerValueCommitment(value)
	require.ErrorContains(t, err, "worker_values_encoded_size_bytes")

	value = workerValueCommitmentFromVector(t, vector)
	value.WorkerValueRoot = value.WorkerValueRoot[:31]
	_, _, err = WorkerValueCommitment(value)
	require.ErrorContains(t, err, "worker_value_root")
}

// ---- helpers ----

// workerValueCommitmentFromVector reads the wire message positionally, by index and by
// name. Reading it positionally is the point: a helper that matched fields by name only
// would happily rebuild the same message from a reordered vector.
func workerValueCommitmentFromVector(t *testing.T, vector workerValueCommitmentVectorV1) WorkerValueCommitmentV3 {
	t.Helper()
	return WorkerValueCommitmentV3{
		SchemaVersion:                uint32(workerValueCommitmentUint(t, vector, 0, "schema_version")),
		ChainId:                      workerValueCommitmentString(t, vector, 1, "chain_id"),
		TaskId:                       workerValueCommitmentBytes(t, vector, 2, "task_id"),
		AcceptedTaskHash:             workerValueCommitmentBytes(t, vector, 3, "accepted_task_hash"),
		WorkerOperatorAddress:        workerValueCommitmentAddress(t, vector, 4, "worker_operator_address"),
		EvidenceSchemaHash:           workerValueCommitmentBytes(t, vector, 5, "evidence_schema_hash"),
		WorkerValueRoot:              workerValueCommitmentBytes(t, vector, 6, "worker_value_root"),
		WorkerValuesEncodedSizeBytes: workerValueCommitmentUint(t, vector, 7, "worker_values_encoded_size_bytes"),
	}
}

func workerValueCommitmentField(t *testing.T, vector workerValueCommitmentVectorV1, index int, name string) workerValueCommitmentFieldV1 {
	t.Helper()
	require.Less(t, index, len(vector.Fields), "the vector has no field %d", index)
	field := vector.Fields[index]
	require.Equal(t, name, field.Name, "field %d must stay %q", index, name)
	return field
}

func workerValueCommitmentString(t *testing.T, vector workerValueCommitmentVectorV1, index int, name string) string {
	t.Helper()
	field := workerValueCommitmentField(t, vector, index, name)
	require.Equal(t, "string", field.Type)
	require.NotNil(t, field.UTF8)
	return *field.UTF8
}

func workerValueCommitmentBytes(t *testing.T, vector workerValueCommitmentVectorV1, index int, name string) []byte {
	t.Helper()
	field := workerValueCommitmentField(t, vector, index, name)
	require.Equal(t, "bytes", field.Type)
	raw := workerValueCommitmentHex(t, name, field.Hex)
	require.Len(t, raw, Hash32Len, name)
	return raw
}

// workerValueCommitmentAddress returns the Bech32 text, because the wire carries the
// text and WorkerValueCommitment decodes it itself. Passing the text keeps the
// production codec inside the binding rather than trusting the fixture's hex column.
func workerValueCommitmentAddress(t *testing.T, vector workerValueCommitmentVectorV1, index int, name string) string {
	t.Helper()
	field := workerValueCommitmentField(t, vector, index, name)
	require.Equal(t, "address", field.Type)
	require.NotEmpty(t, field.Bech32)
	return field.Bech32
}

func workerValueCommitmentUint(t *testing.T, vector workerValueCommitmentVectorV1, index int, name string) uint64 {
	t.Helper()
	field := workerValueCommitmentField(t, vector, index, name)
	require.Contains(t, []string{"uint32", "uint64", "enum"}, field.Type)
	require.NotNil(t, field.Value)
	return *field.Value
}

func workerValueCommitmentHex(t *testing.T, name, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	require.NoError(t, err, "field %q must be hex", name)
	require.Equal(t, value, hex.EncodeToString(raw), "field %q must be canonical lowercase hex", name)
	return raw
}

func workerValueCommitmentVector(t *testing.T) workerValueCommitmentVectorV1 {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(workerValueCommitmentFixturePath))
	require.NoError(t, err)
	var fixture workerValueCommitmentFixtureV1
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&fixture))
	require.Equal(t, workerValueCommitmentFixtureSchema, fixture.Schema)
	require.NotEmpty(t, fixture.Source)
	require.NotEmpty(t, fixture.Notes)
	require.Len(t, fixture.Vectors, 1, "the domain has exactly one published vector")
	if os.Getenv(workerValueCommitmentRegenEnv) == "1" {
		regenerateWorkerValueCommitmentFixture(t, fixture)
	}
	return fixture.Vectors[0]
}

func regenerateWorkerValueCommitmentFixture(t *testing.T, fixture workerValueCommitmentFixtureV1) {
	t.Helper()
	for i := range fixture.Vectors {
		vector := &fixture.Vectors[i]
		fields := vector.encodeFields(t)
		vector.PreimageHex = hex.EncodeToString(vector.preimage(t, fields))
		vector.DigestHex = hex.EncodeToString(vector.digest(t, fields))
	}
	encoded, err := json.MarshalIndent(fixture, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.FromSlash(workerValueCommitmentFixturePath), append(encoded, '\n'), 0o644))
	t.Fatalf(
		"regenerated %s; unset %s and review the diff against the frozen contract before committing",
		workerValueCommitmentFixturePath, workerValueCommitmentRegenEnv,
	)
}
