package keeper

import (
	"bytes"
	"fmt"

	collectionscodec "cosmossdk.io/collections/codec"
	"cosmossdk.io/core/address"
	proto "github.com/cosmos/gogoproto/proto"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
)

func modelKeyMatches(state types.ModelState, modelID []byte) bool {
	return bytes.Equal(state.ModelId, modelID)
}

func profileKeyMatches(state types.ProfileState, modelID []byte, version uint32) bool {
	return bytes.Equal(state.ModelId, modelID) && state.ProfileVersion == version
}

func modelCapabilityKeyMatches(state types.ModelCapabilityState, operator string, modelID []byte) bool {
	return state.OperatorAddress == operator && bytes.Equal(state.ModelId, modelID)
}

func modelSupportKeyMatches(state types.ModelSupportState, operator string, modelID []byte) bool {
	return state.OperatorAddress == operator && bytes.Equal(state.ModelId, modelID)
}

func dailySupportKeyMatches(state types.DailySupportState, epoch uint64, operator string) bool {
	return state.Epoch == epoch && state.OperatorAddress == operator
}

func modelSupportRecheckCursorKeyMatches(state types.ModelSupportRecheckCursorState, modelID []byte) bool {
	return bytes.Equal(state.ModelId, modelID)
}

func modelSupportDeactivateCursorKeyMatches(state types.ModelSupportDeactivateCursorState, modelID []byte) bool {
	return bytes.Equal(state.ModelId, modelID)
}

const modelAccountAddressBytes = 20

type mirroredAddressValueCodec[P, S any] struct {
	public    collectionscodec.ValueCodec[P]
	private   collectionscodec.ValueCodec[S]
	toStore   func(P) (S, error)
	fromStore func(S) (P, error)
}

func newMirroredAddressValueCodec[P, S any](
	public collectionscodec.ValueCodec[P], private collectionscodec.ValueCodec[S],
	toStore func(P) (S, error), fromStore func(S) (P, error),
) mirroredAddressValueCodec[P, S] {
	return mirroredAddressValueCodec[P, S]{public: public, private: private, toStore: toStore, fromStore: fromStore}
}

func (c mirroredAddressValueCodec[P, S]) Encode(value P) ([]byte, error) {
	stored, err := c.toStore(value)
	if err != nil {
		return nil, err
	}
	return c.private.Encode(stored)
}

func (c mirroredAddressValueCodec[P, S]) Decode(raw []byte) (P, error) {
	stored, err := c.private.Decode(raw)
	if err != nil {
		var zero P
		return zero, err
	}
	return c.fromStore(stored)
}

func (c mirroredAddressValueCodec[P, S]) EncodeJSON(value P) ([]byte, error) {
	return c.public.EncodeJSON(value)
}

func (c mirroredAddressValueCodec[P, S]) DecodeJSON(raw []byte) (P, error) {
	return c.public.DecodeJSON(raw)
}

func (c mirroredAddressValueCodec[P, S]) Stringify(value P) string { return c.public.Stringify(value) }
func (c mirroredAddressValueCodec[P, S]) ValueType() string        { return c.public.ValueType() }

func transcodeAddressValue(source, target proto.Message) error {
	encoded, err := proto.Marshal(source)
	if err != nil {
		return err
	}
	return proto.Unmarshal(encoded, target)
}

func encodeModelAddress(codec address.Codec, field, value string, optional bool) ([]byte, error) {
	if optional && value == "" {
		return nil, nil
	}
	if codec == nil || value == "" {
		return nil, fmt.Errorf("%s is required", field)
	}
	raw, err := codec.StringToBytes(value)
	if err != nil || len(raw) != modelAccountAddressBytes {
		return nil, fmt.Errorf("%s is not a canonical account address", field)
	}
	canonical, err := codec.BytesToString(raw)
	if err != nil || canonical != value {
		return nil, fmt.Errorf("%s is not a canonical account address", field)
	}
	return append([]byte(nil), raw...), nil
}

func decodeModelAddress(codec address.Codec, field string, raw []byte, optional bool) (string, error) {
	if optional && len(raw) == 0 {
		return "", nil
	}
	if codec == nil || len(raw) != modelAccountAddressBytes {
		return "", fmt.Errorf("stored %s must be %d bytes", field, modelAccountAddressBytes)
	}
	value, err := codec.BytesToString(raw)
	if err != nil {
		return "", fmt.Errorf("decode stored %s: %w", field, err)
	}
	return value, nil
}

func modelToStore(codec address.Codec, value types.ModelState) (internaltypes.ModelStoreState, error) {
	var stored internaltypes.ModelStoreState
	if err := transcodeAddressValue(&value, &stored); err != nil {
		return stored, err
	}
	address, err := encodeModelAddress(codec, "model proposer address", value.ProposerAddress, false)
	if err != nil {
		return stored, err
	}
	stored.ProposerAddress = address
	return stored, nil
}

func modelFromStore(codec address.Codec, stored internaltypes.ModelStoreState) (types.ModelState, error) {
	var value types.ModelState
	address, err := decodeModelAddress(codec, "model proposer address", stored.ProposerAddress, false)
	if err != nil {
		return value, err
	}
	stored.ProposerAddress = []byte("placeholder")
	if err := transcodeAddressValue(&stored, &value); err != nil {
		return value, err
	}
	value.ProposerAddress = address
	return value, nil
}

func profileToStore(codec address.Codec, value types.ProfileState) (internaltypes.ProfileStoreState, error) {
	var stored internaltypes.ProfileStoreState
	if err := transcodeAddressValue(&value, &stored); err != nil {
		return stored, err
	}
	address, err := encodeModelAddress(codec, "profile proposer address", value.ProposerAddress, false)
	if err != nil {
		return stored, err
	}
	stored.ProposerAddress = address
	return stored, nil
}

func profileFromStore(codec address.Codec, stored internaltypes.ProfileStoreState) (types.ProfileState, error) {
	var value types.ProfileState
	address, err := decodeModelAddress(codec, "profile proposer address", stored.ProposerAddress, false)
	if err != nil {
		return value, err
	}
	stored.ProposerAddress = []byte("placeholder")
	if err := transcodeAddressValue(&stored, &value); err != nil {
		return value, err
	}
	value.ProposerAddress = address
	return value, nil
}

func modelCapabilityToStore(codec address.Codec, value types.ModelCapabilityState) (internaltypes.ModelCapabilityStoreState, error) {
	var stored internaltypes.ModelCapabilityStoreState
	if err := transcodeAddressValue(&value, &stored); err != nil {
		return stored, err
	}
	address, err := encodeModelAddress(codec, "model capability operator address", value.OperatorAddress, false)
	if err != nil {
		return stored, err
	}
	stored.OperatorAddress = address
	return stored, nil
}

func modelCapabilityFromStore(codec address.Codec, stored internaltypes.ModelCapabilityStoreState) (types.ModelCapabilityState, error) {
	var value types.ModelCapabilityState
	address, err := decodeModelAddress(codec, "model capability operator address", stored.OperatorAddress, false)
	if err != nil {
		return value, err
	}
	stored.OperatorAddress = []byte("placeholder")
	if err := transcodeAddressValue(&stored, &value); err != nil {
		return value, err
	}
	value.OperatorAddress = address
	return value, nil
}

func modelSupportToStore(codec address.Codec, value types.ModelSupportState) (internaltypes.ModelSupportStoreState, error) {
	var stored internaltypes.ModelSupportStoreState
	if err := transcodeAddressValue(&value, &stored); err != nil {
		return stored, err
	}
	address, err := encodeModelAddress(codec, "model support operator address", value.OperatorAddress, false)
	if err != nil {
		return stored, err
	}
	stored.OperatorAddress = address
	return stored, nil
}

func modelSupportFromStore(codec address.Codec, stored internaltypes.ModelSupportStoreState) (types.ModelSupportState, error) {
	var value types.ModelSupportState
	address, err := decodeModelAddress(codec, "model support operator address", stored.OperatorAddress, false)
	if err != nil {
		return value, err
	}
	stored.OperatorAddress = []byte("placeholder")
	if err := transcodeAddressValue(&stored, &value); err != nil {
		return value, err
	}
	value.OperatorAddress = address
	return value, nil
}

func modelSupportRecheckCursorToStore(codec address.Codec, value types.ModelSupportRecheckCursorState) (internaltypes.ModelSupportRecheckCursorStoreState, error) {
	var stored internaltypes.ModelSupportRecheckCursorStoreState
	if err := transcodeAddressValue(&value, &stored); err != nil {
		return stored, err
	}
	address, err := encodeModelAddress(codec, "model support recheck cursor operator address", value.LastOperatorAddress, true)
	if err != nil {
		return stored, err
	}
	stored.LastOperatorAddress = address
	return stored, nil
}

func modelSupportRecheckCursorFromStore(codec address.Codec, stored internaltypes.ModelSupportRecheckCursorStoreState) (types.ModelSupportRecheckCursorState, error) {
	var value types.ModelSupportRecheckCursorState
	address, err := decodeModelAddress(codec, "model support recheck cursor operator address", stored.LastOperatorAddress, true)
	if err != nil {
		return value, err
	}
	if address != "" {
		stored.LastOperatorAddress = []byte("placeholder")
	}
	if err := transcodeAddressValue(&stored, &value); err != nil {
		return value, err
	}
	value.LastOperatorAddress = address
	return value, nil
}

func modelSupportDeactivateCursorToStore(codec address.Codec, value types.ModelSupportDeactivateCursorState) (internaltypes.ModelSupportDeactivateCursorStoreState, error) {
	var stored internaltypes.ModelSupportDeactivateCursorStoreState
	if err := transcodeAddressValue(&value, &stored); err != nil {
		return stored, err
	}
	address, err := encodeModelAddress(codec, "model support deactivate cursor operator address", value.LastOperatorAddress, true)
	if err != nil {
		return stored, err
	}
	stored.LastOperatorAddress = address
	return stored, nil
}

func modelSupportDeactivateCursorFromStore(codec address.Codec, stored internaltypes.ModelSupportDeactivateCursorStoreState) (types.ModelSupportDeactivateCursorState, error) {
	var value types.ModelSupportDeactivateCursorState
	address, err := decodeModelAddress(codec, "model support deactivate cursor operator address", stored.LastOperatorAddress, true)
	if err != nil {
		return value, err
	}
	if address != "" {
		stored.LastOperatorAddress = []byte("placeholder")
	}
	if err := transcodeAddressValue(&stored, &value); err != nil {
		return value, err
	}
	value.LastOperatorAddress = address
	return value, nil
}

func dailySupportToStore(codec address.Codec, value types.DailySupportState) (internaltypes.DailySupportStoreState, error) {
	var stored internaltypes.DailySupportStoreState
	if err := transcodeAddressValue(&value, &stored); err != nil {
		return stored, err
	}
	address, err := encodeModelAddress(codec, "daily support operator address", value.OperatorAddress, false)
	if err != nil {
		return stored, err
	}
	stored.OperatorAddress = address
	return stored, nil
}

func dailySupportFromStore(codec address.Codec, stored internaltypes.DailySupportStoreState) (types.DailySupportState, error) {
	var value types.DailySupportState
	address, err := decodeModelAddress(codec, "daily support operator address", stored.OperatorAddress, false)
	if err != nil {
		return value, err
	}
	stored.OperatorAddress = []byte("placeholder")
	if err := transcodeAddressValue(&stored, &value); err != nil {
		return value, err
	}
	value.OperatorAddress = address
	return value, nil
}
