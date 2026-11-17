package keeper

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	"github.com/stretchr/testify/require"
)

func TestDispatchBatchDigestMatchesPinnedWireResults(t *testing.T) {
	var fixture struct {
		Scope struct {
			ChainID string `json:"chain_id"`
		} `json:"weighted_order"`
		Batches []struct {
			Name     string `json:"name"`
			Digest   string `json:"batch_digest_hex"`
			Response string `json:"response_protobuf_hex"`
		} `json:"batch_results"`
	}
	raw, err := os.ReadFile("testdata/dispatch_v1.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Len(t, fixture.Batches, 3)
	for _, row := range fixture.Batches {
		t.Run(row.Name, func(t *testing.T) {
			raw, err := hex.DecodeString(row.Response)
			require.NoError(t, err)
			var response types.MsgRecordDispatchResponse
			require.NoError(t, response.Unmarshal(raw))
			results := make([]types.BatchItemResultV1, len(response.Results))
			for i, result := range response.Results {
				require.NotNil(t, result)
				results[i] = *result
			}
			digest, err := taskBatchDigest(fixture.Scope.ChainID, shared.BatchResultKindDispatchRecord, results)
			require.NoError(t, err)
			require.Equal(t, row.Digest, hex.EncodeToString(digest))
			require.Equal(t, response.BatchDigest, digest)
		})
	}
}
