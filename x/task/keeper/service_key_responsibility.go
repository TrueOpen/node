package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// The four responsibility kinds are now the closed enum frozen in
// hub/v1/common.proto; the previous free-form strings could not be validated
// at the Hub boundary.
const (
	serviceKeyResponsibilityOpenVerifyBuilder = hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_OPEN_VERIFY_BUILDER
	serviceKeyResponsibilitySettleBuilder     = hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_SETTLE_BUILDER
	serviceKeyResponsibilityChallengeVerifier = hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_CHALLENGE_VERIFIER
	serviceKeyResponsibilityWorkerEvidence    = hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE
)

// serviceKeyResponsibilityID derives the 32-byte responsibility_id from the
// length-framed scope instead of concatenating it with a delimiter.
func serviceKeyResponsibilityID(
	participantType shared.ParticipantType,
	kind hubtypes.ServiceKeyResponsibilityKind,
	sessionID, taskID, scopeID, operatorAddress string,
) ([]byte, error) {
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainServiceKeyResponsibilityIDV1)).Raw(
		shared.EnumBE(uint32(participantType)),
		shared.EnumBE(uint32(kind)),
		[]byte(sessionID),
		[]byte(taskID),
		[]byte(scopeID),
		[]byte(operatorAddress),
	).Sum()
}

func (k Keeper) reserveServiceKeyResponsibility(
	ctx context.Context,
	participantType shared.ParticipantType,
	operatorAddress string,
	responsibilityID []byte,
	kind hubtypes.ServiceKeyResponsibilityKind,
	sessionID, taskID string,
	height uint64,
) error {
	// service_authorization_nonce is left unset on purpose. The Hub owns the
	// participant's current binding and stamps the acquiring generation itself; a
	// value read here would be a second, racier copy of state this module does not
	// own, and the Hub rejects a caller-supplied nonce for exactly that reason.
	return k.hubKeeper.ReserveServiceKeyResponsibility(ctx, hubtypes.ServiceKeyResponsibilityState{
		ParticipantType:    participantType,
		OperatorAddress:    strings.TrimSpace(operatorAddress),
		ResponsibilityId:   responsibilityID,
		ResponsibilityKind: kind,
		SessionId:          sessionID,
		TaskId:             taskID,
		CreatedHeight:      height,
	})
}

func (k Keeper) releaseTaskOnlineResponsibilities(ctx context.Context, sessionID, taskID string, height uint64) error {
	if err := k.hubKeeper.ReleaseTaskLiabilities(ctx, sessionID, taskID, height); err != nil {
		return err
	}
	// The Hub bulk terminal release deliberately excludes
	// BUS_OBJECTIVE_EVIDENCE. Those per-Builder rows keep the proof key pinned
	// until Task EvidenceCleanup uses the typed release boundary.
	return k.hubKeeper.ReleaseServiceKeyResponsibilities(ctx, sessionID, taskID)
}

func (k Keeper) acquireCandidateStageDuties(ctx context.Context, facts []types.TaskCandidateFactState, added map[uint32]struct{}) error {
	for _, fact := range facts {
		if _, exists := added[fact.Slot]; !exists {
			continue
		}
		if err := k.hubKeeper.AcquirePendingStageDuty(ctx, fact.OperatorAddress); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) releaseCandidateStageDuties(ctx context.Context, facts []types.TaskCandidateFactState) error {
	for _, fact := range facts {
		if err := k.hubKeeper.ReleasePendingStageDuty(ctx, fact.OperatorAddress); err != nil {
			return err
		}
	}
	return nil
}

// releaseFailedTaskResponsibilities closes every terminal Hub responsibility
// except BUS_OBJECTIVE_EVIDENCE before a pre-settlement task is committed as
// FAILED. Kind 5 remains live through its evidence-retention boundary; the
// retained task/failure rows must not keep any other economic or admission ref.
func (k Keeper) releaseFailedTaskResponsibilities(ctx context.Context, core types.TaskCoreState, height uint64) error {
	if err := k.releaseTaskOnlineResponsibilities(ctx, hex.EncodeToString(core.SessionId), hex.EncodeToString(core.TaskId), height); err != nil {
		return err
	}
	return k.releaseTaskAdmissionRefs(ctx, core, height)
}

// BuilderStageResponsibilityID is the single producer of a Builder-stage
// responsibility_id. It is exported because app/cross_module_invariants.go has to
// recompute the very same value to prove the Task-side selection and the Hub-side
// ServiceKeyResponsibility rows agree; a second hand-written copy of this preimage
// there would let the two drift apart silently, which is exactly the drift the
// invariant exists to catch.
//
// The empty scope_id is not a placeholder to be filled in later: a Builder stage
// duty is scoped by (session, task, builder) alone. It is still a framed field --
// u64_be(0) followed by no bytes -- so omitting it rather than passing "" would
// move the digest.
func BuilderStageResponsibilityID(sessionID, taskID string, kind hubtypes.ServiceKeyResponsibilityKind, builder string) ([]byte, error) {
	return serviceKeyResponsibilityID(
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, kind,
		sessionID, taskID, "", builder,
	)
}

// ChallengeVerifierResponsibilityID is exported for the app-level cross-module
// invariant, which must recompute the Task-owned responsibility identity rather
// than duplicate its canonical preimage.
func ChallengeVerifierResponsibilityID(
	sessionID, taskID string,
	roundID []byte,
	operator string,
) ([]byte, error) {
	if len(roundID) != types.Hash32Len {
		return nil, fmt.Errorf("challenge verifier responsibility requires a round Hash32")
	}
	return serviceKeyResponsibilityID(
		shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, serviceKeyResponsibilityChallengeVerifier,
		sessionID, taskID, hex.EncodeToString(roundID), operator,
	)
}

// WorkerOutputEvidenceResponsibilityID pins the accepted receipt Worker's
// proof key until EvidenceCleanup. The repeated taskID is the frozen scope_id.
func WorkerOutputEvidenceResponsibilityID(sessionID, taskID, worker string) ([]byte, error) {
	return serviceKeyResponsibilityID(
		shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, serviceKeyResponsibilityWorkerEvidence,
		sessionID, taskID, taskID, worker,
	)
}

func (k Keeper) reserveWorkerOutputEvidenceResponsibility(
	ctx context.Context, core types.TaskCoreState, worker string, height uint64,
) error {
	if len(core.SessionId) != types.Hash32Len || len(core.TaskId) != types.Hash32Len {
		return fmt.Errorf("Worker output evidence responsibility scope is incomplete")
	}
	sessionID, taskID := hex.EncodeToString(core.SessionId), hex.EncodeToString(core.TaskId)
	responsibilityID, err := WorkerOutputEvidenceResponsibilityID(sessionID, taskID, worker)
	if err != nil {
		return err
	}
	return k.reserveServiceKeyResponsibility(
		ctx, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, worker,
		responsibilityID, serviceKeyResponsibilityWorkerEvidence,
		sessionID, taskID, height,
	)
}

func (k Keeper) reserveChallengeVerifierResponsibilities(
	ctx context.Context,
	core types.TaskCoreState,
	round types.VerificationRoundState,
	assignment types.VerifierAssignmentState,
	height uint64,
) error {
	if round.VerifyRound != types.ChallengeVerifyRoundV1 || assignment.VerifyRound != round.VerifyRound ||
		!bytes.Equal(round.TaskId, core.TaskId) || !bytes.Equal(assignment.TaskId, core.TaskId) {
		return fmt.Errorf("challenge verifier responsibility scope is inconsistent")
	}
	sessionID := hex.EncodeToString(core.SessionId)
	taskID := hex.EncodeToString(core.TaskId)
	for _, selected := range assignment.SelectedVerifiers {
		responsibilityID, err := ChallengeVerifierResponsibilityID(
			sessionID, taskID, round.RoundId, selected.OperatorAddress,
		)
		if err != nil {
			return err
		}
		if err := k.reserveServiceKeyResponsibility(
			ctx, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, selected.OperatorAddress,
			responsibilityID, serviceKeyResponsibilityChallengeVerifier,
			sessionID, taskID, height,
		); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) reserveBuilderStageResponsibilities(
	ctx context.Context,
	core types.TaskCoreState,
	selection types.TaskBuilderSelectionState,
	kind hubtypes.ServiceKeyResponsibilityKind,
	height uint64,
) error {
	sessionID, taskID, builders, err := k.builderStageResponsibilityScope(ctx, core, selection)
	if err != nil {
		return err
	}
	for _, builder := range builders {
		responsibilityID, err := BuilderStageResponsibilityID(sessionID, taskID, kind, builder)
		if err != nil {
			return err
		}
		if err := k.reserveServiceKeyResponsibility(
			ctx, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, builder,
			responsibilityID, kind, sessionID, taskID, height,
		); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) releaseBuilderStageResponsibilities(
	ctx context.Context,
	core types.TaskCoreState,
	selection types.TaskBuilderSelectionState,
	kind hubtypes.ServiceKeyResponsibilityKind,
) error {
	sessionID, taskID, builders, err := k.builderStageResponsibilityScope(ctx, core, selection)
	if err != nil {
		return err
	}
	for _, builder := range builders {
		responsibilityID, err := BuilderStageResponsibilityID(sessionID, taskID, kind, builder)
		if err != nil {
			return err
		}
		if err := k.hubKeeper.ReleaseServiceKeyResponsibility(
			ctx, shared.ParticipantTypeBuilder, builder,
			hex.EncodeToString(responsibilityID),
		); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) builderStageResponsibilityScope(
	ctx context.Context,
	core types.TaskCoreState,
	selection types.TaskBuilderSelectionState,
) (string, string, []string, error) {
	buildersPerTask := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).BuildersPerTask
	if buildersPerTask == 0 {
		return "", "", nil, fmt.Errorf("builders_per_task is unavailable")
	}
	if len(core.SessionId) != types.Hash32Len || len(core.TaskId) != types.Hash32Len ||
		!bytes.Equal(core.TaskId, selection.TaskId) {
		return "", "", nil, fmt.Errorf("Task Builder responsibility scope is inconsistent")
	}
	if selection.BodyStatus != shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE ||
		selection.SelectedTaskBuilderCount != buildersPerTask ||
		len(selection.SelectedTaskBuildersHash) != types.Hash32Len {
		return "", "", nil, fmt.Errorf("Task Builder responsibility selection is not active and complete")
	}
	builders := append([]string(nil), selection.SelectedTaskBuilders...)
	if uint32(len(builders)) != buildersPerTask {
		return "", "", nil, fmt.Errorf("selected builder responsibility set must contain exactly %d builders", buildersPerTask)
	}
	seen := make(map[string]struct{}, len(builders))
	for _, builder := range builders {
		if _, exists := seen[builder]; exists {
			return "", "", nil, fmt.Errorf("selected builder responsibility set must contain exactly %d unique builders", buildersPerTask)
		}
		seen[builder] = struct{}{}
	}
	if err := validateActiveTaskBuilderSelection(sdk.UnwrapSDKContext(ctx).ChainID(), selection, core.TaskId); err != nil {
		return "", "", nil, fmt.Errorf("Task Builder responsibility selection commitment is invalid")
	}
	return hex.EncodeToString(core.SessionId), hex.EncodeToString(core.TaskId), builders, nil
}
