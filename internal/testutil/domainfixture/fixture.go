package domainfixture

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// Fixture is the common subset of the repository's domain-vector documents.
// Module tests own the exact schema and producer-specific input decoding.
type Fixture struct {
	Schema  string          `json:"schema"`
	Source  json.RawMessage `json:"source"`
	Notes   []string        `json:"notes"`
	Vectors []Vector        `json:"vectors"`
}

type Vector struct {
	Name                 string          `json:"name"`
	Domain               string          `json:"domain"`
	Framing              string          `json:"framing"`
	ContractSection      string          `json:"contract_section"`
	Producer             string          `json:"producer"`
	RejectedByProduction string          `json:"rejected_by_production"`
	Variant              string          `json:"variant"`
	Fields               []Field         `json:"fields"`
	Inputs               json.RawMessage `json:"inputs"`
	PreimageHex          string          `json:"preimage_hex"`
	DigestHex            string          `json:"digest_hex"`
	HashHex              string          `json:"hash_hex"`
	Note                 string          `json:"note"`
}

type Field struct {
	Name              string  `json:"name"`
	Type              string  `json:"type"`
	Hex               string  `json:"hex"`
	Bech32            string  `json:"bech32"`
	UTF8              *string `json:"utf8"`
	Value             *uint64 `json:"value"`
	Signed            *int64  `json:"signed"`
	Bool              *bool   `json:"bool"`
	Present           *bool   `json:"present"`
	Empty             bool    `json:"empty"`
	FramedEmptyReason string  `json:"framed_empty_reason"`
	Fields            []Field `json:"fields"`
}

func Load(t testing.TB, path string) Fixture {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read domain fixture %s: %v", path, err)
	}
	var fixture Fixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode domain fixture %s: %v", path, err)
	}
	if fixture.Schema == "" || len(fixture.Vectors) == 0 {
		t.Fatalf("domain fixture %s has no schema or vectors", path)
	}
	return fixture
}

func ByName(t testing.TB, path string) map[string]Vector {
	t.Helper()
	fixture := Load(t, path)
	vectors := make(map[string]Vector, len(fixture.Vectors))
	for _, vector := range fixture.Vectors {
		if vector.Name == "" || vector.Domain == "" || vector.Digest() == "" {
			t.Fatalf("domain fixture %s has an incomplete vector", path)
		}
		if _, duplicate := vectors[vector.Name]; duplicate {
			t.Fatalf("domain fixture %s repeats vector %q", path, vector.Name)
		}
		vectors[vector.Name] = vector
	}
	return vectors
}

func (v Vector) Digest() string {
	if v.DigestHex != "" {
		return v.DigestHex
	}
	return v.HashHex
}

func (v Vector) DigestBytes(t testing.TB) []byte {
	t.Helper()
	return DecodeHex(t, v.Name+" digest", v.Digest())
}

func (v Vector) Field(t testing.TB, index int, name string) Field {
	t.Helper()
	if index < 0 || index >= len(v.Fields) {
		t.Fatalf("%s field %d is out of range", v.Name, index)
	}
	field := v.Fields[index]
	if field.Name != name {
		t.Fatalf("%s field %d is %q, want %q", v.Name, index, field.Name, name)
	}
	return field
}

func (v Vector) Bytes(t testing.TB, index int, name string) []byte {
	t.Helper()
	return v.Field(t, index, name).Bytes(t, v.Name+"."+name)
}

func (v Vector) AddressBytes(t testing.TB, index int, name string) []byte {
	t.Helper()
	field := v.Field(t, index, name)
	field.requireType(t, v.Name+"."+name, "address")
	return DecodeHex(t, v.Name+"."+name, field.Hex)
}

func (v Vector) Address(t testing.TB, index int, name string) string {
	t.Helper()
	field := v.Field(t, index, name)
	field.requireType(t, v.Name+"."+name, "address")
	if field.Bech32 == "" {
		t.Fatalf("%s.%s has no bech32 value", v.Name, name)
	}
	return field.Bech32
}

func (v Vector) String(t testing.TB, index int, name string) string {
	t.Helper()
	return v.Field(t, index, name).String(t, v.Name+"."+name)
}

func (v Vector) Uint(t testing.TB, index int, name string) uint64 {
	t.Helper()
	return v.Field(t, index, name).Uint(t, v.Name+"."+name)
}

func (v Vector) BoolValue(t testing.TB, index int, name string) bool {
	t.Helper()
	return v.Field(t, index, name).BoolValue(t, v.Name+"."+name)
}

func (v Vector) Frame(t testing.TB, index int, name string) Field {
	t.Helper()
	field := v.Field(t, index, name)
	field.requireType(t, v.Name+"."+name, "frame")
	return field
}

func (v Vector) Repeated(t testing.TB, index, countIndex int, name, countName string) []Field {
	t.Helper()
	frame := v.Frame(t, index, name)
	if len(frame.Fields) == 0 {
		t.Fatalf("%s.%s has no repeated count", v.Name, name)
	}
	count := frame.Fields[0]
	if count.Name != "element_count" {
		t.Fatalf("%s.%s starts with %q, want element_count", v.Name, name, count.Name)
	}
	if count.Uint(t, v.Name+"."+name+".element_count") != uint64(len(frame.Fields)-1) {
		t.Fatalf("%s.%s element_count does not match its elements", v.Name, name)
	}
	if v.Uint(t, countIndex, countName) != uint64(len(frame.Fields)-1) {
		t.Fatalf("%s.%s does not match %s", v.Name, countName, name)
	}
	return frame.Fields[1:]
}

func (f Field) Field(t testing.TB, index int, name string) Field {
	t.Helper()
	if index < 0 || index >= len(f.Fields) {
		t.Fatalf("nested field %d is out of range", index)
	}
	field := f.Fields[index]
	if field.Name != name {
		t.Fatalf("nested field %d is %q, want %q", index, field.Name, name)
	}
	return field
}

func (f Field) Bytes(t testing.TB, where string) []byte {
	t.Helper()
	if f.Type != "bytes" && f.Type != "address" {
		t.Fatalf("%s has type %q, want bytes/address", where, f.Type)
	}
	return DecodeHex(t, where, f.Hex)
}

func (f Field) String(t testing.TB, where string) string {
	t.Helper()
	f.requireType(t, where, "string")
	if f.UTF8 == nil {
		t.Fatalf("%s has no utf8 value", where)
	}
	return *f.UTF8
}

func (f Field) Uint(t testing.TB, where string) uint64 {
	t.Helper()
	if f.Type != "uint32" && f.Type != "uint64" && f.Type != "enum" {
		t.Fatalf("%s has type %q, want uint32/uint64/enum", where, f.Type)
	}
	if f.Value == nil {
		t.Fatalf("%s has no integer value", where)
	}
	return *f.Value
}

func (f Field) BoolValue(t testing.TB, where string) bool {
	t.Helper()
	f.requireType(t, where, "bool")
	if f.Bool == nil {
		t.Fatalf("%s has no bool value", where)
	}
	return *f.Bool
}

func (f Field) Frame(t testing.TB, where string) []Field {
	t.Helper()
	f.requireType(t, where, "frame")
	return f.Fields
}

func (f Field) requireType(t testing.TB, where, want string) {
	t.Helper()
	if f.Type != want {
		t.Fatalf("%s has type %q, want %q", where, f.Type, want)
	}
}

func DecodeHex(t testing.TB, name, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(raw) != value {
		t.Fatalf("%s is not canonical lowercase hex: %v", name, err)
	}
	return raw
}

func RequireDigest(t testing.TB, vector Vector, got []byte) {
	t.Helper()
	want := vector.DigestBytes(t)
	if !bytes.Equal(got, want) {
		t.Fatalf("%s producer digest = %x, want %x", vector.Name, got, want)
	}
}

func RequireBoundSet(t testing.TB, vectors map[string]Vector, bound map[string]struct{}, exceptions map[string]string) {
	t.Helper()
	for name := range vectors {
		if _, ok := bound[name]; ok {
			continue
		}
		if reason := exceptions[name]; reason == "" {
			t.Fatalf("fixture vector %q has no executable producer binding or explicit reason", name)
		}
	}
	for name := range bound {
		if _, ok := vectors[name]; !ok {
			t.Fatalf("producer binding %q has no fixture vector", name)
		}
	}
	for name, reason := range exceptions {
		if reason == "" {
			t.Fatalf("producer binding exception %q has no reason", name)
		}
		if _, ok := vectors[name]; !ok {
			t.Fatalf("producer binding exception %q has no fixture vector", name)
		}
		if _, ok := bound[name]; ok {
			t.Fatalf("fixture vector %q is both bound and excepted", name)
		}
	}
}

func DecodeInputs(t testing.TB, vector Vector, target any) {
	t.Helper()
	if len(vector.Inputs) == 0 || string(vector.Inputs) == "null" {
		t.Fatalf("%s has no producer inputs", vector.Name)
	}
	if err := json.Unmarshal(vector.Inputs, target); err != nil {
		t.Fatalf("decode %s inputs: %v", vector.Name, err)
	}
}

func RequireProducer(t testing.TB, vector Vector, want string) {
	t.Helper()
	if vector.Producer != want {
		t.Fatalf("%s producer is %q, want %q", vector.Name, vector.Producer, want)
	}
}

func Where(vector Vector, suffix string) string {
	return fmt.Sprintf("%s.%s", vector.Name, suffix)
}
