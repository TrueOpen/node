package node

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// This file makes DomainSpec.Fields executable for every domain that has golden
// vector coverage. The registry's own doc comment calls Fields "documentation of
// the frozen preimage order, not an executable schema", and that is exactly the
// gap this closes: a preimage changed and nothing checked by CI disagreed with
// it. Documentation cannot disagree; a test can.
//
// It lives in the repository root package because domainFixturePaths walks ".",
// which at test time is the package directory: only a root-package test sees every
// module's testdata in one run. A per-module copy would be three copies of the same
// loop, each blind to the other two modules' domains, which is exactly the coverage
// question the freeze gates below have to answer for the registry as a whole. The
// root package imports only the shared registry and reads the fixtures as data, so
// it takes on no module dependency.

// domainGoldenVector is the intersection of the golden-vector fixture shapes in the
// tree. Each module's own *_fixture_test.go keeps the strict, exhaustive reader for
// its file (DisallowUnknownFields, tamper and replay cases, production-helper value
// binding); this reader is deliberately permissive and understands only the keys
// every H_FIELDS_V1 vector must carry, so a fixture family added later takes part in
// the differential without having to be registered anywhere first.
//
// It reads the top-level "vectors" array only. framing_v1.json publishes its
// primitives under other keys, but those vectors hash the synthetic
// TRUEOPEN_TEST_FIELDS_V1 domain, which is not in the registry and is filtered out here
// anyway - a real domain must never be pinned outside a "vectors" array.
type domainGoldenVector struct {
	Name    string `json:"name"`
	Domain  string `json:"domain"`
	Framing string `json:"framing"`
	// Variant selects one DomainSpec.Variants entry for a domain whose preimage
	// shape depends on a discriminator. It is required for those domains and
	// forbidden for every other one; see assertRegistryFieldOrder.
	Variant     string              `json:"variant"`
	Fields      []domainGoldenField `json:"fields"`
	PreimageHex string              `json:"preimage_hex"`
	// DigestHex is the Hub / Task / candidate-pool spelling of the published digest
	// and HashHex the economics one. A vector must use exactly one of them.
	DigestHex string `json:"digest_hex"`
	HashHex   string `json:"hash_hex"`

	// file is the fixture the vector was read from. It is not a JSON key; it is
	// what turns a failure message into a file a human can open.
	file string
}

// domainGoldenField covers both field spellings in the tree. The Hub and Task
// fixtures carry a typed field ("type": "uint64", "value": 42) with a name; the
// economics fixture predates that and carries positional framed bytes ("hex", "utf8"
// or "empty": true) with no name at all. Both must reproduce the same preimage, and
// the distinction is what decides whether the name-order check can run.
type domainGoldenField struct {
	Name    string              `json:"name"`
	Type    string              `json:"type"`
	Hex     string              `json:"hex"`
	UTF8    *string             `json:"utf8"`
	Value   *uint64             `json:"value"`
	Signed  *int64              `json:"signed"`
	Bool    *bool               `json:"bool"`
	Present *bool               `json:"present"`
	Empty   bool                `json:"empty"`
	Fields  []domainGoldenField `json:"fields"`
}

// domainVectorFilesWithPositionalFields lists the fixtures whose vectors frame their
// fields positionally, without names. Their preimage and digest are still checked
// here; only the name-order half of the differential is skipped, because there are no
// names to compare. A vector in any other file that omits its field names fails
// instead of being skipped silently.
var domainVectorFilesWithPositionalFields = map[string]string{}

// domainVectorFilesWithoutFields lists the fixtures that publish a preimage and a
// digest but not the field decomposition. SHA256(preimage) is still pinned; the
// per-field re-framing and the name order are not, because the vector does not say
// where one field ends and the next begins.
var domainVectorFilesWithoutFields = map[string]string{
	"x/task/types/testdata/token_ids_v1.json": "Wire v0.4.1 publishes exact token-ID preimages and digests but no fields[] decomposition",
}

// TestGoldenVectorsMatchDomainRegistryFieldOrder is the differential: for every golden
// vector whose domain is a registered H_FIELDS_V1 domain it recomputes the preimage
// from the vector's own fields, recomputes the digest from the preimage, and compares
// the vector's ordered field names against DomainRegistryV1's ordered Fields.
//
// A failure here is never something to fix by editing whichever side is more
// convenient. The registry row and the vector are two independent statements of one
// frozen preimage; if they disagree, one of them is wrong about consensus and the
// answer is in the row's ContractSection.
func TestGoldenVectorsMatchDomainRegistryFieldOrder(t *testing.T) {
	vectors := loadDomainGoldenVectors(t)

	var participating, nameChecked, positional, withoutFields int
	usedPositionalFile := make(map[string]bool, len(domainVectorFilesWithPositionalFields))
	usedFieldlessFile := make(map[string]bool, len(domainVectorFilesWithoutFields))
	stillFlattened := make(map[string]bool, len(flattenedRepeatedTailAllowlistV1))
	coveredVariants := make(map[string]map[string]string)

	for _, vector := range vectors {
		if vector.Domain == "" || vector.PreimageHex == "" {
			continue
		}
		spec, registered := shared.DomainSpecFor(vector.Domain)
		if !registered || spec.Framing != shared.FramingHFieldsV1 {
			continue
		}
		participating++

		label := vector.file + "#" + vector.Name
		t.Run(label, func(t *testing.T) {
			if vector.Framing != "" {
				require.Equal(t, shared.FramingHFieldsV1.String(), vector.Framing,
					"the vector and the registry must agree on the framing")
			}

			digestHex := vector.DigestHex
			if digestHex == "" {
				digestHex = vector.HashHex
			} else {
				require.Empty(t, vector.HashHex,
					"a vector must publish its digest under one key, not two")
			}
			require.Len(t, digestHex, 64, "%s must publish a 32-byte digest", label)

			preimage := domainGoldenHex(t, label, vector.PreimageHex)
			sum := sha256.Sum256(preimage)
			require.Equal(t, digestHex, hex.EncodeToString(sum[:]),
				"%s: SHA256(preimage_hex) is not the published digest", label)

			// H_FIELDS_V1 frames the domain as field 0, so the preimage of a domain can
			// never be a valid preimage of any other domain: the length prefix and the
			// ASCII literal are the first bytes hashed.
			require.True(t, bytes.HasPrefix(preimage, shared.CanonicalFrameBytes([]byte(vector.Domain))),
				"%s: the preimage must begin with the framed domain literal", label)

			if len(vector.Fields) == 0 {
				reason, allowed := domainVectorFilesWithoutFields[vector.file]
				require.True(t, allowed,
					"%s publishes no fields[]; a golden vector must decompose its preimage or its file must be listed in domainVectorFilesWithoutFields", label)
				require.NotEmpty(t, reason)
				usedFieldlessFile[vector.file] = true
				withoutFields++
				return
			}

			framed := make([][]byte, 0, len(vector.Fields)+1)
			framed = append(framed, []byte(vector.Domain))
			named := 0
			for index, field := range vector.Fields {
				framed = append(framed, field.encode(t, fmt.Sprintf("%s field %d", label, index)))
				if field.Name != "" {
					named++
				}
			}
			require.Equal(t, vector.PreimageHex, hex.EncodeToString(shared.CanonicalFrameBytes(framed...)),
				"%s: re-framing the vector's own typed fields must reproduce preimage_hex", label)

			if named == 0 {
				reason, allowed := domainVectorFilesWithPositionalFields[vector.file]
				require.True(t, allowed,
					"%s names none of its fields; a golden vector must name them or its file must be listed in domainVectorFilesWithPositionalFields", label)
				require.NotEmpty(t, reason)
				usedPositionalFile[vector.file] = true
				positional++
				return
			}
			require.Equal(t, len(vector.Fields), named,
				"%s: a vector must name all of its fields or none of them, never some", label)

			assertRegistryFieldOrder(t, spec, label, vector.Variant, vector.Fields, stillFlattened)
			nameChecked++
			if len(spec.Variants) != 0 {
				if coveredVariants[vector.Domain] == nil {
					coveredVariants[vector.Domain] = make(map[string]string, len(spec.Variants))
				}
				previous, duplicate := coveredVariants[vector.Domain][vector.Variant]
				require.False(t, duplicate,
					"%s: %s variant %q is already pinned by %s; one vector per variant, so a second one cannot quietly disagree",
					label, vector.Domain, vector.Variant, previous)
				coveredVariants[vector.Domain][vector.Variant] = label
			}
		})
	}

	// Variant coverage is frozen in both directions, which is the whole point of
	// making the shape machine-readable. A registered variant with no vector is an
	// unpinned preimage - the state the eight allowlisted domains were in - and a
	// vector for an unregistered variant already failed inside
	// registryVariantFields. Between them, "the set of shapes this domain can take"
	// has exactly one answer that both the registry and the fixtures agree on.
	for _, spec := range shared.DomainRegistryV1 {
		if len(spec.Variants) == 0 {
			continue
		}
		if _, external := wireOwnedDomainVectorCoverage[spec.Domain]; external {
			continue
		}
		if _, external := wirePublishedDomainVectorCoverage[spec.Domain]; external {
			continue
		}
		want := make([]string, 0, len(spec.Variants))
		for _, variant := range spec.Variants {
			want = append(want, variant.Key)
		}
		got := make([]string, 0, len(want))
		for key := range coveredVariants[spec.Domain] {
			got = append(got, key)
		}
		sort.Strings(want)
		sort.Strings(got)
		require.Equal(t, want, got,
			"%s: every registered variant needs its own golden vector. A variant in want only is registered but unpinned; publishing the row without the vector is what left this domain on domainVectorCoverageAllowlistV1 in the first place.",
			spec.Domain)
	}

	t.Logf("H_FIELDS_V1 golden vectors: %d participating, %d name-order checked, %d skipped for positional fields, %d skipped for having no fields[]",
		participating, nameChecked, positional, withoutFields)

	// The reach of the differential is ratcheted, not merely reported. Both gates
	// below are keyed by domain, so a vector that stops carrying "domain" or
	// "preimage_hex" - or a fixture family that stops decoding as one - leaves this
	// loop with nothing failing, as long as one other vector still pins the domain.
	// Coverage normally grows. Lower these numbers only when an invalid vector is
	// deliberately removed and its domain is restored to the explicit coverage
	// allowlist, as with the contract-prose BuilderEvidence vector removed here.
	// The current floor excludes vectors removed with their RPC/schema and counts
	// the replacement TaskGasReimbursements pair. Lower it only when another
	// superseded contract is deliberately removed in the same change.
	require.GreaterOrEqual(t, participating, 115,
		"fewer golden vectors reach the differential than before; a vector lost its domain or preimage_hex, or a fixture stopped decoding")
	require.GreaterOrEqual(t, nameChecked, 115,
		"fewer golden vectors have their field order checked against the registry than before")

	// The flattening allowlist is frozen the same way, but it has to be settled here
	// rather than inside a subtest: a single vector can prove a domain still flattened
	// and no vector can prove the opposite, so only the whole run knows whether an
	// entry still has evidence behind it. An entry with none is either a domain that
	// was repaired - delete it - or one whose vectors stopped reaching the check, which
	// is the same silent loss of coverage the ratchets above exist to catch.
	for domain := range flattenedRepeatedTailAllowlistV1 {
		require.True(t, stillFlattened[domain],
			"%s is on flattenedRepeatedTailAllowlistV1 but no golden vector still shows it splicing a repeated group into the outer field list. If the producer was fixed, delete the entry - this list only shrinks. If instead its vectors stopped covering the repeated tail, restore the coverage; the exemption may not outlive the evidence for it.",
			domain)
	}

	// The two skip lists are frozen in both directions: a file stops being listed the
	// moment its vectors grow names or fields, so the exemption cannot outlive the
	// reason for it.
	for path := range domainVectorFilesWithPositionalFields {
		require.True(t, usedPositionalFile[path],
			"%s no longer contributes positional-field vectors; remove it from domainVectorFilesWithPositionalFields", path)
	}
	for path := range domainVectorFilesWithoutFields {
		require.True(t, usedFieldlessFile[path],
			"%s no longer contributes vectors without fields[]; remove it from domainVectorFilesWithoutFields", path)
	}
}

// registryFieldIdentifier is what a DomainSpec.Fields entry has to reduce to before it
// can be compared with a vector's field name. Anything else is prose, and prose fails
// the differential rather than being waved through: a row that cannot be checked is
// the state this test exists to make visible.
var registryFieldIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// registryFieldWrapper matches the two encoding wrappers the registry writes around a
// field name: "uint32_be(count)" and "FieldFrameV1(member: a, b, c)".
//
// The two names are matched literally rather than as "any identifier followed by
// parentheses". A general wrapper pattern also strips wrappers that change what the
// row asserts - "sha256(session_id)" would reduce to "session_id" and compare equal to
// a vector that frames the session id itself - so the row could be rewritten into a
// different claim about the preimage without this differential noticing. A wrapper
// that is not one of these two is prose, and prose fails loudly in registryFieldName.
var registryFieldWrapper = regexp.MustCompile(`^(?:uint32_be|FieldFrameV1)\((.+)\)$`)

// registryFieldName reduces one DomainSpec.Fields entry to the bare name a vector
// uses. The registry sometimes records how a field is encoded ("uint32_be(count)") or
// what a nested frame contains ("FieldFrameV1(member: candidate_pool_snapshot_id,
// slot, slot_version, operator_address)"); the vector records only the outer field
// ("count", "member"). Both spellings describe the same one field at the same one
// position, so the wrapper is stripped and the name compared.
//
// Nothing else is normalised. An entry that is a sentence stays a sentence and
// returns false, which fails the caller loudly.
func registryFieldName(entry string) (string, bool) {
	candidate := entry
	if match := registryFieldWrapper.FindStringSubmatch(entry); match != nil {
		inner := match[1]
		if colon := strings.Index(inner, ":"); colon >= 0 {
			inner = inner[:colon]
		}
		candidate = strings.TrimSpace(inner)
	}
	if !registryFieldIdentifier.MatchString(candidate) {
		return "", false
	}
	return candidate, true
}

// isRepeatedRegistryField reports whether an entry describes a variable-length tail
// rather than one field, e.g. "repeated(endpoint_kind, uri, protocol_version,
// tls_pubkey_hash) in (endpoint_kind, uri) ascending".
func isRepeatedRegistryField(entry string) bool {
	return strings.HasPrefix(entry, "repeated(") || strings.HasPrefix(entry, "repeated ")
}

// assertRegistryFieldOrder compares the vector's ordered field names with the
// registry's ordered Fields.
//
// The only structural allowance is the repeated tail. A domain whose preimage ends in
// a variable-length list records that list as one final "repeated(...)" entry, and per
// canonical_encoding_and_domain_hashing.md §4.4 a compliant producer frames the whole
// group as one REPEATED_V1 position, so the vector should name exactly one field for it
// however many elements it carries.
// The fixed head is matched name-for-name; the tail's *arity* is checked separately by
// assertRepeatedTailIsOnePosition, which is what catches a producer that splices the
// elements into the outer list instead (two endpoints becoming eight endpoint_* fields).
//
// The tail's *names* are still not matched against the registry: the entry neither names
// the sub-fields nor says how many one element contributes, and the 30 repeated entries
// spell themselves six different ways ("repeated(a, b)", "repeated frame(...)",
// "repeated x in y order", ...). What they do get is the shape check below, which every
// field name must satisfy whether it is head or tail. The per-module fixtures pin the
// tail's bytes; this test pins the head's order and the tail's position and arity.
//
// That leaves a known hole: a reorder confined to the repeated tail, or inside a
// nested frame (registryFieldName truncates "FieldFrameV1(member: a, b, c)" at the
// colon), passes here. Closing it needs the registry to name flattened sub-fields,
// which is a registry change, not a test change.
// registryVariantFields resolves the tail a vector selected out of a
// discriminated domain's registry row.
//
// A vector for such a domain has to name its variant. The alternative - letting a
// vector match whichever variant happens to have the right arity - would mean a
// TRUEOPEN_QUERY_SELECTOR_V1 vector for one RPC could satisfy the row for another
// whenever the two take the same number of selector fields, which is most of
// them. The discriminator's value is in the preimage; the vector has to say which
// one it is.
func registryVariantFields(t *testing.T, spec shared.DomainSpec, label, variant string) []string {
	t.Helper()
	require.NotEmpty(t, variant,
		"%s: %s selects its field order by %s, so the vector must name the variant it frames",
		label, spec.Domain, spec.Discriminator)
	for _, registered := range spec.Variants {
		if registered.Key == variant {
			return registered.Fields
		}
	}
	keys := make([]string, 0, len(spec.Variants))
	for _, registered := range spec.Variants {
		keys = append(keys, registered.Key)
	}
	t.Fatalf("%s: %s has no variant %q; DomainRegistryV1 registers %v. A vector for an unregistered variant is a preimage no producer can be asked to reproduce.",
		label, spec.Domain, variant, keys)
	return nil
}

func assertRegistryFieldOrder(t *testing.T, spec shared.DomainSpec, label, variant string, fields []domainGoldenField, stillFlattened map[string]bool) {
	t.Helper()
	require.NotEmpty(t, spec.Fields,
		"%s: %s has golden-vector coverage but no registered field order", label, spec.Domain)

	entries := spec.Fields
	if len(spec.Variants) != 0 {
		entries = append(append([]string(nil), spec.Fields...), registryVariantFields(t, spec, label, variant)...)
	} else {
		require.Empty(t, variant,
			"%s: %s has one fixed field order, so a vector cannot select a variant of it", label, spec.Domain)
	}
	repeatedTail := false
	if isRepeatedRegistryField(entries[len(entries)-1]) {
		repeatedTail = true
		entries = entries[:len(entries)-1]
	}

	want := make([]string, 0, len(entries))
	for index, entry := range entries {
		require.False(t, isRepeatedRegistryField(entry),
			"%s: %s Fields[%d] is a repeated group but is not the last entry; a variable-length tail cannot be followed by a fixed field",
			label, spec.Domain, index)
		name, ok := registryFieldName(entry)
		require.True(t, ok,
			"%s: %s Fields[%d] is %q, which is prose rather than a field name. The domain now has a named golden vector, so the registry row has to name its fields too - resolve it against %s, do not relax this check.",
			label, spec.Domain, index, entry, spec.ContractSection)
		want = append(want, name)
	}

	got := make([]string, 0, len(fields))
	seen := make(map[string]int, len(fields))
	for index, field := range fields {
		// Applies to head and tail alike. The tail has no registry counterpart to
		// compare against, so this is the only thing standing between a flattened
		// repeated element and a vector that names two preimage positions the same
		// way - which would make the vector unable to state a field order at all.
		require.NotEmpty(t, field.Name,
			"%s: %s fields[%d] has no name; a named vector must name every field it frames",
			label, spec.Domain, index)
		if first, duplicate := seen[field.Name]; duplicate {
			t.Fatalf("%s: %s fields[%d] repeats the name %q already used at fields[%d]; each framed position needs its own name",
				label, spec.Domain, index, field.Name, first)
		}
		seen[field.Name] = index
		got = append(got, field.Name)
	}

	if !repeatedTail {
		require.Equal(t, want, got,
			"%s: the vector's field order does not match DomainRegistryV1[%s].Fields. One of the two is wrong about the frozen preimage; %s decides which.",
			label, spec.Domain, spec.ContractSection)
		return
	}
	require.GreaterOrEqual(t, len(got), len(want),
		"%s: the vector has fewer fields than the fixed head of DomainRegistryV1[%s].Fields", label, spec.Domain)
	require.Equal(t, want, got[:len(want)],
		"%s: the fixed head of the vector's field order does not match DomainRegistryV1[%s].Fields; %s decides which side is wrong.",
		label, spec.Domain, spec.ContractSection)
	assertRepeatedTailIsOnePosition(t, spec, label, len(want), len(got), stillFlattened)
}

// assertRepeatedTailIsOnePosition closes the arity half of the repeated-tail hole
// described above.
//
// canonical_encoding_and_domain_hashing.md §4.4 says
// REPEATED_V1([e1..en]) = FRAME_V1(u32_be(n), ENC(e1), ..., ENC(en)):
// the whole group is ONE outer position regardless of n. A registry row that ends in a
// repeated entry therefore describes len(spec.Fields) outer positions, and a compliant
// producer frames exactly that many - head + 1 - no matter how many elements the
// vector's own inputs produce. A producer that splices the count and the elements
// straight into the outer list frames head + n*k instead, so the arity alone separates
// the two without needing the registry to name flattened sub-fields.
//
// This is the check whose absence let 29 domains drift: the caller matches the head
// exactly and then stops, so a preimage with twelve positions satisfied a registry row
// naming five. The tail's byte content is still only pinned by the per-module fixtures.
//
// The oracle is one-sided. head+1 != framed proves flattening; head+1 == framed does not
// prove compliance, because a group of n=1 single-field elements frames head+1 either
// way. So a vector may only ever add evidence: it records the domains it caught in
// stillFlattened, and the caller - not this function - decides what an allowlist entry
// with no evidence behind it means.
func assertRepeatedTailIsOnePosition(t *testing.T, spec shared.DomainSpec, label string, head, framed int, stillFlattened map[string]bool) {
	t.Helper()
	if framed != head+1 {
		stillFlattened[spec.Domain] = true
	}
	if flattenedRepeatedTailAllowlistV1[spec.Domain] {
		return
	}
	require.Equal(t, head+1, framed,
		"%s: %s frames %d outer positions but DomainRegistryV1[%s].Fields names %d. Its repeated group is spliced into the outer field list instead of being framed as one canonical_encoding_and_domain_hashing.md §4.4 REPEATED_V1 position. Fix the producer with shared.CanonicalRepeatedFramesV1 (see x/hub/types/parameter_bucket.go for the shape); do not relax this check and do not add an allowlist entry.",
		label, spec.Domain, framed, spec.Domain, head+1)
}

// flattenedRepeatedTailAllowlistV1 names every domain that still splices a repeated
// group's elements into its outer field list. Each entry is one known
// canonical_encoding_and_domain_hashing.md §4.4 violation awaiting a producer fix.
// The list only shrinks - the assertion above fails on an entry whose
// domain has been fixed, so a batch cannot land without removing the domains it
// repaired.
//
// The original 29-domain audit missed TRUEOPEN_CANDIDATE_ACTIVE_BITMAP_V1:
// its only vector had one single-field element, so the flattened and compliant
// layouts had the same outer arity. Reviewing ENC(element) against monorepo §4.4
// exposed the missing repeated count and raised the repaired total to 30.
var flattenedRepeatedTailAllowlistV1 = map[string]bool{}

// encode applies the §1.2 typed encoders to one fixture field. An unrecognised type
// is fatal on purpose: a new encoder that this function does not know about would
// otherwise frame as an empty field and quietly weaken every vector that uses it.
func (f domainGoldenField) encode(t *testing.T, where string) []byte {
	t.Helper()
	switch f.Type {
	case "":
		// The positional economics spelling: the field carries its framed bytes
		// directly, and "empty" distinguishes a genuinely zero-length field from a
		// field the fixture forgot to fill in.
		switch {
		case f.Empty:
			return nil
		case f.Hex != "":
			return domainGoldenHex(t, where, f.Hex)
		case f.UTF8 != nil:
			return []byte(*f.UTF8)
		}
		t.Fatalf("%s: untyped field carries none of empty / hex / utf8", where)
		return nil
	case "bytes", "address":
		// "address" frames the address-codec bytes, never the Bech32 text; the module
		// fixtures assert that the recorded Bech32 decodes to exactly these bytes.
		return domainGoldenHex(t, where, f.Hex)
	case "string":
		require.NotNil(t, f.UTF8, "%s: a string field requires utf8", where)
		return []byte(*f.UTF8)
	case "uint32":
		require.NotNil(t, f.Value, "%s: a uint32 field requires value", where)
		require.LessOrEqual(t, *f.Value, uint64(^uint32(0)), "%s overflows uint32", where)
		return shared.Uint32BE(uint32(*f.Value))
	case "uint64":
		require.NotNil(t, f.Value, "%s: a uint64 field requires value", where)
		return shared.Uint64BE(*f.Value)
	case "int32":
		require.NotNil(t, f.Signed, "%s: an int32 field requires signed", where)
		require.GreaterOrEqual(t, *f.Signed, int64(-2147483648), "%s underflows int32", where)
		require.LessOrEqual(t, *f.Signed, int64(2147483647), "%s overflows int32", where)
		return shared.Int32BE(int32(*f.Signed))
	case "bool":
		require.NotNil(t, f.Bool, "%s: a bool field requires bool", where)
		return shared.BoolByte(*f.Bool)
	case "enum":
		require.NotNil(t, f.Value, "%s: an enum field requires value", where)
		require.LessOrEqual(t, *f.Value, uint64(^uint32(0)), "%s overflows the enum encoding", where)
		return shared.EnumBE(uint32(*f.Value))
	case "frame":
		require.NotEmpty(t, f.Fields, "%s: a frame field requires nested fields", where)
		inner := make([][]byte, 0, len(f.Fields))
		for index, nested := range f.Fields {
			inner = append(inner, nested.encode(t, fmt.Sprintf("%s.%d", where, index)))
		}
		return shared.CanonicalFrameBytes(inner...)
	case "oneof":
		// ONEOF_V1: u32_be(selected field number) || FRAME_V1(payload fields).
		//
		// This is the encoding of a proto oneof whose SCHEMA declares one, and it is
		// not a generic wrapper for "a domain with more than one shape". The Builder
		// evidence content domain also selects a branch by tag, and its frozen
		// preimage per keeper_api_contract.md §5.5 is flat - schema_version, oneof_tag,
		// then the branch's fields as siblings - so wrapping it in this frame would
		// be a different preimage, not a tidier spelling of the same one. The
		// registry's Variants express that difference; this type does not.
		require.NotNil(t, f.Value, "%s: a oneof field requires the selected field number in value", where)
		require.LessOrEqual(t, *f.Value, uint64(^uint32(0)), "%s overflows the oneof tag encoding", where)
		require.NotEmpty(t, f.Fields, "%s: a oneof field requires the selected branch's payload fields", where)
		payload := make([][]byte, 0, len(f.Fields))
		for index, nested := range f.Fields {
			payload = append(payload, nested.encode(t, fmt.Sprintf("%s.%d", where, index)))
		}
		encoded, err := shared.OneofFrameV1(uint32(*f.Value), payload...)
		require.NoError(t, err, "%s: the oneof payload must be encodable", where)
		return encoded
	case "optional":
		require.NotNil(t, f.Present, "%s: optional field requires explicit presence", where)
		if !*f.Present {
			require.Empty(t, f.Fields, "%s: absent optional cannot carry a value", where)
			return shared.OptionalAbsentFrameV1()
		}
		require.Len(t, f.Fields, 1, "%s: present optional carries exactly one value", where)
		return shared.OptionalPresentFrameV1(f.Fields[0].encode(t, where+".value"))
	default:
		t.Fatalf("%s: unknown field type %q", where, f.Type)
		return nil
	}
}

// domainVectorCoverageAllowlistV1 is every registered H_FIELDS_V1 domain that has no
// golden vector yet. It is frozen in both directions, exactly like
// TestDomainRegistryV1UnregisteredSetIsFrozen: registering a new domain without a
// vector fails the build, and publishing a vector for a domain that is still listed
// here also fails until the entry is deleted.
//
// Existing entries may only be removed. A newly registered domain may be added
// only with the tracked contract gap that explains why no authoritative vector
// exists yet; an unexplained string is not an allowlist.
//
// Removing an entry takes more than publishing a vector, and the Track A batch 1
// migration is why the distinction is spelled out here. A vector whose digest is only
// checked against re-framing its own fields[] pins the framing and nothing else: it
// agrees with itself. TRUEOPEN_UNBONDING_ID_V1 had exactly such a vector and a
// source-shape production binding, and swapping its two trailing Uint64BE fields
// still passed the whole suite, because a binding that classifies fields by encoder
// cannot see a swap between two fields with the same encoder. So a domain leaves
// this list only once some test feeds the vector's own typed inputs to the real
// producer and demands the pinned digest back. That checks field identity rather
// than field encoding, and it is what the eight domains removed in batch 1 have.
//
// Node implementations may test an algorithm locally, but that does not turn a
// locally generated digest into a cross-language release vector. These entries
// therefore remain visible until the named Wire/document gap is closed.
var domainVectorCoverageAllowlistV1 = map[string]string{
	shared.DomainBeaconVRFInputV1:         "no released cross-language input vector (DOC-020)",
	shared.DomainBeginBridgeCutoverV1:     "Wire bridge action vectors missing (DOC-013)",
	shared.DomainBridgeInflightManifestV1: "Wire bridge manifest vectors missing (DOC-013)",
	shared.DomainBridgeSignerPoPV1:        "Wire bridge signer vectors missing (DOC-013)",
	shared.DomainBridgeSignerSetV1:        "Wire bridge signer vectors missing (DOC-013)",
	shared.DomainBuilderSetV1:             "the release carries only the superseded term-based vector; the current six-field producer is tested independently",
	shared.DomainConfirmBridgeCutoverV1:   "Wire bridge action vectors missing (DOC-013)",
	shared.DomainOrderOpeningV2:           "no released H_FIELDS_V1 order-opening vector (DOC-020)",
	shared.DomainReplaceBuilderSetV1:      "no released BuilderSet replacement action vector (DOC-020)",
	shared.DomainRotateBridgeSignerV1:     "Wire bridge action vectors missing (DOC-013)",
	shared.DomainSetBridgeFreezeV1:        "Wire bridge action vectors missing (DOC-013)",
	shared.DomainSetBridgeLimitV1:         "Wire bridge action vectors missing (DOC-013)",
	shared.DomainTaskOrderV2:              "no released H_FIELDS_V1 task-order vector (DOC-020)",
}

// wireOwnedDomainVectorCoverage records domains whose normative producer and
// cross-language vectors live in the exact Wire Go module/release pinned by
// wire_pin_test.go. They are not copied into Node's V1 fixture schema: Node calls
// these Wire producers directly, and duplicating their vectors here would create
// a second release artifact with no independent implementation.
var wireOwnedDomainVectorCoverage = map[string]string{
	shared.DomainBusEnvelopeV2:            "Wire bus signing vectors",
	shared.DomainBuilderEvidenceContentV2: "Wire bus BuilderEvidenceV2 linked vectors",
	shared.DomainBuilderEvidenceIDV2:      "Wire bus BuilderEvidenceV2 linked vectors",
	shared.DomainBuilderFaultV2:           "Wire bus BuilderEvidenceV2 linked vectors",
}

// wirePublishedDomainVectorCoverage records cross-language vectors published
// as assets of the exact pinned Wire release when Node owns either the producer
// or no producer at all. They are not copied into Node merely to satisfy this
// scan; the release pin is their byte-level authority.
var wirePublishedDomainVectorCoverage = map[string]string{
	shared.DomainBridgeDeploymentManifestV1: "Wire testdata/v1/hub/bridge_vrf_v1.json",
	shared.DomainPrefillTokenMetricLeafV2:   "Wire testdata/v1/task/metric_leaf_v2.json",
	shared.DomainBurnBondV1:                 "Wire testdata/v1/hub/bridge_vrf_v1.json",
	shared.DomainMintBondV1:                 "Wire testdata/v1/hub/bridge_vrf_v1.json",
	shared.DomainOutputChunkV1:              "Wire testdata/v1/task/output_mmr_v1.json",
	// Neither domain has a producer here: the stream terminator is signed by the
	// selected Worker's service key and the NATS binding by Cortex's.
	"TRUEOPEN_OUTPUT_FIN_V1":                       "Wire testdata/v1/task/output_mmr_v1.json",
	"TRUEOPEN_NATS_USER_BINDING_V1":                "Wire testdata/v1/bus/nats_user_binding_v1_vectors.json",
	shared.DomainUSDCRouteV1:                       "Wire testdata/v1/hub/bridge_vrf_v1.json",
	shared.DomainVRFKeyPoPV1:                       "Wire testdata/v1/hub/bridge_vrf_v1.json",
	"TRUEOPEN_BUILDER_STORAGE_CONFIRMATION_V1":     "Wire testdata/v1/task/builder_confirmation_v1.json",
	"TRUEOPEN_TASK_DATA_FETCH_BODY_V1":             "Wire testdata/v1/task/task_data_auth_v1.json",
	"TRUEOPEN_TASK_DATA_FINALIZE_RESULT_BODY_V1":   "Wire testdata/v1/task/task_data_auth_v1.json",
	"TRUEOPEN_TASK_DATA_FINALIZE_VERIFIER_BODY_V1": "Wire testdata/v1/task/task_data_auth_v1.json",
	"TRUEOPEN_TASK_DATA_METADATA_BODY_V1":          "Wire testdata/v1/task/task_data_auth_v1.json",
	"TRUEOPEN_TASK_DATA_REQUEST_V1":                "Wire testdata/v1/task/task_data_auth_v1.json",
	"TRUEOPEN_TASK_DATA_UPLOAD_BODY_V1":            "Wire testdata/v1/task/task_data_auth_v1.json",
}

// domainsCoveredWithoutFieldOrder is the gap between "a fixture names this domain"
// and "the differential above can check this domain's field order". It is frozen so
// the gap cannot grow silently.
//
// It is EMPTY. Its last entry was TRUEOPEN_TASK_BUILDER_RANK_V1, whose fixture
// published (seed, address) -> rank pairs with no per-vector preimage: the rank
// values were pinned and the field order was not, so swapping the producer's two
// fields would have moved every rank and agreed with a regenerated file. The
// fixture now publishes both named fields and its preimage.
var domainsCoveredWithoutFieldOrder = map[string]string{
	shared.DomainOutputChunkEquivocationV1: "Wire v0.4.1 fixture publishes linked equivocation cases without a top-level preimage_hex",
}

// TestDomainRegistryV1HFieldsVectorCoverageIsFrozen walks every registered H_FIELDS_V1
// domain and requires it to either have golden-vector coverage or be on the frozen
// allowlist above. Coverage means the domain literal appears as the value of a
// "domain" key in some testdata JSON: that is deliberately the loose definition,
// because the strict one is already enforced by the differential and a domain that no
// fixture even names is unambiguously unpinned.
func TestDomainRegistryV1HFieldsVectorCoverageIsFrozen(t *testing.T) {
	covered := fixtureDomainLiterals(t)

	// Build a sorted slice from the reason-bearing map so comparison remains
	// deterministic while every exception retains an actionable explanation.
	want := make([]string, 0, len(domainVectorCoverageAllowlistV1))
	for domain, reason := range domainVectorCoverageAllowlistV1 {
		require.NotEmpty(t, reason, "%s needs an actionable missing-vector reason", domain)
		want = append(want, domain)
	}
	sort.Strings(want)

	got := make([]string, 0, len(want))
	for _, spec := range shared.DomainRegistryV1 {
		if spec.Framing != shared.FramingHFieldsV1 {
			continue
		}
		if reason, ok := wireOwnedDomainVectorCoverage[spec.Domain]; ok {
			require.NotEmpty(t, reason, "%s needs an actionable external-vector reason", spec.Domain)
			continue
		}
		if reason, ok := wirePublishedDomainVectorCoverage[spec.Domain]; ok {
			require.NotEmpty(t, reason, "%s needs an actionable published-vector location", spec.Domain)
			continue
		}
		if _, ok := covered[spec.Domain]; ok {
			continue
		}
		got = append(got, spec.Domain)
	}
	sort.Strings(got)

	require.Equal(t, want, got,
		"the uncovered H_FIELDS_V1 set moved. A domain that appears only in got needs a golden vector (or, with a reason, a new allowlist entry); a domain that appears only in want has gained one and must be deleted from domainVectorCoverageAllowlistV1.")
}

// TestDomainVectorCoverageWithoutFieldOrderIsFrozen closes the remaining gap: a domain
// can be named by a fixture and still not be checked by the differential, because the
// fixture publishes no per-vector preimage. That is a weaker form of coverage and it
// is listed explicitly rather than counted as equivalent.
func TestDomainVectorCoverageWithoutFieldOrderIsFrozen(t *testing.T) {
	differential := make(map[string]struct{})
	for _, vector := range loadDomainGoldenVectors(t) {
		if vector.Domain == "" || vector.PreimageHex == "" {
			continue
		}
		spec, registered := shared.DomainSpecFor(vector.Domain)
		if !registered || spec.Framing != shared.FramingHFieldsV1 {
			continue
		}
		differential[vector.Domain] = struct{}{}
	}

	want := make([]string, 0, len(domainsCoveredWithoutFieldOrder))
	for domain, reason := range domainsCoveredWithoutFieldOrder {
		require.NotEmpty(t, reason, "%s must record why it is only weakly covered", domain)
		want = append(want, domain)
	}
	sort.Strings(want)

	got := make([]string, 0, len(want))
	for domain := range fixtureDomainLiterals(t) {
		spec, registered := shared.DomainSpecFor(domain)
		if !registered || spec.Framing != shared.FramingHFieldsV1 {
			continue
		}
		if _, ok := differential[domain]; ok {
			continue
		}
		got = append(got, domain)
	}
	sort.Strings(got)

	require.Equal(t, want, got,
		"a domain is named by a fixture but has no vector the differential can check")
	require.Len(t, differential, len(shared.DomainRegistryV1)-
		len(domainVectorCoverageAllowlistV1)-len(want)-len(wireOwnedDomainVectorCoverage)-
		len(wirePublishedDomainVectorCoverage)-domainRegistryNonHFieldsV1Count,
		"every H_FIELDS_V1 domain is exactly one of: checked by the differential, weakly covered, or allowlisted")
}

// domainRegistryNonHFieldsV1Count is the number of registered domains that use
// H_V1, MERKLE_ROOT_V1 or MMR_ROOT_V1 framing and are therefore out of this file's scope.
// TestDomainRegistryV1FramingCountsAreFrozen pins it against the registry so the
// arithmetic above cannot drift.
const domainRegistryNonHFieldsV1Count = 8

func TestDomainRegistryV1FramingCountsAreFrozen(t *testing.T) {
	counts := make(map[shared.Framing]int, 4)
	for _, spec := range shared.DomainRegistryV1 {
		counts[spec.Framing]++
	}
	require.Equal(t, len(shared.DomainRegistryV1)-domainRegistryNonHFieldsV1Count,
		counts[shared.FramingHFieldsV1])
	require.Equal(t, domainRegistryNonHFieldsV1Count,
		counts[shared.FramingHV1]+counts[shared.FramingMerkleRootV1]+counts[shared.FramingMMRRootV1])
}

// fixtureDomainLiterals collects every non-empty string that appears as the value of a
// "domain" key anywhere in any testdata JSON, mapped to the first file it was seen in.
// Walking the decoded document rather than a typed struct is what lets the scan see
// task_builder_rank_v1.json, whose domain is a top-level key rather than a per-vector
// one, and any future fixture shaped differently again.
func fixtureDomainLiterals(t *testing.T) map[string]string {
	t.Helper()
	found := make(map[string]string)
	for _, path := range domainFixturePaths(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var document any
		require.NoError(t, json.Unmarshal(raw, &document), "%s is not valid JSON", path)
		collectDomainLiterals(document, filepath.ToSlash(path), found)
	}
	return found
}

func collectDomainLiterals(node any, path string, found map[string]string) {
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if key == "domain" {
				if literal, ok := value.(string); ok && literal != "" {
					if _, seen := found[literal]; !seen {
						found[literal] = path
					}
				}
			}
			collectDomainLiterals(value, path, found)
		}
	case []any:
		for _, value := range typed {
			collectDomainLiterals(value, path, found)
		}
	}
}

func loadDomainGoldenVectors(t *testing.T) []domainGoldenVector {
	t.Helper()
	var vectors []domainGoldenVector
	for _, path := range domainFixturePaths(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var fixture struct {
			Vectors []domainGoldenVector `json:"vectors"`
		}
		require.NoError(t, json.Unmarshal(raw, &fixture), "%s does not decode as a vector fixture", path)
		for _, vector := range fixture.Vectors {
			vector.file = filepath.ToSlash(path)
			vectors = append(vectors, vector)
		}
	}
	return vectors
}

// domainFixturePaths returns every testdata JSON in the repository, sorted. The walk
// is over the whole tree rather than a fixed list so that a fixture added under a new
// module is picked up by both gates on the day it lands.
func domainFixturePaths(t *testing.T) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) != ".json" || filepath.Base(filepath.Dir(path)) != "testdata" {
			return nil
		}
		paths = append(paths, filepath.ToSlash(path))
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, paths, "no testdata fixtures found; the differential would pass vacuously")
	sort.Strings(paths)
	return paths
}

func domainGoldenHex(t *testing.T, where, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	require.NoError(t, err, "%s: invalid hex", where)
	require.Equal(t, value, hex.EncodeToString(raw), "%s: hex must be lowercase canonical form", where)
	return raw
}
