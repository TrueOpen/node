package keeper

import (
	"fmt"
	"testing"

	"github.com/TrueOpen/node/x/task/types"
	"github.com/stretchr/testify/require"
)

func TestDispatchAuthorityRequiresACommittedSessionAnchor(t *testing.T) {
	for _, delegated := range []bool{false, true} {
		for _, anchor := range []uint64{0, 122, 123, 124} {
			t.Run(fmt.Sprintf("delegated=%t/anchor=%d", delegated, anchor), func(t *testing.T) {
				f := newDispatchFixture(t)
				key := f.ownerKey
				if delegated {
					key = f.delegateKey
				}
				order := f.signedOrder(t, 0, key).Order
				order.SessionAnchorHeight = anchor
				_, err := f.keeper.authorizeSessionOrder(f.ctx, signDispatchOrder(t, order, key), 123)
				if anchor == 0 || anchor > 123 {
					require.Error(t, err)
					code, business := dispatchRejectionCode(err)
					require.True(t, business)
					require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ORDER_SIGNATURE_INVALID, code)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}
