package node

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
	protov2 "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// wire/wire.binpb is the descriptor image published by the pinned TrueOpen/wire
// release, and it is the generation input for every protobuf source in this
// repository. proto/ was deleted when generation moved to it, so the contract now
// has exactly one source.
//
// That makes the pin the only thing standing between the build and a silently
// different contract: a swapped blob would change every generated file, and a
// pin that misdescribes the release's package scope would make the Makefile's
// --exclude-path either drop something the release publishes or admit something
// it withholds. The three rules below close those.
//
// Deliberately not checked here: whether the contract itself lints, and whether
// it is backward compatible. Both are TrueOpen/wire's CI, which runs buf lint
// over the whole module and buf breaking against the previous release before an
// image is ever published. Re-checking a published image would only re-verify
// bytes that were already verified upstream.
const (
	wirePinPath      = "wire/pin.json"
	wirePinSchemaV1  = "trueopen-node-wire-pin-v1"
	wireMakefilePath = "Makefile"
	wireGoModulePath = "github.com/TrueOpen/wire"
)

type wirePin struct {
	Schema           string            `json:"schema"`
	Notes            []string          `json:"notes"`
	SourceRepository string            `json:"source_repository"`
	ReleaseTag       string            `json:"release_tag"`
	ReleaseCommit    string            `json:"release_commit"`
	Descriptor       wirePinArtifact   `json:"descriptor"`
	DomainRegistry   wirePinArtifact   `json:"domain_registry"`
	ReleasedPackages []string          `json:"released_packages"`
	WithheldPackages []wirePinWithheld `json:"withheld_packages"`
}

type wirePinArtifact struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type wirePinWithheld struct {
	Package string `json:"package"`
	Reason  string `json:"reason"`
}

var (
	wireCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	wireDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	wireTagPattern    = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
	// wireExcludeLine reads WIRE_EXCLUDE out of the Makefile so the pin and the
	// generation command cannot describe two different scopes. Restating the
	// excluded set here would be the second statement this test exists to prevent.
	wireExcludeLine = regexp.MustCompile(`(?m)^WIRE_EXCLUDE :=[ \t]*(.*)\r?$`)
	wireExcludePath = regexp.MustCompile(`--exclude-path ([a-z0-9_]+)`)
)

// TestWirePinMatchesTheVendoredImage is rule 1: the vendored blob is the one the
// pin names. The expected digest is checkable rather than assumed because the
// wire release publishes release-manifest.json naming the same value.
func TestWirePinMatchesTheVendoredImage(t *testing.T) {
	pin := loadWirePin(t)

	require.Equal(t, "wire/wire.binpb", pin.Descriptor.Path,
		"the Makefile's WIRE_IMAGE and the pin must name the same file")
	require.Equal(t, "registry/v1/domains.json", pin.DomainRegistry.Path)
	for _, artifact := range []wirePinArtifact{pin.Descriptor, pin.DomainRegistry} {
		body, err := os.ReadFile(artifact.Path)
		require.NoError(t, err, "%s is a pinned wire release artifact and must be present", artifact.Path)
		require.Equal(t, artifact.Bytes, int64(len(body)),
			"%s is %d bytes but the pin records %d", artifact.Path, len(body), artifact.Bytes)
		sum := sha256.Sum256(body)
		require.Equal(t, artifact.SHA256, hex.EncodeToString(sum[:]),
			"%s does not hash to the digest pinned in %s", artifact.Path, wirePinPath)
	}
}

func TestWirePinGoModuleMatchesRelease(t *testing.T) {
	pin := loadWirePin(t)
	raw, err := os.ReadFile("go.mod")
	require.NoError(t, err)
	module, err := modfile.Parse("go.mod", raw, nil)
	require.NoError(t, err)

	version := ""
	for _, requirement := range module.Require {
		if requirement.Mod.Path == wireGoModulePath {
			version = requirement.Mod.Version
			break
		}
	}
	require.Equal(t, pin.ReleaseTag, version,
		"the bus runtime helper and vendored descriptor must come from the same Wire release")
	for _, replacement := range module.Replace {
		require.NotEqual(t, wireGoModulePath, replacement.Old.Path,
			"the pinned Wire module must not be redirected by a replace directive")
	}
}

// TestWirePinAccountsForEveryReleasedPackage is rule 2: the pin's description of
// the release scope has to match what the image actually contains, exactly once
// per package.
//
// It reads the packages out of the image rather than out of any local source,
// which is the point: there is no local source any more, so the image is the only
// place the scope can be observed.
// thirdPartyProtoPackagePrefixes are the import trees a buf image carries
// alongside the module it was built from. They are dependencies of the
// contract, not part of it.
var thirdPartyProtoPackagePrefixes = []string{
	"amino",
	"capability.",
	"cosmos.",
	"cosmos_proto",
	"gogoproto",
	"google.",
	"ics23.",
	"tendermint.",
}

func isThirdPartyProtoPackage(pkg string) bool {
	for _, prefix := range thirdPartyProtoPackagePrefixes {
		if strings.HasPrefix(pkg, prefix) {
			return true
		}
	}
	return false
}

func TestWirePinAccountsForEveryReleasedPackage(t *testing.T) {
	pin := loadWirePin(t)

	body, err := os.ReadFile(pin.Descriptor.Path)
	require.NoError(t, err)
	// A buf image is wire-compatible with FileDescriptorSet; its extra module
	// metadata decodes as unknown fields and is not needed here.
	var set descriptorpb.FileDescriptorSet
	require.NoError(t, protov2.Unmarshal(body, &set), "%s must decode as a FileDescriptorSet", pin.Descriptor.Path)

	present := make(map[string]struct{})
	for _, file := range set.File {
		pkg := file.GetPackage()
		// The image also carries the third-party imports it was built against,
		// which are dependencies rather than published contract; everything else
		// is release scope. Excluding the known third parties is what makes a
		// newly added wire package show up here instead of being skipped
		// silently: the contract package names are owned by wire and share no
		// prefix that could be matched on instead.
		if isThirdPartyProtoPackage(pkg) {
			continue
		}
		present[pkg] = struct{}{}
	}
	require.NotEmpty(t, present, "no contract package found in %s", pin.Descriptor.Path)

	released := append([]string(nil), pin.ReleasedPackages...)
	require.NotEmpty(t, released, "%s must record the released packages", wirePinPath)
	require.True(t, sort.StringsAreSorted(released), "released_packages must be sorted")

	// Disjointness first. Without it a package listed in BOTH lists still makes
	// the union equal the image's package set, so the comparison below would pass
	// while the pin claims the package is simultaneously published and withheld.
	seen := make(map[string]string, len(present))
	for _, pkg := range released {
		require.NotContains(t, seen, pkg, "%s lists %s twice", wirePinPath, pkg)
		seen[pkg] = "released"
	}
	for _, entry := range pin.WithheldPackages {
		where, exists := seen[entry.Package]
		require.False(t, exists, "%s both %s and withholds %s", wirePinPath, where, entry.Package)
		seen[entry.Package] = "withheld"
		// A withheld package with no reason is indistinguishable from one somebody
		// forgot to release.
		require.GreaterOrEqual(t, len(entry.Reason), 40,
			"withheld package %s needs a reason a reader can act on", entry.Package)
	}

	accounted := make([]string, 0, len(seen))
	for pkg := range seen {
		accounted = append(accounted, pkg)
	}
	found := make([]string, 0, len(present))
	for pkg := range present {
		found = append(found, pkg)
	}
	sort.Strings(accounted)
	sort.Strings(found)
	require.Equal(t, found, accounted,
		"%s must account for every package in the pinned image exactly once. A package in the first list only is published by the release and unaccounted for here; one in the second only is named here but absent from the release.",
		wirePinPath)
}

// TestWirePinWithheldPackagesAreExcludedFromGeneration is rule 3: the Makefile's
// WIRE_EXCLUDE and the pin's withheld list are the same set.
//
// This is what keeps --exclude-path honest. Withholding a package in the pin
// while still generating it would produce Go for a contract the release makes no
// promise about; excluding one the pin does not withhold would silently drop a
// released package from the build.
func TestWirePinWithheldPackagesAreExcludedFromGeneration(t *testing.T) {
	pin := loadWirePin(t)

	makefile, err := os.ReadFile(wireMakefilePath)
	require.NoError(t, err)
	line := wireExcludeLine.FindSubmatch(makefile)
	require.NotNil(t, line, "%s must define WIRE_EXCLUDE", wireMakefilePath)

	excluded := make(map[string]struct{})
	for _, match := range wireExcludePath.FindAllSubmatch(line[1], -1) {
		excluded[string(match[1])] = struct{}{}
	}

	withheldRoots := make(map[string]struct{}, len(pin.WithheldPackages))
	for _, entry := range pin.WithheldPackages {
		root := entry.Package
		if dot := strings.Index(root, "."); dot >= 0 {
			root = root[:dot]
		}
		withheldRoots[root] = struct{}{}
		_, ok := excluded[root]
		require.True(t, ok,
			"%s withholds %s but WIRE_EXCLUDE does not exclude %s, so generation would emit Go the release makes no promise about",
			wirePinPath, entry.Package, root)
	}
	for root := range excluded {
		_, ok := withheldRoots[root]
		require.True(t, ok,
			"WIRE_EXCLUDE drops %s from generation but %s withholds no %s package, so a released package is being silently skipped",
			root, wirePinPath, root)
	}
}

// TestNoLocalProtoSourcesRemain pins the cutover itself. proto/ was deleted so
// the contract has one source; a reappearing .proto here would be a second one,
// and because generation reads the image it would have no effect on the build
// while looking authoritative to a reader.
//
// internal/proto is deliberately exempt: it is a separate buf module for
// node-internal Store types that are not part of the wire contract and are not
// published by any release.
func TestNoLocalProtoSourcesRemain(t *testing.T) {
	var stray []string
	require.NoError(t, walkProtoFiles(".", func(path string) {
		if strings.HasPrefix(path, "internal/proto/") || strings.HasPrefix(path, "monorepo/") {
			return
		}
		stray = append(stray, path)
	}))
	require.Empty(t, stray,
		"the protobuf contract lives in TrueOpen/wire and reaches this repository as wire/wire.binpb. A .proto here would be a second source with no effect on generation. Edit it in wire, cut a release, and bump %s.",
		wirePinPath)
}

func loadWirePin(t *testing.T) wirePin {
	t.Helper()
	raw, err := os.ReadFile(wirePinPath)
	require.NoError(t, err)
	var pin wirePin
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&pin), "%s must decode with no unknown keys", wirePinPath)
	require.Equal(t, wirePinSchemaV1, pin.Schema)
	require.NotEmpty(t, pin.Notes, "%s must explain what the pin means", wirePinPath)
	require.True(t, strings.HasPrefix(pin.SourceRepository, "https://"))
	require.Regexp(t, wireTagPattern, pin.ReleaseTag)
	require.Regexp(t, wireCommitPattern, pin.ReleaseCommit)
	require.Regexp(t, wireDigestPattern, pin.Descriptor.SHA256)
	require.Positive(t, pin.Descriptor.Bytes)
	require.Regexp(t, wireDigestPattern, pin.DomainRegistry.SHA256)
	require.Positive(t, pin.DomainRegistry.Bytes)
	return pin
}

// walkProtoFiles calls visit for every .proto path under root, using slash
// separators so the caller's prefix checks read the same on every platform.
func walkProtoFiles(root string, visit func(path string)) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) == ".proto" {
			visit(filepath.ToSlash(path))
		}
		return nil
	})
}
