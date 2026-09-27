package keeper

import (
	"bytes"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func TestTaskCorePrivateValueStoresRawUserAddress(t *testing.T) {
	f := initInternalFixture(t)
	userBytes := bytes.Repeat([]byte{0x72}, taskAccountAddressBytes)
	state := types.TaskCoreState{
		TaskId:      bytes.Repeat([]byte{0x31}, types.Hash32Len),
		UserAddress: sdk.AccAddress(userBytes).String(),
		ModelId:     bytes.Repeat([]byte{0x42}, types.Hash32Len),
		OrderValue:  shared.NewAmount(100),
	}
	valueCodec := taskCoreValueCodec{
		addressCodec: f.keeper.addressCodec,
		public:       codec.CollValue[types.TaskCoreState](f.keeper.cdc),
		private:      codec.CollValue[internaltypes.TaskCoreStoreState](f.keeper.cdc),
	}
	encoded, err := valueCodec.Encode(state)
	require.NoError(t, err)
	var stored internaltypes.TaskCoreStoreState
	require.NoError(t, stored.Unmarshal(encoded))
	require.Equal(t, userBytes, stored.UserAddress)
	require.Equal(t, state.ModelId, stored.ModelId)
	projected, err := valueCodec.Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, state.UserAddress, projected.UserAddress)
	require.Equal(t, state.ModelId, projected.ModelId)
	require.Equal(t, state.OrderValue, projected.OrderValue)

	stored.UserAddress = []byte{0x01}
	corrupt, err := stored.Marshal()
	require.NoError(t, err)
	_, err = valueCodec.Decode(corrupt)
	require.ErrorContains(t, err, "stored task core user address")
}
