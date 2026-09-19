package keeper_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// Chain-halt regression. The jail ladder tombstones an operator and lets
// removeCortexIdentityOnTerminalBond delete its CortexNode row without first
// closing that operator's RESERVED task liabilities. Every such liability
// outlives its identity, and the deadline sweep that closes it later runs inside
// EndBlocker — so a close path that insists on reading CortexNode back becomes a
// FinalizeBlock error, i.e. a deterministic chain halt that also reproduces on
// every restart through handshake replay (localnet @ 115588).
//
// Both halves are locked here: that the tombstone really does leave a RESERVED
// liability behind, and that closing that liability afterwards succeeds.
func TestTombstonedOperatorClosesOrphanedTaskLiabilityWithoutHalting(t *testing.T) {
	threshold := types.DefaultHubParams().Service.TombstoneJailCountThreshold
	require.NotZero(t, threshold)
	f, identity, survivor := seedJailAdmissionLiabilityFixture(t, 5)
	survivorKey := types.NewTaskLiabilityReservationKey(survivor.TaskID, shared.DutyVerifier, identity.Address)
	require.NoError(t, f.keeper.DailySupport.Set(f.ctx, types.NewDailySupportKey(0, identity.Address), types.DailySupportState{
		Epoch: 0, OperatorAddress: identity.Address,
		SupportedProfilesHash: hubHashBytes("terminal-daily-support-profiles"),
		SignatureDigest:       hubHashBytes("terminal-daily-support-signature"),
		AcceptedHeight:        1,
	}))

	// The liability that has to outlive the identity.
	_, err := f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, survivor)
	require.NoError(t, err)

	// A second liability on the same operator. Its fault is what escalates the
	// jail ladder, mirroring the localnet trace where the deadline that tombstoned
	// the operator belonged to a different task than the one that halted the chain.
	pool, err := f.keeper.CurrentCandidatePool.Get(f.ctx)
	require.NoError(t, err)
	ladderTaskID, err := hex.DecodeString(hubHash("tombstone-ladder-task"))
	require.NoError(t, err)
	created, err := f.keeper.AcquireCandidatePoolTaskRef(f.ctx, ladderTaskID, pool.SnapshotId, 2)
	require.NoError(t, err)
	require.True(t, created)
	ladder := survivor
	ladder.TaskID = ladderTaskID
	_, err = f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, ladder)
	require.NoError(t, err)

	// One fault short of the ejection threshold, so the fault below is the step
	// that tombstones. slash_bps stays 0: the tombstone must come from the jail
	// ladder alone, which is the path the halted chain took.
	bond, err := f.keeper.GetServiceBondState(f.ctx, identity.Address)
	require.NoError(t, err)
	bond.JailCount = threshold - 1
	bond.Status = types.ServiceBondStatusJailed
	require.NoError(t, f.keeper.ServiceBond.Set(f.ctx, types.NewServiceBondKey(identity.Address), bond))

	_, err = f.keeper.ApplyTaskRoleFault(f.ctx, types.TaskRoleFaultFact{
		SessionID: hubHashBytes("session-tombstone-ladder"), TaskID: ladderTaskID,
		OperatorAddress: identity.Address, Duty: shared.DutyVerifier,
		FaultType:            types.FaultTypeVerifierMiss,
		ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE,
		EvidenceDigest:       hubHashBytes("tombstone-ladder-evidence"), SlashBps: 0, Height: 3,
	})
	require.NoError(t, err)

	// A terminal bond keeps a REVOKED proof-only identity while another task
	// liability remains open; that identity retires when its final hold closes.
	bond, err = f.keeper.GetServiceBondState(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Equal(t, types.ServiceBondStatusTombstoned, bond.Status)
	require.NotZero(t, bond.ReservedLiability)
	hasNode, err := f.keeper.CortexNode.Has(f.ctx, identity.Address)
	require.NoError(t, err)
	require.True(t, hasNode, "the remaining liability keeps proof-only identity material")
	proofOnly, err := f.keeper.CortexNode.Get(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Equal(t, types.ServiceKeyStatusRevoked, proofOnly.ServiceKeyStatus)
	orphan, err := f.keeper.TaskLiabilityReservation.Get(f.ctx, survivorKey)
	require.NoError(t, err)
	require.Equal(t, types.TaskLiabilityStatusReserved, orphan.Status)

	// The deadline sweep. Before the fix this returned "cortex node ... not found"
	// out of EndBlocker and killed the chain.
	require.NoError(t, f.keeper.ReleaseTaskLiabilities(
		f.ctx, "session-tombstone-orphan", hex.EncodeToString(survivor.TaskID), 4,
	))
	closed, err := f.keeper.TaskLiabilityReservation.Get(f.ctx, survivorKey)
	require.NoError(t, err)
	require.Equal(t, types.TaskLiabilityStatusReleased, closed.Status)
	hasNode, err = f.keeper.CortexNode.Has(f.ctx, identity.Address)
	require.NoError(t, err)
	require.False(t, hasNode, "the proof-only identity retires with its final hold")

	// The money and the candidate slot reference still settle. Only the
	// participant-scoped counter is gone, together with the identity that owned
	// it — and closing must not write that identity back.
	bond, err = f.keeper.GetServiceBondState(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Zero(t, bond.ReservedLiability)
	require.Equal(t, types.ServiceBondStatusTombstoned, bond.Status)
	hasNode, err = f.keeper.CortexNode.Has(f.ctx, identity.Address)
	require.NoError(t, err)
	require.False(t, hasNode, "closing a liability must not resurrect a retired identity")

	// "terminal bond, no identity" is not merely tolerated by genesis, it is what
	// ValidateServiceBondGenesis demands, so the resulting state must re-import.
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
}

// The relaxation above is scoped to terminal bonds. A live bond whose cortex
// identity has gone missing is still a broken invariant and must still be
// rejected rather than silently skipping the counter.
func TestTaskLiabilityCloseStillRejectsAMissingIdentityOnALiveBond(t *testing.T) {
	f, identity, req := seedJailAdmissionLiabilityFixture(t, 6)
	_, err := f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, req)
	require.NoError(t, err)

	bond, err := f.keeper.GetServiceBondState(f.ctx, identity.Address)
	require.NoError(t, err)
	require.True(t, types.IsLiveServiceBondStatus(bond.Status))
	require.NoError(t, f.keeper.CortexNode.Remove(f.ctx, identity.Address))

	err = f.keeper.ReleaseTaskLiabilities(
		f.ctx, "session-live-missing-identity", hex.EncodeToString(req.TaskID), 4,
	)
	require.ErrorContains(t, err, "not found")
}
