package types

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// TestTaskBuilderRankMatchesWireVectors binds wire
// testdata/v1/task/task_builder_rank_v1.json, read from the pinned wire module,
// to TaskBuilderRank: each vector's seed and Bech32 operator must produce its
// published digest.
func TestTaskBuilderRankMatchesWireVectors(t *testing.T) {
	dir, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/TrueOpen/wire").Output()
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(dir)), "testdata", "v1", "task", "task_builder_rank_v1.json"))
	require.NoError(t, err)
	var doc struct {
		Vectors []struct {
			Name      string `json:"name"`
			Domain    string `json:"domain"`
			DigestHex string `json:"digest_hex"`
			Fields    []struct {
				Name   string `json:"name"`
				Hex    string `json:"hex"`
				Bech32 string `json:"bech32"`
			} `json:"fields"`
		} `json:"vectors"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.NotEmpty(t, doc.Vectors)
	for _, vector := range doc.Vectors {
		require.Equal(t, shared.DomainTaskBuilderRankV1, vector.Domain, vector.Name)
		require.Len(t, vector.Fields, 2, vector.Name)
		require.Equal(t, "task_builder_seed", vector.Fields[0].Name)
		require.Equal(t, "builder_operator_address", vector.Fields[1].Name)
		seedBytes, err := hex.DecodeString(vector.Fields[0].Hex)
		require.NoError(t, err)
		require.Len(t, seedBytes, 32)
		rank, err := TaskBuilderRank([32]byte(seedBytes), vector.Fields[1].Bech32)
		require.NoError(t, err, vector.Name)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(rank[:]), vector.Name)
	}
}
