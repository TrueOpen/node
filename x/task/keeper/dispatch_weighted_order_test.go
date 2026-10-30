package keeper

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/TrueOpen/wire/dispatch"
	"github.com/stretchr/testify/require"
)

func TestDispatchWeightedOrderMatchesPinnedWire(t *testing.T) {
	var fixture struct {
		Order struct {
			ChainID     string `json:"chain_id"`
			Randomness  string `json:"randomness"`
			MaxAttempts uint32 `json:"max_attempts"`
			Members     []struct {
				Slot   uint32 `json:"slot"`
				Weight uint64 `json:"weight"`
			} `json:"members"`
			Slots []uint32 `json:"order"`
		} `json:"weighted_order"`
	}
	raw, err := os.ReadFile("testdata/dispatch_v1.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &fixture))
	seed, err := hex.DecodeString(fixture.Order.Randomness)
	require.NoError(t, err)
	require.Len(t, seed, 32)
	members := make([]dispatch.WeightedMember, len(fixture.Order.Members))
	for i, member := range fixture.Order.Members {
		members[i] = dispatch.WeightedMember{Slot: member.Slot, Weight: member.Weight}
	}
	order, err := dispatch.DrawOrder(fixture.Order.ChainID, [32]byte(seed), members, fixture.Order.MaxAttempts)
	require.NoError(t, err)
	require.Equal(t, fixture.Order.Slots, order)
}
