package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"github.com/TrueOpen/wire/dispatch"

	"github.com/TrueOpen/node/x/task/types"
)

var (
	errBuilderEvidenceScopeMismatch = errors.New("builder evidence action scope does not match Task authority")
	errBuilderEvidenceWrongStage    = errors.New("builder evidence action has no authoritative stage prerequisite")
)

type builderEvidenceTaskAuthority struct {
	core           types.TaskCoreState
	builder        string
	frozenSlashBps uint32
}

func (k Keeper) verifyBuilderEvidence(ctx context.Context, chainID string, raw []byte) (dispatch.VerifiedBuilderEvidenceV2, error) {
	return dispatch.VerifyBuilderEvidenceV2(raw, dispatch.EvidenceVerifyOptions{
		ChainID: chainID,
		LookupProofKey: func(signer dispatch.ServiceProofSigner) (dispatch.ProofKeyBinding, error) {
			if signer.ParticipantType != dispatch.ProofParticipantBuilder {
				return dispatch.ProofKeyBinding{}, fmt.Errorf("builder evidence signer must be BUILDER")
			}
			return k.dispatchProofKey(ctx, signer)
		},
	})
}

func (k Keeper) canonicalEvidenceOperator(address string) (string, error) {
	raw, err := dispatch.AddressBytes(address)
	if err != nil {
		return "", err
	}
	canonical, err := k.addressCodec.BytesToString(raw)
	if err != nil || canonical != address {
		return "", fmt.Errorf("evidence operator is not canonical")
	}
	return canonical, nil
}

func (k Keeper) loadBuilderEvidenceTaskAuthority(ctx context.Context, message dispatch.SignedMessageV1) (builderEvidenceTaskAuthority, error) {
	builder, err := k.canonicalEvidenceOperator(message.Binding.BuilderAddress)
	if err != nil {
		return builderEvidenceTaskAuthority{}, err
	}
	taskKey := types.NewTaskKey(message.Scope.TaskID[:])
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return builderEvidenceTaskAuthority{}, fmt.Errorf("builder evidence Task core is unavailable: %w", err)
	}
	if !bytes.Equal(core.TaskId, message.Scope.TaskID[:]) {
		return builderEvidenceTaskAuthority{}, fmt.Errorf("builder evidence Task core does not match its key")
	}
	selection, err := k.loadActiveTaskBuilderSelection(ctx, taskKey, core.TaskId)
	if err != nil {
		return builderEvidenceTaskAuthority{}, fmt.Errorf("builder evidence Task selection is unavailable: %w", err)
	}
	selected := false
	for _, candidate := range selection.SelectedTaskBuilders {
		if candidate == builder {
			selected = true
			break
		}
	}
	if !selected {
		return builderEvidenceTaskAuthority{}, fmt.Errorf("message signer was not selected for the Task")
	}
	// The frozen Task responsibility selects the eligible current or previous
	// generation independently of today's live submission identity.
	if _, err := k.hubKeeper.GetBuilderEvidenceProofKey(ctx, builder, message.Binding.ServiceAuthorizationNonce, core.SessionId, core.TaskId); err != nil {
		return builderEvidenceTaskAuthority{}, fmt.Errorf("Builder Task evidence context: %w", err)
	}
	return builderEvidenceTaskAuthority{core: core, builder: builder, frozenSlashBps: selection.BuilderFaultSlashBpsSnapshot}, nil
}

func validateBuilderEvidenceCommonScope(authority builderEvidenceTaskAuthority, message dispatch.SignedMessageV1) error {
	if !bytes.Equal(message.Scope.TaskHash[:], authority.core.AcceptedTaskHash) {
		return fmt.Errorf("%w: task_hash", errBuilderEvidenceScopeMismatch)
	}
	if message.Scope.ModelID != nil && !bytes.Equal(message.Scope.ModelID[:], authority.core.ModelId) {
		return fmt.Errorf("%w: model_id", errBuilderEvidenceScopeMismatch)
	}
	return nil
}

func (k Keeper) validateBuilderEvidenceAction(ctx context.Context, authority builderEvidenceTaskAuthority, message dispatch.SignedMessageV1) error {
	if err := validateBuilderEvidenceCommonScope(authority, message); err != nil {
		return err
	}
	taskKey := types.NewTaskKey(authority.core.TaskId)
	scope := message.Scope
	switch types.StreamMessageKind(scope.MessageKind) {
	case types.StreamMessageKind_STREAM_MESSAGE_KIND_OPEN_VERIFY:
		if scope.VerifyRound != types.VerifyRoundV1 {
			return fmt.Errorf("%w: OPEN_VERIFY verify_round", errBuilderEvidenceWrongStage)
		}
		assignment, err := k.ReadTaskAssignment(ctx, taskKey)
		if errors.Is(err, collections.ErrNotFound) {
			return fmt.Errorf("%w: recorded Worker assignment", errBuilderEvidenceWrongStage)
		}
		if err != nil {
			return err
		}
		actor, err := k.canonicalEvidenceOperator(scope.PayloadActor)
		if err != nil || actor != assignment.WinnerWorker {
			return fmt.Errorf("%w: OPEN_VERIFY Worker actor", errBuilderEvidenceScopeMismatch)
		}
		receipt, err := k.ReadInferReceipt(ctx, taskKey)
		if errors.Is(err, collections.ErrNotFound) {
			return fmt.Errorf("%w: OPEN_VERIFY requires an accepted InferReceipt", errBuilderEvidenceWrongStage)
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(receipt.TaskId, authority.core.TaskId) || receipt.WinnerWorker != actor {
			return fmt.Errorf("%w: OPEN_VERIFY InferReceipt Worker", errBuilderEvidenceScopeMismatch)
		}
		return nil
	case types.StreamMessageKind_STREAM_MESSAGE_KIND_VERIFIER_ASSIGNMENT_NOTIFY:
		if scope.VerifyRound != types.VerifyRoundV1 && scope.VerifyRound != types.ChallengeVerifyRoundV1 {
			return fmt.Errorf("%w: verifier assignment verify_round", errBuilderEvidenceWrongStage)
		}
		_, err := k.ReadVerifierAssignment(ctx, types.NewVerifyRoundKey(taskKey, scope.VerifyRound))
		if errors.Is(err, collections.ErrNotFound) {
			return fmt.Errorf("%w: finalized Verifier assignment", errBuilderEvidenceWrongStage)
		}
		return err
	default:
		return fmt.Errorf("unsupported Builder evidence message kind %d", scope.MessageKind)
	}
}

func (k Keeper) validateInvalidBuilderProtocolFault(ctx context.Context, authority builderEvidenceTaskAuthority, envelope dispatch.SignedMessageV1, violation types.BuilderProtocolViolation) error {
	scopeErr := validateBuilderEvidenceCommonScope(authority, envelope)
	switch violation {
	case types.BuilderProtocolViolation_BUILDER_PROTOCOL_VIOLATION_WRONG_TASK_SCOPE:
		if errors.Is(scopeErr, errBuilderEvidenceScopeMismatch) {
			return nil
		}
		if scopeErr != nil {
			return scopeErr
		}
		return fmt.Errorf("verified message does not contain the asserted wrong-task-scope violation")
	case types.BuilderProtocolViolation_BUILDER_PROTOCOL_VIOLATION_WRONG_STAGE:
		if scopeErr != nil {
			return scopeErr
		}
		actionErr := k.validateBuilderEvidenceAction(ctx, authority, envelope)
		if errors.Is(actionErr, errBuilderEvidenceWrongStage) {
			return nil
		}
		if actionErr != nil {
			return actionErr
		}
		return fmt.Errorf("verified message does not contain the asserted wrong-stage violation")
	default:
		return fmt.Errorf("protocol violation %s requires payload authority not exposed by StreamActionScopeV1", violation.String())
	}
}
