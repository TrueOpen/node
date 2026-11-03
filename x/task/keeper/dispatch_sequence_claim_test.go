package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"

	"cosmossdk.io/collections"
	"github.com/TrueOpen/node/internal/testutil/wirefixture"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	"github.com/TrueOpen/wire/dispatch"
	sdk "github.com/cosmos/cosmos-sdk/types"
	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/stretchr/testify/require"
)

type zeroDispatchPaymentHub struct{ types.HubKeeper }

func (zeroDispatchPaymentHub) PayDispatchCompensation(context.Context, string, string, []byte, []byte, uint64, uint64) (uint64, error) {
	return 0, nil
}

func TestZeroCompensationMarkerSurvivesSequenceWriters(t *testing.T) {
	for _, action := range []string{"cancel", "record", "close during cancel"} {
		t.Run(action, func(t *testing.T) {
			f := newDispatchFixture(t)
			entry := f.entry(t, f.signedOrder(t, 0, f.ownerKey), f.hub.builders[0])
			claim := dispatchFixtureClaim(t, f, entry, 144)
			f.keeper.hubKeeper = zeroDispatchPaymentHub{HubKeeper: f.hub}
			runDispatchFixtureQueue(t, f, dispatchClaimMaturity, claim.RebuttalDeadlineHeight+1)
			paid, err := f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
			require.NoError(t, err)
			require.Equal(t, shared.NewAmount(0), paid.PaidAmount)
			require.Equal(t, paid.Amount, paid.UnfilledAmount)
			dispatchFixtureHeight(f, 176)
			if action == "record" {
				f.record(t, f.entry(t, f.signedOrder(t, 0, f.ownerKey), f.hub.builders[0]))
			} else {
				_, err = NewMsgServerImpl(f.keeper).CancelOrder(f.ctx, &types.MsgCancelOrder{SessionId: f.sessionID, SignerAddress: f.owner, OrderSequence: 0})
				require.NoError(t, err)
				kind := sessionPendingCancel
				if action == "close during cancel" {
					requestTestWithdrawal(t, f, true, 0)
					kind = sessionPendingWithdraw
				}
				dispatchFixtureHeight(f, 206)
				changed, err := f.keeper.applySessionPendingRow(f.ctx, kind, sessionPendingRow{height: 206, sessionID: f.sessionID}, 206)
				require.NoError(t, err)
				require.True(t, changed)
			}
			sequence, err := f.keeper.OrderSequence.Get(f.ctx, types.NewOrderSequenceStateKey(f.sessionID, 0))
			require.NoError(t, err)
			require.True(t, sequence.CompensationPaid)
			require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
		})
	}
}

func TestPaidReceiptSurvivesPartialSessionCompactionAndRestart(t *testing.T) {
	f := newDispatchFixtureAtHeight(t, 121, func(params *types.TaskParamsV1) {
		params.Session.SessionOrderRetentionBlocks = 120
		params.Session.SessionTerminalSummaryRetentionBlocks = 120
	})
	entry := f.entry(t, f.signedOrder(t, 0, f.ownerKey), f.hub.builders[0])
	claim := dispatchFixtureClaim(t, f, entry, 144)
	runDispatchFixtureQueue(t, f, dispatchClaimMaturity, claim.RebuttalDeadlineHeight+1)
	dispatchFixtureHeight(f, 200)
	pending := requestTestWithdrawal(t, f, true, 0)
	dispatchFixtureHeight(f, pending.PendingWithdrawEffectiveHeight)
	changed, err := f.keeper.applySessionPendingRow(f.ctx, sessionPendingWithdraw, sessionPendingRow{height: pending.PendingWithdrawEffectiveHeight, sessionID: f.sessionID}, pending.PendingWithdrawEffectiveHeight)
	require.NoError(t, err)
	require.True(t, changed)
	gcHeight := pending.PendingWithdrawEffectiveHeight + 120
	dispatchFixtureHeight(f, gcHeight)
	swept, err := f.keeper.SweepSessionHistoryPrune(f.ctx, gcHeight, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), swept.VisitedCount)
	require.Zero(t, swept.CompactedCount)
	cursor, err := f.keeper.SessionHistoryPruneCursor.Get(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cursor.NextOrderSequence)
	require.Equal(t, make([]byte, types.Hash32Len), cursor.RollingSequenceRoot)
	require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
	genesis, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, genesis.Validate())
	fresh := initInternalFixtureWithHub(t, f.hub)
	require.NoError(t, fresh.keeper.Params.Remove(fresh.ctx))
	fresh.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(fresh.ctx).WithChainID("dispatch-test").WithBlockHeight(int64(gcHeight + 1)))
	require.NoError(t, fresh.keeper.InitGenesis(fresh.ctx, *genesis))
	swept, err = fresh.keeper.SweepSessionHistoryPrune(fresh.ctx, gcHeight+1, 4)
	require.NoError(t, err)
	require.Equal(t, uint64(1), swept.CompactedCount)
	summary, err := fresh.keeper.ReadSessionTerminalSummary(fresh.ctx, f.sessionID)
	require.NoError(t, err)
	require.Zero(t, summary.SequenceCount)
	require.Equal(t, pending.PendingWithdrawEffectiveHeight, summary.ClosedHeight)
	_, err = fresh.keeper.SweepSessionTerminalSummaryPrune(fresh.ctx, gcHeight+121, 1)
	require.NoError(t, err)
	_, err = fresh.keeper.ReadSessionTerminalSummary(fresh.ctx, f.sessionID)
	require.ErrorIs(t, err, collections.ErrNotFound)
	require.NoError(t, fresh.keeper.EnsureDispatchInvariant(fresh.ctx))
	genesis, err = fresh.keeper.ExportGenesis(fresh.ctx)
	require.NoError(t, err)
	require.NoError(t, genesis.Validate(), "the payment receipt is independent of the pruned Session and summary")
	require.Len(t, genesis.DispatchCompensationClaims, 1)
	_, err = NewMsgServerImpl(fresh.keeper).ClaimDispatchCompensation(fresh.ctx, &types.MsgClaimDispatchCompensation{Order: entry.SignedOrder, Ticket: entry.WinnerTicket, Accept: entry.Accept, SubmitterAddress: f.worker})
	require.NoError(t, err, "an exact retained ticket still replays after Session compaction")
}

func dispatchFixtureHeight(f *dispatchFixture, height uint64) {
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)))
}

func runDispatchFixtureQueue(t *testing.T, f *dispatchFixture, kind dispatchDeadlineAction, height uint64) {
	t.Helper()
	dispatchFixtureHeight(f, height)
	queue := f.keeper.newDispatchDeadlineQueue(kind)
	_, found, err := queue.head(f.ctx, height)
	require.NoError(t, err)
	require.True(t, found)
	usage, err := queue.run(f.ctx, height, 1, 1<<20)
	require.NoError(t, err)
	require.Equal(t, uint64(1), usage.visited)
}

func dispatchFixtureClaim(t *testing.T, f *dispatchFixture, entry *types.DispatchEntryV1, height uint64) types.DispatchCompensationClaimState {
	t.Helper()
	dispatchFixtureHeight(f, height)
	_, err := NewMsgServerImpl(f.keeper).ClaimDispatchCompensation(f.ctx, &types.MsgClaimDispatchCompensation{Order: entry.SignedOrder, Ticket: entry.WinnerTicket, Accept: entry.Accept, SubmitterAddress: entry.Accept.Member.OperatorAddress})
	require.NoError(t, err)
	row, err := f.keeper.readDispatchClaim(f.ctx, entry.Accept.DispatchTicketHash)
	require.NoError(t, err)
	return row
}

func TestSequencePaymentMarkerSurvivesClaimPruning(t *testing.T) {
	f := newDispatchFixture(t)
	entry := f.entry(t, f.signedOrder(t, 0, f.ownerKey), f.hub.builders[0])
	claim := dispatchFixtureClaim(t, f, entry, 144)
	runDispatchFixtureQueue(t, f, dispatchClaimMaturity, claim.RebuttalDeadlineHeight+1)
	key := types.NewOrderSequenceStateKey(f.sessionID, 0)
	sequence, err := f.keeper.OrderSequence.Get(f.ctx, key)
	require.NoError(t, err)
	require.True(t, sequence.CompensationPaid)
	prune, found, err := nextDueHeightIndex(f.ctx, f.keeper.DispatchClaimPrune, nil, ^uint64(0))
	require.NoError(t, err)
	require.True(t, found)
	runDispatchFixtureQueue(t, f, dispatchClaimPruning, prune.K1())
	_, err = f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
	require.ErrorIs(t, err, collections.ErrNotFound)
	sequence, err = f.keeper.OrderSequence.Get(f.ctx, key)
	require.NoError(t, err)
	require.True(t, sequence.CompensationPaid)
	late := prune.K1() + 2
	dispatchFixtureHeight(f, late)
	retry := f.entry(t, f.signedOrder(t, 0, f.ownerKey), f.hub.builders[0])
	params, err := f.keeper.ParamsForSession(f.ctx, f.sessionID)
	require.NoError(t, err)
	dispatchFixtureHeight(f, late+params.Dispatch.RecordDeadlineBlocks+1)
	request := &types.MsgClaimDispatchCompensation{Order: retry.SignedOrder, Ticket: retry.WinnerTicket, Accept: retry.Accept, SubmitterAddress: f.worker}
	_, err = NewMsgServerImpl(f.keeper).ClaimDispatchCompensation(f.ctx, request)
	require.ErrorContains(t, err, "sequence compensation is already paid")
	control := newDispatchFixture(t)
	require.Equal(t, f.sessionID, control.sessionID)
	dispatchFixtureHeight(control, uint64(sdk.UnwrapSDKContext(f.ctx).BlockHeight()))
	_, err = NewMsgServerImpl(control.keeper).ClaimDispatchCompensation(control.ctx, request)
	require.NoError(t, err, "the refreshed ticket otherwise satisfies admission")
	require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
}

func TestDismissedClaimReplacementUsesPinnedRetentionAndClearsIndexes(t *testing.T) {
	f := newDispatchFixture(t)
	entry := f.entry(t, f.signedOrder(t, 0, f.ownerKey), f.hub.builders[0])
	claim := dispatchFixtureClaim(t, f, entry, 144)
	entry.Record.Rounds[0].Attempts[0].Outcome = types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_TIMEOUT
	entry.Record.Rounds[0].Attempts[0].TimeoutTierOrZero = 1
	signDispatchTestMaterial(t, dispatchRecordMessageName, entry.Record, f.hub.keys[entry.Record.BuilderAddress], func(s []byte) { entry.Record.BuilderServiceSignature = s })
	_, err := NewMsgServerImpl(f.keeper).RebutDispatchCompensation(f.ctx, &types.MsgRebutDispatchCompensation{DispatchTicketHash: claim.DispatchTicketHash, Record: entry.Record, ResponderAddress: entry.Record.BuilderAddress})
	require.NoError(t, err)
	oldIndex, found, err := nextDueHeightIndex(f.ctx, f.keeper.DispatchClaimPrune, nil, ^uint64(0))
	require.NoError(t, err)
	require.True(t, found)
	cachedPrune := f.keeper.newDispatchDeadlineQueue(dispatchClaimPruning)
	_, found, err = cachedPrune.head(f.ctx, oldIndex.K1())
	require.NoError(t, err)
	require.True(t, found)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.Dispatch.DispatchRecordRetentionBlocks += 100
	_, err = NewMsgServerImpl(f.keeper).UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{Authority: sdk.AccAddress(f.keeper.authority).String(), Params: params})
	require.NoError(t, err)
	dispatchFixtureHeight(f, 146)
	retry := f.entry(t, f.signedOrder(t, 0, f.ownerKey), f.hub.builders[0])
	retry.WinnerTicket.DispatchRound = 1
	f.resignEntry(t, retry)
	dispatchFixtureClaim(t, f, retry, 167)
	_, err = f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
	require.ErrorIs(t, err, collections.ErrNotFound)
	present, err := f.keeper.DispatchClaimPrune.Has(f.ctx, oldIndex)
	require.NoError(t, err)
	require.False(t, present)
	usage, err := cachedPrune.run(f.ctx, oldIndex.K1(), 1, 1<<20)
	require.NoError(t, err, "another queue may already have removed the cached head")
	require.Equal(t, uint64(1), usage.visited)
	require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
}

func TestWireRoundAcceptorScopesRemainDisjoint(t *testing.T) {
	// The fixture uses REST hex for Hash32, so decode only those scope leaves.
	var raw struct {
		Cases []struct {
			Name string         `json:"name"`
			Old  map[string]any `json:"registered_scope"`
			New  map[string]any `json:"ticket_scope"`
			Same bool           `json:"same_round"`
		} `json:"round_identity_cases"`
	}
	require.NoError(t, json.Unmarshal(wirefixture.Read(t, "task", "dispatch_compensation_v1.json"), &raw))
	require.NotEmpty(t, raw.Cases)
	for _, c := range raw.Cases {
		t.Run(c.Name, func(t *testing.T) {
			f := newDispatchFixture(t)
			dispatchFixtureHeight(f, 2000)
			decode := func(input map[string]any) types.DispatchRoundAcceptorState {
				id, err := hex.DecodeString(input["session_id"].(string))
				require.NoError(t, err)
				address, err := dispatch.AddressBytes(input["builder_address"].(string))
				require.NoError(t, err)
				builder, err := f.keeper.addressCodec.BytesToString(address)
				require.NoError(t, err)
				return types.DispatchRoundAcceptorState{SessionId: id, OrderSequence: uint64(input["order_sequence"].(float64)), BuilderAddress: builder, BoundChangeHeight: uint64(input["bound_change_height"].(float64)), VrfEpoch: uint64(input["vrf_epoch"].(float64)), DispatchRound: uint32(input["dispatch_round"].(float64)), DispatchTicketHash: bytes.Repeat([]byte{0xaa}, 32), RegisteredHeight: 2000}
			}
			old, newScope := decode(c.Old), decode(c.New)
			require.NoError(t, f.keeper.writeDispatchRoundAcceptor(f.ctx, old))
			allowed, err := f.keeper.dispatchRoundClaimable(f.ctx, &types.DispatchTicketV1{SessionId: newScope.SessionId, OrderSequence: newScope.OrderSequence, BuilderAddress: newScope.BuilderAddress, BoundChangeHeight: newScope.BoundChangeHeight, VrfEpoch: newScope.VrfEpoch, DispatchRound: newScope.DispatchRound}, bytes.Repeat([]byte{0xbb}, 32), 2000)
			require.NoError(t, err)
			require.Equal(t, !c.Same, allowed)
			require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
		})
	}
}

func TestReturnedBuilderDoesNotReuseEarlierBindingAcceptor(t *testing.T) {
	var doc struct {
		Scenario struct {
			Old struct {
				Bound uint64 `json:"bound_change_height"`
			} `json:"old_scope"`
			FirstRegistered    uint64 `json:"first_takeover_registered_height"`
			FirstEffective     uint64 `json:"first_takeover_effective_height"`
			ReturnedRegistered uint64 `json:"return_registered_height"`
			ReturnedEffective  uint64 `json:"return_effective_height"`
			OldClaim           uint64 `json:"old_claimed_height"`
			Rebutted           uint64 `json:"old_rebutted_height"`
			NewESH             uint64 `json:"new_earliest_submit_height"`
			NewClaim           uint64 `json:"new_claimed_height"`
		} `json:"same_epoch_return_case"`
	}
	require.NoError(t, json.Unmarshal(wirefixture.Read(t, "task", "dispatch_compensation_v1.json"), &doc))
	s := doc.Scenario
	f := newDispatchFixtureAtHeight(t, s.Old.Bound)
	origin, other := f.hub.builders[0], f.hub.builders[1]
	x := f.worker
	addWorker := func(seed byte) string {
		key := secp.PrivKeyFromBytes(bytes32(seed))
		address := shared.EVMAddressFromSecp256k1PublicKey(key.PubKey())
		name, err := f.keeper.addressCodec.BytesToString(address[:])
		require.NoError(t, err)
		f.hub.keys[name] = key
		return name
	}
	a, y := addWorker(0x22), addWorker(0x23)
	old := f.entry(t, f.signedOrder(t, 0, f.ownerKey), origin)
	f.worker = a
	accepted := f.entry(t, *old.SignedOrder, origin)
	accepted.WinnerTicket.Target.Slot = 2
	accepted.WinnerTicket.Rank = 1
	accepted.Record.Rounds[0].Attempts[0].Rank = 1
	f.resignEntry(t, accepted)
	old.Record.Rounds[0].Attempts[0].Outcome = types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_TIMEOUT
	old.Record.Rounds[0].Attempts[0].TimeoutTierOrZero = 1
	accepted.Record.Rounds[0].Attempts[0].Outcome = types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_VOIDED
	old.Record.Rounds[0].Attempts = append(old.Record.Rounds[0].Attempts, accepted.Record.Rounds[0].Attempts[0])
	old.Record.DispatchTicketHash = make([]byte, 32)
	signDispatchTestMaterial(t, dispatchRecordMessageName, old.Record, f.hub.keys[origin], func(sig []byte) { old.Record.BuilderServiceSignature = sig })
	takeover := func(builder string, registered, effective uint64) {
		dispatchFixtureHeight(f, registered)
		signed := f.signedOrder(t, 0, f.ownerKey)
		response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: builder, Entries: []*types.DispatchRecordEntryV1{{Entry: &types.DispatchRecordEntryV1_Takeover{Takeover: &types.TakeoverEntryV1{SignedOrder: &signed}}}}})
		require.NoError(t, err)
		require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[0].Status)
		stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
		require.NoError(t, err)
		require.Equal(t, effective, stream.GetPendingTakeover().ActivationHeight)
		dispatchFixtureHeight(f, effective)
		changed, err := f.keeper.applySessionPendingRow(f.ctx, sessionPendingTakeover, sessionPendingRow{height: effective, sessionID: f.sessionID}, effective)
		require.NoError(t, err)
		require.True(t, changed)
	}
	takeover(other, s.FirstRegistered, s.FirstEffective)
	takeover(origin, s.ReturnedRegistered, s.ReturnedEffective)
	f.worker = y
	dispatchFixtureHeight(f, s.NewESH)
	current := f.entry(t, f.signedOrder(t, 0, f.ownerKey), origin)
	current.WinnerTicket.Target.Slot = 3
	f.resignEntry(t, current)
	f.worker = x
	claim := dispatchFixtureClaim(t, f, old, s.OldClaim)
	dispatchFixtureHeight(f, s.Rebutted)
	_, err := NewMsgServerImpl(f.keeper).RebutDispatchCompensation(f.ctx, &types.MsgRebutDispatchCompensation{DispatchTicketHash: claim.DispatchTicketHash, Record: old.Record, ResponderAddress: origin})
	require.NoError(t, err)
	row := dispatchFixtureClaim(t, f, current, s.NewClaim)
	require.Equal(t, s.ReturnedEffective, row.BoundChangeHeight)
	_, err = f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
	require.ErrorIs(t, err, collections.ErrNotFound)
	sequence, err := f.keeper.OrderSequence.Get(f.ctx, types.NewOrderSequenceStateKey(f.sessionID, 0))
	require.ErrorIs(t, err, collections.ErrNotFound)
	require.False(t, sequence.CompensationPaid)
	require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
	genesis, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, genesis.Validate())
	require.Len(t, genesis.DispatchRoundAcceptors, 1)
	require.Equal(t, s.Old.Bound, genesis.DispatchRoundAcceptors[0].BoundChangeHeight)
	fresh := initInternalFixtureWithHub(t, f.hub)
	require.NoError(t, fresh.keeper.Params.Remove(fresh.ctx))
	fresh.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(fresh.ctx).WithChainID("dispatch-test").WithBlockHeight(int64(s.NewClaim + 1)))
	require.NoError(t, fresh.keeper.InitGenesis(fresh.ctx, *genesis))
	restored, err := fresh.keeper.ExportGenesis(fresh.ctx)
	require.NoError(t, err)
	require.Equal(t, genesis.DispatchRoundAcceptors, restored.DispatchRoundAcceptors)
	require.NoError(t, fresh.keeper.EnsureDispatchInvariant(fresh.ctx))
}
