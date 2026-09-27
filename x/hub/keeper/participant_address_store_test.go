package keeper_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestParticipantAddressValuesUseCodecBytes(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	builder := hubIdentity(t, 0x71)
	registerBuilderIdentityForTest(t, f, builder, 1)
	cortex := hubIdentity(t, 0x72)
	registerCortexNodeIdentityForTest(t, f, cortex.Address, cortex, testServiceBondMinInitial, 1, 0)

	builderRow, err := f.keeper.Builder.Get(f.ctx, builder.Address)
	require.NoError(t, err)
	encodedBuilder, err := builderRow.Marshal()
	require.NoError(t, err)
	require.False(t, bytes.Contains(encodedBuilder, []byte(builder.Address)))
	require.True(t, bytes.Contains(encodedBuilder, hubAddressBytes(t, builder.Address)))

	cortexRow, err := f.keeper.CortexNode.Get(f.ctx, cortex.Address)
	require.NoError(t, err)
	encodedCortex, err := cortexRow.Marshal()
	require.NoError(t, err)
	require.False(t, bytes.Contains(encodedCortex, []byte(cortex.Address)))
	require.True(t, bytes.Contains(encodedCortex, hubAddressBytes(t, cortex.Address)))

	index, err := f.keeper.CurrentServiceAddressIndex.Get(f.ctx,
		types.NewCurrentServiceAddressIndexKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, builder.Address))
	require.NoError(t, err)
	encodedIndex, err := index.Marshal()
	require.NoError(t, err)
	require.False(t, bytes.Contains(encodedIndex, []byte(builder.Address)))
	require.True(t, bytes.Contains(encodedIndex, hubAddressBytes(t, builder.Address)))
	descriptor, err := f.keeper.ServiceDescriptor.Get(f.ctx,
		types.NewParticipantKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, builder.Address))
	require.NoError(t, err)
	encodedDescriptor, err := descriptor.Marshal()
	require.NoError(t, err)
	require.False(t, bytes.Contains(encodedDescriptor, []byte(builder.Address)))
	require.True(t, bytes.Contains(encodedDescriptor, hubAddressBytes(t, builder.Address)))
}
