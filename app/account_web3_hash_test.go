package app

import (
	"encoding/json"
	"testing"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/stretchr/testify/require"
)

func TestWeb3EmptyTypeRepairRespectsDefinitionBoundaries(t *testing.T) {
	typed := apitypes.TypedData{Types: apitypes.Types{
		"EIP712Domain": {{Name: "name", Type: "string"}},
		"Empty":        {},
		"LongEmpty":    {},
		"Outer":        {{Name: "first", Type: "Empty"}, {Name: "other", Type: "LongEmpty"}, {Name: "Empty", Type: "string"}},
	}, PrimaryType: "Outer", Domain: apitypes.TypedDataDomain{Name: "Boundary regression"},
		Message: apitypes.TypedDataMessage{"first": map[string]interface{}{}, "other": map[string]interface{}{}, "Empty": "value"}}
	hasher := web3HashCompat{data: typed, affected: make(map[string]bool)}
	encoding := "Outer(Empty first,LongEmpty other,string Empty)Empty()LongEmpty()"
	require.Equal(t, encoding, string(hasher.encodeType("Outer")))
	empty := ethcrypto.Keccak256(ethcrypto.Keccak256([]byte("Empty()")))
	other := ethcrypto.Keccak256(ethcrypto.Keccak256([]byte("LongEmpty()")))
	message := ethcrypto.Keccak256(ethcrypto.Keccak256([]byte(encoding)), empty, other, ethcrypto.Keccak256([]byte("value")))
	domain, err := typed.HashStruct("EIP712Domain", typed.Domain.Map())
	require.NoError(t, err)
	digest, err := web3TypedDataHash(typed)
	require.NoError(t, err)
	require.Equal(t, ethcrypto.Keccak256([]byte{0x19, 0x01}, domain, message), digest)
}

func TestWeb3RepairHandlesNestedStructArraysWithoutMutation(t *testing.T) {
	typed := apitypes.TypedData{Types: apitypes.Types{
		"EIP712Domain": {{Name: "name", Type: "string"}},
		"Empty":        {},
		"Item":         {{Name: "empty", Type: "Empty"}, {Name: "n", Type: "uint256"}},
		"Nested":       {{Name: "items", Type: "Item[][]"}},
	}, PrimaryType: "Nested", Domain: apitypes.TypedDataDomain{Name: "Nested array regression"},
		Message: apitypes.TypedDataMessage{"items": []interface{}{
			[]interface{}{map[string]interface{}{"empty": map[string]interface{}{}, "n": "7"}, map[string]interface{}{"empty": map[string]interface{}{}, "n": "8"}},
			[]interface{}{},
		}}}
	before, err := json.Marshal(typed)
	require.NoError(t, err)
	empty := ethcrypto.Keccak256(ethcrypto.Keccak256([]byte("Empty()")))
	itemHash := func(n byte) []byte {
		word := make([]byte, 32)
		word[31] = n
		return ethcrypto.Keccak256(ethcrypto.Keccak256([]byte("Item(Empty empty,uint256 n)Empty()")), empty, word)
	}
	array := ethcrypto.Keccak256(ethcrypto.Keccak256(itemHash(7), itemHash(8)), ethcrypto.Keccak256(nil))
	message := ethcrypto.Keccak256(ethcrypto.Keccak256([]byte("Nested(Item[][] items)Empty()Item(Empty empty,uint256 n)")), array)
	domain, err := typed.HashStruct("EIP712Domain", typed.Domain.Map())
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		digest, err := web3TypedDataHash(typed)
		require.NoError(t, err)
		require.Equal(t, ethcrypto.Keccak256([]byte{0x19, 0x01}, domain, message), digest)
	}
	after, err := json.Marshal(typed)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestWeb3WithoutReachableEmptyTypesMatchesUpstream(t *testing.T) {
	typed := apitypes.TypedData{Types: apitypes.Types{
		"EIP712Domain": {{Name: "name", Type: "string"}},
		"Plain":        {{Name: "value", Type: "string"}},
		"Unused":       {},
	}, PrimaryType: "Plain", Domain: apitypes.TypedDataDomain{Name: "Unchanged regression"}, Message: apitypes.TypedDataMessage{"value": ""}}
	expected, _, err := apitypes.TypedDataAndHash(typed)
	require.NoError(t, err)
	actual, err := web3TypedDataHash(typed)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func TestWeb3RepairKeepsUpstreamValidation(t *testing.T) {
	for _, message := range []apitypes.TypedDataMessage{
		{}, {"empty": "wrong"}, {"empty": map[string]interface{}{"extra": "value"}},
		{"empty": map[string]interface{}{}, "extra": "value"},
	} {
		typed := apitypes.TypedData{Types: apitypes.Types{
			"EIP712Domain": {{Name: "name", Type: "string"}},
			"Empty":        {}, "Outer": {{Name: "empty", Type: "Empty"}},
		}, PrimaryType: "Outer", Domain: apitypes.TypedDataDomain{Name: "Invalid input regression"}, Message: message}
		_, _, expected := apitypes.TypedDataAndHash(typed)
		require.Error(t, expected)
		_, actual := web3TypedDataHash(typed)
		require.EqualError(t, actual, expected.Error())
	}
}
