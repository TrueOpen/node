package wirefixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Read loads consumer cases from the exact module version selected by go.mod.
func Read(t testing.TB, category, name string) []byte {
	t.Helper()
	directory, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/TrueOpen/wire").Output()
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(directory)), "testdata", "v1", category, name))
	require.NoError(t, err)
	return raw
}
