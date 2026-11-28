package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// legacyHandraiseHubStub adds a populated candidate pool of plain Verifiers to
// the anchor-only Hub view: every pool operator has verification capability,
// fresh declared support and a bond that covers stake and task liability.
type legacyHandraiseHubStub struct {
	anchorOnlyHubStub
	// slots maps a candidate-pool slot to its operator.
	slots map[uint32]string
	// duties counts the pending stage duties acquired per operator.
	duties map[string]int
}

func (s legacyHandraiseHubStub) ResolveCandidatePoolMember(_ context.Context, _ []byte, slot uint32) (hubtypes.CandidatePoolMemberState, hubtypes.CandidateSlotBindingState, error) {
	operator, ok := s.slots[slot]
	if !ok {
		return hubtypes.CandidatePoolMemberState{}, hubtypes.CandidateSlotBindingState{}, context.Canceled
	}
	binding := bytes.Repeat([]byte{byte(0x80 + slot)}, types.Hash32Len)
	return hubtypes.CandidatePoolMemberState{Slot: slot, SlotVersion: 1, OperatorAddress: operator, BindingHash: binding},
		hubtypes.CandidateSlotBindingState{Slot: slot, SlotVersion: 1, OperatorAddress: operator, BindingHash: binding}, nil
}

func (legacyHandraiseHubStub) GetModelStatus(sdk.Context, []byte) hubtypes.ModelStatus {
	return hubtypes.ModelStatusActive
}

func (legacyHandraiseHubStub) IsProfileFrozen(sdk.Context, []byte, uint32) bool { return false }

func (legacyHandraiseHubStub) GetNodeTombstone(sdk.Context, sdk.AccAddress) bool { return false }

func (legacyHandraiseHubStub) GetNodeJailStatus(sdk.Context, sdk.AccAddress, string) hubtypes.JailStatus {
	return hubtypes.JailStatusNone
}

func (legacyHandraiseHubStub) GetModelCapability(_ sdk.Context, address sdk.AccAddress, modelID []byte) (hubtypes.ModelCapabilitySnapshot, bool) {
	return hubtypes.ModelCapabilitySnapshot{
		OperatorAddress: address.String(), ModelID: modelID, CapabilityVersion: 3, VerificationCapability: true,
	}, true
}

func (legacyHandraiseHubStub) GetModelSupport(_ sdk.Context, address sdk.AccAddress, modelID []byte) (hubtypes.ModelSupportSnapshot, bool) {
	return hubtypes.ModelSupportSnapshot{
		OperatorAddress: address.String(), ModelID: modelID, DeclaredSupport: true, SupportActive: true,
		SuspendReason:          hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_NONE,
		SupportFreshUntilEpoch: 1 << 40, SupportVersion: 4,
	}, true
}

func (legacyHandraiseHubStub) GetServiceBond(_ sdk.Context, address sdk.AccAddress, _ uint64) (hubtypes.ServiceBondSnapshot, bool) {
	return hubtypes.ServiceBondSnapshot{
		OperatorAddress: address.String(), Status: hubtypes.ServiceBondStatusActive,
		ActiveBond: 1_000_000, EffectiveActiveBond: 1_000_000, AvailableBond: 900_000,
		RequiredTaskLiability: 30_000, BondVersion: 5,
	}, true
}

func (s legacyHandraiseHubStub) AcquirePendingStageDuty(_ context.Context, operator string) error {
	s.duties[operator]++
	return nil
}

// legacyHandraiseFixture is a task verified by Verifiers alone, with one
// Verifier window READY and collecting hand-raises. No anchor audit and no
// Verifier panel is involved anywhere in its life.
type legacyHandraiseFixture struct {
	*verificationFixture
	builders []string
	// duties counts the pending stage duties the Hub was asked to acquire.
	duties map[string]int
	// window is the READY window and members are its members in rank order.
	window  types.VerifierCandidateWindowState
	members []types.VerifierCandidateWindowMemberState
}

const legacyHandraiseVerifiers = 5

// populatePool fills the task's candidate pool with the given slots and five
// more Verifiers after them, and installs the Hub view that makes every pool
// operator an eligible Verifier.
func (f *legacyHandraiseFixture) populatePool(hub anchorOnlyHubStub, slots map[uint32]string) {
	first := uint32(len(slots))
	for i := uint32(0); i < legacyHandraiseVerifiers; i++ {
		slots[first+i] = sdk.AccAddress(bytes.Repeat([]byte{byte(0x40 + i)}, 20)).String()
	}
	f.hub.pool.SlotCapacity = 16
	f.hub.poolBitmap = make([]byte, 2)
	for slot := range slots {
		f.hub.poolBitmap[slot/8] |= 1 << (slot % 8)
	}
	f.hub.pool.ActiveCount = uint32(len(slots))
	f.duties = map[string]int{}
	f.keeper.hubKeeper = legacyHandraiseHubStub{anchorOnlyHubStub: hub, slots: slots, duties: f.duties}
	f.server = msgServer{k: f.keeper}
}

func (f *legacyHandraiseFixture) at(height uint64) sdk.Context {
	return sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)).WithEventManager(sdk.NewEventManager())
}

func (f *legacyHandraiseFixture) core(t *testing.T) types.TaskCoreState {
	t.Helper()
	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	return core
}

// openWindow materializes the frozen window of verifyRound with the EndBlock
// window-build sweep and loads the READY window and its members.
func (f *legacyHandraiseFixture) openWindow(t *testing.T, verifyRound uint32) {
	t.Helper()
	windowKey := types.NewVerifyRoundKey(f.taskKey, verifyRound)
	frozen, err := f.keeper.VerifierCandidateWindow.Get(f.ctx, windowKey)
	require.NoError(t, err)
	require.Equal(t, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN, frozen.Status)
	require.Equal(t, uint32(legacyHandraiseVerifiers), frozen.EligibleCount)
	require.NotZero(t, frozen.WindowSize)

	build := frozen.WindowRandomnessHeight
	usage, err := f.keeper.processVerifierWindowBuildIndexWithBudget(f.at(build), build, 10, 1<<30)
	require.NoError(t, err)
	require.Equal(t, uint64(1), usage.visited)
	f.window, err = f.keeper.VerifierCandidateWindow.Get(f.ctx, windowKey)
	require.NoError(t, err)
	require.Equal(t, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY, f.window.Status)
	require.Equal(t, types.VerificationStatus_VERIFICATION_STATUS_VERIFY_COLLECTION_OPEN, f.core(t).VerificationStatus)
	require.GreaterOrEqual(t, f.window.BuilderProposalCloseHeight, f.window.GeneratedHeight)
	for rank := uint32(0); rank < f.window.WindowSize; rank++ {
		member, err := f.keeper.ReadVerifierWindowMember(f.ctx, types.NewVerifierWindowMemberKey(f.taskKey, verifyRound, rank))
		require.NoError(t, err)
		f.members = append(f.members, member)
	}
}

// newLegacyHandraiseFixture stages a task on a plain two-commitment Profile,
// puts five Verifiers next to the Worker in its candidate pool, accepts the
// receipt (which freezes their eligibility) and opens the round-1 window.
func newLegacyHandraiseFixture(t *testing.T) *legacyHandraiseFixture {
	t.Helper()
	base := newAnchorOnlyFixture(t, 1_000_000, false)
	f := &legacyHandraiseFixture{verificationFixture: base.verificationFixture, builders: base.builders}
	f.populatePool(base.keeper.hubKeeper.(anchorOnlyHubStub), map[uint32]string{0: base.worker})
	base.server = f.server
	base.submitReceipt(t)
	f.openWindow(t, types.VerifyRoundV1)
	return f
}

// newLegacyChallengeHandraiseFixture stages a task whose round 1 closed with a
// PASS verdict, opens its challenge round through MsgOpenChallengeRound and
// opens the round-2 window. The pool holds the Worker, the three round-1
// Verifiers (which a challenge round leaves out) and five more Verifiers.
func newLegacyChallengeHandraiseFixture(t *testing.T) *legacyHandraiseFixture {
	t.Helper()
	f := &legacyHandraiseFixture{verificationFixture: newVerificationFixture(t)}
	worker := sdk.AccAddress(bytes.Repeat([]byte{0xc0}, 20))
	f.builders = []string{
		sdk.AccAddress(bytes.Repeat([]byte{0xb1}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0xb2}, 20)).String(),
	}
	f.populatePool(
		anchorOnlyHubStub{anchorHubStub: anchorHubStub{verificationHubStub: f.hub}, beacon: bytes32(0x77)},
		map[uint32]string{0: worker.String(), 1: f.operators[0], 2: f.operators[1], 3: f.operators[2]},
	)
	f.setTaskBuilders(t, f.builders...)

	core := f.core(t)
	core.AcceptedTaskHash = bytes32(0x81)
	core.OrderValue = shared.NewAmount(800)
	core.TaskPhase = types.TaskPhase_TASK_PHASE_REVEALING
	core.ReceiptStatus = types.ReceiptStatus_RECEIPT_STATUS_RECEIPT_ACCEPTED
	core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_VERIFICATION_PASSED
	core.EffectiveVerifyRound = types.VerifyRoundV1
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, f.taskKey, core))
	assignment, err := f.keeper.ReadTaskAssignment(f.ctx, f.taskKey)
	require.NoError(t, err)
	assignment.WinnerWorker = worker.String()
	require.NoError(t, f.keeper.WriteTaskAssignment(f.ctx, f.taskKey, assignment))
	closed := types.VerificationRoundState{
		TaskId: f.taskID, VerifyRound: types.VerifyRoundV1, RoundId: bytes32(0x82), GeneratedTokenCount: 1000,
		XClosedHeight:   &types.VerificationRoundState_ClosedHeight{ClosedHeight: 10},
		XVerdict:        &types.VerificationRoundState_Verdict{Verdict: types.TaskVerdict_TASK_VERDICT_PASS},
		XRoundFactsHash: &types.VerificationRoundState_RoundFactsHash{RoundFactsHash: bytes32(0x83)},
	}
	require.NoError(t, f.keeper.WriteVerificationRound(f.ctx, types.NewVerifyRoundKey(f.taskKey, types.VerifyRoundV1), closed))
	require.NoError(t, f.keeper.TaskRoundSummary.Set(f.ctx, f.taskKey, types.TaskRoundSummaryState{
		TaskId: f.taskID, MaxClosedRound: types.VerifyRoundV1, Round1FactsHashOrZero32: closed.GetRoundFactsHash(),
		XChallengeOpenHeight:  &types.TaskRoundSummaryState_ChallengeOpenHeight{ChallengeOpenHeight: 10},
		XChallengeCloseHeight: &types.TaskRoundSummaryState_ChallengeCloseHeight{ChallengeCloseHeight: 100},
	}))
	require.NoError(t, f.keeper.TaskBudget.Set(f.ctx, f.taskKey, types.TaskBudgetState{
		TaskId: f.taskID, PriceBid: shared.NewAmount(1_000_000), VerifyRatioBpsSnapshot: 3000,
		BudgetStatus: types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED,
	}))
	require.NoError(t, f.keeper.WriteInferReceipt(f.ctx, f.taskKey, types.InferReceiptState{
		TaskId: f.taskID, WinnerWorker: worker.String(), InferReceiptHash: bytes32(0x84), OutputHash: bytes32(0x85),
	}))
	bank := dispatchTestBank{store: f.keeper.storeService}
	f.keeper.bankKeeper = bank
	f.server = msgServer{k: f.keeper}
	require.NoError(t, bank.setBalance(f.ctx, worker, 1_000_000))
	_, err = f.server.OpenChallengeRound(f.ctx, &types.MsgOpenChallengeRound{
		TaskId: f.taskID, OpenerAddress: worker.String(), MaxTotalDebit: shared.NewAmount(1_000_000),
	})
	require.NoError(t, err)
	f.openWindow(t, types.ChallengeVerifyRoundV1)
	return f
}

// handraises builds one hand-raise per window member for verifyRound, in
// ascending slot order, scoped to the task's accepted receipt.
func (f *legacyHandraiseFixture) handraises(t *testing.T, verifyRound uint32) []types.VerifierHandraiseV1 {
	t.Helper()
	receipt, err := f.keeper.ReadInferReceipt(f.ctx, f.taskKey)
	require.NoError(t, err)
	core := f.core(t)
	members := append([]types.VerifierCandidateWindowMemberState(nil), f.members...)
	sort.Slice(members, func(i, j int) bool { return members[i].Slot < members[j].Slot })
	out := make([]types.VerifierHandraiseV1, 0, len(members))
	for _, member := range members {
		out = append(out, types.VerifierHandraiseV1{
			SchemaVersion: types.VerifierHandraiseSchemaVersionV1, ChainId: sdkChainID(f.ctx), TaskId: f.taskID,
			VerifyRound: verifyRound, InferReceiptHash: receipt.InferReceiptHash, OutputHash: receipt.OutputHash,
			ModelId: core.ModelId, ProfileVersion: core.ProfileVersion,
			Member: types.CandidateMemberRefV1{
				CandidatePoolSnapshotId: f.window.CandidatePoolSnapshotId, Slot: member.Slot,
				SlotVersion: member.SlotVersion, OperatorAddress: member.OperatorAddress,
			},
			Duty: shared.Duty_DUTY_VERIFIER, ServiceAuthorizationNonce: f.hub.nonce,
			ExpiryHeight: f.window.HandraiseCloseHeight, ServiceSignature: make([]byte, 64),
		})
	}
	return out
}

// submit sends the hand-raises of every window member for verifyRound through
// the real handler, relayed by the first selected Builder at the Builder
// proposal close height. It returns the response, the error and the events
// the call emitted.
func (f *legacyHandraiseFixture) submit(t *testing.T, verifyRound uint32) (*types.MsgSubmitVerifierHandraisesResponse, []abci.Event, error) {
	t.Helper()
	ctx := f.at(f.window.BuilderProposalCloseHeight)
	response, err := f.server.SubmitVerifierHandraises(sdk.WrapSDKContext(ctx), &types.MsgSubmitVerifierHandraises{
		TaskId: f.taskID, Handraises: f.handraises(t, verifyRound), SubmitterAddress: f.builders[0],
	})
	return response, ctx.EventManager().ABCIEvents(), err
}

// storeRows returns every key/value pair of the task module's KV store, in
// iteration order.
func (f *legacyHandraiseFixture) storeRows(t *testing.T) [][2][]byte {
	t.Helper()
	iter, err := f.keeper.storeService.OpenKVStore(f.ctx).Iterator(nil, nil)
	require.NoError(t, err)
	defer iter.Close()
	var rows [][2][]byte
	for ; iter.Valid(); iter.Next() {
		rows = append(rows, [2][]byte{append([]byte(nil), iter.Key()...), append([]byte(nil), iter.Value()...)})
	}
	return rows
}

// legacyStateDigest is the sha256 of every store row followed by every event.
// Each byte string is prefixed with its length and each list with its item
// count, so no two different states share an encoding.
func legacyStateDigest(rows [][2][]byte, events []abci.Event) string {
	hasher := sha256.New()
	writeCount := func(n int) {
		var prefix [8]byte
		binary.BigEndian.PutUint64(prefix[:], uint64(n))
		hasher.Write(prefix[:])
	}
	writeBytes := func(value []byte) {
		writeCount(len(value))
		hasher.Write(value)
	}
	writeCount(len(rows))
	for _, row := range rows {
		writeBytes(row[0])
		writeBytes(row[1])
	}
	writeCount(len(events))
	for _, event := range events {
		writeBytes([]byte(event.Type))
		writeCount(len(event.Attributes))
		for _, attribute := range event.Attributes {
			writeBytes([]byte(attribute.Key))
			writeBytes([]byte(attribute.Value))
		}
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// requireAcceptedProposal submits the hand-raises of every member of the
// fixture's READY window and checks everything the accepted proposal leaves
// behind: the response, the rows, the Hub duties, the event and an exact
// replay. wantDataReady says whether the relaying Builder is recorded as a
// data-ready attester and wantActor is the operator the event names. It
// returns the digest of the store and the events right after the call.
func (f *legacyHandraiseFixture) requireAcceptedProposal(t *testing.T, wantDataReady bool, wantActor string) string {
	t.Helper()
	stage := types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY
	verifyRound := f.window.VerifyRound
	handraises := f.handraises(t, verifyRound)
	require.Len(t, handraises, int(f.window.WindowSize))
	count := uint32(len(handraises))
	before := f.storeRows(t)

	response, events, err := f.submit(t, verifyRound)
	require.NoError(t, err)
	require.Len(t, response.ProposalDigest, types.Hash32Len)
	require.Equal(t, &types.MsgSubmitVerifierHandraisesResponse{
		TaskId: f.taskID, VerifyRound: verifyRound, ProposalDigest: response.ProposalDigest,
		AddedMemberCount: count, UnionCount: count,
	}, response)

	// One candidate fact per accepted slot, carrying the eligibility the Hub
	// reported, and one pending stage duty per accepted operator.
	slots := make([]uint32, 0, len(handraises))
	wantBitmap := make([]byte, 2)
	for _, handraise := range handraises {
		slot := handraise.Member.Slot
		slots = append(slots, slot)
		wantBitmap[slot/8] |= 1 << (slot % 8)
		fact, err := f.keeper.ReadTaskCandidateFact(f.ctx, types.NewTaskCandidateFactKey(f.taskKey, stage, slot))
		require.NoError(t, err)
		digest, err := types.VerifierHandraiseSigningDigest(handraise)
		require.NoError(t, err)
		require.Equal(t, handraise.Member.OperatorAddress, fact.OperatorAddress)
		require.Equal(t, shared.Duty_DUTY_VERIFIER, fact.Duty)
		require.Equal(t, digest[:], fact.HandraiseSigningDigest)
		require.Equal(t, shared.NewAmount(1_000_000), fact.ActiveBondSnapshot)
		require.Equal(t, shared.NewAmount(900_000), fact.AvailableBondSnapshot)
		require.Equal(t, shared.NewAmount(30_000), fact.RequiredTaskLiabilitySnapshot)
		require.EqualValues(t, 5, fact.BondVersionSnapshot)
		require.EqualValues(t, 3, fact.CapabilityVersionSnapshot)
		require.EqualValues(t, 4, fact.SupportVersionSnapshot)
		require.Equal(t, 1, f.duties[handraise.Member.OperatorAddress])
	}
	require.Len(t, f.duties, len(handraises))

	union, err := f.keeper.TaskStageHandraiseUnion.Get(f.ctx, types.NewTaskStageKey(f.taskKey, stage))
	require.NoError(t, err)
	require.Equal(t, types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN, union.Status)
	require.Equal(t, count, union.UnionCount)
	require.Equal(t, uint32(1), union.AcceptedProposalCount)
	require.Equal(t, f.window.HandraiseCloseHeight, union.WindowCloseHeight)
	wantAttesters := []byte{0}
	if wantDataReady {
		wantAttesters[0] = 1
	}
	require.Equal(t, wantAttesters, union.DataReadyAttestingBuilderBitmap)

	segment, err := f.keeper.TaskStageHandraiseUnionSegment.Get(f.ctx, types.NewTaskStageSegmentKey(f.taskKey, stage, 0))
	require.NoError(t, err)
	require.Equal(t, wantBitmap, segment.Bitmap)

	proposal, err := f.keeper.ReadBuilderStageProposal(f.ctx, types.NewBuilderStageProposalKey(f.taskKey, stage, response.ProposalDigest))
	require.NoError(t, err)
	require.Equal(t, f.builders[0], proposal.ProposerOperator)
	require.Equal(t, wantDataReady, proposal.DataReadyAttestation)
	require.Equal(t, f.window.BuilderProposalCloseHeight, proposal.AcceptedHeight)
	require.Equal(t, count, proposal.NewMemberCount)

	// The call added those rows and touched nothing else: every row that was in
	// the store before is still there with the same value, and every new row
	// belongs to one of the four hand-raise collections.
	collections := []struct {
		name   string
		prefix []byte
	}{
		{"fact", types.TaskCandidateFactKey.Bytes()},
		{"union", types.TaskStageHandraiseUnionKey.Bytes()},
		{"segment", types.TaskStageHandraiseUnionSegmentKey.Bytes()},
		{"proposal", types.BuilderStageProposalKey.Bytes()},
	}
	after := f.storeRows(t)
	previous := make(map[string][]byte, len(before))
	for _, row := range before {
		previous[string(row[0])] = row[1]
	}
	added := map[string]int{}
	for _, row := range after {
		if value, existed := previous[string(row[0])]; existed {
			require.Equal(t, value, row[1], "existing row %x must keep its value", row[0])
			delete(previous, string(row[0]))
			continue
		}
		matches := 0
		for _, collection := range collections {
			if bytes.HasPrefix(row[0], collection.prefix) {
				added[collection.name]++
				matches++
			}
		}
		require.Equal(t, 1, matches, "new row %x must belong to exactly one hand-raise collection", row[0])
	}
	require.Empty(t, previous, "the call must not delete a row")
	require.Equal(t, map[string]int{"fact": len(handraises), "union": 1, "segment": 1, "proposal": 1}, added)

	// Exactly one event, with the fields of the accepted proposal.
	require.Len(t, events, 1)
	typed, err := sdk.ParseTypedEvent(events[0])
	require.NoError(t, err)
	addedBitmapHash, err := taskStageAddedBitmapHash(sdkChainID(f.ctx), f.taskID, stage, response.ProposalDigest, slots)
	require.NoError(t, err)
	require.Equal(t, &types.EventVerifierHandraisesAccepted{
		TaskId: f.taskID, VerifyRound: verifyRound, ProposalDigest: response.ProposalDigest,
		AddedBitmapHash: addedBitmapHash[:], BuilderOrWorker: wantActor, UnionCount: count,
	}, typed)

	// An exact replay answers with the retained proposal and writes nothing.
	replay, replayEvents, err := f.submit(t, verifyRound)
	require.NoError(t, err)
	require.Equal(t, &types.MsgSubmitVerifierHandraisesResponse{
		TaskId: f.taskID, VerifyRound: verifyRound, ProposalDigest: response.ProposalDigest, UnionCount: count,
	}, replay)
	require.Empty(t, replayEvents)
	require.Equal(t, after, f.storeRows(t))
	require.Len(t, f.duties, len(handraises))
	for operator, acquired := range f.duties {
		require.Equalf(t, 1, acquired, "the replay must not acquire a second duty for %s", operator)
	}
	return legacyStateDigest(after, events)
}

// The two digests below were computed by running these tests on the base
// branch, before any Verifier-panel code existed. Each covers every key/value
// pair of the task module's KV store right after the accepted hand-raise
// proposal, in iteration order, followed by the ABCI events the call emitted
// (type and attributes, in order).
const (
	legacyRoundOneHandraiseDigest       = "26ca0839153f51f4d03800deef07c935e2344d999263755e0ed4b150c1c68b00"
	legacyChallengeRoundHandraiseDigest = "cc0a58e0df0c20ce6a167a464d3b4eae8faf13602c3f4d6fdef58da111a73824"
)

// A round-1 Verifier task takes hand-raises through the real handler and
// leaves exactly the rows and the event it left before Verifier panels
// existed. A Builder that relays a round-1 proposal attests data readiness and
// is the operator the event names.
func TestLegacyRoundOneHandraiseIsUnchanged(t *testing.T) {
	f := newLegacyHandraiseFixture(t)
	require.Equal(t, legacyRoundOneHandraiseDigest, f.requireAcceptedProposal(t, true, f.builders[0]))
}

// A challenge round is the one later round the legacy path serves. Its
// hand-raises are relayed by a Builder too, but carry no data-ready
// attestation, and the event names the winner Worker.
func TestLegacyChallengeRoundHandraiseIsUnchanged(t *testing.T) {
	f := newLegacyChallengeHandraiseFixture(t)
	assignment, err := f.keeper.ReadTaskAssignment(f.ctx, f.taskKey)
	require.NoError(t, err)
	for _, member := range f.members {
		require.NotContains(t, f.operators, member.OperatorAddress, "a round-1 Verifier is not a challenge candidate")
	}
	require.Equal(t, legacyChallengeRoundHandraiseDigest, f.requireAcceptedProposal(t, false, assignment.WinnerWorker))
}

// On a task that has only its round-1 window, a hand-raise for any later round
// is refused with the error it always had, writes nothing and emits nothing:
// round 2 gets as far as the window lookup and finds no challenge window, and
// rounds 3 and 4 are not verification rounds of such a task at all.
func TestLegacyLaterRoundHandraiseTakesTheLegacyPath(t *testing.T) {
	f := newLegacyHandraiseFixture(t)
	before := f.storeRows(t)
	for verifyRound, want := range map[uint32]string{
		types.ChallengeVerifyRoundV1: "verifier proposal window scope is unavailable",
		3:                            "verifier handraise envelope is invalid",
		4:                            "verifier handraise envelope is invalid",
	} {
		response, events, err := f.submit(t, verifyRound)
		require.Nil(t, response)
		require.ErrorIs(t, err, types.ErrInvalidOpenVerify)
		require.Equal(t, want+": "+types.ErrInvalidOpenVerify.Error(), err.Error(), "round %d", verifyRound)
		require.Empty(t, events, "round %d", verifyRound)
		require.Equal(t, before, f.storeRows(t), "round %d", verifyRound)
		require.Empty(t, f.duties, "round %d", verifyRound)
	}
}
