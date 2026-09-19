package types_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// queryPageFixturePath holds the language-independent vectors for the two §16.1
// page-token domains, TRUEOPEN_QUERY_RPC_V1 and TRUEOPEN_QUERY_SELECTOR_V1.
//
// Neither domain enters consensus state, and that is exactly why they needed
// cross-language vectors: a page token is minted by one implementation and
// presented back to another, so a non-Go client that frames its selector
// differently does not corrupt state, it just cannot page. Before this file the
// only published evidence was a few hex anchors scattered across per-RPC keeper
// contract tests, and several paginated RPCs had none at all.
//
// The file publishes one vector per RPC per domain: the RPC digest over the ASCII
// method literal, and the selector digest over (chain_id, rpc_method_digest, that
// RPC's own ordered selector fields). Both are bound to the real producers -
// shared.QueryRPCDigestV1 and shared.QueryPageDigestsV1 - by feeding the vector's
// own typed inputs in and demanding the pinned digest back.
//
// Regenerating: run the package tests with TRUEOPEN_REGEN_FIXTURES=1. The run
// rewrites the derived hex columns and then fails on purpose, because a
// regenerated consensus fixture must be diffed against the frozen contract before
// it is committed.
const (
	queryPageFixturePath   = "testdata/query_page_v1.json"
	queryPageFixtureSchema = "trueopen-query-page-domains-v1"
	queryPageRegenEnv      = "TRUEOPEN_REGEN_FIXTURES"
)

type queryPageFixture struct {
	Schema  string            `json:"schema"`
	Source  string            `json:"source"`
	Notes   []string          `json:"notes"`
	Vectors []queryPageVector `json:"vectors"`
}

type queryPageVector struct {
	Name            string `json:"name"`
	Domain          string `json:"domain"`
	Framing         string `json:"framing"`
	ContractSection string `json:"contract_section"`
	Producer        string `json:"producer"`
	// Variant is the fully-qualified method literal. It is the registry
	// discriminator value for a TRUEOPEN_QUERY_SELECTOR_V1 vector; a
	// TRUEOPEN_QUERY_RPC_V1 vector leaves it empty, because that domain has one
	// field order and the method is the field rather than the selector of one.
	Variant string `json:"variant,omitempty"`
	// RPCMethod is the method literal every vector here is about, spelled once as
	// a typed input rather than parsed back out of fields[]. For the RPC domain it
	// is also the framed value of fields[0].
	RPCMethod   string               `json:"rpc_method"`
	ChainID     string               `json:"chain_id,omitempty"`
	Fields      []queryPageField     `json:"fields"`
	PreimageHex string               `json:"preimage_hex"`
	DigestHex   string               `json:"digest_hex"`
	Tamper      []queryPageTamper    `json:"tamper"`
	Replay      []queryPageReplay    `json:"replay,omitempty"`
	Reject      []queryPageRejection `json:"reject,omitempty"`
}

type queryPageField struct {
	Name   string  `json:"name"`
	Type   string  `json:"type"`
	Hex    string  `json:"hex,omitempty"`
	UTF8   *string `json:"utf8,omitempty"`
	Value  *uint64 `json:"value,omitempty"`
	Bech32 string  `json:"bech32,omitempty"`
}

type queryPageTamper struct {
	Name      string `json:"name"`
	Field     int    `json:"field"`
	Byte      int    `json:"byte"`
	Bit       int    `json:"bit"`
	DigestHex string `json:"digest_hex"`
}

// queryPageReplay covers the two classes a page token has to survive: the same
// selector presented under a different chain, and a token minted for one RPC
// presented to another. Both are expressed as a whole replacement field rather
// than a bit flip, because that is what an attacker actually has.
type queryPageReplay struct {
	Name      string `json:"name"`
	Reason    string `json:"reason"`
	Field     int    `json:"field"`
	Value     string `json:"value"`
	DigestHex string `json:"digest_hex"`
}

// queryPageRejection pins an input the producer must refuse rather than hash. An
// unregistered RPC and a selector list of the wrong length are the two ways a
// caller can ask for a digest no handler will ever reproduce.
type queryPageRejection struct {
	Name          string `json:"name"`
	RPCMethod     string `json:"rpc_method"`
	SelectorCount int    `json:"selector_count"`
	Reason        string `json:"reason"`
}

func (f queryPageField) encode(t *testing.T, where string) []byte {
	t.Helper()
	switch f.Type {
	case "bytes":
		return queryPageHex(t, where, f.Hex)
	case "address":
		// Frames the address codec bytes, never the Bech32 text (Ruling 24). The
		// Bech32 column is a self-check: a non-Go implementation validates its own
		// decoder here instead of discovering a broken one as a digest mismatch.
		raw := queryPageHex(t, where, f.Hex)
		require.NotEmpty(t, f.Bech32, "%s must record the Bech32 text", where)
		hrp, decoded, err := bech32.DecodeAndConvert(f.Bech32)
		require.NoError(t, err, "%s must be a decodable Bech32 address", where)
		reencoded, err := bech32.ConvertAndEncode(hrp, decoded)
		require.NoError(t, err)
		require.Equal(t, f.Bech32, reencoded, "%s must be canonical Bech32", where)
		require.Equal(t, f.Hex, hex.EncodeToString(decoded),
			"%s: the Bech32 text must decode to the recorded address codec bytes", where)
		return raw
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
	case "enum":
		require.NotNil(t, f.Value, "%s requires value", where)
		require.LessOrEqual(t, *f.Value, uint64(^uint32(0)), "%s overflows the enum encoding", where)
		return shared.EnumBE(uint32(*f.Value))
	default:
		t.Fatalf("%s has unknown fixture field type %q", where, f.Type)
		return nil
	}
}

func (v queryPageVector) encodeFields(t *testing.T) [][]byte {
	t.Helper()
	encoded := make([][]byte, 0, len(v.Fields))
	for index, field := range v.Fields {
		encoded = append(encoded, field.encode(t, fmt.Sprintf("%s fields[%d] (%s)", v.Name, index, field.Name)))
	}
	return encoded
}

func (v queryPageVector) preimage(fields [][]byte) []byte {
	return shared.CanonicalFrameBytes(append([][]byte{[]byte(v.Domain)}, fields...)...)
}

func (v queryPageVector) digestOf(fields [][]byte) string {
	sum := sha256.Sum256(v.preimage(fields))
	return hex.EncodeToString(sum[:])
}

// TestQueryPageFixtureGoldenVectors pins each vector's ordered preimage and
// digest and checks the registry row that owns it, including the per-RPC variant.
func TestQueryPageFixtureGoldenVectors(t *testing.T) {
	fixture := loadQueryPageFixture(t)
	seen := make(map[string]string, len(fixture.Vectors))

	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			previous, duplicate := seen[vector.Name]
			require.False(t, duplicate, "%s is already used by %s", vector.Name, previous)
			seen[vector.Name] = vector.Name

			spec, registered := shared.DomainSpecFor(vector.Domain)
			require.True(t, registered, "%s must be present in DomainRegistryV1", vector.Domain)
			require.Equal(t, vector.Framing, spec.Framing.String())
			require.Equal(t, vector.Domain, shared.MustDomain(vector.Domain))
			require.NotEmpty(t, vector.ContractSection)
			require.NotEmpty(t, vector.Producer)

			_, paginated := shared.QueryPageSelectorSchemaV1[vector.RPCMethod]
			require.True(t, paginated,
				"%s is about %q, which is not a registered paginated Query RPC", vector.Name, vector.RPCMethod)

			fields := vector.encodeFields(t)
			require.Equal(t, vector.PreimageHex, hex.EncodeToString(vector.preimage(fields)))
			require.Equal(t, vector.DigestHex, vector.digestOf(fields))
			require.Len(t, vector.DigestHex, 64)
		})
	}
}

// TestQueryPageFixtureCoversEveryPaginatedRPC is the fixture half of the freeze
// the repository-root test starts. The schema, the registry variants and the
// proto descriptors already agree on which RPCs exist; this says both domains
// have a vector for every one of them, so no method can be paginated in
// production and unpinned here.
func TestQueryPageFixtureCoversEveryPaginatedRPC(t *testing.T) {
	fixture := loadQueryPageFixture(t)

	want := make([]string, 0, len(shared.QueryPageSelectorSchemaV1))
	for rpcMethod := range shared.QueryPageSelectorSchemaV1 {
		want = append(want, rpcMethod)
	}
	sort.Strings(want)

	byDomain := map[string][]string{}
	for _, vector := range fixture.Vectors {
		byDomain[vector.Domain] = append(byDomain[vector.Domain], vector.RPCMethod)
	}
	for _, domain := range []string{shared.DomainQueryRPCV1, shared.DomainQuerySelectorV1} {
		got := append([]string(nil), byDomain[domain]...)
		sort.Strings(got)
		require.Equal(t, want, got, "%s must have exactly one vector per paginated RPC", domain)
	}

	// Every published digest is distinct. For TRUEOPEN_QUERY_RPC_V1 that is the
	// domain's entire job - method literals must not collide - and for the
	// selectors it is what makes the cross-RPC replay class meaningful.
	byDigest := make(map[string]string, len(fixture.Vectors))
	for _, vector := range fixture.Vectors {
		previous, collided := byDigest[vector.DigestHex]
		require.False(t, collided, "%s and %s publish the same digest", vector.Name, previous)
		byDigest[vector.DigestHex] = vector.Name
	}
}

// TestQueryPageFixtureMatchesProductionHelpers is the value binding: the vector's
// own typed inputs go into the real producer and the pinned digest has to come
// back. Re-framing fields[] and hashing the result, which the golden test above
// does, only proves the file agrees with itself.
func TestQueryPageFixtureMatchesProductionHelpers(t *testing.T) {
	fixture := loadQueryPageFixture(t)
	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			switch vector.Domain {
			case shared.DomainQueryRPCV1:
				require.Empty(t, vector.Variant,
					"TRUEOPEN_QUERY_RPC_V1 has one field order; the method is its field, not a discriminator")
				require.Len(t, vector.Fields, 1)
				require.Equal(t, "rpc_method", vector.Fields[0].Name)
				require.NotNil(t, vector.Fields[0].UTF8)
				require.Equal(t, vector.RPCMethod, *vector.Fields[0].UTF8,
					"the framed literal and the vector's rpc_method must be the same string")

				digest, err := shared.QueryRPCDigestV1(vector.RPCMethod)
				require.NoError(t, err)
				require.Equal(t, vector.DigestHex, hex.EncodeToString(digest))

			case shared.DomainQuerySelectorV1:
				require.Equal(t, vector.RPCMethod, vector.Variant,
					"the selector variant is the RPC the vector is about")
				schema := shared.QueryPageSelectorSchemaV1[vector.RPCMethod]
				require.Len(t, vector.Fields, len(schema.Fields)+2,
					"a selector vector frames chain_id, the RPC digest and this RPC's selector fields")
				require.Equal(t, "chain_id", vector.Fields[0].Name)
				require.NotNil(t, vector.Fields[0].UTF8)
				require.Equal(t, vector.ChainID, *vector.Fields[0].UTF8)
				require.Equal(t, "rpc_method_digest", vector.Fields[1].Name)
				for index, name := range schema.Fields {
					require.Equal(t, name, vector.Fields[index+2].Name,
						"selector field %d must be the one QueryPageSelectorSchemaV1 registers", index)
				}

				encoded := vector.encodeFields(t)
				rpcDigest, selectorDigest, err := shared.QueryPageDigestsV1(vector.ChainID, vector.RPCMethod, encoded[2:]...)
				require.NoError(t, err)
				require.Equal(t, vector.Fields[1].Hex, hex.EncodeToString(rpcDigest),
					"the framed rpc_method_digest must be the digest the producer derives from the same method")
				require.Equal(t, vector.DigestHex, hex.EncodeToString(selectorDigest))

			default:
				t.Fatalf("%s carries unexpected domain %s", vector.Name, vector.Domain)
			}
		})
	}
}

// TestQueryPageFixtureRejectsEveryFieldBitFlip is the derived exhaustive gate:
// flipping any single bit of any framed field must move the digest. It computes
// its own expectations, so it stays true for a vector added without a published
// tamper section.
func TestQueryPageFixtureRejectsEveryFieldBitFlip(t *testing.T) {
	fixture := loadQueryPageFixture(t)
	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			base := vector.encodeFields(t)
			require.Equal(t, vector.DigestHex, vector.digestOf(base))

			flipped := 0
			for index, field := range vector.Fields {
				for byteIndex := range base[index] {
					for bit := 0; bit < 8; bit++ {
						mutated := queryPageFlipBit(base, index, byteIndex, bit)
						require.NotEqual(t, vector.DigestHex, vector.digestOf(mutated),
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

// TestQueryPageFixtureTamperVectors checks the published per-field tamper
// goldens, which exist so another implementation can validate its own bit-flip
// handling against numbers it did not compute.
func TestQueryPageFixtureTamperVectors(t *testing.T) {
	fixture := loadQueryPageFixture(t)
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
				got := vector.digestOf(queryPageFlipBit(base, tamper.Field, tamper.Byte, tamper.Bit))
				require.Equal(t, tamper.DigestHex, got, "tamper %q", tamper.Name)
				previous, collided := seen[got]
				require.False(t, collided, "tamper %q collides with %s", tamper.Name, previous)
				seen[got] = tamper.Name
			}
			for index, field := range vector.Fields {
				_, ok := covered[index]
				require.True(t, ok, "field %d (%s) has no published tamper vector", index, field.Name)
			}
		})
	}
}

// TestQueryPageFixtureRejectsReplay checks the published cross-chain and
// cross-RPC replay goldens on the selector vectors. A selector digest that did
// not move under either substitution would let one page token page another
// chain's or another RPC's rows.
func TestQueryPageFixtureRejectsReplay(t *testing.T) {
	fixture := loadQueryPageFixture(t)
	for _, vector := range fixture.Vectors {
		if vector.Domain != shared.DomainQuerySelectorV1 {
			require.Empty(t, vector.Replay,
				"%s: a single-field RPC vector has no replay class the tamper gate does not already cover", vector.Name)
			continue
		}
		t.Run(vector.Name, func(t *testing.T) {
			base := vector.encodeFields(t)
			expectedRows := 1
			if queryPageHasSameArityPeer(vector.RPCMethod) {
				expectedRows = 2
			}
			require.Len(t, vector.Replay, expectedRows,
				"every selector publishes cross-chain replay and cross-RPC replay when a same-arity peer exists")
			seen := map[string]string{vector.DigestHex: "base"}
			replayed := make(map[int]struct{}, 2)

			for _, replay := range vector.Replay {
				require.NotEmpty(t, replay.Reason, "replay %q needs a reason", replay.Name)
				require.Less(t, replay.Field, 2, "the two replay classes substitute chain_id or rpc_method_digest")
				replayed[replay.Field] = struct{}{}

				mutated := make([][]byte, len(base))
				copy(mutated, base)
				if replay.Field == 0 {
					require.NotEqual(t, vector.ChainID, replay.Value, "a cross-chain replay needs a different chain")
					mutated[0] = []byte(replay.Value)
				} else {
					other, known := shared.QueryPageSelectorSchemaV1[replay.Value]
					require.True(t, known, "replay %q must borrow a registered RPC", replay.Name)
					require.NotEqual(t, vector.RPCMethod, replay.Value, "a cross-RPC replay needs a different RPC")
					require.Len(t, other.Fields, len(shared.QueryPageSelectorSchemaV1[vector.RPCMethod].Fields),
						"replay %q borrows an RPC with the same selector arity, so only the RPC digest differs and the digest change cannot be explained by a length change",
						replay.Name)
					borrowed, err := shared.QueryRPCDigestV1(replay.Value)
					require.NoError(t, err)
					mutated[1] = borrowed
				}

				got := vector.digestOf(mutated)
				require.Equal(t, replay.DigestHex, got, "replay %q", replay.Name)
				previous, collided := seen[got]
				require.False(t, collided, "replay %q collides with %s", replay.Name, previous)
				seen[got] = replay.Name
			}
			require.Len(t, replayed, expectedRows, "replay rows must substitute each applicable scope field")
		})
	}
}

func queryPageHasSameArityPeer(rpcMethod string) bool {
	want := len(shared.QueryPageSelectorSchemaV1[rpcMethod].Fields)
	for candidate, schema := range shared.QueryPageSelectorSchemaV1 {
		if candidate != rpcMethod && len(schema.Fields) == want {
			return true
		}
	}
	return false
}

// TestQueryPageFixtureRejectionsAreRefusedByTheProducer executes the published
// negative cases. They are the reason QueryPageDigestsV1 consults the schema at
// all: an unregistered method or a selector list of the wrong length is a page
// token scope no handler can reproduce, so the producer refuses instead of
// hashing something nobody will ever agree with.
func TestQueryPageFixtureRejectionsAreRefusedByTheProducer(t *testing.T) {
	fixture := loadQueryPageFixture(t)
	rejections := 0
	for _, vector := range fixture.Vectors {
		for _, rejection := range vector.Reject {
			rejections++
			t.Run(vector.Name+"/"+rejection.Name, func(t *testing.T) {
				require.NotEmpty(t, rejection.Reason)
				selectors := make([][]byte, rejection.SelectorCount)
				for i := range selectors {
					selectors[i] = []byte{0}
				}
				_, _, err := shared.QueryPageDigestsV1(vector.ChainID, rejection.RPCMethod, selectors...)
				require.Error(t, err, "%s must be refused: %s", rejection.Name, rejection.Reason)
			})
		}
	}
	require.Positive(t, rejections, "the fixture must publish the producer's refusals, not only its successes")

	// The same refusal on the RPC-digest side, which has no selector list to get
	// wrong and therefore only one way to be asked for the impossible.
	_, err := shared.QueryRPCDigestV1("/hub.v1.Query/Model")
	require.Error(t, err, "QueryModel is not paginated and must not be given an RPC digest")
}

func queryPageFlipBit(base [][]byte, field, byteIndex, bit int) [][]byte {
	mutated := make([][]byte, len(base))
	for i := range base {
		mutated[i] = append([]byte(nil), base[i]...)
	}
	mutated[field][byteIndex] ^= 1 << bit
	return mutated
}

func queryPageHex(t *testing.T, where, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	require.NoError(t, err, "%s must be lowercase hex", where)
	require.Equal(t, value, hex.EncodeToString(raw), "%s must be canonical lowercase hex", where)
	return raw
}

func loadQueryPageFixture(t *testing.T) queryPageFixture {
	t.Helper()
	raw, err := os.ReadFile(queryPageFixturePath)
	require.NoError(t, err)
	var fixture queryPageFixture
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&fixture), "%s must decode with no unknown keys", queryPageFixturePath)
	require.Equal(t, queryPageFixtureSchema, fixture.Schema)
	require.NotEmpty(t, fixture.Source)
	require.NotEmpty(t, fixture.Notes)
	require.NotEmpty(t, fixture.Vectors)
	if os.Getenv(queryPageRegenEnv) == "1" {
		regenerateQueryPageFixture(t, fixture)
	}
	return fixture
}

// regenerateQueryPageFixture recomputes only the DERIVED columns: the RPC digest
// a selector vector frames, the Bech32 self-check next to an address, and every
// preimage, digest, tamper and replay hex.
//
// The inputs stay hand-written. Generating fields[] from
// QueryPageSelectorSchemaV1 would be less work and strictly worse: the root
// differential compares this file's field names against the registry row, and a
// fixture derived from the same table would make that comparison agree with
// itself. The names below are a second statement of the selector order, which is
// the only reason comparing them means anything.
func regenerateQueryPageFixture(t *testing.T, fixture queryPageFixture) {
	t.Helper()
	for i := range fixture.Vectors {
		vector := &fixture.Vectors[i]
		for j := range vector.Fields {
			field := &vector.Fields[j]
			switch {
			case field.Name == "rpc_method_digest":
				digest, err := shared.QueryRPCDigestV1(vector.RPCMethod)
				require.NoError(t, err)
				field.Hex = hex.EncodeToString(digest)
			case field.Type == "address":
				encoded, err := bech32.ConvertAndEncode("trueopen", queryPageHex(t, field.Name, field.Hex))
				require.NoError(t, err)
				field.Bech32 = encoded
			}
		}

		base := vector.encodeFields(t)
		vector.PreimageHex = hex.EncodeToString(vector.preimage(base))
		vector.DigestHex = vector.digestOf(base)

		for j := range vector.Tamper {
			tamper := &vector.Tamper[j]
			tamper.DigestHex = vector.digestOf(queryPageFlipBit(base, tamper.Field, tamper.Byte, tamper.Bit))
		}
		for j := range vector.Replay {
			replay := &vector.Replay[j]
			mutated := make([][]byte, len(base))
			copy(mutated, base)
			if replay.Field == 0 {
				mutated[0] = []byte(replay.Value)
			} else {
				borrowed, err := shared.QueryRPCDigestV1(replay.Value)
				require.NoError(t, err)
				mutated[1] = borrowed
			}
			replay.DigestHex = vector.digestOf(mutated)
		}
	}
	encoded, err := json.MarshalIndent(fixture, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(queryPageFixturePath, append(encoded, '\n'), 0o644))
	t.Fatalf("regenerated %s; unset %s and review the diff against §16.1 before committing",
		queryPageFixturePath, queryPageRegenEnv)
}
