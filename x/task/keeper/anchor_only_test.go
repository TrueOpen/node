package keeper

import (
	"bytes"
	"context"
	"math"
	"testing"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// ---- fixture -----------------------------------------------------------

// anchorOnlyHubStub serves a beacon at every height: the anchor-only round is
// sampled from the selection randomness height's beacon.
type anchorOnlyHubStub struct {
	anchorHubStub
	beacon      []byte
	faults      *[]hubtypes.TaskRoleFaultFact
	credits     *[]hubtypes.TaskEarningsCredit
	completions *[]hubtypes.TaskSupportCompletionFact
}

func (s anchorOnlyHubStub) GetBeaconForDomain(_ sdk.Context, _ string, height int64) (hubtypes.BeaconSnapshot, bool) {
	if len(s.beacon) == 0 {
		return hubtypes.BeaconSnapshot{}, false
	}
	return hubtypes.BeaconSnapshot{Height: height, Randomness: append([]byte(nil), s.beacon...)}, true
}

func (s anchorOnlyHubStub) ApplyTaskRoleFault(_ context.Context, fact hubtypes.TaskRoleFaultFact) (hubtypes.RoleFaultState, error) {
	*s.faults = append(*s.faults, fact)
	return stubAppliedRoleFault(fact), nil
}

func (s anchorOnlyHubStub) CreditTaskSettlementEarnings(_ context.Context, _, _ []byte, credits []hubtypes.TaskEarningsCredit, _ uint64) error {
	*s.credits = append(*s.credits, credits...)
	return nil
}

func (anchorOnlyHubStub) CreditTaskMaintenance(context.Context, []byte, shared.Amount, uint64) error {
	return nil
}

func (anchorOnlyHubStub) RecordProfilePriceSample(context.Context, shared.ProfilePriceSampleV1) error {
	return nil
}

func (s anchorOnlyHubStub) RecordTaskSupportCompletion(_ context.Context, fact hubtypes.TaskSupportCompletionFact) error {
	*s.completions = append(*s.completions, fact)
	return nil
}

func (anchorOnlyHubStub) ReleaseTaskLiabilities(context.Context, string, string, uint64) error {
	return nil
}

type anchorOnlyFixture struct {
	credits     *[]hubtypes.TaskEarningsCredit
	completions *[]hubtypes.TaskSupportCompletionFact

	*anchorFixture
	faults       *[]hubtypes.TaskRoleFaultFact
	receiptAt    uint64
	window       types.VerifierCandidateWindowState
	builders     []string
	priceTokens  uint64
	verifyRatio  uint32
	maintenance  uint32
	reserved     uint64
	anchorSchema bool
}

const (
	anchorOnlyGenerated = uint64(10)
	anchorOnlyPrompt    = uint64(5)
)

// newAnchorOnlyFixture stages a task at INFER_PENDING on a Profile version that
// requires the anchor commitment (or, with anchorSchema false, on a plain
// two-commitment Profile) and has it ready for a receipt and for settlement.
func newAnchorOnlyFixture(t *testing.T, bond uint64, anchorSchema bool) *anchorOnlyFixture {
	t.Helper()
	af := newAnchorFixture(t, bond, anchorGoodTokenSize)
	faults := &[]hubtypes.TaskRoleFaultFact{}
	credits := &[]hubtypes.TaskEarningsCredit{}
	completions := &[]hubtypes.TaskSupportCompletionFact{}
	af.keeper.hubKeeper = anchorOnlyHubStub{
		anchorHubStub: anchorHubStub{verificationHubStub: af.hub, bond: bond}, beacon: af.beacon,
		faults: faults, credits: credits, completions: completions,
	}
	af.server = msgServer{k: af.keeper}
	f := &anchorOnlyFixture{anchorFixture: af, faults: faults, credits: credits, completions: completions, receiptAt: 10, anchorSchema: anchorSchema,
		priceTokens: 1_000_000, verifyRatio: 2500, maintenance: 500, reserved: 1_000}

	// The locked Profile and the assignment that snapshots it.
	profile := af.hub.profile
	schema := shared.NewPhase0WorkerEvidenceSchemaV1(1<<30, 1<<26)
	if anchorSchema {
		schema.RequiredInferEvidence = append(schema.RequiredInferEvidence, shared.InferEvidenceRequirementV1{
			EvidenceKind:            shared.EvidenceKind_EVIDENCE_KIND_WORKER_ANCHOR_OPENING,
			CommitmentSchemaVersion: shared.WorkerAnchorCommitmentSchemaVersionV1, MaxEncodedSizeBytes: 16 << 20,
		})
	}
	verification := profile.ExecutionSnapshot.VerificationProfile
	verification.EvidenceSchema = schema
	verification.EvidenceSchemaHash = nil
	schemaHash, err := hubtypes.EvidenceSchemaHash(shared.ModelProfileProjection{
		ModelId: profile.ModelID, ProfileVersion: 1, TokenizerHash: profile.ExecutionSnapshot.TokenizerHash,
		RequiredTopK: profile.ExecutionSnapshot.RequiredTopK, GenerationType: profile.ExecutionSnapshot.GenerationType,
		VerificationProfile: verification, SchemaHash: profile.ExecutionSnapshot.SchemaHash,
	})
	require.NoError(t, err)
	verification.EvidenceSchemaHash = schemaHash
	profile.ExecutionSnapshot.VerificationProfile = verification
	profileHash, err := hubtypes.ProfileExecutionSnapshotHash(profile.ExecutionSnapshot)
	require.NoError(t, err)
	profile.ExecutionSnapshotHash = profileHash
	profile.ResourceTier = 1
	af.hub.profile = profile

	user := sdk.AccAddress(bytes.Repeat([]byte{0xc1}, 20)).String()
	core := types.TaskCoreState{
		TaskId: af.taskID, SessionId: bytes.Repeat([]byte{0x61}, types.Hash32Len), ModelId: profile.ModelID, ProfileVersion: 1,
		UserAddress: user, OrderSequence: 1, AcceptedTaskHash: bytes32(0x71), OrderValue: shared.NewAmount(800),
		EvidenceRetentionBlocksSnapshot: types.DefaultMaxEvidenceRetentionBlocks,
		TaskPhase:                       types.TaskPhase_TASK_PHASE_INFER_PENDING, ReceiptStatus: types.ReceiptStatus_RECEIPT_STATUS_NONE,
		FinalityStatus:   shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_PENDING,
		SettlementStatus: types.SettlementStatus_SETTLEMENT_STATUS_NONE,
	}
	require.NoError(t, af.keeper.TaskCore.Set(af.ctx, af.taskKey, core))
	require.NoError(t, af.keeper.InferReceipt.Remove(af.ctx, af.taskKey))
	require.NoError(t, af.keeper.VerifierAssignment.Remove(af.ctx, types.NewVerifyRoundKey(af.taskKey, types.VerifyRoundV1)))
	assignment, err := af.keeper.ReadTaskAssignment(af.ctx, af.taskKey)
	require.NoError(t, err)
	assignment.WinnerWorker = af.worker
	assignment.DispatchTicketHash = bytes32(0x72)
	assignment.DispatchRecordHash = bytes32(0x77)
	assignment.InferDeadlineHeight = 100
	assignment.EvidenceSchemaHash = schemaHash
	assignment.ProfileExecutionSnapshotHash = profileHash
	assignment.ChallengeOpenWindowBlocksSnapshot = 20
	require.NoError(t, af.keeper.WriteTaskAssignment(af.ctx, af.taskKey, assignment))
	require.NoError(t, af.keeper.TaskBudget.Set(af.ctx, af.taskKey, types.TaskBudgetState{
		TaskId: af.taskID, FeeRuleVersion: 1,
		OriginalReservedAmount: shared.NewAmount(f.reserved), ReservedAmount: shared.NewAmount(f.reserved),
		TxFeeReserveRemaining: shared.NewAmount(100), GasReimbursedTotal: shared.NewAmount(0),
		BudgetStatus: types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED,
		PriceBid:     shared.NewAmount(f.priceTokens),
		WorkerMax:    shared.NewAmount(640), VerifyMax: shared.NewAmount(160),
		VerifyRatioBpsSnapshot: f.verifyRatio, MaxOutputTokens: 640,
		MaintenanceRateBpsSnapshot: f.maintenance, FeePolicyVersionSnapshot: 1,
	}))
	require.NoError(t, af.keeper.TaskBucketRef.Set(af.ctx,
		types.NewTaskBucketRefKey(af.taskKey, int32(shared.BucketKind_BUCKET_KIND_TIMEOUT), hubtypes.DefaultParameterBucketKey),
		types.TaskBucketRefState{
			TaskId: af.taskID, BucketKind: shared.BucketKind_BUCKET_KIND_TIMEOUT,
			BucketKey: hubtypes.DefaultParameterBucketKey, Version: 1, AcquiredHeight: 10,
		}))
	sessionKey, err := sessionStoreKey(core.SessionId)
	require.NoError(t, err)
	f.builders = []string{af.operators[1], af.operators[2]}
	require.NoError(t, af.keeper.WriteStream(af.ctx, sessionKey, types.StreamState{
		SessionId: core.SessionId, OwnerUserAddress: user, BoundBuilder: f.builders[0], BoundChangeHeight: 1,
		Balance: shared.NewAmount(f.reserved), FrozenTotal: shared.NewAmount(f.reserved),
		PendingWithdrawAmount: shared.NewAmount(0), PendingWithdrawKind: types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_NONE,
		NextExpectedSequence: 2, LastActiveHeight: 10, OpenPendingCount: 1,
		Status: types.SessionStatus_SESSION_STATUS_ACTIVE,
	}))
	require.NoError(t, af.keeper.OrderSequence.Set(af.ctx,
		types.NewOrderSequenceStateKey(sessionKey, core.OrderSequence),
		types.OrderSequenceState{
			SessionId: core.SessionId, OrderSequence: core.OrderSequence,
			Status: types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CONSUMED,
			TaskId: core.TaskId, ConsumedHeight: 10,
		}))
	af.setTaskBuilders(t, f.builders...)
	return f
}

func (f *anchorOnlyFixture) receiptMsg(t *testing.T) *types.MsgSubmitInferReceipt {
	t.Helper()
	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	assignment, err := f.keeper.ReadTaskAssignment(f.ctx, f.taskKey)
	require.NoError(t, err)
	commitments := []types.EvidenceCommitmentV1{
		{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING, EvidenceHashOrRoot: bytes32(0x74), EncodedSizeBytes: 100},
		{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_TOKEN_OPENING, EvidenceHashOrRoot: bytes32(0x75),
			EncodedSizeBytes: 8 + 4*(anchorOnlyPrompt+anchorOnlyGenerated)},
	}
	if f.anchorSchema {
		commitments = append(commitments, types.EvidenceCommitmentV1{
			EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_ANCHOR_OPENING, EvidenceHashOrRoot: bytes32(0x76), EncodedSizeBytes: 64,
		})
	}
	return &types.MsgSubmitInferReceipt{SubmitterAddress: f.worker, Receipt: types.InferReceiptV3{
		SchemaVersion: 3, ChainId: sdkChainID(f.ctx), TaskId: f.taskID, TaskHash: core.AcceptedTaskHash,
		WorkerOperatorAddress: f.worker, ServiceAuthorizationNonce: 7, GenerationParamsDigest: f.genDigest,
		OutputHash: bytes32(0x73), OutputSizeBytes: 4, RequiredEvidenceCommitments: commitments,
		ExpiryHeight: 100, GeneratedTokenCount: anchorOnlyGenerated, OutputLeafCount: 2,
		OutputKeyCommitment: make([]byte, 32), WorkerTokenKeyCommitment: make([]byte, 32),
		WorkerValueKeyCommitment: make([]byte, 32), CiphertextOutputRoot: make([]byte, 32),
		DispatchTicketHash: assignment.DispatchTicketHash, ServiceSignature: make([]byte, 64),
	}}
}

// submitReceipt accepts the Worker's receipt at the fixture height and returns
// the window it froze.
func (f *anchorOnlyFixture) submitReceipt(t *testing.T) types.VerifierCandidateWindowState {
	t.Helper()
	response, err := f.server.SubmitInferReceipt(f.at(int64(f.receiptAt)), f.receiptMsg(t))
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Status)
	window, err := f.keeper.VerifierCandidateWindow.Get(f.ctx, types.NewVerifyRoundKey(f.taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	f.window = window
	return window
}

// sampleRound runs the EndBlock selection sweep at the sampling height.
func (f *anchorOnlyFixture) sampleRound(t *testing.T) types.AnchorAuditState {
	t.Helper()
	h := f.window.SelectionRandomnessHeight
	usage, err := f.keeper.processVerifierSelectionRandomnessIndexWithBudget(f.at(int64(h)), h, 10, 1<<30)
	require.NoError(t, err)
	require.Equal(t, uint64(1), usage.visited)
	state, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	return state
}

func (f *anchorOnlyFixture) settlementDeadline(t *testing.T) uint64 {
	t.Helper()
	iter, err := f.keeper.SettlementDeadlineIndex.Iterate(f.ctx, nil)
	require.NoError(t, err)
	defer iter.Close()
	require.True(t, iter.Valid(), "the task must have a settlement deadline")
	key, err := iter.Key()
	require.NoError(t, err)
	require.True(t, bytes.Equal(key.K2(), f.taskKey))
	return key.K1()
}

// workerGrossWithVerifyShare is what an anchor-only PASS pays the Worker before
// maintenance: its own gross plus the share a Verifier round would have taken.
func (f *anchorOnlyFixture) workerGrossWithVerifyShare() uint64 {
	gross := anchorOnlyGenerated * f.priceTokens / 1_000_000
	return gross + gross*uint64(f.verifyRatio)/10_000
}

func (f *anchorOnlyFixture) requireNoVerifierState(t *testing.T) {
	t.Helper()
	for name, has := range map[string]func() (bool, error){
		"window build index": func() (bool, error) {
			return f.keeper.VerifierWindowBuildIndex.Has(f.ctx, types.NewVerifyRoundIndexKey(f.window.WindowRandomnessHeight, f.taskKey, types.VerifyRoundV1))
		},
		"handraise close index": func() (bool, error) {
			return f.keeper.VerifierHandraiseCloseIndex.Has(f.ctx, types.NewVerifyRoundIndexKey(f.window.HandraiseCloseHeight, f.taskKey, types.VerifyRoundV1))
		},
		"commit deadline": func() (bool, error) { return anyDeadlineRow(f, f.keeper.CommitDeadlineIndex) },
		"reveal deadline": func() (bool, error) { return anyDeadlineRow(f, f.keeper.RevealDeadlineIndex) },
		"verify deadline": func() (bool, error) { return anyDeadlineRow(f, f.keeper.VerifyDeadlineIndex) },
		"challenge close": func() (bool, error) { return anyDeadlineRow(f, f.keeper.ChallengeWindowCloseIndex) },
	} {
		found, err := has()
		require.NoError(t, err)
		require.False(t, found, "an anchor-only task must leave no %s", name)
	}
}

func anyDeadlineRow(f *anchorOnlyFixture, set collections.KeySet[types.DeadlineIndexKey]) (bool, error) {
	iter, err := set.Iterate(f.ctx, nil)
	if err != nil {
		return false, err
	}
	defer iter.Close()
	return iter.Valid(), nil
}

// ---- receipt -------------------------------------------------------------

func TestAnchorOnlyReceiptFreezesTheClockWithoutAVerifierWindow(t *testing.T) {
	f := newAnchorOnlyFixture(t, 1_000_000, true)
	window := f.submitReceipt(t)
	require.True(t, isAnchorOnlyWindow(window))
	require.Zero(t, window.WindowSize)
	require.Greater(t, window.SelectionRandomnessHeight, f.receiptAt, "the sampling beacon must lie after the receipt")
	require.Greater(t, window.AssignmentDeadlineHeight, window.SelectionRandomnessHeight)

	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, types.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED, core.TaskPhase)
	require.Equal(t, types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING, core.VerificationStatus)

	scheduled, err := f.keeper.VerifierSelectionRandomnessIndex.Has(f.ctx,
		types.NewVerifyRoundIndexKey(window.SelectionRandomnessHeight, f.taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.True(t, scheduled, "sampling must be scheduled at the selection randomness height")
	fallback, err := f.keeper.VerifyOpenDeadlineIndex.Has(f.ctx, types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, f.taskKey))
	require.NoError(t, err)
	require.True(t, fallback)
	f.requireNoVerifierState(t)
	_, err = f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.Error(t, err, "nothing is sampled at the receipt: the beacon does not exist yet")

	replay, err := f.server.SubmitInferReceipt(f.at(int64(f.receiptAt)), f.receiptMsg(t))
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replay.Status)

	// Before the sampling height the sweep has nothing to do.
	usage, err := f.keeper.processVerifierSelectionRandomnessIndexWithBudget(
		f.at(int64(window.SelectionRandomnessHeight-1)), window.SelectionRandomnessHeight-1, 10, 1<<30)
	require.NoError(t, err)
	require.Zero(t, usage.visited)
}

// A Profile version without the anchor commitment keeps the Verifier path,
// row for row.
func TestProfileWithoutAnchorCommitmentKeepsTheVerifierPath(t *testing.T) {
	f := newAnchorOnlyFixture(t, 1_000_000, false)
	window := f.submitReceipt(t)
	require.False(t, isAnchorOnlyWindow(window))
	require.Equal(t, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN, window.Status)
	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING, core.VerificationStatus)
	build, err := f.keeper.VerifierWindowBuildIndex.Has(f.ctx, types.NewVerifyRoundIndexKey(window.WindowRandomnessHeight, f.taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.True(t, build)
	closeIndexed, err := f.keeper.VerifierHandraiseCloseIndex.Has(f.ctx, types.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, f.taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.True(t, closeIndexed)
	selection, err := f.keeper.VerifierSelectionRandomnessIndex.Has(f.ctx, types.NewVerifyRoundIndexKey(window.SelectionRandomnessHeight, f.taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.False(t, selection, "a Verifier window reaches selection only through the handraise close")
	openVerify := 0
	for _, responsibility := range f.hub.responsibilities {
		if responsibility.ResponsibilityKind == serviceKeyResponsibilityOpenVerifyBuilder {
			openVerify++
		}
	}
	require.Equal(t, len(f.builders), openVerify)
}

// ---- sampling and settlement ---------------------------------------------

// settleNow settles the way MsgSettleTask does at height: load, authorize the
// duty Builder, apply.
func (f *anchorOnlyFixture) settleNow(t *testing.T, height uint64) (types.TaskSettlementState, error) {
	t.Helper()
	ctx := f.at(int64(height))
	inputs, err := f.keeper.loadSettlementInputs(ctx, f.taskKey, height)
	if err != nil {
		return types.TaskSettlementState{}, err
	}
	if _, err := f.keeper.authorizeSettlementSubmitter(ctx, inputs, inputs.DutyBuilder, height); err != nil {
		return types.TaskSettlementState{}, err
	}
	if _, err := f.keeper.executeLoadedTaskSettlement(ctx, inputs, height); err != nil {
		return types.TaskSettlementState{}, err
	}
	return f.keeper.ReadTaskSettlement(f.ctx, f.taskKey)
}

func (f *anchorOnlyFixture) requireRoundOneClosedWithoutVerifiers(t *testing.T) {
	t.Helper()
	roundKey := types.NewVerifyRoundKey(f.taskKey, types.VerifyRoundV1)
	assignment, err := f.keeper.ReadVerifierAssignment(f.ctx, roundKey)
	require.NoError(t, err)
	require.True(t, isAnchorOnlyAssignment(assignment))
	require.Empty(t, assignment.SelectedVerifiers)
	require.Equal(t, f.beacon, assignment.SelectionRandomnessBeacon)
	require.Equal(t, f.window.SelectionRandomnessHeight, assignment.SelectionRandomnessHeight)
	round, err := f.keeper.ReadVerificationRound(f.ctx, roundKey)
	require.NoError(t, err)
	require.Equal(t, types.TaskVerdict_TASK_VERDICT_PASS, round.GetVerdict())
	require.Equal(t, f.window.SelectionRandomnessHeight, round.GetClosedHeight())
	require.Equal(t, anchorOnlyGenerated, round.GeneratedTokenCount)
	summary, err := f.keeper.TaskRoundSummary.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, uint32(types.VerifyRoundV1), summary.MaxClosedRound)
	require.Equal(t, uint32(types.VerifyRoundV1), summary.EffectiveVerifyRound)
	require.Nil(t, summary.XChallengeCloseHeight, "an anchor-only round opens no challenge window")
	require.Equal(t, f.window.SelectionRandomnessHeight, summary.GetRoundsClosedHeight())
	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, types.TaskPhase_TASK_PHASE_SETTLING, core.TaskPhase)
	require.Equal(t, types.VerificationStatus_VERIFICATION_STATUS_VERIFICATION_PASSED, core.VerificationStatus)
	f.requireNoVerifierState(t)
	open, err := f.keeper.VerifyOpenDeadlineIndex.Has(f.ctx, types.NewDeadlineIndexKey(f.window.AssignmentDeadlineHeight, f.taskKey))
	require.NoError(t, err)
	require.False(t, open, "the verify-open fallback must be retired once round 1 is closed")
	selection, err := f.keeper.VerifierSelectionRandomnessIndex.Has(f.ctx,
		types.NewVerifyRoundIndexKey(f.window.SelectionRandomnessHeight, f.taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.False(t, selection)
}

func (f *anchorOnlyFixture) requirePaidInFull(t *testing.T, settlement types.TaskSettlementState) {
	t.Helper()
	require.Equal(t, types.TaskVerdict_TASK_VERDICT_PASS, settlement.Verdict)
	require.Equal(t, anchorOnlyGenerated, settlement.GeneratedTokenCount)
	gross := f.workerGrossWithVerifyShare()
	require.Equal(t, shared.NewAmount(gross), settlement.WorkerGross, "the Worker is paid its gross plus the verify share")
	maintenance := gross * uint64(f.maintenance) / 10_000
	require.Equal(t, shared.NewAmount(gross-maintenance), settlement.WorkerNet)
	require.Zero(t, settlement.VerifierPayoutCount)
	require.Equal(t, shared.NewAmount(0), settlement.VerifierSlotGross)
	require.Equal(t, shared.NewAmount(f.reserved-gross), settlement.RefundAmount, "the escrow is conserved")
	require.Empty(t, *f.faults, "an honest Worker is never faulted")
	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL, core.FinalityStatus)
	require.Equal(t, types.SettlementStatus_SETTLEMENT_STATUS_FINALIZED, core.SettlementStatus)
}

func (f *anchorOnlyFixture) zeroAuditRate(t *testing.T) {
	t.Helper()
	f.params.Verification.AnchorAuditMinRateBps = 0
	f.params.Verification.AnchorAuditRiskMarginBps = 0
	require.NoError(t, f.keeper.Params.Set(f.ctx, f.params))
}

// The coin skips the task: round 1 closes at the sampling height and the task
// settles on the round deadline, with no Verifier anywhere in the pool.
func TestAnchorOnlyUnsampledTaskSettlesWithoutVerifiers(t *testing.T) {
	f := newAnchorOnlyFixture(t, 1_000_000_000, true)
	f.zeroAuditRate(t)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	require.True(t, state.Sampled)
	require.False(t, state.Selected)
	require.Empty(t, state.Positions)
	f.requireRoundOneClosedWithoutVerifiers(t)

	closed := f.window.SelectionRandomnessHeight
	roundDeadline, err := settlementDeadlineHeight(closed, f.params)
	require.NoError(t, err)
	require.Equal(t, roundDeadline, f.settlementDeadline(t), "an unsampled task is not held")

	// EndBlock settles it at the deadline with nobody's help.
	visited, err := f.keeper.processExpiredSettlementDeadlines(f.at(int64(roundDeadline)), roundDeadline, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	settlement, err := f.keeper.ReadTaskSettlement(f.ctx, f.taskKey)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
}

// A duty Builder may settle an unsampled task as soon as round 1 is closed.
func TestAnchorOnlyUnsampledTaskSettlesByMsgRightAfterSampling(t *testing.T) {
	f := newAnchorOnlyFixture(t, 1_000_000_000, true)
	f.zeroAuditRate(t)
	f.submitReceipt(t)
	f.sampleRound(t)
	settlement, err := f.settleNow(t, f.window.SelectionRandomnessHeight+2)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
}

func (f *anchorOnlyFixture) openAndProveSampled(t *testing.T, state types.AnchorAuditState) {
	t.Helper()
	h := int64(state.SampleHeight)
	_, err := f.server.SubmitAnchorOpening(f.at(h+1), f.openingMsg(state, f.worker))
	require.NoError(t, err)
	_, err = f.server.SubmitAnchorProof(f.at(h+2), &types.MsgSubmitAnchorProof{TaskId: f.taskID, OpeningHash: bytes.Repeat([]byte{0x0c}, 32),
		ProofHash: bytes.Repeat([]byte{0x0d}, 32), ProofSizeBytes: 5, ProofUri: "https://da.example/p",
		EpochManifestHash: bytes.Repeat([]byte{0x0e}, 32), EpochManifestUri: "https://da.example/m", SubmitterAddress: f.worker})
	require.NoError(t, err)
}

// The coin selects the task: settlement waits for the validators and pays the
// Worker one block after their PASS.
func TestAnchorOnlySampledPassSettlesOnTheVerdict(t *testing.T) {
	f := newAnchorOnlyFixture(t, 0, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	require.True(t, state.Selected)
	require.Len(t, state.Positions, anchorSamplePositionsV0)
	require.Empty(t, state.SelectedVerifiers)
	require.Equal(t, f.worker, state.WinnerWorker)
	require.Equal(t, f.window.SelectionRandomnessHeight, state.SampleHeight)
	require.Equal(t, state.SampleHeight+f.params.Verification.AnchorOpenWindowBlocks, state.OpeningDeadlineHeight)
	f.requireRoundOneClosedWithoutVerifiers(t)
	require.Equal(t, state.VerdictDeadlineHeight+1, f.settlementDeadline(t), "a sampled PASS round is held for the verdict")

	_, err := f.settleNow(t, state.SampleHeight+2)
	require.ErrorContains(t, err, "pending until", "no settlement before the verdict")
	kind, deadline, found, err := (&queryServer{k: f.keeper}).nextSettlementDeadline(f.at(int64(state.SampleHeight+2)), f.taskKey, mustCore(t, f))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, types.DeadlineKindV1_DEADLINE_KIND_V1_ANCHOR_VERDICT, kind)
	require.Equal(t, state.VerdictDeadlineHeight, deadline)

	f.openAndProveSampled(t, state)
	voteAt := state.SampleHeight + 3
	_, err = f.vote(0, f.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, voteAt)
	require.NoError(t, err)
	_, err = f.settleNow(t, voteAt)
	require.ErrorContains(t, err, "pending until", "one third of the power is not a verdict")
	resp, err := f.vote(1, f.valAcct[1], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, voteAt)
	require.NoError(t, err)
	require.True(t, resp.Finalized)

	settlement, err := f.settleNow(t, voteAt+1)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
}

func mustCore(t *testing.T, f *anchorOnlyFixture) types.TaskCoreState {
	t.Helper()
	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	return core
}

func (f *anchorOnlyFixture) requireWorkerFaulted(t *testing.T, settlement types.TaskSettlementState, faultType string, bps uint32, bond uint64) {
	t.Helper()
	require.Equal(t, types.TaskVerdict_TASK_VERDICT_FAIL, settlement.Verdict)
	require.Equal(t, types.TaskFailureClass_TASK_FAILURE_CLASS_WORKER_EVIDENCE_FAULT, settlement.FailureClass)
	require.Equal(t, shared.NewAmount(0), settlement.WorkerGross, "a rejected Worker is not paid, verify share included")
	require.Equal(t, shared.NewAmount(0), settlement.WorkerNet)
	require.Equal(t, shared.NewAmount(f.reserved), settlement.RefundAmount)
	require.Len(t, *f.faults, 1)
	fault := (*f.faults)[0]
	require.Equal(t, f.worker, fault.OperatorAddress)
	require.Equal(t, shared.DutyWorker, fault.Duty)
	require.Equal(t, faultType, fault.FaultType)
	require.Equal(t, anchorSlashAmount(bond, bps), fault.RequestedAmount)
}

// The validators reject the proof: the task fails, the Worker is slashed and
// jailed exactly as on the Verifier path.
func TestAnchorOnlyRejectFailsTheTaskAndSlashesTheWorker(t *testing.T) {
	const bond = uint64(4_000)
	f := newAnchorOnlyFixture(t, bond, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	require.True(t, state.Selected, "order 800 against bond 4000 must be audited")
	f.openAndProveSampled(t, state)
	voteAt := state.SampleHeight + 3
	_, err := f.vote(0, f.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, voteAt)
	require.NoError(t, err)
	resp, err := f.vote(1, f.valAcct[1], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, voteAt)
	require.NoError(t, err)
	require.True(t, resp.Finalized)
	settlement, err := f.settleNow(t, voteAt+1)
	require.NoError(t, err)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorReject, f.params.Verification.AnchorRejectSlashBps, bond)
}

// The Worker never opens: the verdict deadline imposes the timeout REJECT.
func TestAnchorOnlyOpenTimeoutTakesTheTimeoutSlash(t *testing.T) {
	const bond = uint64(4_000)
	f := newAnchorOnlyFixture(t, bond, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	require.True(t, state.Selected)
	_, err := f.server.SubmitAnchorOpening(f.at(int64(state.OpeningDeadlineHeight+1)), f.openingMsg(state, f.worker))
	require.ErrorContains(t, err, "deadline")
	_, err = f.settleNow(t, state.VerdictDeadlineHeight-1)
	require.ErrorContains(t, err, "pending until")

	deadline := f.settlementDeadline(t)
	visited, err := f.keeper.processExpiredSettlementDeadlines(f.at(int64(deadline)), deadline, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	settlement, err := f.keeper.ReadTaskSettlement(f.ctx, f.taskKey)
	require.NoError(t, err)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorTimeout, f.params.Verification.AnchorTimeoutSlashBps, bond)
	audit, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT, audit.ResultSource)
}

// The Worker opened and proved but the validators stayed silent: NO_VERDICT at
// the deadline pays the Worker and slashes nobody.
func TestAnchorOnlyNoVerdictPaysTheWorkerAndSlashesNobody(t *testing.T) {
	f := newAnchorOnlyFixture(t, 0, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	f.openAndProveSampled(t, state)
	deadline := f.settlementDeadline(t)
	visited, err := f.keeper.processExpiredSettlementDeadlines(f.at(int64(deadline)), deadline, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	settlement, err := f.keeper.ReadTaskSettlement(f.ctx, f.taskKey)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	audit, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT, audit.TaskVerdict)
}

// ---- the rest of the surface ---------------------------------------------

// Only the Worker is a paid role and only the Worker is credited: no Verifier
// exists for the task to pay, fault or activate.
func TestAnchorOnlySettlementPaysAndActivatesOnlyTheWorker(t *testing.T) {
	f := newAnchorOnlyFixture(t, 1_000_000_000, true)
	f.zeroAuditRate(t)
	f.submitReceipt(t)
	f.sampleRound(t)
	settlement, err := f.settleNow(t, f.window.SelectionRandomnessHeight+2)
	require.NoError(t, err)
	require.Len(t, *f.credits, 1)
	require.Equal(t, f.worker, (*f.credits)[0].Beneficiary)
	require.Equal(t, settlement.WorkerNet, (*f.credits)[0].Amount)
	require.Len(t, *f.completions, 1)
	require.Equal(t, f.worker, (*f.completions)[0].OperatorAddress)
	require.Equal(t, shared.DutyWorker, (*f.completions)[0].Duty)
	facts, err := f.keeper.ReadSettlementFacts(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, uint32(1), facts.PaidRoleCount)
	require.Equal(t, make([]byte, types.Hash32Len), facts.ConsensusClusterHash)
}

// The queries Builders and operators poll keep answering for anchor-only rows.
func TestAnchorOnlyRowsAreQueryable(t *testing.T) {
	f := newAnchorOnlyFixture(t, 0, true)
	window := f.submitReceipt(t)
	q := &queryServer{k: f.keeper}

	kind, deadline, found, err := q.nextTaskDeadline(f.at(int64(f.receiptAt+1)), f.taskKey, mustCore(t, f))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN, kind)
	require.Equal(t, window.SelectionRandomnessHeight, deadline, "the next step of an anchor-only task is its sampling height")

	windowResp, err := q.VerifierCandidateWindow(f.ctx, &types.QueryVerifierCandidateWindowRequest{TaskId: f.taskID, VerifyRound: types.VerifyRoundV1})
	require.NoError(t, err)
	require.Empty(t, windowResp.Members)
	require.True(t, isAnchorOnlyWindow(windowResp.Window))

	state := f.sampleRound(t)
	assignmentResp, err := q.VerifierAssignment(f.at(int64(state.SampleHeight)), &types.QueryVerifierAssignmentRequest{TaskId: f.taskID, VerifyRound: types.VerifyRoundV1})
	require.NoError(t, err)
	require.Empty(t, assignmentResp.Assignment.SelectedVerifiers)
	// QueryTask and QueryTaskStage are what a Builder reconciles a task from.
	at := f.at(int64(state.SampleHeight + 1))
	taskResp, err := q.Task(at, &types.QueryTaskRequest{TaskId: f.taskID})
	require.NoError(t, err)
	active := taskResp.Task.GetActive()
	require.NotNil(t, active)
	require.NotNil(t, active.Round1VerifierAssignment, "the zero-Verifier assignment carries the settlement schedule anchor")
	require.Empty(t, active.Round1VerifierAssignment.SelectedVerifiers)
	require.Equal(t, state.SampleHeight+1, active.Round1VerifierAssignment.CommitDeadlineHeight)
	stageResp, err := q.TaskStage(at, &types.QueryTaskStageRequest{TaskId: f.taskID})
	require.NoError(t, err)
	require.Equal(t, types.TaskPhase_TASK_PHASE_SETTLING, stageResp.Stage.TaskPhase)
	require.Equal(t, types.DeadlineKindV1_DEADLINE_KIND_V1_ANCHOR_VERDICT, stageResp.Stage.GetNextDeadlineKind())
	require.Equal(t, state.VerdictDeadlineHeight, stageResp.Stage.GetNextDeadlineHeight())

	auditResp, err := queryServer{k: f.keeper}.AnchorAudit(f.ctx, &types.QueryAnchorAuditRequest{TaskId: f.taskID})
	require.NoError(t, err)
	require.True(t, auditResp.Audit.Selected)
	pending, err := queryServer{k: f.keeper}.AnchorAuditsPending(f.ctx, &types.QueryAnchorAuditsPendingRequest{})
	require.NoError(t, err)
	require.Len(t, pending.Audits, 1, "validators find the sampled task the same way as before")
}

// Nothing a Verifier or a challenger could send applies to an anchor-only task.
func TestAnchorOnlyTaskAcceptsNoVerifierOrChallengeMessage(t *testing.T) {
	f := newAnchorOnlyFixture(t, 0, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	assignment, err := f.keeper.ReadVerifierAssignment(f.ctx, types.NewVerifyRoundKey(f.taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.Error(t, validateVerifierAssignmentScope(assignment, f.taskID, types.VerifyRoundV1),
		"commit and result admission both start from this check")
	for _, operator := range f.operators {
		require.False(t, selectedVerifier(assignment, operator))
	}
	_, err = f.server.OpenChallengeRound(f.at(int64(state.SampleHeight+1)), &types.MsgOpenChallengeRound{
		TaskId: f.taskID, OpenerAddress: mustCore(t, f).UserAddress, MaxTotalDebit: shared.NewAmount(1_000_000),
	})
	require.ErrorIs(t, err, types.ErrInvalidOpenVerify)
	require.ErrorContains(t, err, anchorOnlyNoChallengeReason,
		"the refusal names the mode; it does not depend on the phase the round-1 close leaves the task in")
	// The advisory Verifier result of the first anchor slice has no author here.
	_, err = f.server.SubmitAnchorAuditResult(f.at(int64(state.SampleHeight+1)), &types.MsgSubmitAnchorAuditResult{
		TaskId: f.taskID, OpeningHash: bytes32(0x0c), SubmitterAddress: f.operators[1],
		Verdicts: []types.AnchorPositionVerdictV1{{Position: state.Positions[0], Verdict: types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT}},
	})
	require.Error(t, err)
}

// While the beacon of the sampling height is missing the row stays pending and
// nothing is decided; if it is still missing at the assignment deadline the task
// is failed and refunded like any task that could not be verified, and the
// Worker is not faulted.
func TestAnchorOnlyMissingBeaconNeverFaultsTheWorker(t *testing.T) {
	f := newAnchorOnlyFixture(t, 0, true)
	window := f.submitReceipt(t)
	stub := f.keeper.hubKeeper.(anchorOnlyHubStub)
	stub.beacon = nil
	f.keeper.hubKeeper = stub

	h := window.SelectionRandomnessHeight
	usage, err := f.keeper.processVerifierSelectionRandomnessIndexWithBudget(f.at(int64(h)), h, 10, 1<<30)
	require.NoError(t, err)
	_ = usage
	_, err = f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.Error(t, err, "no beacon, no sample")
	require.Equal(t, types.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED, mustCore(t, f).TaskPhase)
	scheduled, err := f.keeper.VerifierSelectionRandomnessIndex.Has(f.ctx, types.NewVerifyRoundIndexKey(h, f.taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.True(t, scheduled, "the row must be retried on the next block")

	late := window.AssignmentDeadlineHeight + 1
	_, err = f.keeper.processVerifierSelectionRandomnessIndexWithBudget(f.at(int64(late)), late, 10, 1<<30)
	require.NoError(t, err)
	visited, err := f.keeper.processExpiredVerifyOpenDeadlines(f.at(int64(late)), late, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	core := mustCore(t, f)
	require.Equal(t, types.TaskPhase_TASK_PHASE_FAILED, core.TaskPhase)
	require.Equal(t, types.SettlementStatus_SETTLEMENT_STATUS_REFUNDED, core.SettlementStatus)
	require.Empty(t, *f.faults)
}

// The audit is the only verification an anchor-only task has. If the sampling
// step cannot write its row, round 1 is not closed as PASS: nothing of the
// attempt is kept, the task cannot be settled, and the verify-open deadline fails
// and refunds it without faulting the Worker.
func TestAnchorOnlyRoundFailsClosedWithoutAnAuditRow(t *testing.T) {
	f := newAnchorOnlyFixture(t, 0, true)
	window := f.submitReceipt(t)
	// An audit whose deadlines overflow cannot be sized: the sampling step logs
	// it and writes no row.
	f.params.Verification.AnchorVerdictWindowBlocks = math.MaxUint64
	require.NoError(t, f.keeper.Params.Set(f.ctx, f.params))

	h := window.SelectionRandomnessHeight
	usage, err := f.keeper.processVerifierSelectionRandomnessIndexWithBudget(f.at(int64(h)), h, 10, 1<<30)
	require.NoError(t, err, "a task that cannot be audited must not stop the chain")
	require.Equal(t, uint64(1), usage.visited)

	_, err = f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.Error(t, err, "no audit row was written")
	roundKey := types.NewVerifyRoundKey(f.taskKey, types.VerifyRoundV1)
	assigned, err := f.keeper.VerifierAssignment.Has(f.ctx, roundKey)
	require.NoError(t, err)
	require.False(t, assigned, "the zero-Verifier assignment of the failed attempt is not kept")
	closed, err := f.keeper.VerificationRound.Has(f.ctx, roundKey)
	require.NoError(t, err)
	require.False(t, closed, "round 1 is not closed as PASS")
	summarized, err := f.keeper.TaskRoundSummary.Has(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.False(t, summarized)
	core := mustCore(t, f)
	require.Equal(t, types.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED, core.TaskPhase)
	require.Equal(t, types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING, core.VerificationStatus)
	queued, err := f.keeper.VerifierSelectionRandomnessIndex.Has(f.ctx, types.NewVerifyRoundIndexKey(h, f.taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.False(t, queued, "the row is retired, not retried on every block")
	fallback, err := f.keeper.VerifyOpenDeadlineIndex.Has(f.ctx, types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, f.taskKey))
	require.NoError(t, err)
	require.True(t, fallback, "the verify-open deadline still owns the task")
	_, err = f.settleNow(t, h+2)
	require.Error(t, err, "an unaudited anchor-only task has nothing to settle as PASS")

	late := window.AssignmentDeadlineHeight + 1
	visited, err := f.keeper.processExpiredVerifyOpenDeadlines(f.at(int64(late)), late, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	core = mustCore(t, f)
	require.Equal(t, types.TaskPhase_TASK_PHASE_FAILED, core.TaskPhase)
	require.Equal(t, types.SettlementStatus_SETTLEMENT_STATUS_REFUNDED, core.SettlementStatus)
	require.Empty(t, *f.faults, "the Worker is not faulted for an audit the chain could not write")
	require.Empty(t, *f.credits)
}

// The two markers cannot be combined with a task that did not commit the anchor.
func TestZeroVerifierAssignmentWithoutAnchorReceiptIsRefused(t *testing.T) {
	f := newAnchorOnlyFixture(t, 1_000_000_000, true)
	f.zeroAuditRate(t)
	f.submitReceipt(t)
	f.sampleRound(t)
	infer, err := f.keeper.ReadInferReceipt(f.ctx, f.taskKey)
	require.NoError(t, err)
	infer.RequiredEvidenceCommitments = infer.RequiredEvidenceCommitments[:2]
	require.NoError(t, f.keeper.WriteInferReceipt(f.ctx, f.taskKey, infer))
	_, err = f.settleNow(t, f.window.SelectionRandomnessHeight+2)
	require.ErrorIs(t, err, types.ErrInvariantBroken)
}

// The verify share moves to the Worker only on an anchor-only task: the plan of
// a Verifier-path task is the same function of the same inputs as before.
func TestSettlementPlanVerifyShareFollowsTheMode(t *testing.T) {
	f := newAnchorOnlyFixture(t, 1_000_000_000, true)
	f.zeroAuditRate(t)
	f.submitReceipt(t)
	f.sampleRound(t)
	height := f.window.SelectionRandomnessHeight + 2
	inputs, err := f.keeper.loadSettlementInputs(f.at(int64(height)), f.taskKey, height)
	require.NoError(t, err)
	require.True(t, inputs.AnchorOnly)
	inputs.FaultSummaryHash = make([]byte, types.Hash32Len) // framed by prepareSettlementFaults on the real path
	plan := func(in settlementInputs) types.SettlementPlanV1 {
		facts, id, err := f.keeper.BuildSettlementFacts(f.at(int64(height)), in, height)
		require.NoError(t, err)
		out, _, err := f.keeper.BuildSettlementPlan(f.at(int64(height)), in, facts, id, nil)
		require.NoError(t, err)
		return out
	}
	gross := anchorOnlyGenerated * f.priceTokens / 1_000_000
	share := gross * uint64(f.verifyRatio) / 10_000
	require.Equal(t, shared.NewAmount(gross+share), plan(inputs).WorkerGross)

	// Same task, read as a Verifier-path task with one of three slots in the cluster.
	verifierPath := inputs
	verifierPath.AnchorOnly = false
	verifierPath.round1SelectedVerifierCount = 3
	verifierPath.Round1Cluster = []types.ConsensusClusterMemberV1{{VerifierOperatorAddress: f.operators[1], SelectedVerifierIndex: 0}}
	got := plan(verifierPath)
	require.Equal(t, shared.NewAmount(gross), got.WorkerGross, "the Worker's gross is its own on the Verifier path")
	require.Equal(t, shared.NewAmount(share/3), got.VerifierSlotGross)
	require.Len(t, got.VerifierPayouts, 1)

	// A task that does not pay the Worker moves no share to it.
	failed := inputs
	failed.EffectiveResult.Verdict = types.TaskVerdict_TASK_VERDICT_FAIL
	require.Equal(t, shared.NewAmount(0), plan(failed).WorkerGross)
}

// One supporter is enough for orders: a REGISTERED model admits every declared
// supporter as a candidate (this is the existing cold-start rule, unchanged), and
// an anchor-only task never asks for a second one.
func TestSingleDeclaredSupporterIsACandidateOnARegisteredModel(t *testing.T) {
	declared := hubtypes.ModelSupportSnapshot{DeclaredSupport: true}
	require.True(t, candidateModelSupportAllowed(hubtypes.ModelStatusRegistered, declared, hubtypes.ServiceBondSnapshot{}))
	require.True(t, isParentModelOpenForProfile(hubtypes.ModelStatusRegistered))

	// The fixture's candidate pool holds no eligible Verifier at all.
	f := newAnchorOnlyFixture(t, 1_000_000_000, true)
	require.Equal(t, []byte{0}, f.hub.poolBitmap)
	f.zeroAuditRate(t)
	f.submitReceipt(t)
	f.sampleRound(t)
	settlement, err := f.settleNow(t, f.window.SelectionRandomnessHeight+2)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
}

// Evidence cleanup retires an anchor-only task's rows like any other task's.
func TestAnchorOnlyTaskIsCleanedUp(t *testing.T) {
	f := newAnchorOnlyFixture(t, 1_000_000_000, true)
	f.zeroAuditRate(t)
	f.submitReceipt(t)
	f.sampleRound(t)
	_, err := f.settleNow(t, f.window.SelectionRandomnessHeight+2)
	require.NoError(t, err)
	core := mustCore(t, f)
	cleanupAt := core.GetTaskFinalityHeight() + core.EvidenceRetentionBlocksSnapshot
	ctx := f.at(int64(cleanupAt))
	for i := 0; i < 200; i++ {
		processed, err := f.keeper.processEvidenceCleanupEndBlock(ctx, cleanupAt, 16)
		require.NoError(t, err)
		if processed == 0 {
			break
		}
	}
	window, err := f.keeper.VerifierCandidateWindow.Get(f.ctx, types.NewVerifyRoundKey(f.taskKey, types.VerifyRoundV1))
	if err == nil {
		require.Equal(t, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_PRUNED, window.Status)
	}
	_, err = f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.Error(t, err, "an audit that produced no fault is removed")
	pending, err := f.keeper.EvidenceCleanupIndex.Has(f.ctx, types.NewDeadlineIndexKey(cleanupAt, f.taskKey))
	require.NoError(t, err)
	require.False(t, pending, "cleanup must run to completion")
}

// The Builder stage rows of an anchor-only task follow the receipt and the
// round-1 assignment exactly as on the Verifier path, which is the one rule the
// cross-module responsibility check reads.
func TestAnchorOnlyBuilderStageResponsibilitiesFollowTheSameRule(t *testing.T) {
	f := newAnchorOnlyFixture(t, 0, true)
	f.submitReceipt(t)
	count := func(kind hubtypes.ServiceKeyResponsibilityKind, createdAt uint64) int {
		n := 0
		for _, responsibility := range f.hub.responsibilities {
			if responsibility.ResponsibilityKind == kind && responsibility.CreatedHeight == createdAt {
				n++
			}
		}
		return n
	}
	require.Equal(t, len(f.builders), count(serviceKeyResponsibilityOpenVerifyBuilder, f.receiptAt))
	require.Zero(t, count(serviceKeyResponsibilitySettleBuilder, f.window.SelectionRandomnessHeight))
	f.sampleRound(t)
	assignment, err := f.keeper.ReadVerifierAssignment(f.ctx, types.NewVerifyRoundKey(f.taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.Equal(t, len(f.builders), count(serviceKeyResponsibilitySettleBuilder, assignment.OpenVerifyHeight))
}

// A public sweep of a Verifier deadline finds nothing to do on an anchor-only
// task: the zero-Verifier assignment files no commit, reveal or final row.
func TestAnchorOnlyTaskIgnoresVerifierDeadlineSweeps(t *testing.T) {
	f := newAnchorOnlyFixture(t, 0, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	before := mustCore(t, f)
	late := state.SampleHeight + 50
	for _, kind := range []types.DeadlineKindV1{
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_COMMIT,
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_REVEAL,
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_FINAL,
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN,
	} {
		resp, err := f.keeper.sweepTaskDeadline(f.at(int64(late)), &types.TaskDeadlineLocator{TaskId: f.taskID, DeadlineKind: kind}, late)
		if err == nil {
			require.Zero(t, resp.Advanced, "%s must not advance an anchor-only task", kind)
		}
		require.Equal(t, before, mustCore(t, f), "%s must leave the task untouched", kind)
	}
	_, err := f.settleNow(t, late)
	require.ErrorContains(t, err, "pending until", "the task is still held for its verdict")
}

// ---- upgrade: tasks already on the Verifier path ---------------------------

// bondedVerifierAdmissionHubStub adds the Worker bond facts the anchor draw reads
// to the Verifier selection stub.
type bondedVerifierAdmissionHubStub struct{ verifierAdmissionHubStub }

func (bondedVerifierAdmissionHubStub) GetServiceBondFacts(sdk.Context, string) hubtypes.ServiceBondFacts {
	return hubtypes.ServiceBondFacts{Found: true}
}

// The mode of a task is read from the rows it froze, not from its Profile. A
// task whose receipt committed the anchor before anchor-only verification
// existed holds a Verifier window with members; after the upgrade it still draws
// its Verifiers, is audited with them on record, and settles as a Verifier task.
func TestInFlightVerifierTaskWithAnAnchorReceiptKeepsTheVerifierPath(t *testing.T) {
	ready := verifierSelectionReadyFixture(t)
	f, taskKey, window := ready.f, ready.taskKey, ready.window
	assignment, err := f.keeper.ReadTaskAssignment(f.ctx, taskKey)
	require.NoError(t, err)
	receipt := types.InferReceiptState{
		TaskId: window.TaskId, WinnerWorker: assignment.WinnerWorker, GeneratedTokenCount: anchorOnlyGenerated,
		InferReceiptHash: append([]byte(nil), window.InferReceiptHash...),
		RequiredEvidenceCommitments: []types.EvidenceCommitmentV1{
			{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING, EvidenceHashOrRoot: bytes32(0x74), EncodedSizeBytes: 100},
			{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_TOKEN_OPENING, EvidenceHashOrRoot: bytes32(0x75),
				EncodedSizeBytes: 8 + 4*(anchorOnlyPrompt+anchorOnlyGenerated)},
			{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_ANCHOR_OPENING, EvidenceHashOrRoot: bytes32(0x76), EncodedSizeBytes: 64},
		},
	}
	require.NoError(t, f.keeper.WriteInferReceipt(f.ctx, taskKey, receipt))
	require.True(t, receiptIsAnchorOnly(receipt.RequiredEvidenceCommitments), "the receipt is the kind a new task would verify by the audit alone")
	require.False(t, isAnchorOnlyWindow(window), "but this task froze a Verifier window")

	reservations := make([]hubtypes.FrozenFactLiabilityRequest, 0, ready.factCount)
	attempts := 0
	responsibilities := make([]hubtypes.ServiceKeyResponsibilityState, 0, testBuildersPerStageResponsibility)
	releases := make([]string, 0, testBuildersPerStageResponsibility)
	f.keeper.hubKeeper = bondedVerifierAdmissionHubStub{verifierAdmissionHubStub{
		membersBySlot: ready.membersBySlot, bindingsBySlot: ready.bindingsBySlot,
		selectionBeacon: hubtypes.BeaconSnapshot{Height: 30, Randomness: bytes.Repeat([]byte{0x52}, types.Hash32Len)},
		reservations:    &reservations, reservationAttempts: &attempts,
		responsibilities: &responsibilities, responsibilityReleases: &releases,
	}}

	// The EndBlock selection sweep is where the two modes part.
	usage, err := f.keeper.processVerifierSelectionRandomnessIndexWithBudget(f.ctx, 30, 10, 1<<30)
	require.NoError(t, err)
	require.Equal(t, uint64(1), usage.visited)

	roundKey := types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1)
	selected, err := f.keeper.ReadVerifierAssignment(f.ctx, roundKey)
	require.NoError(t, err)
	require.False(t, isAnchorOnlyAssignment(selected))
	require.Equal(t, uint32(3), selected.SelectedVerifierCount)
	require.Len(t, selected.SelectedVerifiers, 3)
	require.Len(t, reservations, 3, "each selected Verifier reserved its task liability")
	require.Equal(t, window.GetVerifierWindowHash(), selected.VerifierCandidateWindowHash)
	core, err := f.keeper.TaskCore.Get(f.ctx, taskKey)
	require.NoError(t, err)
	require.Equal(t, types.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED, core.TaskPhase)
	require.NotEqual(t, types.VerificationStatus_VERIFICATION_STATUS_VERIFICATION_PASSED, core.VerificationStatus,
		"the round is open for its Verifiers, not closed on the Worker's receipt")
	committing, err := f.keeper.CommitDeadlineIndex.Has(f.ctx, types.NewDeadlineIndexKey(selected.CommitDeadlineHeight, taskKey))
	require.NoError(t, err)
	require.True(t, committing, "the Verifier commit deadline is scheduled")
	if round, err := f.keeper.ReadVerificationRound(f.ctx, roundKey); err == nil {
		require.Nil(t, round.XClosedHeight, "round 1 stays open until the Verifiers answer")
	}
	summarized, err := f.keeper.TaskRoundSummary.Has(f.ctx, taskKey)
	require.NoError(t, err)
	require.False(t, summarized)

	// The anchor audit is sampled on the same draw and records the Verifiers.
	audit, err := f.keeper.AnchorAudit.Get(f.ctx, taskKey)
	require.NoError(t, err)
	require.True(t, audit.Sampled)
	require.Len(t, audit.SelectedVerifiers, 3)

	// Settlement keys on the same row: this assignment is not the anchor-only
	// shape, so the task is settled from its Verifier samples (next test).
}

// Settlement side of the same rule. Whatever the receipt committed, a task whose
// round 1 drew Verifiers is settled from their samples: a round closed as PASS on
// the Worker's receipt is not accepted for it, and nothing is paid on that basis.
func TestSettlementRebuildsAVerifierRoundFromItsSamplesDespiteAnAnchorReceipt(t *testing.T) {
	f := newAnchorOnlyFixture(t, 1_000_000_000, true)
	f.zeroAuditRate(t)
	f.submitReceipt(t)
	f.sampleRound(t)
	height := f.window.SelectionRandomnessHeight + 2
	inputs, err := f.keeper.loadSettlementInputs(f.at(int64(height)), f.taskKey, height)
	require.NoError(t, err)
	require.True(t, inputs.AnchorOnly, "as sampled, the task is anchor-only and settles")

	// Give round 1 the assignment a Verifier draw leaves behind.
	roundKey := types.NewVerifyRoundKey(f.taskKey, types.VerifyRoundV1)
	assignment, err := f.keeper.ReadVerifierAssignment(f.ctx, roundKey)
	require.NoError(t, err)
	for index := range 3 {
		verifier := sdk.AccAddress(bytes.Repeat([]byte{byte(0xd1 + index)}, 20)).String()
		assignment.SelectedVerifiers = append(assignment.SelectedVerifiers,
			types.SelectedVerifierV1{OperatorAddress: verifier, Slot: uint32(index + 1), SlotVersion: 1})
	}
	assignment.SelectedVerifierCount = uint32(len(assignment.SelectedVerifiers))
	require.Equal(t, uint32(3), assignment.SelectedVerifierCount)
	require.False(t, isAnchorOnlyAssignment(assignment))
	require.NoError(t, f.keeper.WriteVerifierAssignment(f.ctx, roundKey, assignment))

	_, err = f.keeper.loadSettlementInputs(f.at(int64(height)), f.taskKey, height)
	require.ErrorIs(t, err, types.ErrInvariantBroken)
	require.ErrorContains(t, err, "closed round does not reproduce its frozen commitments",
		"the round is recomputed from Verifier samples; the anchor-only PASS is not taken at its word")
	_, err = f.settleNow(t, height)
	require.Error(t, err)
	require.Empty(t, *f.credits, "nothing is paid on the anchor-only rule to a task that drew Verifiers")
}
