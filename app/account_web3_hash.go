package app

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// web3TypedDataHash repairs the empty-struct type hash in geth v1.15.11.
// Validation and field encoding stay upstream; only affected hash words are
// replaced. Keep the typed-data schema and the wallet-visible message intact.
func web3TypedDataHash(data apitypes.TypedData) ([]byte, error) {
	hasher := web3HashCompat{data: data, affected: make(map[string]bool)}
	if !hasher.needsRepair("EIP712Domain") && !hasher.needsRepair(data.PrimaryType) {
		digest, _, err := apitypes.TypedDataAndHash(data)
		return digest, err
	}
	domain, err := hasher.hashStruct("EIP712Domain", data.Domain.Map(), 1)
	if err != nil {
		return nil, err
	}
	message, err := hasher.hashStruct(data.PrimaryType, data.Message, 1)
	if err != nil {
		return nil, err
	}
	return ethcrypto.Keccak256([]byte{0x19, 0x01}, domain, message), nil
}

type web3HashCompat struct {
	data     apitypes.TypedData
	encoding apitypes.TypedData
	affected map[string]bool
}

func (h *web3HashCompat) needsRepair(name string) bool {
	name = strings.SplitN(name, "[", 2)[0]
	if affected, known := h.affected[name]; known {
		return affected
	}
	for _, dependency := range h.data.Dependencies(name, nil) {
		if len(h.data.Types[dependency]) == 0 {
			h.affected[name] = true
			return true
		}
	}
	h.affected[name] = false
	return false
}

func (h *web3HashCompat) encodeType(name string) []byte {
	encoded := h.data.EncodeType(name)
	for _, dependency := range h.data.Dependencies(name, nil) {
		if len(h.data.Types[dependency]) != 0 {
			continue
		}
		if name == dependency && bytes.Equal(encoded, []byte(name+")")) {
			return []byte(name + "()")
		}
		// Definitions follow a closing parenthesis. Requiring that boundary
		// avoids replacing a field name or a suffix of another type name.
		encoded = bytes.ReplaceAll(encoded, []byte(")"+dependency+")"), []byte(")"+dependency+"()"))
	}
	return encoded
}

func (h *web3HashCompat) hashStruct(name string, value map[string]interface{}, depth int) ([]byte, error) {
	// Validate the original schema and entire value tree once before adapting
	// child hashes. The temporary encoding schema never reaches the wallet.
	encoded, err := h.data.EncodeData(name, value, depth)
	if err != nil {
		return nil, err
	}
	if !h.needsRepair(name) {
		return ethcrypto.Keccak256(encoded), nil
	}
	if h.encoding.Types == nil {
		h.encoding = h.data
		h.encoding.Types = make(apitypes.Types, len(h.data.Types))
		for name, fields := range h.data.Types {
			adapted := make([]apitypes.Type, len(fields))
			copy(adapted, fields)
			for i, field := range fields {
				if h.needsRepair(field.Type) {
					adapted[i].Type = "bytes32"
					if start := strings.IndexByte(field.Type, '['); start >= 0 {
						adapted[i].Type += field.Type[start:]
					}
				}
			}
			h.encoding.Types[name] = adapted
		}
	}
	return h.hashValidatedStruct(name, value, depth)
}

func (h *web3HashCompat) hashValidatedStruct(name string, value map[string]interface{}, depth int) ([]byte, error) {
	adapted := make(map[string]interface{}, len(value))
	for key, item := range value {
		adapted[key] = item
	}
	for _, field := range h.data.Types[name] {
		if !h.needsRepair(field.Type) {
			continue
		}
		var err error
		if strings.HasSuffix(field.Type, "]") {
			base := strings.SplitN(field.Type, "[", 2)[0]
			adapted[field.Name], err = h.adaptArray(base, value[field.Name], depth+1)
		} else {
			child, ok := value[field.Name].(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("invalid validated Web3 struct value")
			}
			adapted[field.Name], err = h.hashValidatedStruct(field.Type, child, depth+1)
		}
		if err != nil {
			return nil, err
		}
	}
	// EIP-712 encodes a struct-valued field as its hash, exactly one bytes32
	// word. Geth can therefore encode the adapted fields and every array; only
	// its synthetic type hash must be replaced by the original corrected one.
	encoded, err := h.encoding.EncodeData(name, adapted, depth)
	if err != nil {
		return nil, err
	}
	if len(encoded) < common.HashLength {
		return nil, fmt.Errorf("invalid upstream Web3 struct encoding length")
	}
	copy(encoded[:common.HashLength], ethcrypto.Keccak256(h.encodeType(name)))
	return ethcrypto.Keccak256(encoded), nil
}

// adaptArray keeps the original shape while replacing affected structs with
// their corrected hashes. Array framing and hashing remain in geth.
func (h *web3HashCompat) adaptArray(base string, value interface{}, depth int) ([]interface{}, error) {
	array := reflect.ValueOf(value)
	if array.Kind() != reflect.Slice && array.Kind() != reflect.Array {
		return nil, fmt.Errorf("invalid validated Web3 array value")
	}
	adapted := make([]interface{}, array.Len())
	for i := 0; i < array.Len(); i++ {
		item := array.Index(i).Interface()
		kind := reflect.ValueOf(item).Kind()
		var err error
		if kind == reflect.Slice || kind == reflect.Array {
			adapted[i], err = h.adaptArray(base, item, depth+1)
		} else {
			child, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("invalid validated Web3 array element")
			}
			adapted[i], err = h.hashValidatedStruct(base, child, depth+1)
		}
		if err != nil {
			return nil, err
		}
	}
	return adapted, nil
}
