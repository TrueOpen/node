package keeper

import (
	"context"
	"cosmossdk.io/collections"
	"errors"
	"testing"

	"github.com/TrueOpen/node/internal/testutil/dispatchpool"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/stretchr/testify/require"
)

type dispatchResponseKeyStore interface {
	GetCurrentServiceKey(context.Context, string, string) (hubtypes.CurrentServiceKeySnapshot, error)
	GetDispatchServiceProofKey(context.Context, shared.ParticipantType, string) (hubtypes.CurrentServiceKeySnapshot, error)
	GetBuilderState(context.Context, string) (hubtypes.BuilderState, error)
	StoreBuilder(context.Context, string, hubtypes.BuilderState) error
}

type unavailableHistoricalDispatchSet struct {
	*dispatchResponseHub
	lookups int
}

func (h *unavailableHistoricalDispatchSet) GetBuilderSetByID(sdk.Context, string) (hubtypes.BuilderSetState, error) {
	h.lookups++
	return hubtypes.BuilderSetState{}, collections.ErrNotFound
}

func TestDispatchResponseUsesRetainedTaskMembersAfterHistoricalSetPruning(t *testing.T) {
	f := newDispatchFixture(t)
	hub := installDispatchResponseKeys(t, f)
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	roster := f.committedRoster(t, entry, 0)
	f.record(t, entry)
	server := NewMsgServerImpl(f.keeper)
	opened, err := server.ReportDispatchRosterUnavailable(f.ctx, &types.MsgReportDispatchRosterUnavailable{TaskId: entry.Record.TaskId, DispatchRound: 0, ReporterAddress: f.owner})
	require.NoError(t, err)
	hub.revoke(t, f.ctx, f.hub.builders[0])
	unavailable := &unavailableHistoricalDispatchSet{dispatchResponseHub: hub}
	f.keeper.hubKeeper = unavailable
	server = NewMsgServerImpl(f.keeper)
	response, err := server.RespondDispatchChallenge(f.ctx, &types.MsgRespondDispatchChallenge{ChallengeId: opened.Challenge.ChallengeId, ResponderAddress: f.hub.builders[1], Response: &types.MsgRespondDispatchChallenge_Roster{Roster: roster}})
	require.NoError(t, err)
	require.Equal(t, types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_DISMISSED, response.Challenge.Status)
	require.Zero(t, unavailable.lookups, "the frozen Task selection already owns the membership fact")
}

// Builder lookups use the actual Hub projection and ACTIVE-only getter. Other
// dependencies retain the shared fixture's transaction-aware implementations.
type dispatchResponseHub struct {
	*dispatchTestHub
	identities     dispatchResponseKeyStore
	failingBuilder string
	readFailure    error
}

func (h *dispatchResponseHub) GetCurrentServiceKey(ctx context.Context, role, operator string) (hubtypes.CurrentServiceKeySnapshot, error) {
	if role != shared.ParticipantTypeBuilder {
		return h.dispatchTestHub.GetCurrentServiceKey(ctx, role, operator)
	}
	if operator == h.failingBuilder {
		return hubtypes.CurrentServiceKeySnapshot{}, h.readFailure
	}
	return h.identities.GetCurrentServiceKey(ctx, role, operator)
}

func (h *dispatchResponseHub) GetDispatchServiceProofKey(ctx context.Context, role shared.ParticipantType, operator string) (hubtypes.CurrentServiceKeySnapshot, error) {
	if role != shared.ParticipantType_PARTICIPANT_TYPE_BUILDER {
		return h.dispatchTestHub.GetDispatchServiceProofKey(ctx, role, operator)
	}
	if operator == h.failingBuilder {
		return hubtypes.CurrentServiceKeySnapshot{}, h.readFailure
	}
	return h.identities.GetDispatchServiceProofKey(ctx, role, operator)
}

func installDispatchResponseKeys(t *testing.T, f *dispatchFixture) *dispatchResponseHub {
	t.Helper()
	identities := dispatchpool.NewKeeper(f.keeper.storeService, f.keeper.transientStoreService, f.keeper.cdc, f.keeper.addressCodec, f.keeper.authority)
	for _, operator := range f.hub.builders {
		require.NoError(t, identities.StoreBuilder(f.ctx, operator, hubtypes.BuilderState{
			SchemaVersion: 1, BuilderAddress: operator, CurrentServiceAddress: operator,
			CurrentServicePubkey:    f.hub.keys[operator].PubKey().SerializeCompressed(),
			CurrentServiceKeyStatus: hubtypes.ServiceKeyStatusActive, ServiceAuthorizationNonce: 1,
		}))
	}
	hub := &dispatchResponseHub{dispatchTestHub: f.hub, identities: identities}
	f.keeper.hubKeeper = hub
	return hub
}

func (h *dispatchResponseHub) revoke(t *testing.T, ctx context.Context, operator string) {
	t.Helper()
	state, err := h.identities.GetBuilderState(ctx, operator)
	require.NoError(t, err)
	state.CurrentServiceKeyStatus = hubtypes.ServiceKeyStatusRevoked
	require.NoError(t, h.identities.StoreBuilder(ctx, operator, state))
	_, err = h.GetCurrentServiceKey(ctx, shared.ParticipantTypeBuilder, operator)
	require.ErrorContains(t, err, "not active", "the production getter must reproduce revoked-key lookup failure")
}

func TestGroupBuilderRebutsAfterOriginKeyRevocation(t *testing.T) {
	f := newDispatchFixture(t)
	hub := installDispatchResponseKeys(t, f)
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(144))
	server := NewMsgServerImpl(f.keeper)
	claim, err := server.ClaimDispatchCompensation(f.ctx, &types.MsgClaimDispatchCompensation{Order: entry.SignedOrder, Ticket: entry.WinnerTicket, Accept: entry.Accept, SubmitterAddress: f.worker})
	require.NoError(t, err)
	entry.Record.Rounds[0].Attempts[0].Outcome = types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_TIMEOUT
	entry.Record.Rounds[0].Attempts[0].TimeoutTierOrZero = 1
	signDispatchTestMaterial(t, "task.v1.DispatchRecordV1", entry.Record, f.hub.keys[entry.Record.BuilderAddress], func(s []byte) { entry.Record.BuilderServiceSignature = s })
	hub.revoke(t, f.ctx, f.hub.builders[0])
	hub.revoke(t, f.ctx, f.hub.builders[1])
	_, err = server.RebutDispatchCompensation(f.ctx, &types.MsgRebutDispatchCompensation{DispatchTicketHash: claim.DispatchTicketHash, Record: entry.Record, ResponderAddress: f.hub.builders[2]})
	require.NoError(t, err)
	row, err := f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
	require.NoError(t, err)
	require.Equal(t, types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_DISMISSED, row.Status)
	require.Equal(t, f.hub.builders[2], row.GetRebuttedByOrEmpty())
	require.Equal(t, shared.NewAmount(0), row.PaidAmount)
	_, pending, err := f.keeper.newDispatchDeadlineQueue(dispatchClaimMaturity).head(f.ctx, claim.RebuttalDeadlineHeight+1)
	require.NoError(t, err)
	require.False(t, pending)
	require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
}

func TestGroupBuilderRespondsAfterOriginKeyRevocation(t *testing.T) {
	for _, kind := range []string{"roster", "skip"} {
		t.Run(kind, func(t *testing.T) {
			f := newDispatchFixture(t)
			hub := installDispatchResponseKeys(t, f)
			server := NewMsgServerImpl(f.keeper)
			var opened *types.DispatchChallengeState
			var request *types.MsgRespondDispatchChallenge
			if kind == "skip" {
				entry, reject := f.rejectedDispatchEntry(t)
				f.record(t, entry)
				response, err := server.ChallengeDispatchSkip(f.ctx, &types.MsgChallengeDispatchSkip{TaskId: entry.Record.TaskId, AttemptRank: 0, ChallengerAddress: f.owner})
				require.NoError(t, err)
				opened = response.Challenge
				request = &types.MsgRespondDispatchChallenge{Response: &types.MsgRespondDispatchChallenge_Reject{Reject: reject}}
			} else {
				entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
				roster := f.committedRoster(t, entry, 0)
				f.record(t, entry)
				response, err := server.ReportDispatchRosterUnavailable(f.ctx, &types.MsgReportDispatchRosterUnavailable{TaskId: entry.Record.TaskId, DispatchRound: 0, ReporterAddress: f.owner})
				require.NoError(t, err)
				opened = response.Challenge
				request = &types.MsgRespondDispatchChallenge{Response: &types.MsgRespondDispatchChallenge_Roster{Roster: roster}}
			}
			hub.revoke(t, f.ctx, f.hub.builders[0])
			hub.revoke(t, f.ctx, f.hub.builders[1])
			request.ChallengeId, request.ResponderAddress = opened.ChallengeId, f.hub.builders[2]
			response, err := server.RespondDispatchChallenge(f.ctx, request)
			require.NoError(t, err)
			require.Equal(t, types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_DISMISSED, response.Challenge.Status)
			count, err := f.keeper.pendingDispatchCount(f.ctx, opened.TaskId)
			require.NoError(t, err)
			require.Zero(t, count)
			require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
		})
	}
}

func TestDispatchResponseAuthorizationPreservesLiveIdentityAndReadErrors(t *testing.T) {
	for _, scenario := range []string{"origin", "revoked origin signer", "revoked signer", "previous signer", "current replacement", "outsider", "origin read failure", "member read failure"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDispatchFixture(t)
			hub := installDispatchResponseKeys(t, f)
			origin, member := f.hub.builders[0], f.hub.builders[1]
			signer, expected := member, member
			var expectedError error
			switch scenario {
			case "origin":
				signer, expected = origin, origin
			case "revoked origin signer":
				hub.revoke(t, f.ctx, origin)
				signer, expectedError = origin, sdkerrors.ErrUnauthorized
			case "revoked signer":
				hub.revoke(t, f.ctx, member)
				expectedError = sdkerrors.ErrUnauthorized
			case "previous signer", "current replacement":
				state, err := hub.identities.GetBuilderState(f.ctx, member)
				require.NoError(t, err)
				state.XPreviousServicePubkey = &hubtypes.BuilderState_PreviousServicePubkey{PreviousServicePubkey: state.CurrentServicePubkey}
				state.XPreviousServiceAuthorizationNonce = &hubtypes.BuilderState_PreviousServiceAuthorizationNonce{PreviousServiceAuthorizationNonce: 1}
				state.XPreviousRetiredHeight = &hubtypes.BuilderState_PreviousRetiredHeight{PreviousRetiredHeight: 120}
				key := secp.PrivKeyFromBytes(bytes32(0x73))
				raw := shared.EVMAddressFromSecp256k1PublicKey(key.PubKey())
				state.CurrentServiceAddress, err = f.keeper.addressCodec.BytesToString(raw[:])
				require.NoError(t, err)
				state.CurrentServicePubkey, state.ServiceAuthorizationNonce = key.PubKey().SerializeCompressed(), 2
				require.NoError(t, hub.identities.StoreBuilder(f.ctx, member, state))
				if scenario == "previous signer" {
					expectedError = sdkerrors.ErrUnauthorized
				} else {
					signer = state.CurrentServiceAddress
				}
			case "outsider":
				signer, expectedError = f.owner, sdkerrors.ErrUnauthorized
			case "origin read failure", "member read failure":
				hub.failingBuilder = origin
				if scenario == "member read failure" {
					hub.failingBuilder = member
				}
				hub.readFailure = errors.New("injected service identity read failure")
				expectedError = hub.readFailure
			}
			operator, err := f.keeper.authorizeDispatchResponse(f.ctx, origin, f.hub.builders, signer, dispatchTaskBuilderScope)
			if expectedError != nil {
				require.ErrorIs(t, err, expectedError)
				require.Empty(t, operator)
			} else {
				require.NoError(t, err)
				require.Equal(t, expected, operator)
			}
		})
	}
}
