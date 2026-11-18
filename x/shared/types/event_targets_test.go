package types

import (
	"bytes"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"
)

func TestCanonicalEventTargetsUsesRawAddressOrderAndPreservesRoles(t *testing.T) {
	address := func(marker byte) string {
		value, err := bech32.ConvertAndEncode("trueopen", bytes.Repeat([]byte{marker}, 20))
		require.NoError(t, err)
		return value
	}
	first, last := address(1), address(0xf0)
	input := []*EventTarget{{Role: EventRole_EVENT_ROLE_BUILDER, Address: last}, {Role: EventRole_EVENT_ROLE_WORKER, Address: last}, {Role: EventRole_EVENT_ROLE_BUILDER, Address: first}, {Role: EventRole_EVENT_ROLE_BUILDER, Address: last}}
	result, err := CanonicalEventTargets(input)
	require.NoError(t, err)
	require.Equal(t, []*EventTarget{{Role: EventRole_EVENT_ROLE_WORKER, Address: last}, {Role: EventRole_EVENT_ROLE_BUILDER, Address: first}, {Role: EventRole_EVENT_ROLE_BUILDER, Address: last}}, result)
	require.Equal(t, last, input[0].Address, "canonicalization must not reorder caller-owned targets")
	for _, bad := range [][]*EventTarget{{nil}, {{Role: EventRole_EVENT_ROLE_UNSPECIFIED, Address: first}}, {{Role: EventRole(99), Address: first}}, {{Role: EventRole_EVENT_ROLE_BUILDER, Address: "invalid"}}} {
		_, err := CanonicalEventTargets(bad)
		require.Error(t, err)
	}
}
