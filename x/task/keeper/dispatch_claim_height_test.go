package keeper

import (
	"context"
	"fmt"
	"testing"

	"cosmossdk.io/collections"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	"github.com/TrueOpen/wire/dispatch"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/stretchr/testify/require"
)

type rebuttalFaultHub struct {
	*dispatchTestHub
	fail  bool
	facts []hubtypes.TaskRoleFaultFact
}

func (h *rebuttalFaultHub) ApplyTaskRoleFault(_ context.Context, fact hubtypes.TaskRoleFaultFact) (hubtypes.RoleFaultState, error) {
	if h.fail {
		return hubtypes.RoleFaultState{}, fmt.Errorf("injected Worker fault failure")
	}
	h.facts = append(h.facts, fact)
	return stubAppliedRoleFault(fact), nil
}

func TestClaimRebuttalRetainsIndependentKeyBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		builderRetired, workerRetired uint64
		unknownRecord, unknownReject  bool
	}{
		{name: "current"},
		{name: "previous Builder", builderRetired: 240},
		{name: "previous Cortex", workerRetired: 245},
		{name: "both previous", builderRetired: 240, workerRetired: 245},
		{name: "Builder retirement boundary", builderRetired: 239, workerRetired: 245},
		{name: "Cortex retirement boundary", builderRetired: 240, workerRetired: 239},
		{name: "post Builder retirement", builderRetired: 238},
		{name: "post Cortex retirement", workerRetired: 238},
		{name: "unknown record nonce", builderRetired: 240, unknownRecord: true},
		{name: "unknown reject nonce", workerRetired: 245, unknownReject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDispatchFixture(t)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(239))
			entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
			oldBuilder, oldWorker := f.hub.keys[f.hub.builders[0]], f.hub.keys[f.worker]
			workerService := f.worker
			// The claim uses current ticket/acceptance keys. Only the rebuttal
			// below uses the old keys, isolating each proof's retirement check.
			if tc.builderRetired != 0 {
				rotateDispatchFixtureKey(t, f, shared.ParticipantTypeBuilder, f.hub.builders[0], tc.builderRetired, 0x78)
				f.hub.keys[f.hub.builders[0]] = secp.PrivKeyFromBytes(bytes32(0x78))
				entry.WinnerTicket.ServiceAuthorizationNonce = 2
			}
			if tc.workerRetired != 0 {
				workerService = rotateDispatchFixtureKey(t, f, shared.ParticipantTypeCortexNode, f.worker, tc.workerRetired, 0x79)
				f.hub.keys[f.worker] = secp.PrivKeyFromBytes(bytes32(0x79))
				entry.Accept.ServiceAuthorizationNonce = 2
			}
			f.resignEntry(t, entry)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(260))
			server := NewMsgServerImpl(f.keeper)
			claim, err := server.ClaimDispatchCompensation(f.ctx, &types.MsgClaimDispatchCompensation{Order: entry.SignedOrder, Ticket: entry.WinnerTicket, Accept: entry.Accept, SubmitterAddress: workerService})
			require.NoError(t, err)
			reject := &types.DispatchRejectV1{SchemaVersion: 1, ChainId: "dispatch-test", TaskId: entry.Record.TaskId, TaskHash: entry.Record.TaskHash, DispatchTicketHash: claim.DispatchTicketHash, Member: entry.WinnerTicket.Target, ServiceAuthorizationNonce: 1, RejectReason: types.DispatchRejectReasonV1_DISPATCH_REJECT_REASON_V1_NO_CAPACITY}
			if tc.unknownReject {
				reject.ServiceAuthorizationNonce = 3
			}
			signDispatchTestMaterial(t, "task.v1.DispatchRejectV1", reject, oldWorker, func(s []byte) { reject.ServiceSignature = s })
			digest, err := dispatchMaterialDigest("task.v1.DispatchRejectV1", reject)
			require.NoError(t, err)
			attempt := entry.Record.Rounds[0].Attempts[0]
			attempt.Outcome = types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_REJECTED
			attempt.RejectDigestOrZero32 = digest[:]
			if tc.unknownRecord {
				entry.Record.ServiceAuthorizationNonce = 3
			}
			signDispatchTestMaterial(t, "task.v1.DispatchRecordV1", entry.Record, oldBuilder, func(s []byte) { entry.Record.BuilderServiceSignature = s })
			hub := &rebuttalFaultHub{dispatchTestHub: f.hub}
			f.keeper.hubKeeper = hub
			server = NewMsgServerImpl(f.keeper)
			before, err := f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
			require.NoError(t, err)
			events := len(sdk.UnwrapSDKContext(f.ctx).EventManager().Events())
			request := &types.MsgRebutDispatchCompensation{DispatchTicketHash: claim.DispatchTicketHash, Record: entry.Record, ResponderAddress: f.hub.builders[1]}
			request.XReject = &types.MsgRebutDispatchCompensation_Reject{Reject: reject}
			if tc.name == "both previous" {
				hub.fail = true
				_, err = server.RebutDispatchCompensation(f.ctx, request)
				require.ErrorContains(t, err, "injected Worker fault failure")
				unchanged, err := f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
				require.NoError(t, err)
				require.Equal(t, before, unchanged)
				require.Len(t, sdk.UnwrapSDKContext(f.ctx).EventManager().Events(), events)
				hub.fail = false
			}
			_, err = server.RebutDispatchCompensation(f.ctx, request)
			invalid := tc.builderRetired != 0 && tc.builderRetired <= 239 || tc.workerRetired != 0 && tc.workerRetired <= 239 || tc.unknownRecord || tc.unknownReject
			after, readErr := f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
			require.NoError(t, readErr)
			if invalid {
				require.ErrorIs(t, err, dispatch.ErrProofKeyUnavailable)
				require.Equal(t, before, after)
				require.Empty(t, hub.facts)
				require.Len(t, sdk.UnwrapSDKContext(f.ctx).EventManager().Events(), events)
				return
			}
			require.NoError(t, err)
			require.Equal(t, uint64(239), after.EarliestSubmitHeight)
			require.Equal(t, types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_DISMISSED, after.Status)
			require.Len(t, hub.facts, 1)
			require.Equal(t, hubtypes.FaultTypeWorkerDispatchContradiction, hub.facts[0].FaultType)
			require.Equal(t, f.worker, hub.facts[0].OperatorAddress)
			require.Equal(t, digest[:], hub.facts[0].EvidenceDigest)
			_, err = server.RebutDispatchCompensation(f.ctx, request)
			require.Error(t, err)
			require.Len(t, hub.facts, 1, "replay cannot punish the Worker twice")
			require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
		})
	}
}

func TestClaimOrderHeightSurvivesReplacementReplayAndGenesis(t *testing.T) {
	f := newDispatchFixture(t)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(239))
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(260))
	server := NewMsgServerImpl(f.keeper)
	request := &types.MsgClaimDispatchCompensation{Order: entry.SignedOrder, Ticket: entry.WinnerTicket, Accept: entry.Accept, SubmitterAddress: f.worker}
	claim, err := server.ClaimDispatchCompensation(f.ctx, request)
	require.NoError(t, err)
	row, err := f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
	require.NoError(t, err)
	require.Equal(t, uint64(239), row.EarliestSubmitHeight)
	// A replay's order body is not re-admitted and cannot replace the first fact.
	request.Order.Order.EarliestSubmitHeight = 250
	again, err := server.ClaimDispatchCompensation(f.ctx, request)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, again.Status)
	query := &queryServer{k: f.keeper}
	single, err := query.DispatchCompensationClaim(f.ctx, &types.QueryDispatchCompensationClaimRequest{DispatchTicketHash: claim.DispatchTicketHash})
	require.NoError(t, err)
	require.Equal(t, row, *single.Claim)
	listed, err := query.DispatchCompensationClaimsByBuilder(f.ctx, &types.QueryDispatchCompensationClaimsByBuilderRequest{BuilderAddress: row.BuilderAddress})
	require.NoError(t, err)
	require.Len(t, listed.Claims, 1)
	require.Equal(t, row, *listed.Claims[0])
	genesis := types.DefaultGenesis()
	require.NoError(t, f.keeper.exportDispatchGenesis(f.ctx, genesis))
	require.NoError(t, genesis.Validate())
	raw, err := proto.Marshal(genesis)
	require.NoError(t, err)
	var restored types.GenesisState
	require.NoError(t, proto.Unmarshal(raw, &restored))
	other := newDispatchFixture(t)
	other.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(other.ctx).WithBlockHeight(260))
	require.NoError(t, other.keeper.initDispatchGenesis(other.ctx, restored, 260))
	imported, err := other.keeper.readDispatchClaim(other.ctx, claim.DispatchTicketHash)
	require.NoError(t, err)
	require.Equal(t, row, imported)
	require.NoError(t, other.keeper.EnsureDispatchInvariant(other.ctx))
	for _, invalid := range []uint64{0, row.ClaimedHeight, row.ClaimedHeight + 1} {
		bad := row
		bad.EarliestSubmitHeight = invalid
		require.Error(t, types.ValidateDispatchClaim(bad))
		badGenesis := *types.DefaultGenesis()
		badGenesis.DispatchCompensationClaims = []types.DispatchCompensationClaimState{bad}
		require.Error(t, badGenesis.Validate())
	}
	mutated := row
	mutated.EarliestSubmitHeight++
	require.ErrorContains(t, f.keeper.writeDispatchClaim(f.ctx, mutated), "immutable")
	after, err := f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
	require.NoError(t, err)
	require.Equal(t, row, after)
	rotateDispatchFixtureKey(t, other, shared.ParticipantTypeBuilder, row.BuilderAddress, 240, 0x78)
	// Another ticket under the same TaskID may retain an entirely different
	// order height. It cannot invalidate this original ticket's old signature.
	recorded := newDispatchFixture(t)
	replacement := recorded.entry(t, recorded.signedOrder(t, 0, recorded.delegateKey), recorded.hub.builders[0])
	recorded.record(t, replacement)
	state, err := recorded.keeper.readDispatchRecord(recorded.ctx, replacement.Record.TaskId)
	require.NoError(t, err)
	require.Equal(t, row.TaskId, state.TaskId)
	state.RecordedHeight, state.ClaimWindowCloseHeight = 260, row.ClaimWindowCloseHeight
	for _, acceptedHeight := range []uint64{238, 250} {
		state.EarliestSubmitHeight = acceptedHeight
		// Store/projection conversion checks the retained primary just as Query does.
		require.NoError(t, other.keeper.writeDispatchRecord(other.ctx, state))
		entry.Record.Rounds[0].Attempts[0].Outcome = types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_TIMEOUT
		entry.Record.Rounds[0].Attempts[0].TimeoutTierOrZero = 1
		signDispatchTestMaterial(t, "task.v1.DispatchRecordV1", entry.Record, other.hub.keys[row.BuilderAddress], func(s []byte) { entry.Record.BuilderServiceSignature = s })
		_, err = other.keeper.verifyDispatchRebuttal(other.ctx, imported, entry.Record)
		require.NoError(t, err)
		postCutover := imported
		postCutover.EarliestSubmitHeight = 240
		_, err = other.keeper.verifyDispatchRebuttal(other.ctx, postCutover, entry.Record)
		require.ErrorIs(t, err, dispatch.ErrProofKeyUnavailable, "another ticket's earlier height cannot authorize a post-cutover claim")
	}
	found, err := f.keeper.DispatchClaimRebuttal.Has(f.ctx, collections.Join(row.RebuttalDeadlineHeight, row.DispatchTicketHash))
	require.NoError(t, err)
	require.True(t, found, "rejected mutation cannot damage the maturity index")
}

func TestClaimOrderHeightLifetime(t *testing.T) {
	for _, rebut := range []bool{false, true} {
		t.Run(fmt.Sprint(rebut), func(t *testing.T) {
			f := newDispatchFixture(t)
			entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(144))
			server := NewMsgServerImpl(f.keeper)
			request := &types.MsgClaimDispatchCompensation{Order: entry.SignedOrder, Ticket: entry.WinnerTicket, Accept: entry.Accept, SubmitterAddress: f.worker}
			claim, err := server.ClaimDispatchCompensation(f.ctx, request)
			require.NoError(t, err)
			if rebut {
				// Rotation after registration leaves the claim's authority intact.
				current := rotateDispatchFixtureKey(t, f, shared.ParticipantTypeBuilder, entry.Record.BuilderAddress, 145, 0x78)
				f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(145))
				stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
				require.NoError(t, err)
				stream.BoundChangeHeight = 145
				require.NoError(t, f.keeper.WriteStream(f.ctx, types.NewSessionStoreKey(f.sessionID), stream))
				entry.Record.Rounds[0].Attempts[0].Outcome = types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_TIMEOUT
				entry.Record.Rounds[0].Attempts[0].TimeoutTierOrZero = 1
				signDispatchTestMaterial(t, "task.v1.DispatchRecordV1", entry.Record, f.hub.keys[entry.Record.BuilderAddress], func(s []byte) { entry.Record.BuilderServiceSignature = s })
				_, err = server.RebutDispatchCompensation(f.ctx, &types.MsgRebutDispatchCompensation{DispatchTicketHash: claim.DispatchTicketHash, Record: entry.Record, ResponderAddress: entry.Record.BuilderAddress})
				require.Error(t, err, "the previous service cannot submit a new transaction")
				_, err = server.RebutDispatchCompensation(f.ctx, &types.MsgRebutDispatchCompensation{DispatchTicketHash: claim.DispatchTicketHash, Record: entry.Record, ResponderAddress: current})
				require.NoError(t, err)
			} else {
				height := claim.RebuttalDeadlineHeight + 1
				f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)))
				queue := f.keeper.newDispatchDeadlineQueue(dispatchClaimMaturity)
				_, found, err := queue.head(f.ctx, height)
				require.NoError(t, err)
				require.True(t, found)
				_, err = queue.run(f.ctx, height, 1, 1<<20)
				require.NoError(t, err)
			}
			row, err := f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
			require.NoError(t, err)
			require.Equal(t, entry.SignedOrder.Order.EarliestSubmitHeight, row.EarliestSubmitHeight)
			genesis := types.DefaultGenesis()
			require.NoError(t, f.keeper.exportDispatchGenesis(f.ctx, genesis))
			require.NoError(t, genesis.Validate())
			other := newDispatchFixture(t)
			other.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(other.ctx).WithBlockHeight(int64(row.GetResolvedHeight())))
			require.NoError(t, other.keeper.initDispatchGenesis(other.ctx, *genesis, row.GetResolvedHeight()))
			imported, err := other.keeper.readDispatchClaim(other.ctx, claim.DispatchTicketHash)
			require.NoError(t, err)
			require.Equal(t, row, imported)
			replay, err := server.ClaimDispatchCompensation(f.ctx, request)
			require.NoError(t, err)
			require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replay.Status)
			key, found, err := nextDueHeightIndex(f.ctx, f.keeper.DispatchClaimPrune, nil, ^uint64(0))
			require.NoError(t, err)
			require.True(t, found)
			require.GreaterOrEqual(t, key.K1(), row.ClaimWindowCloseHeight)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(key.K1())))
			queue := f.keeper.newDispatchDeadlineQueue(dispatchClaimPruning)
			_, found, err = queue.head(f.ctx, key.K1())
			require.NoError(t, err)
			require.True(t, found)
			_, err = queue.run(f.ctx, key.K1(), 1, 1<<20)
			require.NoError(t, err)
			found, err = f.keeper.DispatchCompensationClaim.Has(f.ctx, claim.DispatchTicketHash)
			require.NoError(t, err)
			require.False(t, found, "the order height needs no separate lifetime or index")
			require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
		})
	}
}
