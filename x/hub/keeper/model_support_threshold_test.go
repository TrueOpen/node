package keeper

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

func TestModelSupportThresholdUsesModelAggregates(t *testing.T) {
	params := types.SupportParamsV1{
		ActiveSupporterMinCount:    2,
		ActiveSupportStakeMultiple: 3,
	}
	model := types.ModelState{
		SupportMinStake:      100,
		ActiveSupporterCount: 2,
		ActiveSupportStake:   300,
	}
	active, err := modelSupportThresholdMet(model, params)
	require.NoError(t, err)
	require.True(t, active)

	model.ActiveSupporterCount = 1
	active, err = modelSupportThresholdMet(model, params)
	require.NoError(t, err)
	require.False(t, active)

	model.ActiveSupporterCount = 2
	model.ActiveSupportStake = 299
	active, err = modelSupportThresholdMet(model, params)
	require.NoError(t, err)
	require.False(t, active)

	model.SupportMinStake = math.MaxUint64
	_, err = modelSupportThresholdMet(model, params)
	require.ErrorContains(t, err, "overflows")
}
