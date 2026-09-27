package keeper

import (
	"fmt"

	collectionscodec "cosmossdk.io/collections/codec"
	"cosmossdk.io/core/address"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

type taskCoreValueCodec struct {
	addressCodec address.Codec
	public       collectionscodec.ValueCodec[types.TaskCoreState]
	private      collectionscodec.ValueCodec[internaltypes.TaskCoreStoreState]
}

func (c taskCoreValueCodec) Encode(value types.TaskCoreState) ([]byte, error) {
	stored, err := taskCoreToStore(c.addressCodec, value)
	if err != nil {
		return nil, err
	}
	return c.private.Encode(stored)
}

func (c taskCoreValueCodec) Decode(raw []byte) (types.TaskCoreState, error) {
	stored, err := c.private.Decode(raw)
	if err != nil {
		return types.TaskCoreState{}, err
	}
	return taskCoreFromStore(c.addressCodec, stored)
}

func (c taskCoreValueCodec) EncodeJSON(value types.TaskCoreState) ([]byte, error) {
	return c.public.EncodeJSON(value)
}

func (c taskCoreValueCodec) DecodeJSON(raw []byte) (types.TaskCoreState, error) {
	return c.public.DecodeJSON(raw)
}

func (c taskCoreValueCodec) Stringify(value types.TaskCoreState) string {
	return c.public.Stringify(value)
}

func (c taskCoreValueCodec) ValueType() string {
	return c.public.ValueType()
}

func taskCoreToStore(addressCodec address.Codec, state types.TaskCoreState) (internaltypes.TaskCoreStoreState, error) {
	encoded, err := state.Marshal()
	if err != nil {
		return internaltypes.TaskCoreStoreState{}, err
	}
	var stored internaltypes.TaskCoreStoreState
	if err := stored.Unmarshal(encoded); err != nil {
		return internaltypes.TaskCoreStoreState{}, err
	}
	if state.UserAddress == "" {
		return stored, nil
	}
	raw, err := addressCodec.StringToBytes(state.UserAddress)
	if err != nil || len(raw) != taskAccountAddressBytes {
		return internaltypes.TaskCoreStoreState{}, fmt.Errorf("task core user address is not canonical")
	}
	canonical, err := addressCodec.BytesToString(raw)
	if err != nil || canonical != state.UserAddress {
		return internaltypes.TaskCoreStoreState{}, fmt.Errorf("task core user address is not canonical")
	}
	stored.UserAddress = append([]byte(nil), raw...)
	return stored, nil
}

func taskCoreFromStore(addressCodec address.Codec, stored internaltypes.TaskCoreStoreState) (types.TaskCoreState, error) {
	var user string
	if len(stored.UserAddress) != 0 {
		if len(stored.UserAddress) != taskAccountAddressBytes {
			return types.TaskCoreState{}, fmt.Errorf("stored task core user address must be %d bytes", taskAccountAddressBytes)
		}
		var err error
		user, err = addressCodec.BytesToString(stored.UserAddress)
		if err != nil {
			return types.TaskCoreState{}, fmt.Errorf("encode stored task core user address: %w", err)
		}
		stored.UserAddress = []byte("placeholder")
	}
	encoded, err := stored.Marshal()
	if err != nil {
		return types.TaskCoreState{}, err
	}
	var state types.TaskCoreState
	if err := state.Unmarshal(encoded); err != nil {
		return types.TaskCoreState{}, err
	}
	state.UserAddress = user
	return state, nil
}
