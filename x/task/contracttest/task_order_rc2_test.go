package contracttest

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/internal/testutil/sessionfixture"
	"github.com/TrueOpen/node/internal/testutil/wirefixture"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestTaskOrderHashMatchesPublishedWire(t *testing.T) {
	orders := publishedTaskOrders(t)
	var document struct {
		Vectors []struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
			Digest string `json:"digest_hex"`
		} `json:"vectors"`
	}
	require.NoError(t, json.Unmarshal(wirefixture.Read(t, "task", "task_order_v3.json"), &document))
	checked := 0
	for _, vector := range document.Vectors {
		if vector.Domain != shared.DomainTaskOrderV3 {
			continue
		}
		checked++
		t.Run(vector.Name, func(t *testing.T) {
			order, found := orders[vector.Name]
			require.True(t, found)
			digest, err := tasktypes.TaskOrderHash(order)
			if vector.Name == "task_order_v3_max_amounts" {
				require.ErrorContains(t, err, "amounts", "the published encoding-only case has a forbidden priority fee")
				return
			}
			require.NoError(t, err)
			require.Equal(t, vector.Digest, hex.EncodeToString(digest[:]))
		})
	}
	require.Equal(t, len(orders), checked)
}

func TestTaskOrderEIP712MatchesPinnedWire(t *testing.T) {
	var document struct {
		Domain struct {
			ChainID   string `json:"chain_id"`
			Separator string `json:"domain_separator"`
		} `json:"domain"`
		FeeDenom string `json:"fee_denom"`
		Vectors  []struct {
			Name          string `json:"name"`
			Order         string `json:"task_order_vector"`
			TaskHash      string `json:"task_hash"`
			HashStruct    string `json:"hash_struct"`
			SigningDigest string `json:"signing_digest"`
		} `json:"vectors"`
	}
	require.NoError(t, json.Unmarshal(wirefixture.Read(t, "task", "task_order_eip712_v1.json"), &document))
	chain, err := strconv.ParseUint(document.Domain.ChainID, 10, 64)
	require.NoError(t, err)
	orders := publishedTaskOrders(t)
	require.NotEmpty(t, document.Vectors)
	for _, vector := range document.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			_, name, found := strings.Cut(vector.Order, "#")
			require.True(t, found)
			order, found := orders[name]
			require.True(t, found)
			hash, err := tasktypes.TaskOrderHash(order)
			require.NoError(t, err)
			require.Equal(t, vector.TaskHash, hex.EncodeToString(hash[:]))
			digest, err := tasktypes.BuildTaskOrderEIP712Digest(chain, document.FeeDenom, order, hash[:])
			require.NoError(t, err)
			require.Equal(t, document.Domain.Separator, hex.EncodeToString(digest.DomainSeparator[:]))
			require.Equal(t, vector.HashStruct, hex.EncodeToString(digest.HashStruct[:]))
			require.Equal(t, vector.SigningDigest, hex.EncodeToString(digest.SigningDigest[:]))
		})
	}
}

func publishedTaskOrders(t *testing.T) map[string]tasktypes.TaskOrderV3 {
	t.Helper()
	core := wireCoreTaskOrder(t)
	lower := wireCoreTaskOrder(t)
	lower.GenerationParams = tasktypes.GenerationParamsV1{
		GenerationParamsSchemaVersion: 1, MaxOutputTokens: 1, MaxOutputDuration: 1,
		DecodingParams: tasktypes.DecodingParamsV1{TopPPpm: 1, PresencePenaltyMilli: -2000, FrequencyPenaltyMilli: -2000, RepetitionPenaltyPpm: 100000},
	}
	maximum := wireCoreTaskOrder(t)
	maximum.PriceBid = shared.NewAmount(^uint64(0))
	maximum.MaxFee = maximum.PriceBid
	maximum.AssignmentPriorityFee = maximum.PriceBid
	maximum.TxFeeReserve = maximum.PriceBid
	return map[string]tasktypes.TaskOrderV3{"task_order_v3_core": core, "task_order_v3_lower_bound": lower, "task_order_v3_max_amounts": maximum}
}

func wireCoreTaskOrder(t *testing.T) tasktypes.TaskOrderV3 {
	return sessionfixture.CoreOrder(t)
}
