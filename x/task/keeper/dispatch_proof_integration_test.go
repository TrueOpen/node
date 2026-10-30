package keeper

import (
	"bytes"
	"testing"

	"github.com/TrueOpen/wire/dispatch"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func TestDispatchDrawRejectsHonestOrderAndProvesFrozenWeightForgery(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(map[bool]string{false: "honest draw", true: "forged roster weight"}[wrong], func(t *testing.T) {
			f := newDispatchFixture(t)
			entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
			delta := uint64(0)
			if wrong {
				delta = 1
			}
			roster := f.committedRoster(t, entry, delta)
			f.record(t, entry)
			response, err := NewMsgServerImpl(f.keeper).ChallengeDispatchDraw(f.ctx, &types.MsgChallengeDispatchDraw{TaskId: entry.Record.TaskId, Roster: roster, ChallengerAddress: f.owner})
			if wrong {
				require.NoError(t, err)
				require.Equal(t, types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_UPHELD, response.Challenge.Status)
			} else {
				require.Error(t, err)
			}
			require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
		})
	}
}

func TestDispatchSkipResponseRequiresCommittedAuthenticatedReject(t *testing.T) {
	f := newDispatchFixture(t)
	entry, reject := f.rejectedDispatchEntry(t)
	f.record(t, entry)
	server := NewMsgServerImpl(f.keeper)
	challenge, err := server.ChallengeDispatchSkip(f.ctx, &types.MsgChallengeDispatchSkip{TaskId: entry.Record.TaskId, AttemptRank: 0, ChallengerAddress: f.owner})
	require.NoError(t, err)
	request := &types.MsgRespondDispatchChallenge{ChallengeId: challenge.Challenge.ChallengeId, ResponderAddress: f.hub.builders[0], Response: &types.MsgRespondDispatchChallenge_Reject{Reject: reject}}
	reject.ServiceSignature[0] ^= 1
	_, err = server.RespondDispatchChallenge(f.ctx, request)
	require.Error(t, err)
	row, err := f.keeper.readDispatchChallenge(f.ctx, challenge.Challenge.ChallengeId)
	require.NoError(t, err)
	require.True(t, dispatchChallengePending(row.Status))
	reject.ServiceSignature[0] ^= 1
	response, err := server.RespondDispatchChallenge(f.ctx, request)
	require.NoError(t, err)
	require.Equal(t, types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_DISMISSED, response.Challenge.Status)
	require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
}

func (f *dispatchFixture) rejectedDispatchEntry(t *testing.T) (*types.DispatchEntryV1, *types.DispatchRejectV1) {
	t.Helper()
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	member := &types.CandidateMemberRefV1{CandidatePoolSnapshotId: f.hub.pool.SnapshotId, Slot: 0, SlotVersion: 1, OperatorAddress: f.hub.builders[2]}
	reject := &types.DispatchRejectV1{SchemaVersion: 1, ChainId: "dispatch-test", TaskId: entry.Record.TaskId, TaskHash: entry.Record.TaskHash, DispatchTicketHash: bytes32(0x66), Member: member, ServiceAuthorizationNonce: 1, RejectReason: types.DispatchRejectReasonV1_DISPATCH_REJECT_REASON_V1_NO_CAPACITY}
	signDispatchTestMaterial(t, "task.v1.DispatchRejectV1", reject, f.hub.keys[member.OperatorAddress], func(s []byte) { reject.ServiceSignature = s })
	digest, err := dispatchMaterialDigest("task.v1.DispatchRejectV1", reject)
	require.NoError(t, err)
	entry.WinnerTicket.Rank = 1
	entry.Record.Rounds[0].Attempts[0].Rank = 1
	entry.Record.Rounds[0].Attempts = append([]*types.DispatchAttemptV1{{DispatchTicketHash: reject.DispatchTicketHash, Rank: 0, Member: member, Outcome: types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_REJECTED, RejectDigestOrZero32: digest[:]}}, entry.Record.Rounds[0].Attempts...)
	f.resignEntry(t, entry)
	return entry, reject
}

func TestSignedDoubleDispatchDoesNotRequireFundedTaskAndDeduplicatesReporters(t *testing.T) {
	f := newDispatchFixture(t)
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	entry.Record.Rounds[0].Attempts = append(entry.Record.Rounds[0].Attempts, &types.DispatchAttemptV1{DispatchTicketHash: bytes32(0x67), Rank: 1, Member: &types.CandidateMemberRefV1{CandidatePoolSnapshotId: f.hub.pool.SnapshotId, Slot: 0, SlotVersion: 1, OperatorAddress: f.hub.builders[2]}, Outcome: types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_ACCEPTED, RejectDigestOrZero32: make([]byte, 32)})
	signDispatchTestMaterial(t, "task.v1.DispatchRecordV1", entry.Record, f.hub.keys[entry.Record.BuilderAddress], func(s []byte) { entry.Record.BuilderServiceSignature = s })
	server := NewMsgServerImpl(f.keeper)
	request := &types.MsgChallengeDispatchDoubleDispatch{Record: entry.Record, Order: entry.SignedOrder, ChallengerAddress: f.owner}
	response, err := server.ChallengeDispatchDoubleDispatch(f.ctx, request)
	require.NoError(t, err)
	require.Equal(t, types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_UPHELD, response.Challenge.Status)
	found, err := f.keeper.TaskCore.Has(f.ctx, entry.Record.TaskId)
	require.NoError(t, err)
	require.False(t, found)
	request.ChallengerAddress = f.worker
	_, err = server.ChallengeDispatchDoubleDispatch(f.ctx, request)
	require.Error(t, err)
	require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
}

func TestDispatchOmissionUsesBuilderRootWithoutRecheckingStandingKey(t *testing.T) {
	f := newDispatchFixture(t)
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	extra := f.hub.member
	extra.Slot = 0
	extra.OperatorAddress = f.hub.builders[2]
	extra.BindingHash = bytes32(0x68)
	f.hub.extraMembers = append(f.hub.extraMembers, extra)
	f.hub.pool.ActiveCount = 2
	member := &types.CandidateMemberRefV1{CandidatePoolSnapshotId: f.hub.pool.SnapshotId, Slot: 0, SlotVersion: 1, OperatorAddress: extra.OperatorAddress}
	handraise := &types.StandingHandraiseV1{SchemaVersion: 1, ChainId: "dispatch-test", BuilderAddress: entry.Record.BuilderAddress, Member: member, Profiles: []*types.DispatchProfileV1{{ModelId: entry.SignedOrder.Order.ModelId, ProfileVersion: 1}}, FreeSlotCount: 1, IssuedHeight: 121, ExpiryHeight: 180, HandraiseNonce: 1, ServiceAuthorizationNonce: 1}
	signDispatchTestMaterial(t, "task.v1.StandingHandraiseV1", handraise, f.hub.keys[extra.OperatorAddress], func(s []byte) { handraise.ServiceSignature = s })
	digest, err := dispatchMaterialDigest("task.v1.StandingHandraiseV1", handraise)
	require.NoError(t, err)
	leaf, err := dispatch.HandraiseLeafDigest(digest, true, uint32(types.AdmitRejectReasonV1_ADMIT_REJECT_REASON_V1_NONE), 0)
	require.NoError(t, err)
	rootHash := dispatch.HandraiseRoot([][32]byte{leaf})
	root := &types.SignedHandraiseRootV1{ServiceAuthorizationNonce: 1, ChainId: "dispatch-test", BuilderAddress: entry.Record.BuilderAddress, RootHeight: 122, RootVersion: 1, LeafCount: 1, Root: rootHash[:]}
	signDispatchTestMaterial(t, "task.v1.SignedHandraiseRootV1", root, f.hub.keys[entry.Record.BuilderAddress], func(s []byte) { root.BuilderServiceSignature = s })
	rootDigest, err := dispatchMaterialDigest("task.v1.SignedHandraiseRootV1", root)
	require.NoError(t, err)
	entry.Record.Rounds[0].HandraiseRootDigest = rootDigest[:]
	entry.WinnerTicket.HandraiseRootDigest = rootDigest[:]
	entry.Record.Rounds[0].Attempts[0].Rank = 1
	entry.WinnerTicket.Rank = 1
	roster := f.committedRoster(t, entry, 0)
	f.record(t, entry)
	// The root authenticates admission. Losing the standing signer's current
	// proof key must not erase an honest omission proof against the Builder.
	delete(f.hub.keys, extra.OperatorAddress)
	request := &types.MsgChallengeDispatchOmission{TaskId: entry.Record.TaskId, Roster: roster, HandraiseRoot: root, InclusionProof: &types.HandraiseInclusionProofV1{Leaf: &types.HandraiseRootLeafV1{Handraise: handraise, Admitted: true, AdmitRejectReason: types.AdmitRejectReasonV1_ADMIT_REJECT_REASON_V1_NONE, BuilderMeasuredRttMs: 0}}, NonInclusionProof: &types.DispatchRosterNonInclusionProofV1{XSuccessor: &types.DispatchRosterNonInclusionProofV1_Successor{Successor: roster.Members[0]}}, ChallengerAddress: f.owner}
	response, err := NewMsgServerImpl(f.keeper).ChallengeDispatchOmission(f.ctx, request)
	require.NoError(t, err)
	require.Equal(t, types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_UPHELD, response.Challenge.Status)
	require.True(t, bytes.Equal(response.Challenge.TaskId, entry.Record.TaskId))
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Status)
}
