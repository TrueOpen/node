package keeper_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestEarningsAddressValueAndGenesisRoundTrip(t *testing.T) {
	address := hubAddress(t, 0x51)
	genesis := types.DefaultGenesis()
	genesis.Earnings = []types.EarningsState{{
		Address:                address,
		ClaimableTaskFee:       shared.NewAmount(20),
		ClaimableServiceReward: shared.NewAmount(0),
		ClaimableBuilderReward: shared.NewAmount(0),
		ClaimableAmount:        shared.NewAmount(20),
		EarningsVersion:        1,
		LastUpdatedHeight:      21,
	}}

	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	stored, err := f.keeper.Earnings.Get(f.ctx, address)
	require.NoError(t, err)
	require.Equal(t, hubAddressBytes(t, address), stored.Address)
	encoded, err := stored.Marshal()
	require.NoError(t, err)
	require.False(t, bytes.Contains(encoded, []byte(address)))

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Equal(t, genesis.Earnings, exported.Earnings)
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.Equal(t, exported.Earnings, reexported.Earnings)
}
