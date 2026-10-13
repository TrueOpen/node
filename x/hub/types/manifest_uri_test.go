package types

import (
	"bytes"
	"encoding/base32"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type manifestURIFixture struct {
	Schema   string `json:"schema"`
	MaxBytes uint32 `json:"max_manifest_uri_bytes"`
	Accepted []struct {
		URI  string `json:"uri"`
		Case string `json:"case"`
	} `json:"accepted"`
	Rejected []struct {
		URI    string `json:"uri"`
		Reason string `json:"reason"`
	} `json:"rejected"`
}

func loadManifestURIFixture(t *testing.T) manifestURIFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/manifest_uri_v1.json")
	require.NoError(t, err)
	var fixture manifestURIFixture
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Equal(t, "trueopen-manifest-uri-v1", fixture.Schema)
	require.NotEmpty(t, fixture.Accepted)
	require.NotEmpty(t, fixture.Rejected)
	return fixture
}

// TestManifestURIFixture runs the validator over every accepted and rejected
// form published in wire testdata/v1/hub/manifest_uri_v1.json.
func TestManifestURIFixture(t *testing.T) {
	fixture := loadManifestURIFixture(t)
	require.Equal(t, DefaultHubParams().Model.MaxManifestUriBytes, fixture.MaxBytes)

	longest := 0
	for _, c := range fixture.Accepted {
		require.NoError(t, ValidateManifestURI(c.URI, fixture.MaxBytes), "accepted %q (%s)", c.URI, c.Case)
		longest = max(longest, len(c.URI))
	}
	require.Equal(t, int(fixture.MaxBytes), longest, "an accepted URI sits exactly at max_manifest_uri_bytes")
	for _, c := range fixture.Rejected {
		require.NotEmpty(t, c.Reason)
		want, ok := manifestURIRejectionChecks[c.Reason]
		require.True(t, ok, "no expected check recorded for fixture reason %q", c.Reason)
		require.ErrorContains(t, ValidateManifestURI(c.URI, fixture.MaxBytes), want, "rejected %q (%s)", c.URI, c.Reason)
	}
}

// manifestURIRejectionChecks names the check expected to refuse each rejected
// fixture case, so a rule that silently stops firing is caught even when a
// later check would still reject the same URI.
var manifestURIRejectionChecks = map[string]string{
	"empty":                                "length 0 outside",
	"one byte over max_manifest_uri_bytes": "length 2049 outside",
	"http is not an allowed scheme":        "scheme must be exactly",
	"scheme must be lowercase":             "scheme must be exactly",
	"unknown scheme":                       "scheme must be exactly",
	"userinfo":                             "userinfo is not allowed",
	"fragment":                             "fragment is not allowed",
	"space":                                "not printable ASCII",
	"control character":                    "not printable ASCII",
	"non-ASCII byte":                       "not printable ASCII",
	"missing host":                         "missing host",
	"uppercase host":                       "must be lowercase letters",
	"trailing dot in host":                 "ends in a dot",
	"single-label host":                    "not a fully qualified name",
	"label starts with a hyphen":           "is malformed",
	"IPv4 octet with a leading zero":       "not canonical decimal 0..255",
	"IPv4 octet out of range":              "not canonical decimal 0..255",
	"IPv4 literal with three octets":       "needs four octets",
	"IPv6 literal not in canonical lowercase form": "not in canonical form",
	"IPv6 literal not compressed":                  "not in canonical form",
	"IPv4-mapped IPv6 literal; its canonical text is the dotted IPv4 form, so use the IPv4 literal": "not in canonical form",
	"IPv4-mapped IPv6 literal in hex groups; its canonical text is the dotted IPv4 form":            "not in canonical form",
	"IPv6 literal with a dotted IPv4 tail; canonical form uses hex groups":                          "not in canonical form",
	"IPv4-compatible IPv6 literal with a dotted IPv4 tail; canonical form uses hex groups":          "not in canonical form",
	"IPv6: a shorter zero run compressed instead of the longest":                                    "not in canonical form",
	"IPv6: of two equal zero runs the second is compressed":                                         "not in canonical form",
	"IPv6: a single zero group compressed to ::":                                                    "not in canonical form",
	"IPv6 group with a leading zero":                                                                "not in canonical form",
	"all-digit top-level label":                                                                     "is all digits",
	"label ends with a hyphen":                                                                      "is malformed",
	"64-byte label":                                                                                 "is malformed",
	"non-digit after the port":                                                                      "is not canonical decimal",
	"IPv6 zone":                                                                                     "IPv6 zone is not allowed",
	"port after the IPv6 literal without a colon":                                                   "is not a :port",
	"characters after the IPv6 literal that are not :port":                                          "is not a :port",
	"empty port after the IPv6 literal":                                                             "is not canonical decimal",
	"second closing bracket after the IPv6 literal":                                                 "is not a :port",
	"port 0":                                                  "is not canonical decimal",
	"port with a leading zero":                                "is not canonical decimal",
	"port out of range":                                       "outside 1..65535",
	"lowercase percent-encoding":                              "uppercase hex",
	"truncated percent-encoding":                              "uppercase hex",
	"character outside RFC 3986":                              "is not allowed",
	"CIDv0 of the wrong length":                               "CIDv0 must be 46 characters",
	"CIDv0 with non-base58 characters":                        "is not base58btc",
	"uppercase CIDv1":                                         "CID must be CIDv0",
	"multibase prefix other than b":                           "CID must be CIDv0",
	"CIDv1 whose multihash length does not match its digest":  "base32 is not canonical",
	"CID version encoded as a non-minimal varint":             "CID version: not a minimal varint 1",
	"content codec encoded as a non-minimal varint":           "CIDv1 codec: varint is not minimally encoded",
	"multihash function code encoded as a non-minimal varint": "multihash code: varint is not minimally encoded",
	"multihash digest length encoded as a non-minimal varint": "multihash length: varint is not minimally encoded",
	"ipfs URI with a query":                                   "ipfs URI does not take a query",
	"missing CID":                                             "CID must be CIDv0",
}

// TestManifestURIChecksBeyondFixture covers rules the published fixture never
// reaches first: each URI is refused by exactly the named check.
func TestManifestURIChecksBeyondFixture(t *testing.T) {
	digest := bytes.Repeat([]byte{0xab}, 32)
	cidv1 := func(raw ...[]byte) string {
		return "ipfs://b" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(bytes.Join(raw, nil)))
	}
	cidv0 := func(raw []byte) string {
		const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
		n := new(big.Int).SetBytes(raw)
		var out []byte
		for n.Sign() > 0 {
			mod := new(big.Int)
			n.DivMod(n, big.NewInt(58), mod)
			out = append([]byte{alphabet[mod.Int64()]}, out...)
		}
		return "ipfs://" + string(out)
	}
	// Sanity: the builders produce accepted CIDs when the content is right.
	require.NoError(t, ValidateManifestURI(cidv1([]byte{0x01, 0x55, 0x12, 0x20}, digest), 2048))
	require.NoError(t, ValidateManifestURI(cidv0(append([]byte{0x12, 0x20}, digest...)), 2048))

	for _, c := range []struct{ uri, want string }{
		{"https://models.example.123/m.json", "is all digits"},
		{"ipfs://bAFYBEIGDYRZT5SFP7UDM7HU76UH7Y26NF3EFUYLQABF3OCLGTQY55FBZDI", "lowercase unpadded base32"},
		{"https://[2001:db8::1%eth0]/m.json", "IPv6 zone is not allowed"},
		{"https://[1.2.3.4]/m.json", "is not an IPv6 literal"},
		{cidv0(append([]byte{0x12, 0x21}, digest...)), "not a sha2-256 multihash"},
		{cidv1([]byte{0x02, 0x55, 0x12, 0x20}, digest), "CID version: not a minimal varint 1"},
		{cidv1([]byte{0x01, 0x55, 0x12, 0x21}, digest), "multihash length does not match"},
		{cidv1([]byte{0x01, 0x55, 0x12, 0x00}), "multihash length does not match"},
		{cidv1([]byte{0x01}, bytes.Repeat([]byte{0x80}, 9), []byte{0x01, 0x12, 0x20}, digest), "varint longer than 9 bytes"},
	} {
		require.ErrorContains(t, ValidateManifestURI(c.uri, 2048), c.want, c.uri)
	}
}

// TestManifestURILengthFollowsParam checks that the length cap is the param
// value, not a constant: the longest accepted fixture URI fails one byte under it.
func TestManifestURILengthFollowsParam(t *testing.T) {
	fixture := loadManifestURIFixture(t)
	for _, c := range fixture.Accepted {
		if uint32(len(c.URI)) != fixture.MaxBytes {
			continue
		}
		require.NoError(t, ValidateManifestURI(c.URI, fixture.MaxBytes))
		require.ErrorContains(t, ValidateManifestURI(c.URI, fixture.MaxBytes-1), "outside 1..")
		return
	}
	t.Fatal("fixture has no accepted URI at max_manifest_uri_bytes")
}

// TestProjectionManifestURIIsLiteral pins the golden projection's manifest_uri:
// it is valid, and its "&" is written literally (no HTML escaping) and byte for
// byte as registered.
func TestProjectionManifestURIIsLiteral(t *testing.T) {
	projection := goldenModelProfileProjection(t)
	require.NoError(t, ValidateManifestURI(projection.ManifestUri, DefaultHubParams().Model.MaxManifestUriBytes))
	canonical, err := CanonicalModelProfileProjection(projection)
	require.NoError(t, err)
	require.Contains(t, string(canonical), `"manifest_uri":"`+projection.ManifestUri+`"`)
	require.Contains(t, projection.ManifestUri, "&")
	require.NotContains(t, string(canonical), "\\u0026")
}

// TestManifestURIEntersRegistrationDigest checks that manifest_uri is covered
// by both the projection hash and the registration digest.
func TestManifestURIEntersRegistrationDigest(t *testing.T) {
	projection := goldenModelProfileProjection(t)
	const chainID, proposer = "trueopen-golden-1", "trueopen1rfjz7r3u8t65teavh5utquj3kwvsj983p3jclz"
	digest, canonical, err := ModelRegistrationDigest(chainID, proposer, projection)
	require.NoError(t, err)

	projection.ManifestUri = strings.Replace(projection.ManifestUri, "rev=3", "rev=4", 1)
	changedDigest, changedCanonical, err := ModelRegistrationDigest(chainID, proposer, projection)
	require.NoError(t, err)
	require.NotEqual(t, canonical, changedCanonical)
	require.NotEqual(t, digest, changedDigest)
}

func TestMaxManifestURIBytesParam(t *testing.T) {
	params := DefaultHubParams()
	require.Equal(t, uint32(2048), params.Model.MaxManifestUriBytes)
	require.NoError(t, params.Validate())

	zero := DefaultHubParams()
	zero.Model.MaxManifestUriBytes = 0
	require.ErrorContains(t, zero.Validate(), "model parameters are invalid")

	changed := DefaultHubParams()
	changed.Model.MaxManifestUriBytes = 4096
	field, ok := GenesisOnlyHubParamsChanged(params, changed)
	require.True(t, ok)
	require.Equal(t, "model.validation_limits", field.Name)

	baseline, err := HubParamsHash("trueopen-test", 1, params)
	require.NoError(t, err)
	changedHash, err := HubParamsHash("trueopen-test", 1, changed)
	require.NoError(t, err)
	require.NotEqual(t, baseline, changedHash)
}
