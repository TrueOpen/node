package keeper_test

import (
	"encoding/hex"
	"testing"

	"cosmossdk.io/collections"
	"github.com/TrueOpen/wire/dispatch"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
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
		SupportedModelsHash: hubHashBytes("terminal-daily-support-models"),
		SignatureDigest:     hubHashBytes("terminal-daily-support-signature"),
		AcceptedHeight:      1,
	}))

	// The liability that has to outlive the identity.
	_, err := f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, survivor)
	require.NoError(t, err)
	require.NoError(t, f.keeper.AcquirePendingStageDuty(f.ctx, identity.Address))

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
	require.NoError(t, f.keeper.WriteServiceBondValue(f.ctx, types.NewServiceBondKey(identity.Address), bond))

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
	proofOnly, err := f.keeper.ReadCortexNodeStore(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Equal(t, types.ServiceKeyStatusRevoked, proofOnly.ServiceKeyStatus)
	replacement := hubIdentity(t, 216)
	rotationProof := serviceKeyRotationProofBytes(sdk.UnwrapSDKContext(f.ctx).ChainID(), shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, sdk.MustAccAddressFromBech32(identity.Address), replacement.PubKey, 1, 2)
	_, err = keeper.NewMsgServerImpl(f.keeper).RotateServiceKey(f.ctx, &types.MsgRotateServiceKey{ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, OperatorAddress: identity.Address, ExpectedCurrentServiceAuthorizationNonce: 1, NewServicePubkey: replacement.PubKey, NewServiceKeyProof: hubSign(t, replacement, rotationProof)})
	require.ErrorContains(t, err, "terminal participant", "a proof-only terminal identity cannot register a replacement")
	orphan, err := f.keeper.ReadTaskLiabilityValue(f.ctx, survivorKey)
	require.NoError(t, err)
	require.Equal(t, types.TaskLiabilityStatusReserved, orphan.Status)

	// The deadline sweep. Before the fix this returned "cortex node ... not found"
	// out of EndBlocker and killed the chain.
	require.NoError(t, f.keeper.ReleaseTaskLiabilities(
		f.ctx, "session-tombstone-orphan", hex.EncodeToString(survivor.TaskID), 4,
	))
	closed, err := f.keeper.ReadTaskLiabilityValue(f.ctx, survivorKey)
	require.NoError(t, err)
	require.Equal(t, types.TaskLiabilityStatusReleased, closed.Status)
	hasNode, err = f.keeper.CortexNode.Has(f.ctx, identity.Address)
	require.NoError(t, err)
	require.True(t, hasNode, "the remaining stage duty keeps the proof-only identity")
	require.NoError(t, f.keeper.ReleasePendingStageDuty(f.ctx, identity.Address))
	hasNode, err = f.keeper.CortexNode.Has(f.ctx, identity.Address)
	require.NoError(t, err)
	require.True(t, hasNode, "retained candidate bindings still own dispatch proof authority")

	// The money and the candidate slot reference still settle. Only the
	// participant-scoped counter is gone, together with the identity that owned
	// it — and closing must not write that identity back.
	bond, err = f.keeper.GetServiceBondState(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Zero(t, bond.ReservedLiability)
	require.Equal(t, types.ServiceBondStatusTombstoned, bond.Status)
	hasNode, err = f.keeper.CortexNode.Has(f.ctx, identity.Address)
	require.NoError(t, err)
	require.True(t, hasNode, "closing a liability preserves the already retained proof identity")

	// Terminal proof-only identities with retained candidate bindings must import.
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
}

func TestTerminalCandidateProofIdentityFollowsSnapshotLifetime(t *testing.T) {
	f, identity, request := seedJailAdmissionLiabilityFixture(t, 7)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(3))
	require.NoError(t, f.keeper.SetTombstone(f.ctx, identity.Address, 3, "dispatch proof retention"))
	node, err := f.keeper.ReadCortexNodeStore(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Zero(t, node.ActiveTaskLiabilityCount)
	require.Zero(t, node.PendingStageDutyCount)
	require.Zero(t, node.PendingEvidenceSubmissionCount)
	proof, err := f.keeper.GetDispatchServiceProofKey(f.ctx, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, identity.Address)
	require.NoError(t, err)
	key, err := proof.ResolveProofKey(1)
	require.NoError(t, err)
	require.Equal(t, dispatch.ProofKeyRevoked, key.Status)
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	restored := initCandidatePoolFixture(t)
	restored.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restored.ctx).WithBlockHeight(3))
	require.NoError(t, restored.keeper.InitGenesis(restored.ctx, *exported))
	_, err = restored.keeper.GetDispatchServiceProofKey(restored.ctx, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, identity.Address)
	require.NoError(t, err)
	_, err = f.keeper.ReleaseCandidatePoolTaskRef(f.ctx, request.TaskID, request.CandidatePoolSnapshotID, 4)
	require.NoError(t, err)
	snapshot, err := f.keeper.CandidatePoolSnapshot.Get(f.ctx, request.CandidatePoolSnapshotID)
	require.NoError(t, err)
	_, err = f.keeper.ProcessCandidatePoolExpiries(f.ctx, snapshot.ExpiresHeight, 100)
	require.NoError(t, err)
	iter, err := f.keeper.CandidatePoolPruneIndex.Iterate(f.ctx, nil)
	require.NoError(t, err)
	require.True(t, iter.Valid())
	due, err := iter.Key()
	require.NoError(t, err)
	require.NoError(t, iter.Close())
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(due.K1())))
	_, err = f.keeper.ProcessCandidateSnapshotPrunes(f.ctx, due.K1(), 100)
	require.NoError(t, err)
	_, err = f.keeper.GetDispatchServiceProofKey(f.ctx, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, identity.Address)
	require.ErrorIs(t, err, collections.ErrNotFound)
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
