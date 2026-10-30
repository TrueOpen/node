package keeper

import (
	"testing"

	"github.com/TrueOpen/node/x/task/types"
	"github.com/stretchr/testify/require"
)

func TestOmissionRootRequiresTwoBlockMarginForNewerRoots(t *testing.T) {
	for _, tc := range []struct {
		name           string
		height         uint64
		exact, allowed bool
	}{
		{name: "same height newer version", height: 120, allowed: true},
		{name: "two blocks before order", height: 121, allowed: true},
		{name: "one block before order", height: 122},
		{name: "order height", height: 123},
		{name: "future root", height: 124},
		{name: "overflowing root", height: ^uint64(0)},
		{name: "exact committed root", height: 123, exact: true, allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDispatchFixture(t)
			boundHeight := uint64(120)
			if tc.exact {
				boundHeight = tc.height
			}
			bound := &types.SignedHandraiseRootV1{ChainId: "dispatch-test", BuilderAddress: f.hub.builders[0], ServiceAuthorizationNonce: 1, RootHeight: boundHeight, RootVersion: 1, LeafCount: 1, Root: bytes32(0x70)}
			signDispatchTestMaterial(t, "task.v1.SignedHandraiseRootV1", bound, f.hub.keys[bound.BuilderAddress], func(s []byte) { bound.BuilderServiceSignature = s })
			digest, err := dispatchMaterialDigest("task.v1.SignedHandraiseRootV1", bound)
			require.NoError(t, err)
			record := types.DispatchRecordState{BuilderAddress: bound.BuilderAddress, EarliestSubmitHeight: 123}
			round := &types.DispatchRoundV1{HandraiseRootDigest: digest[:]}
			evidence := *bound
			if !tc.exact {
				evidence.RootHeight, evidence.RootVersion, evidence.Root = tc.height, 2, bytes32(0x71)
				signDispatchTestMaterial(t, "task.v1.SignedHandraiseRootV1", &evidence, f.hub.keys[bound.BuilderAddress], func(s []byte) { evidence.BuilderServiceSignature = s })
			}
			err = f.keeper.verifyOmissionRoot(f.ctx, record, round, &evidence, bound)
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
