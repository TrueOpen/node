package node

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"

	gogoproto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
	protov2 "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// paramsFixturePath holds the Wire cross-language vectors for Hub and Task
// parameter roots.
//
// The leaf order is not this file's opinion. §1.2 fixes it:
// a nested message frames its fields recursively in proto field-number ascending
// order. So the fixture's nested fields[] are generated from the PROTO DESCRIPTOR,
// while the digest comes from x/task/types/params.go. The two paths are
// independent and an adjacent-field swap test proves the ordering check works.
//
// Regenerating: run with TRUEOPEN_REGEN_FIXTURES=1. The run rewrites the derived
// columns and then fails on purpose, because a regenerated consensus fixture must
// be diffed before it is committed.
const (
	paramsFixturePath   = "testdata/params_v1.json"
	paramsFixtureSchema = "trueopen-params-domains-v1"
	paramsRegenEnv      = "TRUEOPEN_REGEN_FIXTURES"
)

type paramsFixture struct {
	Schema  string         `json:"schema"`
	Source  string         `json:"source"`
	Notes   []string       `json:"notes"`
	Vectors []paramsVector `json:"vectors"`
}

type paramsVector struct {
	Name            string `json:"name"`
	Domain          string `json:"domain"`
	Framing         string `json:"framing"`
	ContractSection string `json:"contract_section"`
	Producer        string `json:"producer"`
	// ProtoMessage is the fully-qualified name of the params message whose
	// descriptor defines the leaf order below. It is what makes the nested fields
	// derivable instead of hand-maintained.
	ProtoMessage  string              `json:"proto_message"`
	ChainID       string              `json:"chain_id"`
	ParamsVersion uint64              `json:"params_version"`
	Fields        []paramsField       `json:"fields"`
	PreimageHex   string              `json:"preimage_hex"`
	DigestHex     string              `json:"digest_hex"`
	Tamper        []paramsTamperCase  `json:"tamper"`
	Replay        []paramsReplayCase  `json:"replay"`
	Leaves        *paramsLeafAccounts `json:"leaf_accounting,omitempty"`
}

// paramsLeafAccounts publishes what the tree contains, so a non-Go implementation
// that walks its own descriptor can check it arrived at the same shape before it
// starts comparing bytes.
type paramsLeafAccounts struct {
	Submessages int `json:"submessages"`
	Repeated    int `json:"repeated"`
	Scalars     int `json:"scalars"`
}

type paramsField struct {
	Name   string        `json:"name"`
	Type   string        `json:"type"`
	UTF8   *string       `json:"utf8,omitempty"`
	Value  *uint64       `json:"value,omitempty"`
	Bool   *bool         `json:"bool,omitempty"`
	Fields []paramsField `json:"fields,omitempty"`
}

type paramsTamperCase struct {
	Name      string `json:"name"`
	Field     int    `json:"field"`
	Byte      int    `json:"byte"`
	Bit       int    `json:"bit"`
	DigestHex string `json:"digest_hex"`
}

type paramsReplayCase struct {
	Name      string `json:"name"`
	Reason    string `json:"reason"`
	ChainID   string `json:"chain_id"`
	Version   uint64 `json:"params_version"`
	DigestHex string `json:"digest_hex"`
}

// encode applies the §1.2 typed encoders. A "frame" is both NESTED_V1 and
// REPEATED_V1: the difference is that a repeated frame's first field is the
// element count, which §4.4 puts inside the frame rather than beside it, so no
// separate fixture type is needed or wanted.
func (f paramsField) encode(t *testing.T, where string) []byte {
	t.Helper()
	switch f.Type {
	case "string":
		require.NotNil(t, f.UTF8, "%s requires utf8", where)
		return []byte(*f.UTF8)
	case "uint32":
		require.NotNil(t, f.Value, "%s requires value", where)
		require.LessOrEqual(t, *f.Value, uint64(^uint32(0)), "%s overflows uint32", where)
		return shared.Uint32BE(uint32(*f.Value))
	case "uint64":
		require.NotNil(t, f.Value, "%s requires value", where)
		return shared.Uint64BE(*f.Value)
	case "bool":
		require.NotNil(t, f.Bool, "%s requires bool", where)
		return shared.BoolByte(*f.Bool)
	case "frame":
		require.NotEmpty(t, f.Fields, "%s requires nested fields", where)
		inner := make([][]byte, 0, len(f.Fields))
		for index, nested := range f.Fields {
			inner = append(inner, nested.encode(t, fmt.Sprintf("%s.%s", where, paramsFieldLabel(nested, index))))
		}
		return shared.CanonicalFrameBytes(inner...)
	default:
		t.Fatalf("%s has unknown fixture field type %q", where, f.Type)
		return nil
	}
}

func paramsFieldLabel(field paramsField, index int) string {
	if field.Name != "" {
		return field.Name
	}
	return strconv.Itoa(index)
}

func (v paramsVector) encodeFields(t *testing.T) [][]byte {
	t.Helper()
	encoded := make([][]byte, 0, len(v.Fields))
	for index, field := range v.Fields {
		encoded = append(encoded, field.encode(t, fmt.Sprintf("%s.%s", v.Name, paramsFieldLabel(field, index))))
	}
	return encoded
}

func (v paramsVector) preimage(fields [][]byte) []byte {
	return shared.CanonicalFrameBytes(append([][]byte{[]byte(v.Domain)}, fields...)...)
}

func (v paramsVector) digestOf(fields [][]byte) string {
	sum := sha256.Sum256(v.preimage(fields))
	return hex.EncodeToString(sum[:])
}

// TestParamsFixtureGoldenVectors pins each vector's ordered preimage and digest
// and checks the registry row that owns it. The three outer positions are the
// whole of DomainRegistryV1[...].Fields: params is one of them, not 116.
func TestParamsFixtureGoldenVectors(t *testing.T) {
	fixture := loadParamsFixture(t)
	require.Len(t, fixture.Vectors, 2, "one vector per params domain")

	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			spec, registered := shared.DomainSpecFor(vector.Domain)
			require.True(t, registered)
			require.Equal(t, vector.Framing, spec.Framing.String())
			require.Equal(t, []string{"chain_id", "params_version", "params"}, spec.Fields)
			require.NotEmpty(t, vector.ContractSection)
			require.NotEmpty(t, vector.Producer)

			require.Len(t, vector.Fields, 3)
			require.Equal(t, "chain_id", vector.Fields[0].Name)
			require.NotNil(t, vector.Fields[0].UTF8)
			require.Equal(t, vector.ChainID, *vector.Fields[0].UTF8)
			require.Equal(t, "params_version", vector.Fields[1].Name)
			require.NotNil(t, vector.Fields[1].Value)
			require.Equal(t, vector.ParamsVersion, *vector.Fields[1].Value)
			require.Equal(t, "params", vector.Fields[2].Name)
			require.Equal(t, "frame", vector.Fields[2].Type,
				"the params body is ONE nested position; a run of raw leaves here would be the §4.4 promotion this domain was fixed for")

			fields := vector.encodeFields(t)
			require.Equal(t, vector.PreimageHex, hex.EncodeToString(vector.preimage(fields)))
			require.Equal(t, vector.DigestHex, vector.digestOf(fields))
			require.Len(t, vector.DigestHex, 64)
		})
	}
	require.NotEqual(t, fixture.Vectors[0].DigestHex, fixture.Vectors[1].DigestHex,
		"the two params domains must not collide")
}

// TestParamsFixtureNestedOrderMatchesProtoFieldNumbers is the independent half of
// the differential for these two domains: it rebuilds the whole nested tree from
// the proto descriptor and requires the fixture to be exactly that, name for name
// and type for type.
//
// This is what replaces "write the 116 leaves out twice". §1.2 says the order is
// proto field-number ascending, the descriptor is that statement, and neither
// side of the comparison is the params encoder under test.
func TestParamsFixtureNestedOrderMatchesProtoFieldNumbers(t *testing.T) {
	fixture := loadParamsFixture(t)
	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			descriptor := paramsMessageDescriptor(t, vector.ProtoMessage)
			message := paramsDynamicFromFields(t, descriptor, vector.Fields[2], vector.Name+".params")
			want := paramsFieldsFromMessage(t, descriptor, message)
			require.Equal(t, want, vector.Fields[2].Fields,
				"%s: the published tree is not the proto field-number ordering of %s", vector.Name, vector.ProtoMessage)

			accounts := paramsCountLeaves(vector.Fields[2])
			require.NotNil(t, vector.Leaves, "%s must publish its leaf accounting", vector.Name)
			require.Equal(t, *vector.Leaves, accounts)
			require.Positive(t, accounts.Repeated, "both params messages carry repeated fields")
			require.Positive(t, accounts.Submessages)
		})
	}
}

// TestParamsFixtureMatchesProductionHelpers is the value binding. The params
// message is rebuilt from the fixture BY FIELD NAME - never by position - so the
// producer sees the values the fixture publishes without inheriting the order the
// fixture publishes them in. That is the whole point: the producer supplies the
// order, the fixture supplies the values, and the pinned digest is where the two
// have to agree.
func TestParamsFixtureMatchesProductionHelpers(t *testing.T) {
	fixture := loadParamsFixture(t)
	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			got := paramsProducerDigest(t, vector, vector.ChainID, vector.ParamsVersion)
			require.Equal(t, vector.DigestHex, got)
		})
	}
}

// TestParamsFixtureCatchesAnAdjacentLeafSwap demonstrates that the two sides are
// really independent.
//
// It swaps the values of two adjacent same-width scalar leaves inside the params
// tree - the mutation an encoder-class binding is blind to, and the one issue
// #141 Track A batch 1 found TRUEOPEN_UNBONDING_ID_V1 exposed to. Re-framing the
// mutated fixture must move the digest, and the producer, fed the same tree by
// name, must land on the mutated digest too. If either side were derived from the
// other, one of the two requirements below would be unsatisfiable.
func TestParamsFixtureCatchesAnAdjacentLeafSwap(t *testing.T) {
	fixture := loadParamsFixture(t)
	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			preferredLeft := ""
			if vector.Domain == shared.DomainTaskParamsV1 {
				// The first adjacent pair is the ordered idle/close TTL invariant and
				// cannot be swapped into another valid TaskParamsV1 value. Use two
				// independent per-block caps so the producer can still validate the
				// mutated message before proving that field order changes its digest.
				preferredLeft = "max_session_terminal_summary_prune_items_per_block"
			}
			mutated, swapped := paramsSwapAdjacentScalars(vector.Fields[2], preferredLeft)
			require.NotEmpty(t, swapped, "%s must contain two adjacent same-type scalar leaves", vector.Name)

			mutatedVector := vector
			mutatedVector.Fields = append([]paramsField(nil), vector.Fields...)
			mutatedVector.Fields[2] = mutated

			reframed := mutatedVector.digestOf(mutatedVector.encodeFields(t))
			require.NotEqual(t, vector.DigestHex, reframed,
				"swapping %s must change the preimage; if it does not, the two leaves are indistinguishable and the vector needs distinct values", swapped)
			require.Equal(t, reframed, paramsProducerDigest(t, mutatedVector, vector.ChainID, vector.ParamsVersion),
				"the producer must agree with the mutated tree; if it does not, the fixture order and the producer order have drifted apart")
		})
	}
}

// TestParamsFixtureRejectsEveryFieldBitFlip is the derived exhaustive gate over
// the three outer positions, which - because the third is the whole params frame
// - covers every byte of every leaf.
func TestParamsFixtureRejectsEveryFieldBitFlip(t *testing.T) {
	fixture := loadParamsFixture(t)
	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			base := vector.encodeFields(t)
			require.Equal(t, vector.DigestHex, vector.digestOf(base))
			flipped := 0
			for index, field := range vector.Fields {
				for byteIndex := range base[index] {
					for bit := 0; bit < 8; bit++ {
						require.NotEqual(t, vector.DigestHex, vector.digestOf(paramsFlipBit(base, index, byteIndex, bit)),
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

// TestParamsFixtureTamperAndReplayVectors checks the published goldens. The
// tamper rows name byte offsets inside the params frame so another implementation
// can localise a nested framing bug instead of seeing one opaque mismatch; the
// replay rows cover the two scope fields, which is the whole of the scope.
func TestParamsFixtureTamperAndReplayVectors(t *testing.T) {
	fixture := loadParamsFixture(t)
	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			base := vector.encodeFields(t)
			require.NotEmpty(t, vector.Tamper)

			covered := make(map[int]struct{}, len(vector.Fields))
			seen := map[string]string{vector.DigestHex: "base"}
			for _, tamper := range vector.Tamper {
				require.NotEmpty(t, tamper.Name)
				require.Less(t, tamper.Field, len(vector.Fields))
				covered[tamper.Field] = struct{}{}
				got := vector.digestOf(paramsFlipBit(base, tamper.Field, tamper.Byte, tamper.Bit))
				require.Equal(t, tamper.DigestHex, got, "tamper %q", tamper.Name)
				previous, collided := seen[got]
				require.False(t, collided, "tamper %q collides with %s", tamper.Name, previous)
				seen[got] = tamper.Name
			}
			for index, field := range vector.Fields {
				_, ok := covered[index]
				require.True(t, ok, "field %d (%s) has no published tamper vector", index, field.Name)
			}

			require.NotEmpty(t, vector.Replay)
			for _, replay := range vector.Replay {
				require.NotEmpty(t, replay.Reason, "replay %q needs a reason", replay.Name)
				require.False(t, replay.ChainID == vector.ChainID && replay.Version == vector.ParamsVersion,
					"replay %q changes nothing", replay.Name)
				got := paramsProducerDigest(t, vector, replay.ChainID, replay.Version)
				require.Equal(t, replay.DigestHex, got, "replay %q", replay.Name)
				previous, collided := seen[got]
				require.False(t, collided, "replay %q collides with %s", replay.Name, previous)
				seen[got] = replay.Name
			}
		})
	}
}

// paramsProducerDigest rebuilds the params message from the vector's tree and
// returns the digest the real producer derives from it.
func paramsProducerDigest(t *testing.T, vector paramsVector, chainID string, version uint64) string {
	t.Helper()
	descriptor := paramsMessageDescriptor(t, vector.ProtoMessage)
	dynamic := paramsDynamicFromFields(t, descriptor, vector.Fields[2], vector.Name+".params")
	encoded, err := protov2.Marshal(dynamic.Interface())
	require.NoError(t, err)

	switch vector.Domain {
	case shared.DomainHubParamsV2:
		var params hubtypes.HubParamsV2
		require.NoError(t, gogoproto.Unmarshal(encoded, &params))
		digest, err := hubtypes.HubParamsHash(chainID, version, params)
		require.NoError(t, err)
		return hex.EncodeToString(digest)
	case shared.DomainTaskParamsV1:
		var params tasktypes.TaskParamsV1
		require.NoError(t, gogoproto.Unmarshal(encoded, &params))
		digest, err := tasktypes.TaskParamsHashV1(chainID, version, params)
		require.NoError(t, err)
		return hex.EncodeToString(digest)
	default:
		t.Fatalf("%s carries unexpected domain %s", vector.Name, vector.Domain)
		return ""
	}
}

func paramsMessageDescriptor(t *testing.T, fullName string) protoreflect.MessageDescriptor {
	t.Helper()
	files, err := gogoproto.MergedRegistry()
	require.NoError(t, err)
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(fullName))
	require.NoError(t, err, "%s must be a registered proto message", fullName)
	message, ok := descriptor.(protoreflect.MessageDescriptor)
	require.True(t, ok, "%s is not a message", fullName)
	return message
}

// paramsFieldsFromMessage renders a message as the fixture's nested field list,
// in proto field-number ascending order.
//
// It reads every declared field rather than only the populated ones. proto3 omits
// defaults from the wire, so a leaf whose value happens to be zero is absent from
// the dynamic message and present in the Go struct the producer hashes; skipping
// it here would publish a tree with a hole in it that the producer does not have.
func paramsFieldsFromMessage(t *testing.T, descriptor protoreflect.MessageDescriptor, message protoreflect.Message) []paramsField {
	t.Helper()
	fields := descriptor.Fields()
	rendered := make([]paramsField, 0, fields.Len())
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		name := string(field.Name())
		if field.IsList() {
			list := message.Get(field).List()
			// §4.4: REPEATED_V1 puts u32_be(n) inside the frame, ahead of the
			// elements, so the count is a nested field and not a sibling of the list.
			elements := make([]paramsField, 0, list.Len()+1)
			count := uint64(list.Len())
			elements = append(elements, paramsField{Name: "element_count", Type: "uint32", Value: &count})
			for index := 0; index < list.Len(); index++ {
				element := paramsScalarOrMessageField(t, field, list.Get(index),
					fmt.Sprintf("%s_%d", name, index))
				elements = append(elements, element)
			}
			rendered = append(rendered, paramsField{Name: name, Type: "frame", Fields: elements})
			continue
		}
		rendered = append(rendered, paramsScalarOrMessageField(t, field, message.Get(field), name))
	}
	return rendered
}

func paramsScalarOrMessageField(t *testing.T, field protoreflect.FieldDescriptor, value protoreflect.Value, name string) paramsField {
	t.Helper()
	switch field.Kind() {
	case protoreflect.MessageKind:
		nested := value.Message()
		return paramsField{
			Name: name, Type: "frame",
			Fields: paramsFieldsFromMessage(t, field.Message(), nested),
		}
	case protoreflect.Uint32Kind:
		number := uint64(value.Uint())
		return paramsField{Name: name, Type: "uint32", Value: &number}
	case protoreflect.Uint64Kind:
		number := value.Uint()
		return paramsField{Name: name, Type: "uint64", Value: &number}
	case protoreflect.BoolKind:
		flag := value.Bool()
		return paramsField{Name: name, Type: "bool", Bool: &flag}
	case protoreflect.StringKind:
		text := value.String()
		return paramsField{Name: name, Type: "string", UTF8: &text}
	default:
		t.Fatalf("%s has proto kind %s, which §1.2 does not encode in a params tree", name, field.Kind())
		return paramsField{}
	}
}

// paramsDynamicFromFields rebuilds a message from a published tree, matching
// fields BY NAME. Position is deliberately not used: the producer must supply the
// order, and a fixture whose entries were reordered has to reach the producer as
// the same message so the disagreement shows up in the digest.
func paramsDynamicFromFields(t *testing.T, descriptor protoreflect.MessageDescriptor, frame paramsField, where string) protoreflect.Message {
	t.Helper()
	require.Equal(t, "frame", frame.Type, "%s must be a nested frame", where)
	message := dynamicpb.NewMessage(descriptor)

	seen := make(map[string]struct{}, len(frame.Fields))
	for _, published := range frame.Fields {
		child := fmt.Sprintf("%s.%s", where, published.Name)
		field := descriptor.Fields().ByName(protoreflect.Name(published.Name))
		require.NotNil(t, field, "%s is not a field of %s", child, descriptor.FullName())
		_, duplicate := seen[published.Name]
		require.False(t, duplicate, "%s is published twice", child)
		seen[published.Name] = struct{}{}

		if field.IsList() {
			require.Equal(t, "frame", published.Type, "%s is a repeated field and frames as REPEATED_V1", child)
			require.NotEmpty(t, published.Fields)
			count := published.Fields[0]
			require.Equal(t, "element_count", count.Name, "%s must put u32_be(n) first inside the frame", child)
			require.NotNil(t, count.Value)
			elements := published.Fields[1:]
			require.Equal(t, uint64(len(elements)), *count.Value, "%s element_count disagrees with the elements", child)

			list := message.Mutable(field).List()
			for index, element := range elements {
				list.Append(paramsValueFromField(t, field, element, list, fmt.Sprintf("%s[%d]", child, index)))
			}
			continue
		}
		message.Set(field, paramsValueFromField(t, field, published, nil, child))
	}
	require.Len(t, seen, descriptor.Fields().Len(),
		"%s must publish every declared field of %s; §1.2 frames all of them, including the ones holding proto3 defaults",
		where, descriptor.FullName())
	return message
}

func paramsValueFromField(t *testing.T, field protoreflect.FieldDescriptor, published paramsField, list protoreflect.List, where string) protoreflect.Value {
	t.Helper()
	switch field.Kind() {
	case protoreflect.MessageKind:
		var nested protoreflect.Message
		if list != nil {
			nested = list.NewElement().Message()
		} else {
			nested = dynamicpb.NewMessage(field.Message())
		}
		rebuilt := paramsDynamicFromFields(t, field.Message(), published, where)
		protov2.Merge(nested.Interface(), rebuilt.Interface())
		return protoreflect.ValueOfMessage(nested)
	case protoreflect.Uint32Kind:
		require.Equal(t, "uint32", published.Type, "%s", where)
		require.NotNil(t, published.Value)
		require.LessOrEqual(t, *published.Value, uint64(^uint32(0)), "%s overflows uint32", where)
		return protoreflect.ValueOfUint32(uint32(*published.Value))
	case protoreflect.Uint64Kind:
		require.Equal(t, "uint64", published.Type, "%s", where)
		require.NotNil(t, published.Value)
		return protoreflect.ValueOfUint64(*published.Value)
	case protoreflect.BoolKind:
		require.Equal(t, "bool", published.Type, "%s", where)
		require.NotNil(t, published.Bool)
		return protoreflect.ValueOfBool(*published.Bool)
	case protoreflect.StringKind:
		require.Equal(t, "string", published.Type, "%s", where)
		require.NotNil(t, published.UTF8)
		return protoreflect.ValueOfString(*published.UTF8)
	default:
		t.Fatalf("%s has proto kind %s, which §1.2 does not encode in a params tree", where, field.Kind())
		return protoreflect.Value{}
	}
}

func paramsCountLeaves(frame paramsField) paramsLeafAccounts {
	var accounts paramsLeafAccounts
	var walk func(field paramsField)
	walk = func(field paramsField) {
		if field.Type != "frame" {
			accounts.Scalars++
			return
		}
		repeated := len(field.Fields) > 0 && field.Fields[0].Name == "element_count"
		if repeated {
			accounts.Repeated++
		} else {
			accounts.Submessages++
		}
		for index, nested := range field.Fields {
			if repeated && index == 0 {
				// The element count is framing, not a parameter; counting it as a
				// scalar would make the accounting disagree with the proto message.
				continue
			}
			walk(nested)
		}
	}
	walk(frame)
	// frame itself is the params message, which is not one of its own submessages.
	accounts.Submessages--
	return accounts
}

// paramsSwapAdjacentScalars swaps the VALUES of the first two adjacent scalar
// leaves that share a type, anywhere in the tree, and reports which pair it hit.
// It swaps values rather than whole entries so the published field names stay put
// - the mutation has to be one the name-based rebuild cannot notice.
func paramsSwapAdjacentScalars(frame paramsField, preferredLeft string) (paramsField, string) {
	cloned := paramsCloneField(frame)
	var swapped string
	var walk func(field *paramsField)
	walk = func(field *paramsField) {
		if swapped != "" || field.Type != "frame" {
			return
		}
		for i := 0; i+1 < len(field.Fields); i++ {
			left, right := &field.Fields[i], &field.Fields[i+1]
			if left.Type == "frame" || left.Type != right.Type || left.Name == "element_count" ||
				(preferredLeft != "" && left.Name != preferredLeft) {
				continue
			}
			left.Value, right.Value = right.Value, left.Value
			left.UTF8, right.UTF8 = right.UTF8, left.UTF8
			left.Bool, right.Bool = right.Bool, left.Bool
			swapped = left.Name + " <-> " + right.Name
			return
		}
		for i := range field.Fields {
			walk(&field.Fields[i])
			if swapped != "" {
				return
			}
		}
	}
	walk(&cloned)
	return cloned, swapped
}

func paramsCloneField(field paramsField) paramsField {
	cloned := field
	if len(field.Fields) != 0 {
		cloned.Fields = make([]paramsField, len(field.Fields))
		for index, nested := range field.Fields {
			cloned.Fields[index] = paramsCloneField(nested)
		}
	}
	return cloned
}

func paramsFlipBit(base [][]byte, field, byteIndex, bit int) [][]byte {
	mutated := make([][]byte, len(base))
	for i := range base {
		mutated[i] = append([]byte(nil), base[i]...)
	}
	mutated[field][byteIndex] ^= 1 << bit
	return mutated
}

func loadParamsFixture(t *testing.T) paramsFixture {
	t.Helper()
	raw, err := os.ReadFile(paramsFixturePath)
	require.NoError(t, err)
	var fixture paramsFixture
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&fixture), "%s must decode with no unknown keys", paramsFixturePath)
	require.Equal(t, paramsFixtureSchema, fixture.Schema)
	require.NotEmpty(t, fixture.Source)
	require.NotEmpty(t, fixture.Notes)
	if os.Getenv(paramsRegenEnv) == "1" {
		regenerateParamsFixture(t, fixture)
	}
	return fixture
}

// regenerateParamsFixture rebuilds the nested tree from the proto descriptor and
// recomputes every derived hex column.
//
// The VALUES are synthesised rather than copied from default params: a framing
// vector needs every leaf to differ from its neighbours, and a realistic params
// set has runs of equal values that would make an adjacent swap invisible. What
// the vector is for is the order and the nesting, and both are the descriptor's
// statement, not this function's.
func regenerateParamsFixture(t *testing.T, fixture paramsFixture) {
	t.Helper()
	for i := range fixture.Vectors {
		vector := &fixture.Vectors[i]
		descriptor := paramsMessageDescriptor(t, vector.ProtoMessage)
		counter := 0
		message := paramsSyntheticMessage(t, descriptor, &counter)
		tree := paramsField{
			Name: "params", Type: "frame",
			Fields: paramsFieldsFromMessage(t, descriptor, message),
		}
		version := vector.ParamsVersion
		chainID := vector.ChainID
		vector.Fields = []paramsField{
			{Name: "chain_id", Type: "string", UTF8: &chainID},
			{Name: "params_version", Type: "uint64", Value: &version},
			tree,
		}
		accounts := paramsCountLeaves(tree)
		vector.Leaves = &accounts

		base := vector.encodeFields(t)
		vector.PreimageHex = hex.EncodeToString(vector.preimage(base))
		vector.DigestHex = vector.digestOf(base)
		for j := range vector.Tamper {
			tamper := &vector.Tamper[j]
			tamper.DigestHex = vector.digestOf(paramsFlipBit(base, tamper.Field, tamper.Byte, tamper.Bit))
		}
		for j := range vector.Replay {
			replay := &vector.Replay[j]
			replay.DigestHex = paramsProducerDigest(t, *vector, replay.ChainID, replay.Version)
		}
	}
	encoded, err := json.MarshalIndent(fixture, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paramsFixturePath, append(encoded, '\n'), 0o644))
	t.Fatalf("regenerated %s; unset %s and review the diff against §18.0 before committing",
		paramsFixturePath, paramsRegenEnv)
}

// paramsSyntheticMessage fills every declared leaf with a value no neighbouring
// leaf shares, walking in proto field-number order so the counter is stable
// across runs.
func paramsSyntheticMessage(t *testing.T, descriptor protoreflect.MessageDescriptor, counter *int) protoreflect.Message {
	t.Helper()
	message := dynamicpb.NewMessage(descriptor)
	fields := descriptor.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		if field.IsList() {
			list := message.Mutable(field).List()
			// Three elements: enough for the count, the order and the boundary
			// between the last element and the next sibling field all to be pinned.
			for element := 0; element < 3; element++ {
				if field.Kind() == protoreflect.MessageKind {
					nested := list.NewElement().Message()
					protov2.Merge(nested.Interface(), paramsSyntheticMessage(t, field.Message(), counter).Interface())
					list.Append(protoreflect.ValueOfMessage(nested))
					continue
				}
				list.Append(paramsSyntheticScalar(t, field, counter))
			}
			continue
		}
		if field.Kind() == protoreflect.MessageKind {
			nested := dynamicpb.NewMessage(field.Message())
			protov2.Merge(nested.Interface(), paramsSyntheticMessage(t, field.Message(), counter).Interface())
			message.Set(field, protoreflect.ValueOfMessage(nested))
			continue
		}
		message.Set(field, paramsSyntheticScalar(t, field, counter))
	}
	return message
}

func paramsSyntheticScalar(t *testing.T, field protoreflect.FieldDescriptor, counter *int) protoreflect.Value {
	t.Helper()
	*counter++
	seq := *counter
	switch field.Kind() {
	case protoreflect.Uint32Kind:
		return protoreflect.ValueOfUint32(uint32(seq))
	case protoreflect.Uint64Kind:
		return protoreflect.ValueOfUint64(uint64(1_000_000 + seq))
	case protoreflect.BoolKind:
		// Two adjacent bools cannot be told apart by value; there is exactly one
		// bool leaf per params message, and its neighbours are other types.
		return protoreflect.ValueOfBool(seq%2 == 1)
	case protoreflect.StringKind:
		if field.Name() == "atomic_units" {
			// Amount.atomic_units is decimal text on the wire, so the vector keeps it
			// decimal even though this encoder never parses it.
			return protoreflect.ValueOfString(strconv.Itoa(1_000 + seq))
		}
		return protoreflect.ValueOfString("v" + strconv.Itoa(seq))
	default:
		t.Fatalf("%s has proto kind %s, which §1.2 does not encode in a params tree", field.FullName(), field.Kind())
		return protoreflect.Value{}
	}
}
