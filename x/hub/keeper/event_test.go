package keeper

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestMustEmitHubEventProducesOneParseableTypedEvent(t *testing.T) {
	event := &types.EventServiceStakeChanged{
		Operator:         "trueopen1operator",
		Amount:           shared.Amount{AtomicUnits: "20"},
		ActiveBondAmount: shared.Amount{AtomicUnits: "100"},
		BondVersion:      4,
		EffectiveEpoch:   8,
	}
	eventManager := sdk.NewEventManager()
	ctx := sdk.WrapSDKContext(sdk.Context{}.WithEventManager(eventManager))

	mustEmitHubEvent(ctx, event)

	abciEvents := eventManager.ABCIEvents()
	require.Len(t, abciEvents, 1)
	require.Equal(t, proto.MessageName(event), abciEvents[0].Type)
	parsed, err := sdk.ParseTypedEvent(abciEvents[0])
	require.NoError(t, err)
	require.True(t, proto.Equal(event, parsed))
}
