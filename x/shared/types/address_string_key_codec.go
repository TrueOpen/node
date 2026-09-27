package types

import (
	"bytes"
	"encoding/json"
	"fmt"

	collectionscodec "cosmossdk.io/collections/codec"
	"cosmossdk.io/core/address"
)

// AddressStringKeyCodec retains canonical Bech32 at the caller boundary while
// encoding only address codec bytes in store keys.
type AddressStringKeyCodec struct {
	AddressCodec address.Codec
}

var _ collectionscodec.KeyCodec[string] = AddressStringKeyCodec{}

func (c AddressStringKeyCodec) raw(value string) ([]byte, error) {
	if c.AddressCodec == nil || value == "" {
		return nil, fmt.Errorf("%w: address key is empty or codec is unavailable", collectionscodec.ErrEncoding)
	}
	raw, err := c.AddressCodec.StringToBytes(value)
	if err != nil || len(raw) == 0 || len(raw) > 255 {
		return nil, fmt.Errorf("%w: address key is invalid", collectionscodec.ErrEncoding)
	}
	canonical, err := c.AddressCodec.BytesToString(raw)
	if err != nil || canonical != value {
		return nil, fmt.Errorf("%w: address key is not canonical", collectionscodec.ErrEncoding)
	}
	return raw, nil
}

func (c AddressStringKeyCodec) Encode(buffer []byte, key string) (int, error) {
	raw, err := c.raw(key)
	if err != nil {
		return 0, err
	}
	if len(buffer) < len(raw) {
		return 0, fmt.Errorf("%w: address key buffer is too short", collectionscodec.ErrEncoding)
	}
	return copy(buffer, raw), nil
}

func (c AddressStringKeyCodec) Decode(buffer []byte) (int, string, error) {
	if c.AddressCodec == nil || len(buffer) == 0 || len(buffer) > 255 {
		return 0, "", fmt.Errorf("%w: stored address key has invalid length", collectionscodec.ErrEncoding)
	}
	value, err := c.AddressCodec.BytesToString(append([]byte(nil), buffer...))
	if err != nil {
		return 0, "", fmt.Errorf("%w: stored address key cannot be decoded: %v", collectionscodec.ErrEncoding, err)
	}
	raw, err := c.raw(value)
	if err != nil || !bytes.Equal(raw, buffer) {
		return 0, "", fmt.Errorf("%w: stored address key is not canonical", collectionscodec.ErrEncoding)
	}
	return len(buffer), value, nil
}

func (c AddressStringKeyCodec) Size(key string) int {
	raw, _ := c.raw(key)
	return len(raw)
}

func (c AddressStringKeyCodec) EncodeNonTerminal(buffer []byte, key string) (int, error) {
	raw, err := c.raw(key)
	if err != nil {
		return 0, err
	}
	if len(buffer) < len(raw)+1 {
		return 0, fmt.Errorf("%w: address key buffer is too short", collectionscodec.ErrEncoding)
	}
	buffer[0] = byte(len(raw))
	return copy(buffer[1:], raw) + 1, nil
}

func (c AddressStringKeyCodec) DecodeNonTerminal(buffer []byte) (int, string, error) {
	if len(buffer) == 0 || buffer[0] == 0 || len(buffer)-1 < int(buffer[0]) {
		return 0, "", fmt.Errorf("%w: stored address key length prefix is invalid", collectionscodec.ErrEncoding)
	}
	length := int(buffer[0])
	_, value, err := c.Decode(buffer[1 : length+1])
	if err != nil {
		return 0, "", err
	}
	return length + 1, value, nil
}

func (c AddressStringKeyCodec) SizeNonTerminal(key string) int {
	return c.Size(key) + 1
}

func (AddressStringKeyCodec) Stringify(key string) string { return key }
func (AddressStringKeyCodec) KeyType() string             { return "shared.AccountAddress" }

func (c AddressStringKeyCodec) EncodeJSON(key string) ([]byte, error) {
	if _, err := c.raw(key); err != nil {
		return nil, err
	}
	return json.Marshal(key)
}

func (c AddressStringKeyCodec) DecodeJSON(encoded []byte) (string, error) {
	var key string
	if err := json.Unmarshal(encoded, &key); err != nil {
		return "", err
	}
	if _, err := c.raw(key); err != nil {
		return "", err
	}
	return key, nil
}
