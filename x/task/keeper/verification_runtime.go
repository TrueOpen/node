package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

const fixedPointOneV1 = uint32(1_000_000)

type verifyCommitPlan struct {
	key        types.CommitKey
	keyBytes   []byte
	digest     []byte
	state      types.CommitState
	core       types.TaskCoreState
	assignment types.VerifierAssignmentState
	isReplay   bool
}

type verifyResultPlan struct {
	key         types.CommitKey
	digest      []byte
	payloadHash []byte
	state       types.ResultReceiptState
	core        types.TaskCoreState
	round       uint32
	verifier    string
	isReplay    bool
}

func (k Keeper) planVerifyCommit(ctx context.Context, commit types.VerifyCommitV1, submitter string) (verifyCommitPlan, error) {
	return k.planVerifyCommitWithAuthority(ctx, commit, submitter, false)
}

func (k Keeper) planVerifyCommitForBuilder(ctx context.Context, commit types.VerifyCommitV1, submitter string) (verifyCommitPlan, error) {
	return k.planVerifyCommitWithAuthority(ctx, commit, submitter, true)
}

func (k Keeper) planVerifyCommitWithAuthority(ctx context.Context, commit types.VerifyCommitV1, submitter string, builderRelay bool) (verifyCommitPlan, error) {
	var plan verifyCommitPlan
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return plan, err
	}
	if err := validateVerifyCommitEnvelope(sdkCtx.ChainID(), height, commit); err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
	if err := k.requireVerificationSubmitter(ctx, commit.TaskId, commit.VerifierOperatorAddress, submitter, builderRelay); err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidSignature, err.Error())
	}
	digest, err := types.VerifyCommitSigningDigest(commit)
	if err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
	commitKey, err := types.DeriveCommitKey(commit.ChainId, commit.TaskId, commit.VerifyRound, commit.VerifierOperatorAddress)
	if err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
	key := types.NewCommitKey(commitKey[:])
	signatureDigest := sha256.Sum256(commit.ServiceSignature)
	existing, found, err := k.getCommitStateIfExists(ctx, key)
	if err != nil {
		return plan, err
	}
	if found {
		if !commitReplayMatches(existing, commit, commitKey[:], digest[:], signatureDigest[:]) {
			return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "conflicting verify commit replay")
		}
		return verifyCommitPlan{key: key, keyBytes: commitKey[:], digest: digest[:], state: existing, isReplay: true}, nil
	}
	if height > commit.ExpiryHeight {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verify commit credential has expired")
	}

	operatorBytes, operator, err := k.canonicalAddress("verifier_operator_address", commit.VerifierOperatorAddress)
	if err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
	taskKey := types.NewTaskKey(commit.TaskId)
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "task core unavailable")
	}
	if !bytes.Equal(core.TaskId, commit.TaskId) {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "task is not accepting verifier commits")
	}
	assignment, err := k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, commit.VerifyRound))
	if err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier assignment unavailable")
	}
	if err := validateVerifierAssignmentScope(assignment, commit.TaskId, commit.VerifyRound); err != nil {
		return plan, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if err := k.validateActiveVerifierStage(ctx, taskKey, core, commit.VerifyRound,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED,
		types.VerificationStatus_VERIFICATION_STATUS_COMMITTING); err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "task is not accepting verifier commits")
	}
	if !selectedVerifier(assignment, operator) {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier is not selected for this round")
	}
	if assignment.CommitDeadlineHeight == 0 || height > assignment.CommitDeadlineHeight {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "commit deadline has passed")
	}
	if err := k.requireCurrentCortexAuthorization(ctx, sdkCtx, operatorBytes, operator, commit.ServiceAuthorizationNonce); err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidSignature, err.Error())
	}
	if err := k.hubKeeper.VerifyCurrentCortexServiceDigest(ctx, operator, commit.ServiceSignature, digest[:], height); err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidSignature, err.Error())
	}
	state := types.CommitState{
		CommitKey:               append([]byte(nil), commitKey[:]...),
		TaskId:                  append([]byte(nil), commit.TaskId...),
		VerifyRound:             commit.VerifyRound,
		VerifierOperatorAddress: operator,
		CommitHash:              append([]byte(nil), commit.CommitHash...),
		CommitSigningDigest:     append([]byte(nil), digest[:]...),
		SignatureDigest:         append([]byte(nil), signatureDigest[:]...),
		CommitHeight:            height,
		Status:                  types.CommitStatusV1_COMMIT_STATUS_V1_ACCEPTED,
	}
	return verifyCommitPlan{key: key, keyBytes: commitKey[:], digest: digest[:], state: state, core: core, assignment: assignment}, nil
}

func (k Keeper) applyVerifyCommit(ctx context.Context, plan verifyCommitPlan) error {
	if plan.isReplay {
		return nil
	}
	if err := k.CommitState.Set(ctx, plan.key, plan.state); err != nil {
		return err
	}
	taskKey := types.NewTaskKey(plan.state.TaskId)
	if plan.core.VerificationStatus == types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED {
		height, err := currentBlockHeight(ctx)
		if err != nil {
			return err
		}
		advanceTaskPhase(&plan.core, types.TaskPhase_TASK_PHASE_COMMITTING)
		plan.core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_COMMITTING
		plan.core.UpdatedHeight = height
		if err := k.TaskCore.Set(ctx, taskKey, plan.core); err != nil {
			return err
		}
	}
	if err := emitTypedEvent(ctx, &types.EventCommitAccepted{
		SessionId: plan.core.SessionId, TaskId: plan.state.TaskId, VerifyRound: plan.state.VerifyRound,
		Verifier: plan.state.VerifierOperatorAddress, CommitHash: plan.state.CommitHash,
	}); err != nil {
		return err
	}
	all, err := k.allSelectedVerifierCommitsAccepted(ctx, plan.assignment)
	if err != nil {
		return err
	}
	if all {
		_, err := k.startRevealPhaseFromAssignment(
			ctx,
			taskKey,
			plan.assignment,
			types.RevealPhaseTrigger_REVEAL_PHASE_TRIGGER_ALL_COMMITS,
		)
		return err
	}
	return nil
}

func (k Keeper) planVerifyResult(ctx context.Context, receipt types.ResultReceiptV2, submitter string) (verifyResultPlan, error) {
	return k.planVerifyResultWithAuthority(ctx, receipt, submitter, false)
}

func (k Keeper) planVerifyResultForBuilder(ctx context.Context, receipt types.ResultReceiptV2, submitter string) (verifyResultPlan, error) {
	return k.planVerifyResultWithAuthority(ctx, receipt, submitter, true)
}

func (k Keeper) planVerifyResultWithAuthority(ctx context.Context, receipt types.ResultReceiptV2, submitter string, builderRelay bool) (verifyResultPlan, error) {
	var plan verifyResultPlan
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return plan, err
	}
	if err := validateVerifyResultEnvelope(sdkCtx.ChainID(), height, receipt); err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
	if err := k.requireVerificationSubmitter(ctx, receipt.TaskId, receipt.VerifierOperatorAddress, submitter, builderRelay); err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidSignature, err.Error())
	}
	digest, err := types.ResultReceiptSigningDigest(receipt)
	if err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
	metricSummaryHash, err := types.MetricSummaryHash(receipt.MetricSummary)
	if err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
	commitKey, err := types.DeriveCommitKey(receipt.ChainId, receipt.TaskId, receipt.VerifyRound, receipt.VerifierOperatorAddress)
	if err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
	key := types.NewCommitKey(commitKey[:])
	signatureDigest := sha256.Sum256(receipt.ServiceSignature)
	existing, found, err := k.getResultReceiptStateIfExists(ctx, key)
	if err != nil {
		return plan, err
	}
	if found {
		if !resultReplayMatches(existing, receipt, commitKey[:], metricSummaryHash[:], digest[:], signatureDigest[:]) {
			return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "conflicting verify result replay")
		}
		return verifyResultPlan{key: key, digest: digest[:], payloadHash: existing.ResultPayloadHash, state: existing, round: receipt.VerifyRound, verifier: receipt.VerifierOperatorAddress, isReplay: true}, nil
	}
	if height > receipt.ExpiryHeight {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verify result credential has expired")
	}

	operatorBytes, operator, err := k.canonicalAddress("verifier_operator_address", receipt.VerifierOperatorAddress)
	if err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
	taskKey := types.NewTaskKey(receipt.TaskId)
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, receipt.TaskId) {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "task core unavailable")
	}
	if core.TaskPhase != types.TaskPhase_TASK_PHASE_REVEALING || core.VerificationStatus != types.VerificationStatus_VERIFICATION_STATUS_REVEALING {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "task is not accepting verifier results")
	}
	assignment, err := k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, receipt.VerifyRound))
	if err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier assignment unavailable")
	}
	if err := validateVerifierAssignmentScope(assignment, receipt.TaskId, receipt.VerifyRound); err != nil {
		return plan, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	selectedIndex, selected := selectedVerifierIndex(assignment, operator)
	if !selected {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier is not selected for this round")
	}
	if assignment.RevealDeadlineHeight == 0 || height > assignment.RevealDeadlineHeight {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "reveal deadline has passed")
	}
	commit, found, err := k.getCommitStateIfExists(ctx, key)
	if err != nil {
		return plan, err
	}
	if !found || commit.Status != types.CommitStatusV1_COMMIT_STATUS_V1_ACCEPTED ||
		!bytes.Equal(commit.CommitKey, commitKey[:]) || !bytes.Equal(commit.TaskId, receipt.TaskId) ||
		commit.VerifyRound != receipt.VerifyRound || commit.VerifierOperatorAddress != operator {
		return plan, errorsmod.Wrap(types.ErrCommitNotFound, "accepted verifier commit is required before result")
	}
	taskAssignment, err := k.TaskAssignment.Get(ctx, taskKey)
	if err != nil || !bytes.Equal(taskAssignment.GenerationParamsDigest, receipt.GenerationParamsDigest) {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "generation_params_digest does not match task assignment")
	}
	infer, err := k.InferReceipt.Get(ctx, taskKey)
	if err != nil || !bytes.Equal(infer.TaskId, receipt.TaskId) || len(infer.InferReceiptHash) != types.Hash32Len {
		return plan, errorsmod.Wrap(types.ErrInvariantBroken, "accepted infer receipt is unavailable")
	}
	metricLeafCount := uint64(receipt.MetricSummary.FiniteCount) + uint64(receipt.MetricSummary.MissingComparedCount)
	if metricLeafCount > uint64(^uint32(0)) || metricLeafCount != infer.GeneratedTokenCount {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "metric leaf count does not match the accepted infer receipt")
	}
	if err := k.validateMetricSummaryAgainstFrozenProfile(sdkCtx, core, taskAssignment, receipt.MetricSummary); err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
	payloadHash, err := types.VerifierResultPayloadHash(types.VerifierResultPayloadInput{
		ChainID: receipt.ChainId, TaskID: receipt.TaskId, TaskHash: core.AcceptedTaskHash,
		VerifyRound: receipt.VerifyRound, SelectedVerifierIndex: selectedIndex,
		VerifierOperatorAddress: operator, InferReceiptHash: infer.InferReceiptHash,
		ProfileExecutionSnapshotHash: taskAssignment.ProfileExecutionSnapshotHash,
		GenerationParamsDigest:       receipt.GenerationParamsDigest, MetricRoot: receipt.MetricRoot,
		MetricLeafCount: uint32(metricLeafCount), MetricSummaryHash: metricSummaryHash[:],
		AggregateProofHash:                receipt.AggregateProofHash,
		VerifierEvidenceBundleHash:        receipt.VerifierEvidenceBundleHash,
		VerifierEvidenceManifestSizeBytes: receipt.VerifierEvidenceManifestSizeBytes,
	})
	if err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
	commitment, err := types.ResultCommitmentHash(
		receipt.ChainId, receipt.TaskId, core.AcceptedTaskHash, receipt.VerifyRound,
		operator, payloadHash[:], receipt.Salt,
	)
	if err != nil || !bytes.Equal(commit.CommitHash, commitment[:]) {
		return plan, errorsmod.Wrap(types.ErrInvalidOpenVerify, "result receipt does not open verifier commitment")
	}
	if err := k.requireCurrentCortexAuthorization(ctx, sdkCtx, operatorBytes, operator, receipt.ServiceAuthorizationNonce); err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidSignature, err.Error())
	}
	if err := k.hubKeeper.VerifyCurrentCortexServiceDigest(ctx, operator, receipt.ServiceSignature, digest[:], height); err != nil {
		return plan, errorsmod.Wrap(types.ErrInvalidSignature, err.Error())
	}
	state := types.ResultReceiptState{
		CommitKey: commitKey[:], TaskId: append([]byte(nil), receipt.TaskId...), VerifyRound: receipt.VerifyRound,
		VerifierOperatorAddress: operator, SelectedVerifierIndex: selectedIndex,
		MetricRoot:    append([]byte(nil), receipt.MetricRoot...),
		MetricSummary: receipt.MetricSummary, MetricSummaryHash: metricSummaryHash[:],
		AggregateProofHash:                append([]byte(nil), receipt.AggregateProofHash...),
		VerifierEvidenceBundleHash:        append([]byte(nil), receipt.VerifierEvidenceBundleHash...),
		VerifierEvidenceManifestSizeBytes: receipt.VerifierEvidenceManifestSizeBytes,
		Salt:                              append([]byte(nil), receipt.Salt...), ResultPayloadHash: payloadHash[:],
		ResultReceiptSigningDigest: digest[:], SignatureDigest: signatureDigest[:], AcceptedHeight: height,
	}
	return verifyResultPlan{key: key, digest: digest[:], payloadHash: payloadHash[:], state: state, core: core, round: receipt.VerifyRound, verifier: operator}, nil
}

func (k Keeper) requireVerificationSubmitter(
	ctx context.Context,
	taskID []byte,
	verifierOperator, submitter string,
	builderRelay bool,
) error {
	if !builderRelay {
		return k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeCortexNode, verifierOperator, submitter)
	}
	if len(taskID) != types.Hash32Len {
		return fmt.Errorf("task_id must be %d bytes", types.Hash32Len)
	}
	selection, err := k.TaskBuilderSelection.Get(ctx, types.NewTaskKey(taskID))
	if err != nil {
		return fmt.Errorf("Task Builder selection unavailable")
	}
	if _, err := k.authorizeOpenTaskBuilder(ctx, selection, submitter); err != nil {
		return err
	}
	return nil
}

func (k Keeper) applyVerifyResult(ctx context.Context, plan verifyResultPlan) error {
	if plan.isReplay {
		return nil
	}
	if err := k.ResultReceiptState.Set(ctx, plan.key, plan.state); err != nil {
		return err
	}
	if err := emitTypedEvent(ctx, &types.EventResultAccepted{
		SessionId: plan.core.SessionId, TaskId: plan.core.TaskId,
		VerifyRound: plan.round, Verifier: plan.verifier,
		ResultReceiptSigningDigest: plan.state.ResultReceiptSigningDigest,
		ResultPayloadHash:          plan.state.ResultPayloadHash,
	}); err != nil {
		return err
	}
	taskKey := types.NewTaskKey(plan.state.TaskId)
	assignment, err := k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, plan.round))
	if err != nil {
		return err
	}
	facts, err := k.BuildVerificationDeadlineFacts(ctx, assignment)
	if err != nil {
		return err
	}
	if facts.AcceptedResultCount != assignment.SelectedVerifierCount {
		return nil
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return err
	}
	_, _, err = k.closeRoundOnce(ctx, taskKey, plan.round, height, height,
		shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNSPECIFIED)
	return err
}

func validateVerifyCommitEnvelope(chainID string, height uint64, commit types.VerifyCommitV1) error {
	if commit.SchemaVersion != 1 || commit.ChainId != chainID || len(commit.TaskId) != types.Hash32Len ||
		!isPhase0VerifyRound(commit.VerifyRound) || len(commit.CommitHash) != types.Hash32Len || len(commit.ServiceSignature) != 64 ||
		commit.ExpiryHeight == 0 {
		return fmt.Errorf("verify commit envelope is invalid")
	}
	_, err := types.CanonicalOperatorAddressBytes("verifier_operator_address", commit.VerifierOperatorAddress)
	return err
}

func validateVerifyResultEnvelope(chainID string, height uint64, receipt types.ResultReceiptV2) error {
	if receipt.SchemaVersion != types.ResultReceiptSchemaVersionV2 || receipt.ChainId != chainID || len(receipt.TaskId) != types.Hash32Len ||
		!isPhase0VerifyRound(receipt.VerifyRound) || len(receipt.GenerationParamsDigest) != types.Hash32Len ||
		len(receipt.MetricRoot) != types.Hash32Len || len(receipt.AggregateProofHash) != types.Hash32Len ||
		len(receipt.VerifierEvidenceBundleHash) != types.Hash32Len || len(receipt.Salt) != types.Hash32Len ||
		receipt.VerifierEvidenceManifestSizeBytes == 0 || len(receipt.ServiceSignature) != 64 || receipt.ExpiryHeight == 0 ||
		isZeroHash32(receipt.MetricRoot) || isZeroHash32(receipt.AggregateProofHash) ||
		isZeroHash32(receipt.VerifierEvidenceBundleHash) || isZeroHash32(receipt.Salt) {
		return fmt.Errorf("verify result envelope is invalid")
	}
	_, err := types.CanonicalOperatorAddressBytes("verifier_operator_address", receipt.VerifierOperatorAddress)
	return err
}

func validateVerifierAssignmentScope(assignment types.VerifierAssignmentState, taskID []byte, round uint32) error {
	if !bytes.Equal(assignment.TaskId, taskID) || assignment.VerifyRound != round || !isPhase0VerifyRound(round) ||
		assignment.SelectedVerifierCount == 0 || int(assignment.SelectedVerifierCount) != len(assignment.SelectedVerifiers) {
		return fmt.Errorf("verifier assignment scope is inconsistent")
	}
	seen := make(map[string]struct{}, len(assignment.SelectedVerifiers))
	seenSlots := make(map[uint32]struct{}, len(assignment.SelectedVerifiers))
	for _, selected := range assignment.SelectedVerifiers {
		if selected.SlotVersion == 0 {
			return fmt.Errorf("selected verifier slot version is zero")
		}
		if _, err := types.CanonicalOperatorAddressBytes("selected verifier", selected.OperatorAddress); err != nil {
			return err
		}
		if _, ok := seen[selected.OperatorAddress]; ok {
			return fmt.Errorf("selected verifier identity is duplicated")
		}
		if _, ok := seenSlots[selected.Slot]; ok {
			return fmt.Errorf("selected verifier slot is duplicated")
		}
		seen[selected.OperatorAddress] = struct{}{}
		seenSlots[selected.Slot] = struct{}{}
	}
	return nil
}

func selectedVerifier(assignment types.VerifierAssignmentState, operator string) bool {
	_, ok := selectedVerifierIndex(assignment, operator)
	return ok
}

func selectedVerifierIndex(assignment types.VerifierAssignmentState, operator string) (uint32, bool) {
	for index, selected := range assignment.SelectedVerifiers {
		if selected.OperatorAddress == operator {
			return uint32(index), true
		}
	}
	return 0, false
}

func isPhase0VerifyRound(round uint32) bool {
	return round == types.VerifyRoundV1 || round == types.ChallengeVerifyRoundV1
}

func isZeroHash32(value []byte) bool {
	if len(value) != types.Hash32Len {
		return false
	}
	for _, b := range value {
		if b != 0 {
			return false
		}
	}
	return true
}

func (k Keeper) requireCurrentCortexAuthorization(ctx context.Context, sdkCtx sdk.Context, operatorBytes []byte, operator string, nonce uint64) error {
	node, found := k.hubKeeper.GetCortexNode(sdkCtx, sdk.AccAddress(operatorBytes))
	if !found || node.OperatorAddress != operator || node.ServiceKeyStatus != hubtypes.ServiceKeyStatusActive ||
		node.ServiceAuthorizationNonce != nonce {
		return fmt.Errorf("current verifier service authorization nonce or identity mismatch")
	}
	return nil
}

func (k Keeper) validateMetricSummaryAgainstFrozenProfile(sdkCtx sdk.Context, core types.TaskCoreState, assignment types.TaskAssignmentState, summary types.MetricSummaryV1) error {
	profile, found := k.hubKeeper.GetProfileState(sdkCtx, core.ModelId, core.ProfileVersion)
	if !found || !bytes.Equal(profile.ExecutionSnapshotHash, assignment.ProfileExecutionSnapshotHash) {
		return fmt.Errorf("frozen profile execution snapshot is unavailable")
	}
	verification := profile.ExecutionSnapshot.VerificationProfile
	if verification.MetricAggregateProofVersion != assignment.MetricAggregateProofVersion ||
		verification.CanonicalEncodingVersion != assignment.CanonicalEncodingVersion ||
		verification.MetricAggregateProofVersion != "PREFILL_METRIC_AGGREGATE_PROOF_V1" ||
		verification.TokenScope != shared.TokenScope_TOKEN_SCOPE_ALL_GENERATED_OUTPUT_TOKENS {
		return fmt.Errorf("verification profile versions do not match task assignment")
	}
	metrics := verification.Metrics
	if metrics.CompareTopkJaccard != (summary.XTopkJaccardMeanFp_1E6 != nil) ||
		metrics.CompareUnionJs != (summary.XUnionJsP99Fp_1E6 != nil) {
		return fmt.Errorf("metric summary optional fields do not match frozen profile")
	}
	if summary.RankDeltaNonzeroRateFp_1E6 > fixedPointOneV1 ||
		summary.GetTopkJaccardMeanFp_1E6() > fixedPointOneV1 || summary.GetUnionJsP99Fp_1E6() > fixedPointOneV1 {
		return fmt.Errorf("metric summary fixed-point rate exceeds 1e6")
	}
	return nil
}

func (k Keeper) getCommitStateIfExists(ctx context.Context, key types.CommitKey) (types.CommitState, bool, error) {
	state, err := k.CommitState.Get(ctx, key)
	if err != nil {
		if errIsNotFound(err) {
			return types.CommitState{}, false, nil
		}
		return types.CommitState{}, false, err
	}
	return state, true, nil
}

func (k Keeper) getResultReceiptStateIfExists(ctx context.Context, key types.CommitKey) (types.ResultReceiptState, bool, error) {
	state, err := k.ResultReceiptState.Get(ctx, key)
	if err != nil {
		if errIsNotFound(err) {
			return types.ResultReceiptState{}, false, nil
		}
		return types.ResultReceiptState{}, false, err
	}
	return state, true, nil
}

func commitReplayMatches(state types.CommitState, commit types.VerifyCommitV1, key, digest, signatureDigest []byte) bool {
	return state.Status == types.CommitStatusV1_COMMIT_STATUS_V1_ACCEPTED && bytes.Equal(state.CommitKey, key) &&
		bytes.Equal(state.TaskId, commit.TaskId) && state.VerifyRound == commit.VerifyRound &&
		state.VerifierOperatorAddress == commit.VerifierOperatorAddress && bytes.Equal(state.CommitHash, commit.CommitHash) &&
		bytes.Equal(state.CommitSigningDigest, digest) && bytes.Equal(state.SignatureDigest, signatureDigest)
}

func resultReplayMatches(state types.ResultReceiptState, receipt types.ResultReceiptV2, key, summaryHash, digest, signatureDigest []byte) bool {
	return bytes.Equal(state.CommitKey, key) && bytes.Equal(state.TaskId, receipt.TaskId) && state.VerifyRound == receipt.VerifyRound &&
		state.VerifierOperatorAddress == receipt.VerifierOperatorAddress && bytes.Equal(state.MetricRoot, receipt.MetricRoot) &&
		proto.Equal(&state.MetricSummary, &receipt.MetricSummary) && bytes.Equal(state.MetricSummaryHash, summaryHash) &&
		bytes.Equal(state.AggregateProofHash, receipt.AggregateProofHash) &&
		bytes.Equal(state.VerifierEvidenceBundleHash, receipt.VerifierEvidenceBundleHash) &&
		state.VerifierEvidenceManifestSizeBytes == receipt.VerifierEvidenceManifestSizeBytes && bytes.Equal(state.Salt, receipt.Salt) &&
		bytes.Equal(state.ResultReceiptSigningDigest, digest) && bytes.Equal(state.SignatureDigest, signatureDigest)
}
