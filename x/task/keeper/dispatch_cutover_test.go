package keeper

import (
	"encoding/hex"
	"strconv"
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/stretchr/testify/require"
)

func rotateDispatchFixtureKey(t *testing.T, f *dispatchFixture, role, operator string, retired uint64, seed byte) string {
	t.Helper()
	old := f.hub.keys[operator]
	current := secp.PrivKeyFromBytes(bytes32(seed))
	raw := shared.EVMAddressFromSecp256k1PublicKey(current.PubKey())
	address, err := f.keeper.addressCodec.BytesToString(raw[:])
	require.NoError(t, err)
	if f.hub.bindings == nil {
		f.hub.bindings = make(map[string]hubtypes.CurrentServiceKeySnapshot)
	}
	f.hub.bindings[operator] = hubtypes.CurrentServiceKeySnapshot{
		ParticipantType: role, OperatorAddress: operator, ServiceAddress: address,
		ServicePubkey:      hex.EncodeToString(current.PubKey().SerializeCompressed()),
		AuthorizationNonce: 2, Status: hubtypes.ServiceKeyStatusActive,
		PreviousServicePubkey: old.PubKey().SerializeCompressed(), PreviousAuthorizationNonce: 1,
		PreviousRetiredHeight: retired,
	}
	return address
}

func TestFirstDispatchRecordingUsesCurrentSubmitterAndPreviousMaterials(t *testing.T) {
	for _, earliest := range []uint64{239, 240, 241} {
		t.Run(strconv.FormatUint(earliest, 10), func(t *testing.T) {
			f := newDispatchFixture(t)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(earliest)))
			entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
			current := rotateDispatchFixtureKey(t, f, shared.ParticipantTypeBuilder, f.hub.builders[0], 240, 0x78)
			rotateDispatchFixtureKey(t, f, shared.ParticipantTypeCortexNode, f.worker, 240, 0x79)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(max(240, earliest))))
			_, proofErr := f.keeper.verifyDispatchMaterial(f.ctx, "task.v1.DispatchRecordV1", entry.Record, &entry.SignedOrder.Order.EarliestSubmitHeight)
			if earliest < 240 {
				require.NoError(t, proofErr)
			} else {
				require.ErrorContains(t, proofErr, "previous key cannot prove a post-cutover order")
			}
			response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: current, Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
			require.NoError(t, err)
			stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
			require.NoError(t, err)
			if earliest < 240 {
				require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[0].Status)
				require.Equal(t, uint64(1), stream.NextExpectedSequence)
				record, err := f.keeper.readDispatchRecord(f.ctx, entry.Record.TaskId)
				require.NoError(t, err)
				require.Equal(t, uint64(1), record.ServiceAuthorizationNonce)
				require.Equal(t, entry.Record.BoundChangeHeight, record.BoundChangeHeight)
				_, err = NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
				require.Error(t, err, "the previous key never authorizes the outer transaction")
			} else {
				require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_TICKET_INVALID, response.Results[0].RejectCode)
				require.Zero(t, stream.NextExpectedSequence)
				require.Equal(t, shared.NewAmount(0), stream.FrozenTotal)
			}
		})
	}
}

func TestCompensationClaimUsesPreviousTicketAndAcceptance(t *testing.T) {
	f := newDispatchFixture(t)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(239))
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	rotateDispatchFixtureKey(t, f, shared.ParticipantTypeBuilder, f.hub.builders[0], 240, 0x78)
	workerService := rotateDispatchFixtureKey(t, f, shared.ParticipantTypeCortexNode, f.worker, 240, 0x79)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(260))
	claimed, err := NewMsgServerImpl(f.keeper).ClaimDispatchCompensation(f.ctx, &types.MsgClaimDispatchCompensation{Order: entry.SignedOrder, Ticket: entry.WinnerTicket, Accept: entry.Accept, SubmitterAddress: workerService})
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, claimed.Status)
	entry.Record.ServiceAuthorizationNonce = 3
	signDispatchTestMaterial(t, "task.v1.DispatchRecordV1", entry.Record, f.hub.keys[f.hub.builders[0]], func(signature []byte) { entry.Record.BuilderServiceSignature = signature })
	_, err = f.keeper.verifyDispatchMaterial(f.ctx, "task.v1.DispatchRecordV1", entry.Record, &entry.SignedOrder.Order.EarliestSubmitHeight)
	require.Error(t, err, "unknown generations are never inferred from a retained public key")
}

func TestPreviousRebuttalUsesUnrecordedClaimOrderHeight(t *testing.T) {
	f := newDispatchFixture(t)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(239))
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	rotateDispatchFixtureKey(t, f, shared.ParticipantTypeBuilder, f.hub.builders[0], 240, 0x78)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(260))
	server := NewMsgServerImpl(f.keeper)
	claim, err := server.ClaimDispatchCompensation(f.ctx, &types.MsgClaimDispatchCompensation{Order: entry.SignedOrder, Ticket: entry.WinnerTicket, Accept: entry.Accept, SubmitterAddress: f.worker})
	require.NoError(t, err)
	attempt := entry.Record.Rounds[0].Attempts[0]
	attempt.Outcome = types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_TIMEOUT
	attempt.TimeoutTierOrZero = 1
	signDispatchTestMaterial(t, "task.v1.DispatchRecordV1", entry.Record, f.hub.keys[f.hub.builders[0]], func(signature []byte) { entry.Record.BuilderServiceSignature = signature })
	response, err := server.RebutDispatchCompensation(f.ctx, &types.MsgRebutDispatchCompensation{DispatchTicketHash: claim.DispatchTicketHash, ResponderAddress: f.hub.builders[1], Record: entry.Record})
	require.NoError(t, err)
	require.Equal(t, types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_DISMISSED, response.ClaimStatus)
	after, err := f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
	require.NoError(t, err)
	require.Equal(t, uint64(239), after.EarliestSubmitHeight)
	found, err := f.keeper.DispatchRecord.Has(f.ctx, entry.Record.TaskId)
	require.NoError(t, err)
	require.False(t, found, "an accepted record is not required to defend an unrecorded ticket")
}

func TestAheadDispatchAndTakeoverSequencesRemainUnconsumed(t *testing.T) {
	for _, takeover := range []bool{false, true} {
		t.Run(strconv.FormatBool(takeover), func(t *testing.T) {
			f := newDispatchFixture(t)
			before, err := f.keeper.ReadStream(f.ctx, f.sessionID)
			require.NoError(t, err)
			order := f.signedOrder(t, 1, f.delegateKey)
			item := dispatchEntryItem(f.entry(t, order, f.hub.builders[0]))
			submitter := f.hub.builders[0]
			if takeover {
				item = &types.DispatchRecordEntryV1{Entry: &types.DispatchRecordEntryV1_Takeover{Takeover: &types.TakeoverEntryV1{SignedOrder: &order}}}
				submitter = f.hub.builders[1]
			}
			response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: submitter, Entries: []*types.DispatchRecordEntryV1{item}})
			require.NoError(t, err)
			require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SEQUENCE_NOT_NEXT, response.Results[0].RejectCode)
			after, err := f.keeper.ReadStream(f.ctx, f.sessionID)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestRejectedBatchDependencyCanRetryTheSameSequences(t *testing.T) {
	f := newDispatchFixture(t)
	first := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	second := f.entry(t, f.signedOrder(t, 1, f.delegateKey), f.hub.builders[0])
	first.Accept.ServiceSignature[0] ^= 1
	request := &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(first), dispatchEntryItem(second)}}
	response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, request)
	require.NoError(t, err)
	require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ACCEPT_SIGNATURE_INVALID, response.Results[0].RejectCode)
	require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CONFLICT, response.Results[1].RejectCode)
	stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Zero(t, stream.NextExpectedSequence)
	require.Equal(t, shared.NewAmount(0), stream.FrozenTotal)
	first.Accept.ServiceSignature[0] ^= 1
	response, err = NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, request)
	require.NoError(t, err)
	for _, result := range response.Results {
		require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Status)
	}
	stream, err = f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, uint64(2), stream.NextExpectedSequence)
}

func TestMissingTakeoverIdentityRejectsTheMessageWithoutCommit(t *testing.T) {
	f := newDispatchFixture(t)
	sessionID := f.sessionID
	f.sessionID = bytes32(0x77)
	unknown := f.signedOrder(t, 0, f.delegateKey)
	f.sessionID = sessionID
	valid := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	takeover := &types.DispatchRecordEntryV1{Entry: &types.DispatchRecordEntryV1_Takeover{Takeover: &types.TakeoverEntryV1{SignedOrder: &unknown}}}
	response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(valid), takeover}})
	require.ErrorIs(t, err, types.ErrInvalidSessionID)
	require.Nil(t, response)
	stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Zero(t, stream.NextExpectedSequence)
	require.Equal(t, shared.NewAmount(0), stream.FrozenTotal)
}

func TestRepeatedAttemptTicketHashIsAnIsolatedBusinessRejection(t *testing.T) {
	f := newDispatchFixture(t)
	signed := f.signedOrder(t, 0, f.delegateKey)
	invalid := f.entry(t, signed, f.hub.builders[0])
	invalid.WinnerTicket.Rank = 1
	signDispatchTestMaterial(t, "task.v1.DispatchTicketV1", invalid.WinnerTicket, f.hub.keys[f.hub.builders[0]], func(signature []byte) { invalid.WinnerTicket.BuilderServiceSignature = signature })
	ticketHash, err := dispatchMaterialDigest("task.v1.DispatchTicketV1", invalid.WinnerTicket)
	require.NoError(t, err)
	invalid.Accept.DispatchTicketHash = ticketHash[:]
	signDispatchTestMaterial(t, "task.v1.DispatchAcceptV1", invalid.Accept, f.hub.keys[f.worker], func(signature []byte) { invalid.Accept.ServiceSignature = signature })
	accepted := invalid.Record.Rounds[0].Attempts[0]
	accepted.DispatchTicketHash, accepted.Rank = ticketHash[:], 1
	invalid.Record.DispatchTicketHash = ticketHash[:]
	invalid.Record.Rounds[0].Attempts = []*types.DispatchAttemptV1{
		{DispatchTicketHash: ticketHash[:], Member: accepted.Member, Outcome: types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_VOIDED, RejectDigestOrZero32: make([]byte, 32)}, accepted,
	}
	signDispatchTestMaterial(t, "task.v1.DispatchRecordV1", invalid.Record, f.hub.keys[f.hub.builders[0]], func(signature []byte) { invalid.Record.BuilderServiceSignature = signature })
	valid := f.entry(t, signed, f.hub.builders[0])
	response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(invalid), dispatchEntryItem(valid)}})
	require.NoError(t, err)
	require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_TICKET_INVALID, response.Results[0].RejectCode)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[1].Status)
}
