package keeper

import (
	"bytes"
	"context"
	"fmt"
	"math/bits"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (m msgServer) OpenChallengeRound(ctx context.Context, req *types.MsgOpenChallengeRound) (*types.MsgOpenChallengeRoundResponse, error) {
	if req == nil || len(req.TaskId) != types.Hash32Len {
		return nil, status.Error(codes.InvalidArgument, "task_id must be Hash32")
	}
	openerBytes, opener, err := m.k.canonicalAddress("opener_address", req.OpenerAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	maxDebit, err := shared.ParseAmount(req.MaxTotalDebit)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "max_total_debit: %v", err)
	}
	taskKey := types.NewTaskKey(req.TaskId)
	if replay, found, err := m.k.challengeRoundReplay(ctx, taskKey, opener, maxDebit); err != nil {
		return nil, err
	} else if found {
		return replay, nil
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	core, err := m.k.TaskCore.Get(ctx, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, req.TaskId) || len(core.AcceptedTaskHash) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "task is unavailable")
	}
	if core.SettlementStatus == types.SettlementStatus_SETTLEMENT_STATUS_FINALIZED ||
		core.SettlementStatus == types.SettlementStatus_SETTLEMENT_STATUS_REFUNDED || core.XTaskFinalityHeight != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "task is already final")
	}
	if core.TaskPhase != types.TaskPhase_TASK_PHASE_REVEALING ||
		(core.VerificationStatus != types.VerificationStatus_VERIFICATION_STATUS_VERIFICATION_PASSED &&
			core.VerificationStatus != types.VerificationStatus_VERIFICATION_STATUS_VERIFY_FAILED) {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "task is not in the closed round-1 challenge window")
	}
	round1, err := m.k.ReadVerificationRound(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
	if err != nil || round1.XClosedHeight == nil || !isExplicitRoundVerdict(round1.GetVerdict()) ||
		len(round1.GetRoundFactsHash()) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "round 1 has no explicit closed verdict")
	}
	summary, err := m.k.TaskRoundSummary.Get(ctx, taskKey)
	if err != nil || summary.MaxClosedRound != types.VerifyRoundV1 || summary.OpenRoundCount != 0 ||
		!bytes.Equal(summary.Round1FactsHashOrZero32, round1.GetRoundFactsHash()) || summary.XChallengeCloseHeight == nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "round 1 summary is unavailable")
	}
	if height > summary.GetChallengeCloseHeight() {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "challenge window has closed")
	}
	budget, err := m.k.TaskBudget.Get(ctx, taskKey)
	if err != nil || budget.BudgetStatus != types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "task budget is not reserved")
	}
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if params.Challenge.MaxVerifyRound != types.ChallengeVerifyRoundV1 || params.Challenge.MaxOpenRoundPerTask != 1 {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "challenge round limits are invalid")
	}
	assignment1, err := m.k.ReadVerifierAssignment(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
	if err != nil || assignment1.SelectedVerifierCount == 0 ||
		assignment1.SelectedVerifierCount != uint32(len(assignment1.SelectedVerifiers)) {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "round 1 verifier assignment is unavailable")
	}
	funding, quote, err := deriveChallengeFunding(req.TaskId, round1.GeneratedTokenCount, budget, params)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	funding.OpenerAddress = opener
	if maxDebit < mustAmountValue(quote.TotalLock) {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "max_total_debit is below the derived total lock")
	}
	hubParams := m.k.hubKeeper.GetHubParams(sdkCtx)
	if hubParams.BusinessDenom == "" {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "business denomination is unavailable")
	}
	totalLock := mustAmountValue(quote.TotalLock)
	if m.k.bankKeeper.GetBalance(ctx, sdk.AccAddress(openerBytes), hubParams.BusinessDenom).Amount.LT(sdkmath.NewIntFromUint64(totalLock)) {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "opener balance is below the derived total lock")
	}

	clock, err := freezeChallengeRoundClock(height, params.Challenge)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	workerAssignment, err := m.k.ReadTaskAssignment(ctx, taskKey)
	if err != nil || len(workerAssignment.CandidatePoolSnapshotId) != types.Hash32Len || len(workerAssignment.CandidatePoolHash) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "task assignment is unavailable")
	}
	infer, err := m.k.ReadInferReceipt(ctx, taskKey)
	if err != nil || len(infer.InferReceiptHash) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "infer receipt is unavailable")
	}
	slotCapacity, segmentBytes, candidates, err := m.k.deriveVerifierEligibilityCandidates(ctx, core, workerAssignment, hubParams)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	candidates = excludePriorRoundVerifiers(candidates, assignment1)
	windowSize, err := verifierWindowSize(uint32(len(candidates)), params.Weights.VerifierCandidateRatioPpm,
		params.Weights.VerifierCandidateWindowMin, params.Weights.VerifierCandidateWindowMax)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if windowSize < params.Challenge.ChallengeVerifierCount && uint32(len(candidates)) >= params.Challenge.ChallengeVerifierCount {
		windowSize = params.Challenge.ChallengeVerifierCount
	}
	window := types.VerifierCandidateWindowState{
		SchemaVersion: 1, TaskId: append([]byte(nil), req.TaskId...), VerifyRound: types.ChallengeVerifyRoundV1,
		InferReceiptHash: append([]byte(nil), infer.InferReceiptHash...), CandidatePoolSnapshotId: append([]byte(nil), workerAssignment.CandidatePoolSnapshotId...),
		CandidatePoolHash: append([]byte(nil), workerAssignment.CandidatePoolHash...), EligibilityFrozenHeight: height,
		WindowRandomnessHeight: clock.windowRandomness, BuilderProposalCloseHeight: clock.handraiseClose,
		HandraiseCloseHeight: clock.handraiseClose, SelectionRandomnessHeight: clock.selectionRandomness,
		AssignmentDeadlineHeight: clock.commitDeadline, WindowSize: windowSize,
	}
	window, segments, err := freezeVerifierEligibilitySource(sdkCtx.ChainID(), window, slotCapacity, segmentBytes, candidates)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	roundID, err := types.VerifyRoundID(sdkCtx.ChainID(), core.AcceptedTaskHash, types.ChallengeVerifyRoundV1)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	fundingHash, err := types.RoundFundingLockHash(sdkCtx.ChainID(), funding)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	funding.FundingLockHash = fundingHash[:]
	round2 := types.VerificationRoundState{
		TaskId: append([]byte(nil), req.TaskId...), TaskHash: append([]byte(nil), core.AcceptedTaskHash...),
		VerifyRound: types.ChallengeVerifyRoundV1, RoundId: roundID[:],
		XOpenerAddress:      &types.VerificationRoundState_OpenerAddress{OpenerAddress: opener},
		XPrevRoundFactsHash: &types.VerificationRoundState_PrevRoundFactsHash{PrevRoundFactsHash: append([]byte(nil), round1.GetRoundFactsHash()...)},
		XPrevRoundVerdict:   &types.VerificationRoundState_PrevRoundVerdict{PrevRoundVerdict: round1.GetVerdict()},
		RoundOpenHeight:     height, RoundCloseDeadlineHeight: clock.roundClose,
		InferReceiptRef:              append([]byte(nil), infer.InferReceiptHash...),
		ProfileExecutionSnapshotHash: append([]byte(nil), workerAssignment.ProfileExecutionSnapshotHash...),
		GenerationParamsDigest:       append([]byte(nil), workerAssignment.GenerationParamsDigest...),
		XFundingLockHash:             &types.VerificationRoundState_FundingLockHash{FundingLockHash: fundingHash[:]},
	}

	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	if err := m.k.clearPriorOpenVerifyScratch(cache, taskKey, params); err != nil {
		return nil, err
	}
	if totalLock > 0 {
		coins := sdk.NewCoins(sdk.NewCoin(hubParams.BusinessDenom, sdkmath.NewIntFromUint64(totalLock)))
		if err := m.k.bankKeeper.SendCoinsFromAccountToModule(cache, sdk.AccAddress(openerBytes), shared.TaskChallengeEffectModuleName, coins); err != nil {
			return nil, err
		}
	}
	key2 := types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1)
	if err := m.k.WriteVerificationRound(cache, key2, round2); err != nil {
		return nil, err
	}
	if err := m.k.WriteRoundFunding(cache, key2, funding); err != nil {
		return nil, err
	}
	summary.OpenRoundCount = 1
	summary.Round2FactsHashOrZero32 = make([]byte, types.Hash32Len)
	summary.Round2EffectRootOrZero32 = make([]byte, types.Hash32Len)
	summary.XRound2Outcome = nil
	summary.XTaskRoundSummaryHash = nil
	if err := m.k.TaskRoundSummary.Set(cache, taskKey, summary); err != nil {
		return nil, err
	}
	if err := removeDeadlineIndex(cache, m.k.ChallengeWindowCloseIndex, taskKey, summary.GetChallengeCloseHeight()); err != nil {
		return nil, err
	}
	if err := m.k.VerifierCandidateWindow.Set(cache, key2, window); err != nil {
		return nil, err
	}
	for _, segment := range segments {
		if bitmapIsZero(segment.Bitmap) {
			continue
		}
		if err := m.k.VerifierCandidateEligibilitySegment.Set(cache,
			types.NewVerifierEligibilitySegmentKey(taskKey, types.ChallengeVerifyRoundV1, segment.SegmentIndex), segment); err != nil {
			return nil, err
		}
	}
	if err := m.k.VerifierWindowBuildIndex.Set(cache, types.NewVerifyRoundIndexKey(clock.windowRandomness, taskKey, types.ChallengeVerifyRoundV1)); err != nil {
		return nil, err
	}
	if err := m.k.VerifierHandraiseCloseIndex.Set(cache, types.NewVerifyRoundIndexKey(clock.handraiseClose, taskKey, types.ChallengeVerifyRoundV1)); err != nil {
		return nil, err
	}
	if err := m.k.VerifyOpenDeadlineIndex.Set(cache, types.NewDeadlineIndexKey(clock.commitDeadline, taskKey)); err != nil {
		return nil, err
	}
	if err := m.k.hubKeeper.AcquireBeaconConsumerRef(cache, clock.windowRandomness, hubtypes.BeaconConsumerKindVerifierWindow, hex32(taskKey)); err != nil {
		return nil, err
	}
	if err := m.k.hubKeeper.AcquireBeaconConsumerRef(cache, clock.selectionRandomness, hubtypes.BeaconConsumerKindVerifierSelection, hex32(taskKey)); err != nil {
		return nil, err
	}
	core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING
	core.EffectiveVerifyRound = types.VerifyRoundV1
	core.UpdatedHeight = height
	if err := m.k.TaskCore.Set(cache, taskKey, core); err != nil {
		return nil, err
	}
	if err := emitTypedEvent(cache, &types.EventChallengeRoundOpened{
		TaskId: req.TaskId, VerifyRound: types.ChallengeVerifyRoundV1, Opener: opener,
		PrevRoundFactsHash: round1.GetRoundFactsHash(), Bond: funding.ChallengeOpenBond,
		VerifierBudget: funding.VerifierBudget, RoundCloseDeadlineHeight: clock.roundClose,
	}); err != nil {
		return nil, err
	}
	write()
	quote = roundFundingQuote(funding)
	return &types.MsgOpenChallengeRoundResponse{
		TaskId: append([]byte(nil), req.TaskId...), VerifyRound: types.ChallengeVerifyRoundV1,
		RoundId: roundID[:], Funding: quote, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

type challengeRoundClock struct {
	windowRandomness    uint64
	handraiseClose      uint64
	selectionRandomness uint64
	commitDeadline      uint64
	roundClose          uint64
}

func freezeChallengeRoundClock(openHeight uint64, params types.ChallengeParamsV1) (challengeRoundClock, error) {
	values := []uint64{params.CandidateWindowDelayBlocks, params.HandraiseWindowBlocks, params.AssignmentDelayBlocks,
		params.CommitWindowBlocks, params.RevealWindowBlocks}
	current := openHeight
	heights := make([]uint64, len(values))
	for i, delta := range values {
		if delta == 0 {
			return challengeRoundClock{}, fmt.Errorf("challenge round clock contains a zero window")
		}
		next, overflow := checkedHeightAdd(current, delta)
		if overflow {
			return challengeRoundClock{}, fmt.Errorf("challenge round clock overflows")
		}
		heights[i], current = next, next
	}
	return challengeRoundClock{windowRandomness: heights[0], handraiseClose: heights[1], selectionRandomness: heights[2], commitDeadline: heights[3], roundClose: heights[4]}, nil
}

func deriveChallengeFunding(taskID []byte, tokenCount uint64, budget types.TaskBudgetState, params types.TaskParamsV1) (types.RoundFundingState, shared.RoundFundingQuoteV1, error) {
	priceBid, err := shared.ParseAmount(budget.PriceBid)
	if err != nil {
		return types.RoundFundingState{}, shared.RoundFundingQuoteV1{}, fmt.Errorf("price_bid: %w", err)
	}
	workerGross, ok := mulDivFloorRound(tokenCount, priceBid, 1_000_000)
	if !ok {
		return types.RoundFundingState{}, shared.RoundFundingQuoteV1{}, fmt.Errorf("round 1 worker gross overflows")
	}
	verifyTotal, ok := mulDivFloorRound(workerGross, uint64(budget.VerifyRatioBpsSnapshot), uint64(types.BasisPointsMaximum))
	if !ok {
		return types.RoundFundingState{}, shared.RoundFundingQuoteV1{}, fmt.Errorf("round 1 verifier total overflows")
	}
	if params.Weights.SelectedVerifierCount == 0 || params.Challenge.ChallengeVerifierCount == 0 {
		return types.RoundFundingState{}, shared.RoundFundingQuoteV1{}, fmt.Errorf("verifier counts are zero")
	}
	slotFee := verifyTotal / uint64(params.Weights.SelectedVerifierCount)
	verifierBudget, overflow := checkedMulUint64(slotFee, uint64(params.Challenge.ChallengeVerifierCount))
	if overflow {
		return types.RoundFundingState{}, shared.RoundFundingQuoteV1{}, fmt.Errorf("challenge verifier budget overflows")
	}
	bond, err := shared.ParseAmount(params.Challenge.ChallengeOpenBond)
	if err != nil {
		return types.RoundFundingState{}, shared.RoundFundingQuoteV1{}, fmt.Errorf("challenge open bond: %w", err)
	}
	total, overflow := checkedAddUint64(bond, verifierBudget)
	if overflow {
		return types.RoundFundingState{}, shared.RoundFundingQuoteV1{}, fmt.Errorf("challenge total lock overflows")
	}
	zero := shared.NewAmount(0)
	funding := types.RoundFundingState{
		TaskId: append([]byte(nil), taskID...), VerifyRound: types.ChallengeVerifyRoundV1,
		ChallengeOpenBond: shared.NewAmount(bond), ChallengeSlotFee: shared.NewAmount(slotFee),
		ChallengeVerifierCount: params.Challenge.ChallengeVerifierCount, VerifierBudget: shared.NewAmount(verifierBudget),
		TotalLock: shared.NewAmount(total), BondLockedAmount: shared.NewAmount(bond),
		VerifierBudgetRemaining: shared.NewAmount(verifierBudget), VerifierPaid: zero,
		BondRefund: zero, BondSlash: zero, OpenerRecovery: zero, TreasuryResidual: zero,
		ChallengeInconclusiveBondSlashBpsSnapshot: params.Challenge.ChallengeInconclusiveBondSlashBps,
		ChallengeFaultSlashBpsSnapshot:            params.Challenge.ChallengeFaultSlashBps,
	}
	return funding, roundFundingQuote(funding), nil
}

func roundFundingQuote(funding types.RoundFundingState) shared.RoundFundingQuoteV1 {
	return shared.RoundFundingQuoteV1{
		TaskId: append([]byte(nil), funding.TaskId...), VerifyRound: funding.VerifyRound,
		ChallengeOpenBond: funding.ChallengeOpenBond, ChallengeSlotFee: funding.ChallengeSlotFee,
		ChallengeVerifierCount: funding.ChallengeVerifierCount, VerifierBudget: funding.VerifierBudget, TotalLock: funding.TotalLock,
	}
}

func (k Keeper) challengeRoundReplay(ctx context.Context, taskKey types.TaskKey, opener string, maxDebit uint64) (*types.MsgOpenChallengeRoundResponse, bool, error) {
	key := types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1)
	round, err := k.ReadVerificationRound(ctx, key)
	if err != nil {
		if errIsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	funding, err := k.ReadRoundFunding(ctx, key)
	if err != nil || round.GetOpenerAddress() != opener || funding.OpenerAddress != opener ||
		!bytes.Equal(round.TaskId, taskKey) || !bytes.Equal(round.GetFundingLockHash(), funding.FundingLockHash) {
		return nil, false, errorsmod.Wrap(types.ErrInvalidOpenVerify, "conflicting challenge round replay")
	}
	if maxDebit < mustAmountValue(funding.TotalLock) {
		return nil, false, errorsmod.Wrap(types.ErrInvalidOpenVerify, "replay max_total_debit is below the frozen total lock")
	}
	return &types.MsgOpenChallengeRoundResponse{
		TaskId: append([]byte(nil), round.TaskId...), VerifyRound: round.VerifyRound, RoundId: append([]byte(nil), round.RoundId...),
		Funding: roundFundingQuote(funding), Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP,
	}, true, nil
}

func excludePriorRoundVerifiers(candidates []verifierWindowCandidate, assignment types.VerifierAssignmentState) []verifierWindowCandidate {
	excluded := make(map[string]struct{}, len(assignment.SelectedVerifiers))
	for _, verifier := range assignment.SelectedVerifiers {
		excluded[verifier.OperatorAddress] = struct{}{}
	}
	out := make([]verifierWindowCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, found := excluded[candidate.OperatorAddress]; !found {
			out = append(out, candidate)
		}
	}
	return out
}

func (k Keeper) clearPriorOpenVerifyScratch(ctx context.Context, taskKey types.TaskKey, params types.TaskParamsV1) error {
	stage := types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY
	if cursor, err := k.TaskCandidateFinalizeCursor.Has(ctx, types.NewTaskStageKey(taskKey, stage)); err != nil || cursor {
		if err != nil {
			return err
		}
		return errorsmod.Wrap(types.ErrInvariantBroken, "round 1 verifier finalize cursor is still open")
	}
	factIter, err := k.TaskCandidateFact.Iterate(ctx, collections.NewSuperPrefixedTripleRange[types.Hash32Key, int32, uint32](taskKey, int32(stage)))
	if err != nil {
		return err
	}
	var factKeys []types.TaskCandidateFactKeyTriple
	for ; factIter.Valid(); factIter.Next() {
		key, err := factIter.Key()
		if err != nil {
			factIter.Close()
			return err
		}
		factKeys = append(factKeys, key)
		if uint32(len(factKeys)) > params.Proposals.MaxCandidateUnionMembersPerStage {
			factIter.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "round 1 verifier facts exceed the registered bound")
		}
	}
	factIter.Close()
	for _, key := range factKeys {
		if err := k.TaskCandidateFact.Remove(ctx, key); err != nil {
			return err
		}
	}
	proposalIter, err := k.BuilderStageProposal.Iterate(ctx, collections.NewSuperPrefixedTripleRange[types.Hash32Key, int32, types.Hash32Key](taskKey, int32(stage)))
	if err != nil {
		return err
	}
	var proposalKeys []types.BuilderStageProposalKeyTriple
	for ; proposalIter.Valid(); proposalIter.Next() {
		key, err := proposalIter.Key()
		if err != nil {
			proposalIter.Close()
			return err
		}
		proposalKeys = append(proposalKeys, key)
		if uint32(len(proposalKeys)) > params.Proposals.MaxCandidateAcceptedProposalsPerStage {
			proposalIter.Close()
			return errorsmod.Wrap(types.ErrInvariantBroken, "round 1 verifier proposals exceed the registered bound")
		}
	}
	proposalIter.Close()
	for _, key := range proposalKeys {
		if err := k.BuilderStageProposal.Remove(ctx, key); err != nil {
			return err
		}
	}
	stageKey := types.NewTaskStageKey(taskKey, stage)
	if exists, err := k.TaskStageHandraiseUnion.Has(ctx, stageKey); err != nil {
		return err
	} else if exists {
		if err := k.TaskStageHandraiseUnion.Remove(ctx, stageKey); err != nil {
			return err
		}
	}
	return nil
}

func mulDivFloorRound(left, right, denominator uint64) (uint64, bool) {
	if denominator == 0 {
		return 0, false
	}
	high, low := bits.Mul64(left, right)
	if high >= denominator {
		return 0, false
	}
	quotient, _ := bits.Div64(high, low, denominator)
	return quotient, true
}

func mustAmountValue(amount shared.Amount) uint64 {
	value, err := shared.ParseAmount(amount)
	if err != nil {
		panic(err)
	}
	return value
}

func isExplicitRoundVerdict(verdict types.TaskVerdict) bool {
	return verdict == types.TaskVerdict_TASK_VERDICT_PASS || verdict == types.TaskVerdict_TASK_VERDICT_FAIL
}
