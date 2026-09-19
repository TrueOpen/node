package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	collcodec "cosmossdk.io/collections/codec"
)

// Hash32KeySize is the fixed width of every digest or identifier stored with
// Hash32KeyCodec.
const Hash32KeySize = sha256.Size

// Hash32Key is a raw Hash32 store-key component. The codec, rather than this
// []byte alias, enforces its width at the collection boundary.
type Hash32Key = []byte

// Hash32KeyCodec stores a Hash32 as exactly 32 raw bytes in terminal and
// non-terminal positions. Decode always copies the iterator-owned buffer.
var Hash32KeyCodec collcodec.KeyCodec[Hash32Key] = hash32KeyCodec{}

type hash32KeyCodec struct{}

func (hash32KeyCodec) Encode(buffer []byte, key Hash32Key) (int, error) {
	if len(key) != Hash32KeySize {
		return 0, fmt.Errorf("%w: hash32 store key must be %d bytes, got %d", collcodec.ErrEncoding, Hash32KeySize, len(key))
	}
	if len(buffer) < Hash32KeySize {
		return 0, fmt.Errorf("%w: hash32 store key buffer is %d bytes, want %d", collcodec.ErrEncoding, len(buffer), Hash32KeySize)
	}
	return copy(buffer, key), nil
}

func (hash32KeyCodec) Decode(buffer []byte) (int, Hash32Key, error) {
	if len(buffer) != Hash32KeySize {
		return 0, nil, fmt.Errorf("%w: hash32 store key must be %d bytes, got %d", collcodec.ErrEncoding, Hash32KeySize, len(buffer))
	}
	return Hash32KeySize, append(Hash32Key(nil), buffer...), nil
}

func (hash32KeyCodec) Size(Hash32Key) int { return Hash32KeySize }

func (c hash32KeyCodec) EncodeNonTerminal(buffer []byte, key Hash32Key) (int, error) {
	return c.Encode(buffer, key)
}

func (hash32KeyCodec) DecodeNonTerminal(buffer []byte) (int, Hash32Key, error) {
	if len(buffer) < Hash32KeySize {
		return 0, nil, fmt.Errorf("%w: hash32 store key needs %d bytes, got %d", collcodec.ErrEncoding, Hash32KeySize, len(buffer))
	}
	return Hash32KeySize, append(Hash32Key(nil), buffer[:Hash32KeySize]...), nil
}

func (hash32KeyCodec) SizeNonTerminal(Hash32Key) int { return Hash32KeySize }

func (hash32KeyCodec) Stringify(key Hash32Key) string { return hex.EncodeToString(key) }

func (hash32KeyCodec) KeyType() string { return "trueopen.Hash32" }

func (c hash32KeyCodec) EncodeJSON(value Hash32Key) ([]byte, error) {
	if len(value) != Hash32KeySize {
		return nil, fmt.Errorf("hash32 store key must be %d bytes, got %d", Hash32KeySize, len(value))
	}
	return json.Marshal(c.Stringify(value))
}

func (hash32KeyCodec) DecodeJSON(b []byte) (Hash32Key, error) {
	var text string
	if err := json.Unmarshal(b, &text); err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(text)
	if err != nil {
		return nil, err
	}
	if len(raw) != Hash32KeySize {
		return nil, fmt.Errorf("hash32 store key must be %d bytes, got %d", Hash32KeySize, len(raw))
	}
	if hex.EncodeToString(raw) != text {
		return nil, fmt.Errorf("hash32 store key must use lowercase hex")
	}
	return raw, nil
}
