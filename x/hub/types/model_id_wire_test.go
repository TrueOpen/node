package types

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// readPinnedWireVector reads a vector straight from the wire module this
// repository pins, so no local copy can drift from the release.
func readPinnedWireVector(t *testing.T, rel string) []byte {
	t.Helper()
	dir, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/TrueOpen/wire").Output()
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(dir)), filepath.FromSlash(rel)))
	require.NoError(t, err)
	return raw
}

// TestModelIDMatchesWireVectors binds wire testdata/v1/hub/model_id_v1.json to
// the production path: the proposer address goes through CanonicalAddress and
// the identity through DeriveModelIDV1. Every positive vector must reproduce its
// digest and every negative case must be refused at the stage it names.
func TestModelIDMatchesWireVectors(t *testing.T) {
	type field struct {
		Name   string `json:"name"`
		UTF8   string `json:"utf8"`
		Bech32 string `json:"bech32"`
		Hex    string `json:"hex"`
	}
	var doc struct {
		Vectors []struct {
			Name      string  `json:"name"`
			Domain    string  `json:"domain"`
			DigestHex string  `json:"digest_hex"`
			Fields    []field `json:"fields"`
		} `json:"vectors"`
		Negative []struct {
			Name   string         `json:"name"`
			Expect string         `json:"expect"`
			Input  map[string]any `json:"input"`
		} `json:"negative"`
	}
	require.NoError(t, json.Unmarshal(readPinnedWireVector(t, "testdata/v1/hub/model_id_v1.json"), &doc))
	require.NotEmpty(t, doc.Vectors)
	require.NotEmpty(t, doc.Negative)

	codec := addresscodec.NewBech32Codec("trueopen")
	derive := func(chainID, provider, repoID, proposer string) ([]byte, error, error) {
		raw, _, addressErr := shared.CanonicalAddress(codec, "proposer_address", proposer)
		if addressErr != nil {
			return nil, addressErr, nil
		}
		modelID, err := DeriveModelIDV1(chainID, provider, repoID, raw)
		return modelID, nil, err
	}

	inputs := map[string]string{}
	for _, vector := range doc.Vectors {
		require.Equal(t, shared.DomainModelIDV1, vector.Domain)
		values := map[string]string{}
		for _, f := range vector.Fields {
			if f.Name == "proposer_address" {
				raw, _, err := shared.CanonicalAddress(codec, f.Name, f.Bech32)
				require.NoError(t, err, vector.Name)
				require.Equal(t, f.Hex, hex.EncodeToString(raw), vector.Name)
				values[f.Name] = f.Bech32
				continue
			}
			values[f.Name] = f.UTF8
		}
		modelID, addressErr, err := derive(values["chain_id"], values["provider"], values["repo_id"], values["proposer_address"])
		require.NoError(t, addressErr, vector.Name)
		require.NoError(t, err, vector.Name)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(modelID), vector.Name)
		if vector.Name == "model_id_v1" {
			inputs = values
		}
	}
	require.NotEmpty(t, inputs, "the base model_id_v1 vector must be published")

	for _, negative := range doc.Negative {
		values := map[string]string{}
		for key, value := range inputs {
			values[key] = value
		}
		for key, value := range negative.Input {
			switch key {
			case "repo_id_hex":
				decoded, err := hex.DecodeString(value.(string))
				require.NoError(t, err)
				values["repo_id"] = string(decoded)
			case "repo_id_length_bytes":
				length := int(value.(float64))
				values["repo_id"] = "a/" + strings.Repeat("b", length-2)
			default:
				values[key] = value.(string)
			}
		}
		_, addressErr, err := derive(values["chain_id"], values["provider"], values["repo_id"], values["proposer_address"])
		if negative.Expect == "reject_proposer_address" {
			require.Error(t, addressErr, negative.Name)
			continue
		}
		require.NoError(t, addressErr, negative.Name)
		require.Error(t, err, negative.Name)
		want := map[string]string{
			"reject_chain_id":               "chain_id",
			"reject_provider_not_canonical": "provider",
			"reject_provider_not_supported": "provider",
			"reject_repo_id":                "repo_id",
		}[negative.Expect]
		require.NotEmpty(t, want, "unknown expectation %q", negative.Expect)
		require.ErrorContains(t, err, want, negative.Name)
	}
}
