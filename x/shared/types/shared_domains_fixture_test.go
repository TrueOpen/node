package types_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// This file holds the golden vectors for the consensus domains whose producer
// lives in x/shared/types itself, rather than in one of the two modules that
// call it. TRUEOPEN_BATCH_RESULT_V1 is the first: BatchResultDigestV1 is the single
// implementation shared by the Hub and Task batch handlers, so a vector kept in
// either module's fixture would file a cross-module contract under one module's
// ownership.
//
// The economics fixture next door already covers the five cross-module economics
// domains, and it stays separate: its vectors are schema v2 with positional,
// unnamed fields, which is why the root differential has to skip the name-order
// half for that file (see domainVectorFilesWithPositionalFields). The vectors here
// name every field, so they take part in the differential in full.
//
// What makes a vector here worth having is the binding, not the bytes. A fixture
// that only re-frames its own fields[] and hashes the result proves the framing
// primitive works and nothing about the function that ships: the Track A batch 1
// audit measured that on TRUEOPEN_UNBONDING_ID_V1, where swapping two same-width
// fields inside the producer left the published vector, the source-shape binding
// and the whole suite green. So every vector in this file states its expectation
// as
//
//	got, err := <the real producer>(<the vector's own typed inputs>)
//	require.Equal(t, vector.DigestHex, hex.EncodeToString(got))
//
// and TestSharedDomainFixtureBindsProducers fails outright on a vector that has no
// such case, rather than letting an unbound vector be published.
//
// Regenerating: run the package tests with TRUEOPEN_REGEN_FIXTURES=1. The run
// rewrites preimage_hex and digest_hex from the fixture's own fields and then
// fails on purpose, because a regenerated consensus fixture is a proposed change
// to a frozen preimage and has to be diffed against the contract before it is
// committed.
const sharedDomainFixturePath = "testdata/shared_domains_v1.json"

const sharedDomainFixtureRegenEnv = "TRUEOPEN_REGEN_FIXTURES"

const sharedDomainFixtureSchema = "trueopen.shared.domains.v1"

type sharedDomainFixture struct {
	Schema  string               `json:"schema"`
	Source  string               `json:"source"`
	Notes   []string             `json:"notes"`
	Vectors []sharedDomainVector `json:"vectors"`
}

type sharedDomainVector struct {
	Name            string `json:"name"`
	Domain          string `json:"domain"`
	Framing         string `json:"framing"`
	ContractSection string `json:"contract_section"`
	// Producer names the one production function the vector is executed against.
	// The binding asserts it against a literal, so a vector cannot claim a producer
	// that nothing calls.
	Producer    string              `json:"producer"`
	Fields      []sharedDomainField `json:"fields"`
	PreimageHex string              `json:"preimage_hex"`
	DigestHex   string              `json:"digest_hex"`
}

type sharedDomainField struct {
	Name   string              `json:"name"`
	Type   string              `json:"type"`
	Hex    string              `json:"hex,omitempty"`
	UTF8   *string             `json:"utf8,omitempty"`
	Value  *uint64             `json:"value,omitempty"`
	Fields []sharedDomainField `json:"fields,omitempty"`
}

// encode applies the §1.2 typed encoders. An unknown type is fatal rather than
// framed as empty: a type this reader does not understand would otherwise weaken
// every vector that used it without failing anything.
func (f sharedDomainField) encode(t *testing.T) []byte {
	t.Helper()
	switch f.Type {
	case "bytes":
		return sharedDomainHex(t, f.Name, f.Hex)
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
		require.LessOrEqual(t, *f.Value, uint64(^uint32(0)), "field %q overflows the enum encoding", f.Name)
		return shared.EnumBE(uint32(*f.Value))
	case "frame":
		require.NotEmpty(t, f.Fields, "field %q requires nested fields", f.Name)
		inner := make([][]byte, 0, len(f.Fields))
		for _, nested := range f.Fields {
			inner = append(inner, nested.encode(t))
		}
		return shared.CanonicalFrameBytes(inner...)
	default:
		t.Fatalf("unknown fixture field type %q on field %q", f.Type, f.Name)
		return nil
	}
}

func (v sharedDomainVector) encodeFields(t *testing.T) [][]byte {
	t.Helper()
	encoded := make([][]byte, 0, len(v.Fields))
	for _, field := range v.Fields {
		encoded = append(encoded, field.encode(t))
	}
	return encoded
}

func (v sharedDomainVector) preimage(t *testing.T, fields [][]byte) []byte {
	t.Helper()
	require.Equal(t, shared.FramingHFieldsV1.String(), v.Framing,
		"the domains in this fixture are all H_FIELDS_V1")
	return shared.CanonicalFrameBytes(append([][]byte{[]byte(v.Domain)}, fields...)...)
}

// TestSharedDomainFixtureGoldenVectors pins the ordered preimage and digest of
// every vector and checks the registry row that owns the domain.
func TestSharedDomainFixtureGoldenVectors(t *testing.T) {
	fixture := loadSharedDomainFixture(t)
	require.NotEmpty(t, fixture.Vectors)

	seenDomains := make(map[string]string, len(fixture.Vectors))
	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			previous, duplicate := seenDomains[vector.Domain]
			require.False(t, duplicate, "%s already covered by %s", vector.Domain, previous)
			seenDomains[vector.Domain] = vector.Name

			spec, registered := shared.DomainSpecFor(vector.Domain)
			require.True(t, registered, "%s must be present in DomainRegistryV1", vector.Domain)
			require.Equal(t, vector.Framing, spec.Framing.String())
			require.Equal(t, vector.Domain, shared.MustDomain(vector.Domain))
			require.Equal(t, spec.ContractSection, vector.ContractSection,
				"the vector must cite the registry row's own contract section")
			require.NotEmpty(t, vector.Producer)
			require.NotEmpty(t, vector.Fields)

			fields := vector.encodeFields(t)
			preimage := vector.preimage(t, fields)
			digest := sha256.Sum256(preimage)
			require.Equal(t, vector.PreimageHex, hex.EncodeToString(preimage))
			require.Equal(t, vector.DigestHex, hex.EncodeToString(digest[:]))
			require.Len(t, vector.DigestHex, 64)
		})
	}
}

// TestSharedDomainFixtureRejectsEveryFieldBitFlip is the derived per-field
// sensitivity gate required: flipping a single bit of any
// framed field must move the digest. It is derived rather than published, so the
// fixture carries no tamper section for another implementation to disagree with.
func TestSharedDomainFixtureRejectsEveryFieldBitFlip(t *testing.T) {
	fixture := loadSharedDomainFixture(t)
	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			base := vector.encodeFields(t)
			baseDigest := sha256.Sum256(vector.preimage(t, base))
			require.Equal(t, vector.DigestHex, hex.EncodeToString(baseDigest[:]))

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
						changed := sha256.Sum256(vector.preimage(t, mutated))
						require.NotEqual(t, vector.DigestHex, hex.EncodeToString(changed[:]),
							"%s field %d (%s) byte %d bit %d did not change the digest",
							vector.Name, index, field.Name, byteIndex, bit)
						flipped++
					}
				}
			}
			require.Positive(t, flipped)
		})
	}
}

// TestSharedDomainFixtureBindsProducers is the reason the fixture exists: each
// vector's typed inputs go into the real producer and the producer's own output is
// compared with the pinned digest. Nothing here re-assembles a preimage, so a
// producer that reordered two same-width fields fails - which a fixture checked
// only against its own fields[] would not notice.
func TestSharedDomainFixtureBindsProducers(t *testing.T) {
	fixture := loadSharedDomainFixture(t)
	bound := make(map[string]struct{}, len(fixture.Vectors))

	for _, vector := range fixture.Vectors {
		switch vector.Name {
		case "batch_result_v1_model_support":
			t.Run(vector.Name, func(t *testing.T) {
				require.Equal(t, shared.DomainBatchResultV1, vector.Domain)
				require.Equal(t, "BatchResultDigestV1", vector.Producer)
				elements := sharedRepeatedFields(t, vector, 3, 2, "item_count")
				items := make([]shared.BatchResultDigestItemV1, 0, len(elements))
				for index, element := range elements {
					require.Equal(t, "frame", element.Type)
					require.Len(t, element.Fields, 2)
					items = append(items, shared.BatchResultDigestItemV1{
						ObjectID: sharedDomainNestedBytes(t, element, 0, fmt.Sprintf("item_%d_object_id", index)),
						Status:   uint32(sharedDomainNestedUint(t, element, 1, fmt.Sprintf("item_%d_status", index))),
					})
				}
				// item_index is positional and deliberately not hashed a second time, so
				// the two items differ in both of the fields that ARE hashed: a producer
				// that swapped object_id and status inside the loop, or that emitted the
				// pairs in the wrong order, lands on a different digest.
				require.NotEqual(t, items[0].Status, items[1].Status)

				got, err := shared.BatchResultDigestV1(
					sharedDomainString(t, vector, 0, "chain_id"),
					sharedDomainString(t, vector, 1, "batch_kind"),
					items,
				)
				require.NoError(t, err)
				require.Equal(t, vector.DigestHex, hex.EncodeToString(got))

				// The kind is a closed V1 string set framed as UTF-8, so a kind outside
				// it must be refused rather than hashed into a digest no consumer can
				// interpret.
				require.True(t, shared.IsBatchResultKind(sharedDomainString(t, vector, 1, "batch_kind")))
				_, err = shared.BatchResultDigestV1(
					sharedDomainString(t, vector, 0, "chain_id"), "MODEL_SUPPORTED", items)
				require.Error(t, err)
			})
			bound[vector.Name] = struct{}{}
		default:
			t.Fatalf("fixture vector %q has no executable producer binding; add one rather than publishing an unbound vector", vector.Name)
		}
	}
	require.Len(t, bound, len(fixture.Vectors))
}

// TestSharedDomainFixtureCoversItsDomains freezes the reach of the fixture in both
// directions: a domain that loses its vector fails here instead of quietly
// dropping back to having no executable field-order coverage at all.
func TestSharedDomainFixtureCoversItsDomains(t *testing.T) {
	fixture := loadSharedDomainFixture(t)
	covered := make(map[string]struct{}, len(fixture.Vectors))
	for _, vector := range fixture.Vectors {
		covered[vector.Domain] = struct{}{}
	}
	want := []string{shared.DomainBatchResultV1}
	for _, domain := range want {
		_, ok := covered[domain]
		require.True(t, ok, "%s has no golden vector in %s", domain, sharedDomainFixturePath)
	}
	require.Len(t, covered, len(want),
		"an unexpected domain appeared in %s; add it to the frozen list", sharedDomainFixturePath)
}

// ---- helpers ----

func loadSharedDomainFixture(t *testing.T) sharedDomainFixture {
	t.Helper()
	raw, err := os.ReadFile(sharedDomainFixturePath)
	require.NoError(t, err)
	var fixture sharedDomainFixture
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&fixture))
	require.Equal(t, sharedDomainFixtureSchema, fixture.Schema)
	require.NotEmpty(t, fixture.Source)
	require.NotEmpty(t, fixture.Notes)
	if os.Getenv(sharedDomainFixtureRegenEnv) == "1" {
		regenerateSharedDomainFixture(t, fixture)
	}
	return fixture
}

func sharedDomainFieldAt(t *testing.T, vector sharedDomainVector, index int, name string) sharedDomainField {
	t.Helper()
	require.Less(t, index, len(vector.Fields))
	field := vector.Fields[index]
	require.Equal(t, name, field.Name, "%s field %d must stay %q", vector.Name, index, name)
	return field
}

func sharedDomainString(t *testing.T, vector sharedDomainVector, index int, name string) string {
	t.Helper()
	field := sharedDomainFieldAt(t, vector, index, name)
	require.Equal(t, "string", field.Type)
	require.NotNil(t, field.UTF8)
	return *field.UTF8
}

func sharedDomainUint(t *testing.T, vector sharedDomainVector, index int, name string) uint64 {
	t.Helper()
	field := sharedDomainFieldAt(t, vector, index, name)
	require.Contains(t, []string{"uint32", "uint64", "enum"}, field.Type)
	require.NotNil(t, field.Value)
	return *field.Value
}

func sharedRepeatedFields(t *testing.T, vector sharedDomainVector, head, countIndex int, countName string) []sharedDomainField {
	t.Helper()
	require.Len(t, vector.Fields, head+1, "%s repeated value must occupy one outer field", vector.Name)
	repeated := vector.Fields[head]
	require.Equal(t, "frame", repeated.Type)
	require.NotEmpty(t, repeated.Fields)
	innerCount := repeated.Fields[0]
	require.Equal(t, "element_count", innerCount.Name)
	require.Equal(t, "uint32", innerCount.Type)
	require.NotNil(t, innerCount.Value)
	elements := repeated.Fields[1:]
	require.Equal(t, uint64(len(elements)), *innerCount.Value)
	require.Equal(t, uint64(len(elements)), sharedDomainUint(t, vector, countIndex, countName))
	return elements
}

func sharedDomainNestedBytes(t *testing.T, frame sharedDomainField, index int, name string) []byte {
	t.Helper()
	require.Less(t, index, len(frame.Fields))
	field := frame.Fields[index]
	require.Equal(t, name, field.Name)
	require.Equal(t, "bytes", field.Type)
	return sharedDomainHex(t, name, field.Hex)
}

func sharedDomainNestedUint(t *testing.T, frame sharedDomainField, index int, name string) uint64 {
	t.Helper()
	require.Less(t, index, len(frame.Fields))
	field := frame.Fields[index]
	require.Equal(t, name, field.Name)
	require.Contains(t, []string{"uint32", "uint64", "enum"}, field.Type)
	require.NotNil(t, field.Value)
	return *field.Value
}

func sharedDomainHex(t *testing.T, name, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	require.NoError(t, err, "field %q must be hex", name)
	require.Equal(t, value, hex.EncodeToString(raw), "field %q must be canonical lower-case hex", name)
	return raw
}

func regenerateSharedDomainFixture(t *testing.T, fixture sharedDomainFixture) {
	t.Helper()
	for i := range fixture.Vectors {
		vector := &fixture.Vectors[i]
		preimage := vector.preimage(t, vector.encodeFields(t))
		digest := sha256.Sum256(preimage)
		vector.PreimageHex = hex.EncodeToString(preimage)
		vector.DigestHex = hex.EncodeToString(digest[:])
	}
	encoded, err := json.MarshalIndent(fixture, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(sharedDomainFixturePath, append(encoded, '\n'), 0o644))
	t.Fatalf(
		"regenerated %s; unset %s and review the diff against the frozen contract before committing",
		sharedDomainFixturePath, sharedDomainFixtureRegenEnv,
	)
}
