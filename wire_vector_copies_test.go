package node

import (
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/module"
)

// nodeOwnedFixtures are JSON fixtures under a testdata directory that are not
// copies of a wire vector. Every other testdata JSON file must be a wire vector.
var nodeOwnedFixtures = map[string]string{
	"x/task/keeper/testdata/settlement_build_facts_v1.json": "REST response fixture for the SettlementBuildFacts query; node owns the endpoint",
}

// pinnedWireModuleDir returns the module-cache directory of the wire release
// named by wire/pin.json.
func pinnedWireModuleDir(t *testing.T) string {
	t.Helper()
	pin := loadWirePin(t)
	escaped, err := module.EscapePath(wireGoModulePath)
	require.NoError(t, err)
	modCache, err := exec.Command("go", "env", "GOMODCACHE").Output()
	require.NoError(t, err)
	dir := filepath.Join(strings.TrimSpace(string(modCache)), escaped+"@"+pin.ReleaseTag)
	_, err = os.Stat(dir)
	require.NoError(t, err, "the pinned wire module %s must be downloaded (go mod download)", pin.ReleaseTag)
	return dir
}

// TestWireVectorCopiesAreReleaseBytes requires every testdata JSON file in this
// repository to be either a byte-for-byte copy of the pinned wire release's
// testdata/v1 vector with the same file name, or a listed node-owned fixture.
// A hand-edited or stale copy of a wire vector therefore fails here instead of
// silently asserting a local variant of the contract.
func TestWireVectorCopiesAreReleaseBytes(t *testing.T) {
	wireDir := pinnedWireModuleDir(t)
	published := map[string]string{}
	require.NoError(t, filepath.WalkDir(filepath.Join(wireDir, "testdata", "v1"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		name := d.Name()
		require.NotContains(t, published, name, "wire publishes two vectors named %s", name)
		published[name] = p
		return nil
	}))

	copies := 0
	seenOwned := map[string]bool{}
	require.NoError(t, filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".json") || path.Base(filepath.Dir(p)) != "testdata" {
			return nil
		}
		slashed := filepath.ToSlash(p)
		if _, owned := nodeOwnedFixtures[slashed]; owned {
			seenOwned[slashed] = true
			return nil
		}
		source, ok := published[d.Name()]
		require.True(t, ok, "%s is neither a wire vector in the pinned release nor a listed node-owned fixture", slashed)
		want, err := os.ReadFile(source)
		require.NoError(t, err)
		got, err := os.ReadFile(p)
		require.NoError(t, err)
		rel, err := filepath.Rel(wireDir, source)
		require.NoError(t, err)
		require.True(t, string(want) == string(got),
			"%s must be wire's %s byte for byte; copy it from the pinned release", slashed, filepath.ToSlash(rel))
		copies++
		return nil
	}))
	for owned := range nodeOwnedFixtures {
		require.True(t, seenOwned[owned], "node-owned fixture %s no longer exists; drop its entry", owned)
	}
	require.GreaterOrEqual(t, copies, 20, "fewer wire vector copies were checked than expected")
	t.Logf("%d wire vector copies match the pinned release", copies)
}
