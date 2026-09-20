package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	taskkeeper "github.com/TrueOpen/node/x/task/keeper"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// EnsureCrossModuleReferences validates references whose two ends are
// owned by different modules. Module Genesis validators cannot prove these
// relationships independently.
func (app *App) EnsureCrossModuleReferences(ctx context.Context) error {
	if err := app.ensureTaskLiabilityReferences(ctx); err != nil {
		return err
	}
	if err := app.ensureBuilderDutyResponsibilities(ctx); err != nil {
		return err
	}
	if err := app.ensureChallengeVerifierResponsibilities(ctx); err != nil {
		return err
	}
	if err := app.ensureTaskReferenceOwnership(ctx); err != nil {
		return err
	}
	if err := app.ensureRoleFaultEvidenceScope(ctx); err != nil {
		return err
	}
	if err := app.ensureWorkerOutputEvidenceResponsibilities(ctx); err != nil {
		return err
	}
	return app.ensureBusObjectiveEvidenceResponsibilities(ctx)
}

// EnsureGenesisValidatorVrfKeyCoverage enforces the production-genesis VRF
// bootstrap contract after both staking and hub have initialized. Module
// validation alone cannot prove this join because each side owns only one end.
// Keep this check in the fresh-bootstrap branch of InitChainer only:
// validators may join a running chain before their next-epoch VRF registration
// activates, and an exported state may legitimately contain a later active
// epoch or pending rotation.
func (app *App) EnsureGenesisValidatorVrfKeyCoverage(ctx context.Context) error {
	params, err := app.HubKeeper.Params.Get(ctx)
	if err != nil {
		return fmt.Errorf("load Hub params for genesis VRF coverage: %w", err)
	}
	if params.Beacon.VrfRequiredFromHeight == 0 {
		return nil
	}

	var coverageErr error
	err = app.StakingKeeper.IterateValidators(ctx, func(_ int64, validator stakingtypes.ValidatorI) bool {
		operatorBytes, err := app.StakingKeeper.ValidatorAddressCodec().StringToBytes(validator.GetOperator())
		if err != nil {
			coverageErr = fmt.Errorf("decode genesis validator operator %q: %w", validator.GetOperator(), err)
			return true
		}
		operator := sdk.AccAddress(operatorBytes).String()
		state, err := app.HubKeeper.VrfKey.Get(ctx, operator)
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				coverageErr = fmt.Errorf("genesis validator %s has no active VRF key in app_state.hub.vrf_keys", operator)
			} else {
				coverageErr = fmt.Errorf("load genesis validator %s VRF key: %w", operator, err)
			}
			return true
		}
		if state.OperatorAddress != operator {
			coverageErr = fmt.Errorf("genesis validator %s VRF row carries operator_address %q", operator, state.OperatorAddress)
			return true
		}
		if len(state.ActiveVrfPubkey) != hubtypes.VrfPubkeyLen {
			coverageErr = fmt.Errorf("genesis validator %s active VRF key must be %d bytes", operator, hubtypes.VrfPubkeyLen)
			return true
		}
		if state.ActiveFromEpoch != 0 {
			coverageErr = fmt.Errorf("genesis validator %s vrf_keys.active_from_epoch must be 0", operator)
			return true
		}
		if state.XPendingVrfPubkey != nil || state.XPendingFromEpoch != nil {
			coverageErr = fmt.Errorf("genesis validator %s vrf_keys row must not carry pending fields", operator)
			return true
		}
		return false
	})
	if err != nil {
		return fmt.Errorf("iterate genesis validators for VRF coverage: %w", err)
	}
	return coverageErr
}

func (app *App) ensureWorkerOutputEvidenceResponsibilities(ctx context.Context) error {
	type expectedResponsibility struct {
		worker, sessionID, taskID string
		id                        []byte
		createdHeight             uint64
	}
	expected := make(map[string]expectedResponsibility)
	evidenceReceipts, err := app.TaskKeeper.WorkerEvidenceReceipt.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; evidenceReceipts.Valid(); evidenceReceipts.Next() {
		receipt, err := evidenceReceipts.Value()
		if err != nil {
			evidenceReceipts.Close()
			return err
		}
		fault, err := app.HubKeeper.RoleFault.Get(ctx, hubtypes.NewRoleFaultKey(receipt.FaultId))
		if err != nil || !bytes.Equal(fault.FaultId, receipt.FaultId) || !bytes.Equal(fault.TaskId, receipt.TaskId) ||
			fault.OperatorAddress != receipt.WorkerOperatorAddress || fault.Duty != shared.Duty_DUTY_WORKER ||
			fault.FaultClass != hubtypes.FaultKind_FAULT_KIND_EQUIVOCATION ||
			fault.ClassificationSource != shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE {
			evidenceReceipts.Close()
			return fmt.Errorf("Worker evidence receipt has no matching Hub objective RoleFault")
		}
	}
	if err := evidenceReceipts.Close(); err != nil {
		return err
	}
	receipts, err := app.TaskKeeper.InferReceipt.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; receipts.Valid(); receipts.Next() {
		entry, err := receipts.KeyValue()
		if err != nil {
			receipts.Close()
			return err
		}
		selection, err := app.TaskKeeper.TaskBuilderSelection.Get(ctx, entry.Key)
		if err != nil {
			receipts.Close()
			return fmt.Errorf("InferReceipt has no Task Builder selection: %w", err)
		}
		if selection.BodyStatus == shared.StoredBodyStatus_STORED_BODY_STATUS_PRUNED {
			continue
		}
		core, err := app.TaskKeeper.TaskCore.Get(ctx, entry.Key)
		if err != nil || !bytes.Equal(core.TaskId, entry.Value.TaskId) || entry.Value.WinnerWorker == "" || entry.Value.ReceiptHeight == 0 {
			receipts.Close()
			return fmt.Errorf("InferReceipt has no canonical Worker evidence responsibility authority")
		}
		sessionID, taskID := hex.EncodeToString(core.SessionId), hex.EncodeToString(core.TaskId)
		id, err := taskkeeper.WorkerOutputEvidenceResponsibilityID(sessionID, taskID, entry.Value.WinnerWorker)
		if err != nil {
			receipts.Close()
			return err
		}
		expected[hex.EncodeToString(id)] = expectedResponsibility{
			worker: entry.Value.WinnerWorker, sessionID: sessionID, taskID: taskID,
			id: id, createdHeight: entry.Value.ReceiptHeight,
		}
	}
	if err := receipts.Close(); err != nil {
		return err
	}
	rows, err := app.HubKeeper.ServiceKeyResponsibility.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; rows.Valid(); rows.Next() {
		entry, err := rows.KeyValue()
		if err != nil {
			rows.Close()
			return err
		}
		state := entry.Value
		if state.ResponsibilityKind != hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE {
			continue
		}
		id := hex.EncodeToString(state.ResponsibilityId)
		want, ok := expected[id]
		if !ok || state.ParticipantType != shared.ParticipantType_PARTICIPANT_TYPE_CORTEX || state.OperatorAddress != want.worker ||
			!bytes.Equal(state.ResponsibilityId, want.id) || state.SessionId != want.sessionID || state.TaskId != want.taskID ||
			state.CreatedHeight != want.createdHeight || state.ServiceAuthorizationNonce == 0 ||
			entry.Key.K1() != int32(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX) || entry.Key.K2() != state.OperatorAddress ||
			!bytes.Equal(entry.Key.K3(), state.ResponsibilityId) {
			rows.Close()
			return fmt.Errorf("Worker output evidence responsibility %s has no exact Task authority", id)
		}
		delete(expected, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(expected) != 0 {
		return fmt.Errorf("accepted InferReceipts are missing %d Worker output evidence responsibilities", len(expected))
	}
	return nil
}

// ensureRoleFaultEvidenceScope enforces the single-producer rule for
// classification evidence: the API contract gives
// `evidence_digest` exactly one production point in the whole chain (the Task
// failure classifier), and the data-structure contractrequires that a
// RoleFault sourced from DEADLINE / SETTLEMENT / VERIFICATION_ROUND carry the
// byte-identical digest — and the same classification_source — as the
// TaskFailureClassState for its (task_id, verify_round).
//
// The failure this catches is a Hub that re-derives the pair from fault_class
// instead of copying the Task verdict. That produces a fault whose fault_id,
// fault_summary_hash and freeze failure refs all commit to a digest no
// classification transaction ever produced, so the divergence is invisible
// until an export or a freeze root disagrees between nodes.
func (app *App) ensureRoleFaultEvidenceScope(ctx context.Context) error {
	faults, err := app.HubKeeper.RoleFault.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer faults.Close()
	for ; faults.Valid(); faults.Next() {
		fault, err := faults.Value()
		if err != nil {
			return err
		}
		switch fault.ClassificationSource {
		case shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE,
			shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT,
			shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_VERIFICATION_ROUND:
		default:
			// OBJECTIVE_EVIDENCE copies an equivocation digest instead and is
			// explicitly forbidden from having a TaskFailureClassState at all. It
			// is Wire #25 and has no enum value here yet; skipping unknown sources
			// keeps this check from misreading one the day it lands.
			continue
		}
		taskKey := tasktypes.TaskKey(fault.TaskId)
		classes, err := app.TaskKeeper.TaskFailureClass.Iterate(ctx,
			collections.NewPrefixedPairRange[tasktypes.Hash32Key, uint32](taskKey))
		if err != nil {
			return err
		}
		// The join is only asserted when it is unambiguous. RoleFaultState carries
		// no verify_round, so with two rounds' class rows present there is no way
		// to tell which one the fault belongs to — and because the class rows and
		// the fault rows retire on different retention params, one round's row can
		// legitimately be pruned while the other survives. Reporting a mismatch
		// there would refuse to boot a node over a bookkeeping skew. With exactly
		// one class row the correspondence is forced, which is the case the
		// single-production-point rule is really about.
		var only tasktypes.TaskFailureClassState
		count := 0
		for ; classes.Valid(); classes.Next() {
			class, err := classes.Value()
			if err != nil {
				classes.Close()
				return err
			}
			count++
			if count > 1 {
				break
			}
			only = class
		}
		classes.Close()
		if count != 1 {
			continue
		}
		if !bytes.Equal(only.EvidenceDigest, fault.EvidenceDigest) ||
			only.ClassificationSource != fault.ClassificationSource {
			return fmt.Errorf("role fault %s evidence_digest disagrees with the task failure class of task %s",
				hex.EncodeToString(fault.FaultId), hex.EncodeToString(fault.TaskId))
		}
	}
	return nil
}

func (app *App) ensureChallengeVerifierResponsibilities(ctx context.Context) error {
	type expectedResponsibility struct {
		operator string
		id       []byte
		session  string
		task     string
		height   uint64
	}
	expected := make(map[string]expectedResponsibility)
	assignments, err := app.TaskKeeper.VerifierAssignment.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; assignments.Valid(); assignments.Next() {
		entry, err := assignments.KeyValue()
		if err != nil {
			assignments.Close()
			return err
		}
		assignment := entry.Value
		if assignment.VerifyRound != tasktypes.ChallengeVerifyRoundV1 {
			continue
		}
		taskKey := entry.Key.K1()
		core, err := app.TaskKeeper.TaskCore.Get(ctx, taskKey)
		if err != nil {
			assignments.Close()
			return fmt.Errorf("round 2 verifier assignment has no Task core: %w", err)
		}
		if core.FinalityStatus == shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL {
			continue
		}
		round, err := app.TaskKeeper.VerificationRound.Get(ctx, entry.Key)
		if err != nil || len(round.RoundId) != tasktypes.Hash32Len || assignment.OpenVerifyHeight == 0 {
			assignments.Close()
			return fmt.Errorf("round 2 verifier assignment has no canonical round authority")
		}
		sessionID := hex.EncodeToString(core.SessionId)
		taskID := hex.EncodeToString(core.TaskId)
		for _, selected := range assignment.SelectedVerifiers {
			id, err := taskkeeper.ChallengeVerifierResponsibilityID(
				sessionID, taskID, round.RoundId, selected.OperatorAddress,
			)
			if err != nil {
				assignments.Close()
				return err
			}
			key := hex.EncodeToString(id)
			if _, duplicate := expected[key]; duplicate {
				assignments.Close()
				return fmt.Errorf("round 2 verifier responsibility id is duplicated")
			}
			expected[key] = expectedResponsibility{
				operator: selected.OperatorAddress, id: id, session: sessionID,
				task: taskID, height: assignment.OpenVerifyHeight,
			}
		}
	}
	if err := assignments.Close(); err != nil {
		return err
	}
	rows, err := app.HubKeeper.ServiceKeyResponsibility.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; rows.Valid(); rows.Next() {
		entry, err := rows.KeyValue()
		if err != nil {
			rows.Close()
			return err
		}
		state := entry.Value
		if state.ResponsibilityKind != hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_CHALLENGE_VERIFIER {
			continue
		}
		key := hex.EncodeToString(state.ResponsibilityId)
		want, ok := expected[key]
		if !ok || state.ParticipantType != shared.ParticipantType_PARTICIPANT_TYPE_CORTEX ||
			state.OperatorAddress != want.operator || !bytes.Equal(state.ResponsibilityId, want.id) ||
			state.SessionId != want.session || state.TaskId != want.task || state.CreatedHeight != want.height ||
			entry.Key.K1() != int32(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX) ||
			entry.Key.K2() != state.OperatorAddress || !bytes.Equal(entry.Key.K3(), state.ResponsibilityId) {
			rows.Close()
			return fmt.Errorf("round 2 verifier responsibility %s has no exact Task authority", key)
		}
		delete(expected, key)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(expected) != 0 {
		return fmt.Errorf("Task-owned round 2 verifier duty is missing %d Hub responsibilities", len(expected))
	}
	return nil
}

type expectedBusObjectiveEvidenceResponsibility struct {
	operatorAddress  string
	responsibilityID []byte
	sessionID        string
	taskID           string
	nonce            uint64
	createdHeight    uint64
}

// ensureBusObjectiveEvidenceResponsibilities closes the Genesis/load boundary
// between Task's retained Builder selection and Hub's kind-5 rows. Module-local
// validation cannot prove either direction because each module owns only one
// side of the relationship.
func (app *App) ensureBusObjectiveEvidenceResponsibilities(ctx context.Context) error {
	expected := make(map[string]expectedBusObjectiveEvidenceResponsibility)
	selections, err := app.TaskKeeper.TaskBuilderSelection.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; selections.Valid(); selections.Next() {
		entry, err := selections.KeyValue()
		if err != nil {
			selections.Close()
			return err
		}
		selection := entry.Value
		taskHex := hex.EncodeToString(entry.Key)
		if !bytes.Equal(entry.Key, selection.TaskId) {
			selections.Close()
			return fmt.Errorf("Task Builder selection %s does not match its store key", taskHex)
		}
		switch selection.BodyStatus {
		case shared.StoredBodyStatus_STORED_BODY_STATUS_PRUNED:
			if len(selection.SelectedTaskBuilders) != 0 || selection.SelectedTaskBuilderCount == 0 {
				selections.Close()
				return fmt.Errorf("PRUNED Task Builder selection %s has a non-canonical retained header", taskHex)
			}
			continue
		case shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE:
		default:
			selections.Close()
			return fmt.Errorf("Task Builder selection %s has invalid body_status", taskHex)
		}
		core, err := app.TaskKeeper.TaskCore.Get(ctx, entry.Key)
		if err != nil || !bytes.Equal(core.TaskId, selection.TaskId) || len(core.SessionId) != tasktypes.Hash32Len {
			selections.Close()
			return fmt.Errorf("active Task Builder selection %s has no matching Task core", taskHex)
		}
		if selection.CreatedHeight == 0 || selection.SelectedTaskBuilderCount == 0 ||
			selection.SelectedTaskBuilderCount != uint32(len(selection.SelectedTaskBuilders)) ||
			len(selection.SelectedTaskBuildersHash) != tasktypes.Hash32Len {
			selections.Close()
			return fmt.Errorf("active Task Builder selection %s is incomplete", taskHex)
		}
		recomputed, err := tasktypes.SelectedTaskBuildersHash(
			sdk.UnwrapSDKContext(ctx).ChainID(), selection.TaskId, selection.BuilderSetId,
			selection.BuilderSetHash, selection.SelectedTaskBuilders,
		)
		if err != nil || !bytes.Equal(recomputed, selection.SelectedTaskBuildersHash) {
			selections.Close()
			return fmt.Errorf("active Task Builder selection %s commitment is invalid", taskHex)
		}
		seenBuilders := make(map[string]struct{}, len(selection.SelectedTaskBuilders))
		for _, builder := range selection.SelectedTaskBuilders {
			binding, err := app.HubKeeper.GetBuilderObjectiveEvidenceCurrentBinding(ctx, builder)
			if err != nil || binding.ParticipantType != shared.ParticipantTypeBuilder ||
				binding.OperatorAddress != builder || binding.AuthorizationNonce == 0 {
				selections.Close()
				return fmt.Errorf("Task %s Builder %s has no retained objective-evidence binding", taskHex, builder)
			}
			if _, duplicate := seenBuilders[binding.OperatorAddress]; duplicate {
				selections.Close()
				return fmt.Errorf("Task %s repeats objective-evidence Builder %s", taskHex, builder)
			}
			seenBuilders[binding.OperatorAddress] = struct{}{}
			locator := shared.BusObjectiveEvidenceResponsibilityV1{
				SchemaVersion: 1, BuilderOperator: builder,
				SessionId: append([]byte(nil), core.SessionId...), TaskId: append([]byte(nil), core.TaskId...),
				ServiceAuthorizationNonce: binding.AuthorizationNonce,
			}
			responsibilityID, err := app.HubKeeper.BusObjectiveEvidenceResponsibilityID(locator)
			if err != nil {
				selections.Close()
				return err
			}
			id := hex.EncodeToString(responsibilityID)
			if _, duplicate := expected[id]; duplicate {
				selections.Close()
				return fmt.Errorf("Task %s has a duplicate BUS objective-evidence responsibility id", taskHex)
			}
			expected[id] = expectedBusObjectiveEvidenceResponsibility{
				operatorAddress: builder, responsibilityID: responsibilityID,
				sessionID: hex.EncodeToString(core.SessionId), taskID: taskHex,
				nonce: binding.AuthorizationNonce, createdHeight: selection.CreatedHeight,
			}
		}
	}
	if err := selections.Close(); err != nil {
		return err
	}

	counts := make(map[string]uint32)
	rows, err := app.HubKeeper.ServiceKeyResponsibility.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; rows.Valid(); rows.Next() {
		entry, err := rows.KeyValue()
		if err != nil {
			rows.Close()
			return err
		}
		state := entry.Value
		if state.ResponsibilityKind != hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE {
			continue
		}
		id := hex.EncodeToString(state.ResponsibilityId)
		want, ok := expected[id]
		if !ok || state.ParticipantType != shared.ParticipantType_PARTICIPANT_TYPE_BUILDER ||
			state.OperatorAddress != want.operatorAddress || !bytes.Equal(state.ResponsibilityId, want.responsibilityID) ||
			state.SessionId != want.sessionID || state.TaskId != want.taskID ||
			state.ServiceAuthorizationNonce != want.nonce || state.CreatedHeight != want.createdHeight ||
			entry.Key.K1() != int32(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER) ||
			entry.Key.K2() != state.OperatorAddress || !bytes.Equal(entry.Key.K3(), state.ResponsibilityId) {
			rows.Close()
			return fmt.Errorf("BUS objective-evidence responsibility %s has no exact Task-owned authority", id)
		}
		indexKey := hubtypes.NewServiceKeyResponsibilityByTaskKey(
			state.SessionId, state.TaskId, state.ParticipantType, state.OperatorAddress, state.ResponsibilityId,
		)
		hasIndex, err := app.HubKeeper.ServiceKeyResponsibilityByTaskIndex.Has(ctx, indexKey)
		if err != nil || !hasIndex {
			rows.Close()
			return fmt.Errorf("BUS objective-evidence responsibility %s is missing its ByTask index", id)
		}
		if counts[state.OperatorAddress] == ^uint32(0) {
			rows.Close()
			return fmt.Errorf("BUS objective-evidence responsibility count overflows for Builder %s", state.OperatorAddress)
		}
		counts[state.OperatorAddress]++
		delete(expected, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(expected) != 0 {
		return fmt.Errorf("active Task Builder selections are missing %d BUS objective-evidence responsibilities", len(expected))
	}
	builders, err := app.HubKeeper.Builder.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer builders.Close()
	for ; builders.Valid(); builders.Next() {
		builder, err := builders.Value()
		if err != nil {
			return err
		}
		if builder.PendingEvidenceSubmissionCount != counts[builder.BuilderAddress] {
			return fmt.Errorf("Builder %s pending_evidence_submission_count does not match active BUS responsibilities", builder.BuilderAddress)
		}
		delete(counts, builder.BuilderAddress)
	}
	if len(counts) != 0 {
		return fmt.Errorf("BUS objective-evidence responsibilities reference %d missing Builders", len(counts))
	}
	return nil
}

type expectedBuilderDutyResponsibility struct {
	operatorAddress  string
	responsibilityID []byte
	kind             hubtypes.ServiceKeyResponsibilityKind
	sessionID        string
	taskID           string
	createdHeight    uint64
}

func (app *App) ensureBuilderDutyResponsibilities(ctx context.Context) error {
	expected := make(map[string]expectedBuilderDutyResponsibility)
	selections, err := app.TaskKeeper.TaskBuilderSelection.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; selections.Valid(); selections.Next() {
		entry, err := selections.KeyValue()
		if err != nil {
			selections.Close()
			return err
		}
		selection := entry.Value
		if selection.BuilderSetRefReleased {
			continue
		}
		// Since X-16 a Task-side store key is the raw 32 bytes, so entry.Key feeds
		// TaskCore.Get and NewVerifyRoundKey unchanged. taskHex exists only for the
		// error text: a %s of the raw digest would emit unprintable bytes, and go vet
		// does not flag that.
		taskHex := hex.EncodeToString(entry.Key)
		core, err := app.TaskKeeper.TaskCore.Get(ctx, entry.Key)
		if err != nil || !bytes.Equal(core.TaskId, selection.TaskId) {
			selections.Close()
			return fmt.Errorf("active Task Builder selection %s has no matching Task core", taskHex)
		}
		receipt, err := app.TaskKeeper.InferReceipt.Get(ctx, entry.Key)
		if errors.Is(err, collections.ErrNotFound) {
			continue
		}
		if err != nil || !bytes.Equal(receipt.TaskId, core.TaskId) || receipt.ReceiptHeight == 0 {
			selections.Close()
			return fmt.Errorf("Task %s has a non-canonical infer receipt", taskHex)
		}
		if selection.BodyStatus != shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE ||
			selection.SelectedTaskBuilderCount != 3 || len(selection.SelectedTaskBuilders) != 3 ||
			len(selection.SelectedTaskBuildersHash) != tasktypes.Hash32Len {
			selections.Close()
			return fmt.Errorf("Task %s Builder duty selection is not active and complete", taskHex)
		}
		recomputed, err := tasktypes.SelectedTaskBuildersHash(
			sdk.UnwrapSDKContext(ctx).ChainID(), selection.TaskId, selection.BuilderSetId,
			selection.BuilderSetHash, selection.SelectedTaskBuilders,
		)
		if err != nil || !bytes.Equal(recomputed, selection.SelectedTaskBuildersHash) {
			selections.Close()
			return fmt.Errorf("Task %s Builder duty selection commitment is invalid", taskHex)
		}
		kind := hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_OPEN_VERIFY_BUILDER
		createdHeight := receipt.ReceiptHeight
		assignment, assignmentErr := app.TaskKeeper.VerifierAssignment.Get(
			ctx, tasktypes.NewVerifyRoundKey(entry.Key, tasktypes.VerifyRoundV1),
		)
		if assignmentErr == nil {
			if !bytes.Equal(assignment.TaskId, core.TaskId) || assignment.OpenVerifyHeight == 0 {
				selections.Close()
				return fmt.Errorf("Task %s has a non-canonical verifier assignment", taskHex)
			}
			kind = hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_SETTLE_BUILDER
			createdHeight = assignment.OpenVerifyHeight
		} else if !errors.Is(assignmentErr, collections.ErrNotFound) {
			selections.Close()
			return assignmentErr
		}
		sessionID := hex.EncodeToString(core.SessionId)
		taskID := hex.EncodeToString(core.TaskId)
		for _, builder := range selection.SelectedTaskBuilders {
			// The id is recomputed through the Task keeper's own producer rather
			// than re-spelled here. This invariant only proves anything if both
			// sides derive the value the same way, and the local copy of the
			// preimage this replaces was one edit away from agreeing with nothing.
			responsibilityID, err := taskkeeper.BuilderStageResponsibilityID(sessionID, taskID, kind, builder)
			if err != nil {
				selections.Close()
				return err
			}
			key := hex.EncodeToString(responsibilityID)
			if _, duplicate := expected[key]; duplicate {
				selections.Close()
				return fmt.Errorf("Task %s Builder duty responsibility id is duplicated", taskHex)
			}
			expected[key] = expectedBuilderDutyResponsibility{
				operatorAddress: builder, responsibilityID: responsibilityID, kind: kind,
				sessionID: sessionID, taskID: taskID, createdHeight: createdHeight,
			}
		}
	}
	if err := selections.Close(); err != nil {
		return err
	}

	rows, err := app.HubKeeper.ServiceKeyResponsibility.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; rows.Valid(); rows.Next() {
		entry, err := rows.KeyValue()
		if err != nil {
			rows.Close()
			return err
		}
		state := entry.Value
		if state.ResponsibilityKind != hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_OPEN_VERIFY_BUILDER &&
			state.ResponsibilityKind != hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_SETTLE_BUILDER {
			continue
		}
		key := hex.EncodeToString(state.ResponsibilityId)
		want, ok := expected[key]
		if !ok || state.ParticipantType != shared.ParticipantType_PARTICIPANT_TYPE_BUILDER ||
			state.OperatorAddress != want.operatorAddress || !bytes.Equal(state.ResponsibilityId, want.responsibilityID) ||
			state.ResponsibilityKind != want.kind || state.SessionId != want.sessionID || state.TaskId != want.taskID ||
			state.CreatedHeight != want.createdHeight ||
			entry.Key.K1() != int32(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER) ||
			entry.Key.K2() != state.OperatorAddress || !bytes.Equal(entry.Key.K3(), state.ResponsibilityId) {
			rows.Close()
			return fmt.Errorf("Builder duty responsibility %s has no exact Task-owned authority", key)
		}
		delete(expected, key)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(expected) != 0 {
		return fmt.Errorf("Task-owned Builder duty is missing %d Hub responsibilities", len(expected))
	}
	return nil
}

func (app *App) ensureTaskLiabilityReferences(ctx context.Context) error {
	liabilities, err := app.HubKeeper.TaskLiabilityReservation.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer liabilities.Close()
	for ; liabilities.Valid(); liabilities.Next() {
		liability, err := liabilities.Value()
		if err != nil {
			return err
		}
		// A Hub liability row is not width-validated here, so a malformed task_id now
		// fails at the codec rather than missing the row: Hash32KeyCodec is
		// fail-closed, so Get returns an encoding error instead of ErrNotFound and
		// this loop reports it through `return coreErr` below. Before X-16 the same
		// row hex-encoded to a wrong-length string and fell through to the terminal
		// summary probe. Either way the invariant fails; only the message differs.
		taskKey := tasktypes.TaskKey(liability.TaskId)
		taskHex := hex.EncodeToString(liability.TaskId)
		core, coreErr := app.TaskKeeper.TaskCore.Get(ctx, taskKey)
		if coreErr != nil {
			if !errors.Is(coreErr, collections.ErrNotFound) {
				return coreErr
			}
			if _, summaryErr := app.TaskKeeper.TaskTerminalSummary.Get(ctx, taskKey); summaryErr != nil {
				return fmt.Errorf("task liability %s/%s references missing task", taskHex, liability.OperatorAddress)
			}
			continue
		}
		if !bytes.Equal(core.TaskId, liability.TaskId) {
			return fmt.Errorf("task liability %s primary scope mismatch", taskHex)
		}
		if liability.Status != hubtypes.TaskLiabilityStatusReserved {
			continue
		}
		switch liability.Duty {
		case shared.DutyWorker:
			assignment, err := app.TaskKeeper.TaskAssignment.Get(ctx, taskKey)
			if err != nil || assignment.WinnerWorker != liability.OperatorAddress {
				return fmt.Errorf("reserved worker liability %s/%s has no matching assignment", taskHex, liability.OperatorAddress)
			}
		case shared.DutyVerifier:
			assignment, err := app.TaskKeeper.VerifierAssignment.Get(ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1))
			if err != nil || !containsSelectedVerifier(assignment.SelectedVerifiers, liability.OperatorAddress) {
				return fmt.Errorf("reserved verifier liability %s/%s has no matching assignment", taskHex, liability.OperatorAddress)
			}
		default:
			return fmt.Errorf("task liability %s has unsupported duty %s", taskHex, liability.Duty)
		}
	}

	assignments, err := app.TaskKeeper.TaskAssignment.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer assignments.Close()
	for ; assignments.Valid(); assignments.Next() {
		entry, err := assignments.KeyValue()
		if err != nil {
			return err
		}
		if entry.Value.WinnerWorker == "" {
			continue
		}
		core, err := app.TaskKeeper.TaskCore.Get(ctx, entry.Key)
		if err != nil || isTerminalTaskPhase(core.TaskPhase) {
			continue
		}
		// Both sides key on the raw task_id now, so entry.Key is handed straight to
		// the Hub key. The hex render that used to bridge them survives only as the
		// %s below.
		taskHex := hex.EncodeToString(entry.Key)
		key := hubtypes.NewTaskLiabilityReservationKey(entry.Key, shared.DutyWorker, entry.Value.WinnerWorker)
		liability, err := app.HubKeeper.TaskLiabilityReservation.Get(ctx, key)
		if err != nil || liability.Status != hubtypes.TaskLiabilityStatusReserved {
			return fmt.Errorf("active worker assignment %s has no reserved liability", taskHex)
		}
	}

	verifierAssignments, err := app.TaskKeeper.VerifierAssignment.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer verifierAssignments.Close()
	for ; verifierAssignments.Valid(); verifierAssignments.Next() {
		entry, err := verifierAssignments.KeyValue()
		if err != nil {
			return err
		}
		taskKey := entry.Key.K1()
		core, err := app.TaskKeeper.TaskCore.Get(ctx, taskKey)
		if err != nil || isTerminalTaskPhase(core.TaskPhase) {
			continue
		}
		// taskKey is the raw Task store key and is now also the Hub key; taskHex is
		// hoisted out of the inner loop only for the error texts.
		taskHex := hex.EncodeToString(taskKey)
		for _, selected := range entry.Value.SelectedVerifiers {
			key := hubtypes.NewTaskLiabilityReservationKey(taskKey, shared.DutyVerifier, selected.OperatorAddress)
			liability, err := app.HubKeeper.TaskLiabilityReservation.Get(ctx, key)
			if err != nil || liability.Status != hubtypes.TaskLiabilityStatusReserved {
				return fmt.Errorf("active verifier assignment %s/%s has no reserved liability", taskHex, selected.OperatorAddress)
			}
		}
	}
	return nil
}

type parameterBucketReferenceKey struct {
	kind    shared.BucketKind
	key     string
	version uint64
}

func (app *App) ensureTaskReferenceOwnership(ctx context.Context) error {
	// Both sides of this join are raw Hash32 now, so the map is keyed by a
	// fixed-width array rather than by hex text. It used to be keyed by hex for
	// one reason only -- a []byte cannot be a Go map key -- which then forced the
	// Task-side loop below to re-render its raw store key just to index it.
	type taskRefKey [shared.Hash32KeySize]byte
	refKey := func(raw []byte) taskRefKey {
		var key taskRefKey
		copy(key[:], raw)
		return key
	}
	poolRefs := make(map[taskRefKey]taskRefKey)
	rows, err := app.HubKeeper.CandidatePoolTaskRef.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; rows.Valid(); rows.Next() {
		entry, err := rows.KeyValue()
		if err != nil {
			rows.Close()
			return err
		}
		ref := entry.Value
		// Both key components and both row fields are the same raw Hash32, so the
		// key/value identity below is a direct comparison instead of two hex
		// renderings. The hex that survives is only the %s in the error texts.
		taskHex := hex.EncodeToString(ref.TaskId)
		snapshotHex := hex.EncodeToString(ref.SnapshotId)
		if len(ref.TaskId) != shared.Hash32KeySize || len(ref.SnapshotId) != shared.Hash32KeySize ||
			!bytes.Equal(entry.Key.K1(), ref.TaskId) || !bytes.Equal(entry.Key.K2(), ref.SnapshotId) ||
			ref.AcquiredHeight == 0 || ref.Status != hubtypes.CandidatePoolTaskRefStatus_CANDIDATE_POOL_TASK_REF_STATUS_ACQUIRED {
			rows.Close()
			return fmt.Errorf("candidate pool task reference is non-canonical")
		}
		if _, duplicate := poolRefs[refKey(ref.TaskId)]; duplicate {
			rows.Close()
			return fmt.Errorf("task %s owns multiple candidate pool references", taskHex)
		}
		taskKey := tasktypes.TaskKey(ref.TaskId)
		assignment, err := app.TaskKeeper.TaskAssignment.Get(ctx, taskKey)
		if err != nil || !bytes.Equal(assignment.TaskId, ref.TaskId) ||
			!bytes.Equal(assignment.CandidatePoolSnapshotId, ref.SnapshotId) || assignment.CandidatePoolRefReleased {
			rows.Close()
			return fmt.Errorf("candidate pool task reference %s/%s has no matching Task assignment", taskHex, snapshotHex)
		}
		poolRefs[refKey(ref.TaskId)] = refKey(ref.SnapshotId)
	}
	rows.Close()

	assignments, err := app.TaskKeeper.TaskAssignment.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; assignments.Valid(); assignments.Next() {
		entry, err := assignments.KeyValue()
		if err != nil {
			assignments.Close()
			return err
		}
		assignment := entry.Value
		if len(assignment.CandidatePoolSnapshotId) == 0 {
			continue
		}
		// Both stores key on Hash32KeyCodec, so entry.Key is exactly 32 bytes and
		// indexes poolRefs directly -- the hex rendering this loop used to need
		// only existed to bridge the two spellings.
		taskHex := hex.EncodeToString(entry.Key)
		snapshotKey, exists := poolRefs[refKey(entry.Key)]
		if assignment.CandidatePoolRefReleased {
			if exists {
				assignments.Close()
				return fmt.Errorf("released Task assignment %s still owns a candidate pool reference", taskHex)
			}
			continue
		}
		if !exists || len(assignment.CandidatePoolSnapshotId) != shared.Hash32KeySize ||
			snapshotKey != refKey(assignment.CandidatePoolSnapshotId) {
			assignments.Close()
			return fmt.Errorf("Task assignment %s has no matching candidate pool reference", taskHex)
		}
	}
	assignments.Close()

	builderRefs := make(map[string]uint64)
	builderRows, err := app.HubKeeper.BuilderSetTaskRef.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; builderRows.Valid(); builderRows.Next() {
		entry, err := builderRows.KeyValue()
		if err != nil {
			builderRows.Close()
			return err
		}
		ref := entry.Value
		// entry.Key.K1() is the raw task_id now, so the key/value identity is a direct
		// comparison. builderRefs stays keyed by hex: builder_set_id is text either
		// way, and taskHex is already being computed here for the three error texts.
		taskHex := hex.EncodeToString(ref.TaskId)
		if len(ref.TaskId) != shared.Hash32KeySize || ref.BuilderSetVersion == 0 || ref.AcquiredHeight == 0 ||
			!bytes.Equal(entry.Key.K1(), ref.TaskId) || entry.Key.K2() != ref.BuilderSetVersion {
			builderRows.Close()
			return fmt.Errorf("BuilderSet task reference is non-canonical")
		}
		if _, duplicate := builderRefs[taskHex]; duplicate {
			builderRows.Close()
			return fmt.Errorf("task %s owns multiple BuilderSet references", taskHex)
		}
		taskKey := tasktypes.TaskKey(ref.TaskId)
		selection, err := app.TaskKeeper.TaskBuilderSelection.Get(ctx, taskKey)
		set, setErr := app.HubKeeper.BuilderSet.Get(ctx, ref.BuilderSetVersion)
		if err != nil || setErr != nil || !bytes.Equal(selection.TaskId, ref.TaskId) ||
			selection.BuilderSetId != set.BuilderSetId || !bytes.Equal(selection.BuilderSetHash, set.BuilderSetHash) ||
			selection.BuilderSetRefReleased {
			builderRows.Close()
			return fmt.Errorf("BuilderSet task reference %s/%d has no matching Task selection", taskHex, ref.BuilderSetVersion)
		}
		builderRefs[taskHex] = ref.BuilderSetVersion
	}
	builderRows.Close()

	selections, err := app.TaskKeeper.TaskBuilderSelection.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; selections.Valid(); selections.Next() {
		entry, err := selections.KeyValue()
		if err != nil {
			selections.Close()
			return err
		}
		selection := entry.Value
		// builderRefs is keyed by the hex task_id (see the loop above).
		taskHex := hex.EncodeToString(entry.Key)
		builderSetVersion, exists := builderRefs[taskHex]
		if selection.BuilderSetRefReleased {
			if exists {
				selections.Close()
				return fmt.Errorf("released Task selection %s still owns a BuilderSet reference", taskHex)
			}
			continue
		}
		set, setErr := app.HubKeeper.BuilderSet.Get(ctx, builderSetVersion)
		if !exists || setErr != nil || set.BuilderSetId != selection.BuilderSetId {
			selections.Close()
			return fmt.Errorf("Task selection %s has no matching BuilderSet reference", taskHex)
		}
	}
	selections.Close()

	bucketRefCounts := make(map[parameterBucketReferenceKey]uint64)
	bucketRows, err := app.TaskKeeper.TaskBucketRef.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; bucketRows.Valid(); bucketRows.Next() {
		entry, err := bucketRows.KeyValue()
		if err != nil {
			bucketRows.Close()
			return err
		}
		ref := entry.Value
		// TaskBucketRef is a Task-owned collection, so both sides of the K1 check are
		// raw bytes since X-16 and the comparison is bytes.Equal — a `!=` here would
		// have compared slice headers, which is why the compiler rejects it. Only the
		// message needs the hex form; bucketRefCounts is keyed by (kind, key, version)
		// and never by the task, so no Go map has to be re-keyed here.
		taskKey := tasktypes.TaskKey(ref.TaskId)
		taskHex := hex.EncodeToString(ref.TaskId)
		if len(ref.TaskId) != 32 || !bytes.Equal(entry.Key.K1(), ref.TaskId) || entry.Key.K2() != int32(ref.BucketKind) ||
			entry.Key.K3() != ref.BucketKey || !hubtypes.IsParameterBucketKind(ref.BucketKind) ||
			hubtypes.ValidateParameterBucketKey(ref.BucketKey) != nil || ref.Version == 0 || ref.AcquiredHeight == 0 {
			bucketRows.Close()
			return fmt.Errorf("Task parameter bucket reference is non-canonical")
		}
		core, err := app.TaskKeeper.TaskCore.Get(ctx, taskKey)
		if err != nil || !bytes.Equal(core.TaskId, ref.TaskId) {
			bucketRows.Close()
			return fmt.Errorf("Task parameter bucket reference %s points at a missing task", taskHex)
		}
		// The phase says nothing here, and gating on a terminal one was wrong:
		// keeper.finalizeTerminalTask is what releases the three admission refs,
		// and its sweep only visits tasks that are ALREADY SETTLED or FAILED, so
		// every settled task holds these rows for the whole finality delay window
		// by construction. What the release is recorded in is the flag below --
		// releaseTaskAdmissionRefs sets it in the same call that removes these
		// rows, and refuses a partial release -- which is also how the candidate
		// pool and BuilderSet reference checks above are written.
		assignment, err := app.TaskKeeper.TaskAssignment.Get(ctx, taskKey)
		if err != nil || !bytes.Equal(assignment.TaskId, ref.TaskId) {
			bucketRows.Close()
			return fmt.Errorf("Task parameter bucket reference %s has no Task assignment", taskHex)
		}
		if assignment.CandidatePoolRefReleased {
			bucketRows.Close()
			return fmt.Errorf("Task parameter bucket reference %s outlives its released admission refs", taskHex)
		}
		key := parameterBucketReferenceKey{kind: ref.BucketKind, key: ref.BucketKey, version: ref.Version}
		if bucketRefCounts[key] == ^uint64(0) {
			bucketRows.Close()
			return fmt.Errorf("Task parameter bucket reference count overflows")
		}
		bucketRefCounts[key]++
	}
	bucketRows.Close()

	versionRows, err := app.HubKeeper.ParameterBucketVersion.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; versionRows.Valid(); versionRows.Next() {
		entry, err := versionRows.KeyValue()
		if err != nil {
			versionRows.Close()
			return err
		}
		version := entry.Value
		key := parameterBucketReferenceKey{kind: version.BucketKind, key: version.BucketKey, version: version.Version}
		if entry.Key.K1() != int32(version.BucketKind) || entry.Key.K2() != version.BucketKey || entry.Key.K3() != version.Version ||
			version.TaskRefCount != bucketRefCounts[key] {
			versionRows.Close()
			return fmt.Errorf("Hub parameter bucket %s/%s/%d task_ref_count=%d, Task refs=%d",
				version.BucketKind.String(), version.BucketKey, version.Version, version.TaskRefCount, bucketRefCounts[key])
		}
		delete(bucketRefCounts, key)
	}
	versionRows.Close()
	if len(bucketRefCounts) != 0 {
		return fmt.Errorf("Task parameter bucket reference points at a missing Hub version")
	}
	return nil
}

func containsSelectedVerifier(selected []tasktypes.SelectedVerifierV1, operator string) bool {
	for _, member := range selected {
		if member.OperatorAddress == operator {
			return true
		}
	}
	return false
}

func isTerminalTaskPhase(phase tasktypes.TaskPhase) bool {
	return phase == tasktypes.TaskPhase_TASK_PHASE_SETTLED || phase == tasktypes.TaskPhase_TASK_PHASE_FAILED
}
