package keeper_test

import (
	"encoding/hex"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// available_bond is a *dynamic* quantity: available_bond = effective_active_bond
// - reserved_liability, and reserved_liability moves on every reservation and
// every release of any duty belonging to the same operator, while bond_version
// deliberately does not change for either. A candidate fact frozen at handraise
// time therefore carries an available_bond snapshot that is already stale by the
// time the assignment reserves against it.
//
// Requiring equality here made concurrent duties structurally impossible and
// halted consensus: the verifier assignment path reserves inside EndBlocker, so
// the rejection surfaced as a FinalizeBlock error rather than a task failure
// (devnet stopped at height 1011 with "current service bond does not match or
// cover the frozen liability fact"). §B.1.3 only states the inequality
// "available_bond >= required_task_liability", which is what this pins.
func TestFrozenTaskLiabilityAdmitsConcurrentDuties(t *testing.T) {
	f, identity, first := seedJailAdmissionLiabilityFixture(t, 11)
	address := sdk.MustAccAddressFromBech32(identity.Address)

	// The Task module freezes both facts from the same pre-reservation bond.
	second := first
	secondTaskID, err := hex.DecodeString(hubHash("concurrent-duty-second-task"))
	require.NoError(t, err)
	second.TaskID = secondTaskID
	created, err := f.keeper.AcquireCandidatePoolTaskRef(f.ctx, secondTaskID, second.CandidatePoolSnapshotID, 2)
	require.NoError(t, err)
	require.True(t, created)

	_, err = f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, first)
	require.NoError(t, err)

	bond, ok := f.keeper.GetServiceBond(sdk.UnwrapSDKContext(f.ctx), address, second.OrderValue)
	require.True(t, ok)
	require.Less(t, bond.AvailableBond, second.AvailableBondSnapshot,
		"the first duty must make the second fact's snapshot stale")
	require.GreaterOrEqual(t, bond.AvailableBond, bond.RequiredTaskLiability,
		"the operator must still be able to cover a second duty")
	require.Equal(t, second.BondVersionSnapshot, bond.BondVersion,
		"reserving must not bump bond_version, so nothing else absorbs the change")

	reservation, err := f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, second)
	require.NoError(t, err, "a stale available_bond snapshot must not reject the duty")
	require.Equal(t, types.TaskLiabilityStatusReserved, reservation.Status)

	state, err := f.keeper.GetServiceBondState(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Equal(t, 2*first.RequiredTaskLiability, state.ReservedLiability)
	for _, taskID := range [][]byte{first.TaskID, second.TaskID} {
		has, err := f.keeper.TaskLiabilityReservation.Has(
			f.ctx, types.NewTaskLiabilityReservationKey(taskID, shared.DutyVerifier, identity.Address))
		require.NoError(t, err)
		require.True(t, has)
	}
}

// Relaxing the equality must not relax the economics: the reservation still has
// to be covered by the *live* available bond at reservation time.
func TestFrozenTaskLiabilityStillRequiresLiveCoverage(t *testing.T) {
	f, identity, req := seedJailAdmissionLiabilityFixture(t, 12)

	bond, err := f.keeper.GetServiceBondState(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Greater(t, req.RequiredTaskLiability, uint64(0))
	// One unit short of a single duty, while the frozen fact still claims the
	// full pre-reservation snapshot.
	bond.ReservedLiability = bond.EffectiveActiveBond - (req.RequiredTaskLiability - 1)
	require.NoError(t, f.keeper.ServiceBond.Set(f.ctx, types.NewServiceBondKey(identity.Address), bond))

	_, err = f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, req)
	require.ErrorContains(t, err, "does not match or cover")

	has, err := f.keeper.TaskLiabilityReservation.Has(
		f.ctx, types.NewTaskLiabilityReservationKey(req.TaskID, shared.DutyVerifier, identity.Address))
	require.NoError(t, err)
	require.False(t, has)
}
