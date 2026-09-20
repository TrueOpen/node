package types

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	collcodec "cosmossdk.io/collections/codec"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// ---- Store key codecs (the data-structure contract) ----
//
// NO-MIGRATION POLICY. Everything in this file describes the on-disk key layout
// of a chain whose only genesis is the fresh V1 genesis. There is no v0
// predecessor: the prefixes in keys.go are all at schema version 1
// (CurrentStoreSchemaVersion == MinSupportedStoreSchemaVersion == 1), no
// StateVersionState / StoreMigrationState row exists to record a source layout,
// and x/task registers no migration handler. Consequently:
//
//   - There is no hex key space to fall back to and no dual-write window. A
//     lowercase-hex key written by an earlier revision of this module is not a
//     key this module can read, and that is intentional.
//   - Changing a codec, a component width or a component order in this file
//     after launch is a state migration, not a refactor: every affected row and
//     every affected index row has to be re-keyed under a new prefix version
//     while the old one is still readable. The fresh-genesis window is the only
//     time these decisions are free, which is why they are being made now.
//   - The one thing that is safe to change post-launch is a purely cosmetic
//     method (Stringify, EncodeJSON): those do not participate in the store key.
//
// §0.1 requires Hash32 to be "a fixed 32 bytes; it must not be stored as an
// arbitrary string". These codecs
// are how that requirement is enforced at the store boundary rather than by
// convention.

// Hash32Key and Hash32KeyCodec remain aliases so existing Task callers keep
// their source API while Hub and Task share one fixed-width implementation.
type Hash32Key = shared.Hash32Key

var Hash32KeyCodec = shared.Hash32KeyCodec

// AddrKey is the store-key form of an account address: the raw canonical
// address bytes an address.Codec produces, never the bech32 rendering of them.
type AddrKey = []byte

// AddrKeyMaxLen bounds an address store-key component. It is the cosmos-sdk
// address length cap, and it is also the largest length AddrKeyCodec can encode
// in a non-terminal position, because that position spends exactly one byte on
// the length prefix.
const AddrKeyMaxLen = 255

// AddrKeyCodec encodes an address store-key component as its raw canonical
// address bytes.
//
// Unlike Hash32 an address has no frozen width — cosmos-sdk accounts are 20
// bytes and module accounts derived with address.Module are 32 — so this codec
// cannot be fixed-width and does spend a 1-byte length prefix in a non-terminal
// position, exactly like collections.BytesKey and sdk.AccAddressKey. Two
// consequences are deliberate:
//
//   - In a non-terminal position addresses of different widths are grouped by
//     width before content. Nothing in this module reaches a consensus decision
//     by iterating across distinct addresses (see the audit recorded on
//     WorkerActiveTaskIndex / VerifierActiveJobIndex / VerifyResultState in
//     keys.go): every walk either fixes the address as an exact prefix or
//     re-sorts the rows it collected by raw address bytes, which is the D-9
//     "address raw bytes ASC" tie-break.
//   - This encoding is NOT the bech32 order. Replacing bech32 text keys with raw
//     bytes changes the global iteration order of every address-keyed
//     collection, which is why it is a fresh-genesis-only change.
//
// It is used in preference to sdk.AccAddressKey for the same two reasons
// Hash32KeyCodec exists: sdk.AccAddressKey delegates to collections.BytesKey and
// so returns a decoded key aliasing the iterator buffer, and it accepts an empty
// or over-long address instead of failing closed.
var AddrKeyCodec collcodec.KeyCodec[AddrKey] = addrKeyCodec{}

type addrKeyCodec struct{}

func (addrKeyCodec) validate(key AddrKey) error {
	if len(key) == 0 || len(key) > AddrKeyMaxLen {
		return fmt.Errorf("%w: address store key must be 1..%d bytes, got %d", collcodec.ErrEncoding, AddrKeyMaxLen, len(key))
	}
	return nil
}

func (c addrKeyCodec) Encode(buffer []byte, key AddrKey) (int, error) {
	if err := c.validate(key); err != nil {
		return 0, err
	}
	if len(buffer) < len(key) {
		return 0, fmt.Errorf("%w: address store key buffer is %d bytes, want %d", collcodec.ErrEncoding, len(buffer), len(key))
	}
	return copy(buffer, key), nil
}

func (addrKeyCodec) Decode(buffer []byte) (int, AddrKey, error) {
	if len(buffer) == 0 || len(buffer) > AddrKeyMaxLen {
		return 0, nil, fmt.Errorf("%w: address store key must be 1..%d bytes, got %d", collcodec.ErrEncoding, AddrKeyMaxLen, len(buffer))
	}
	return len(buffer), append(AddrKey(nil), buffer...), nil
}

func (addrKeyCodec) Size(key AddrKey) int { return len(key) }

func (c addrKeyCodec) EncodeNonTerminal(buffer []byte, key AddrKey) (int, error) {
	if err := c.validate(key); err != nil {
		return 0, err
	}
	if len(buffer) < len(key)+1 {
		return 0, fmt.Errorf("%w: address store key buffer is %d bytes, want %d", collcodec.ErrEncoding, len(buffer), len(key)+1)
	}
	buffer[0] = uint8(len(key))
	return copy(buffer[1:], key) + 1, nil
}

func (addrKeyCodec) DecodeNonTerminal(buffer []byte) (int, AddrKey, error) {
	if len(buffer) == 0 {
		return 0, nil, fmt.Errorf("%w: address store key non-terminal buffer is empty", collcodec.ErrEncoding)
	}
	length := int(buffer[0])
	if length == 0 {
		return 0, nil, fmt.Errorf("%w: address store key must not be empty", collcodec.ErrEncoding)
	}
	if len(buffer[1:]) < length {
		return 0, nil, fmt.Errorf("%w: address store key needs %d bytes, got %d", collcodec.ErrEncoding, length, len(buffer[1:]))
	}
	return length + 1, append(AddrKey(nil), buffer[1:length+1]...), nil
}

func (addrKeyCodec) SizeNonTerminal(key AddrKey) int { return len(key) + 1 }

func (addrKeyCodec) Stringify(key AddrKey) string { return hex.EncodeToString(key) }

func (addrKeyCodec) KeyType() string { return "task.Address" }

func (addrKeyCodec) EncodeJSON(value AddrKey) ([]byte, error) {
	return json.Marshal(hex.EncodeToString(value))
}

func (addrKeyCodec) DecodeJSON(b []byte) (AddrKey, error) {
	var text string
	if err := json.Unmarshal(b, &text); err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(text)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > AddrKeyMaxLen {
		return nil, fmt.Errorf("address store key must be 1..%d bytes, got %d", AddrKeyMaxLen, len(raw))
	}
	return raw, nil
}
