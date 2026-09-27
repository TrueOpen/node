package node

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/module"
)

// wireParamsVectorPath is where the pinned wire module publishes the Hub and
// Task parameter commitment vectors.
const wireParamsVectorPath = "testdata/v1/shared/params_v1.json"

// TestParamsFixtureIsWireReleaseVector requires testdata/params_v1.json to be
// the pinned wire release's params vector byte for byte, so the parameter
// digests asserted by params_fixture_test.go are wire's, not a local variant.
func TestParamsFixtureIsWireReleaseVector(t *testing.T) {
	pin := loadWirePin(t)
	escaped, err := module.EscapePath(wireGoModulePath)
	require.NoError(t, err)
	modCache, err := exec.Command("go", "env", "GOMODCACHE").Output()
	require.NoError(t, err)
	published := filepath.Join(strings.TrimSpace(string(modCache)), escaped+"@"+pin.ReleaseTag, wireParamsVectorPath)

	want, err := os.ReadFile(published)
	require.NoError(t, err, "the pinned wire module %s must be downloaded (go mod download)", pin.ReleaseTag)
	got, err := os.ReadFile(paramsFixturePath)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got),
		"%s must be wire %s %s byte for byte", paramsFixturePath, pin.ReleaseTag, wireParamsVectorPath)
}
