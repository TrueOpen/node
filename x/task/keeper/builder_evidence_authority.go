package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/wire/bus"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

var (
	errBuilderEvidenceScopeMismatch = errors.New("builder evidence action scope does not match Task authority")
	errBuilderEvidenceWrongStage    = errors.New("builder evidence action has no authoritative stage prerequisite")
)

type builderEvidenceTaskAuthority struct {
	core    types.TaskCoreState
	builder string
	// frozenSlashBps is carried with the authority rather than re-read at the
	// fact boundary: the rate belongs to the same selection row that proves this
	// Builder was on duty, so the two can never be sourced from different Tasks.
	frozenSlashBps uint32
}

func (k Keeper) verifyBuilderEvidenceEnvelope(ctx context.Context, chainID string, raw []byte) (bus.VerifiedEvidenceEnvelope, error) {
	return bus.VerifyEvidenceEnvelope(raw, bus.EvidenceVerifyOptions{
		ChainID: chainID,
		LookupKey: func(participantType int32, operatorAddress string, authorizationNonce uint64) ([]byte, bus.ProofKeyStatus, error) {
			if participantType != bus.ParticipantBuilder {
				return nil, 0, fmt.Errorf("builder evidence sender participant type must be BUILDER")
			}
			builder, err := k.canonicalBusOperator(operatorAddress)
			if err != nil {
				return nil, 0, err
			}
			binding, err := k.hubKeeper.GetBuilderEvidenceProofKey(ctx, builder, authorizationNonce)
			if err != nil {
				return nil, 0, err
			}
			if binding.ParticipantType != shared.ParticipantTypeBuilder || binding.OperatorAddress != builder ||
				binding.AuthorizationNonce != authorizationNonce {
				return nil, 0, fmt.Errorf("Builder proof key does not match the signed binding")
			}
			publicKey, err := hex.DecodeString(binding.ServicePubkey)
			if err != nil || len(publicKey) != 33 || hex.EncodeToString(publicKey) != binding.ServicePubkey {
				return nil, 0, fmt.Errorf("Builder proof key is not canonical compressed secp256k1")
			}
			switch binding.Status {
			case hubtypes.ServiceKeyStatusActive:
				return publicKey, bus.ProofKeyActive, nil
			case hubtypes.ServiceKeyStatusRevoked:
				return publicKey, bus.ProofKeyRevoked, nil
			default:
				return nil, 0, fmt.Errorf("Builder proof key status is not usable")
			}
		},
	})
}

func (k Keeper) canonicalBusOperator(address string) (string, error) {
	raw, err := bus.OperatorAddressCodecBytes(address)
	if err != nil {
		return "", err
	}
	canonical, err := k.addressCodec.BytesToString(raw)
	if err != nil {
		return "", fmt.Errorf("canonicalize Bus operator: %w", err)
	}
	return canonical, nil
}

func (k Keeper) loadBuilderEvidenceTaskAuthority(ctx context.Context, envelope bus.VerifiedEvidenceEnvelope) (builderEvidenceTaskAuthority, error) {
	if envelope.Fields.SenderParticipantType != bus.ParticipantBuilder || len(envelope.Scope.TaskID) != types.Hash32Len {
		return builderEvidenceTaskAuthority{}, fmt.Errorf("verified envelope is not attributable to one Builder Task")
	}
	builder, err := k.canonicalBusOperator(envelope.Fields.SenderOperatorAddress)
	if err != nil {
		return builderEvidenceTaskAuthority{}, err
	}
	taskKey := types.NewTaskKey(envelope.Scope.TaskID)
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return builderEvidenceTaskAuthority{}, fmt.Errorf("builder evidence Task core is unavailable: %w", err)
	}
	if !bytes.Equal(core.TaskId, envelope.Scope.TaskID) {
		return builderEvidenceTaskAuthority{}, fmt.Errorf("builder evidence Task core does not match its key")
	}
	selection, err := k.loadActiveTaskBuilderSelection(ctx, taskKey, envelope.Scope.TaskID)
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
		return builderEvidenceTaskAuthority{}, fmt.Errorf("envelope signer was not selected for the Task")
	}
	return builderEvidenceTaskAuthority{
		core:           core,
		builder:        builder,
		frozenSlashBps: selection.BuilderFaultSlashBpsSnapshot,
	}, nil
}

func validateBuilderEvidenceCommonScope(authority builderEvidenceTaskAuthority, envelope bus.VerifiedEvidenceEnvelope) error {
	if len(envelope.Scope.TaskHash) != 0 && !bytes.Equal(envelope.Scope.TaskHash, authority.core.AcceptedTaskHash) {
		return fmt.Errorf("%w: task_hash", errBuilderEvidenceScopeMismatch)
	}
	if envelope.Scope.ModelID != nil && *envelope.Scope.ModelID != authority.core.ModelId {
		return fmt.Errorf("%w: model_id", errBuilderEvidenceScopeMismatch)
	}
	return nil
}

func (k Keeper) validateBuilderEvidenceAction(ctx context.Context, authority builderEvidenceTaskAuthority, envelope bus.VerifiedEvidenceEnvelope) error {
	if err := validateBuilderEvidenceCommonScope(authority, envelope); err != nil {
		return err
	}
	taskKey := types.NewTaskKey(authority.core.TaskId)
	scope := envelope.Scope
	switch scope.Kind {
	case bus.KindOrderBroadcast:
		return k.validateBuilderOrderBroadcast(authority.core, envelope)
	case bus.KindWorkerHandraise:
		return k.validateBuilderCandidateActor(ctx, taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK, scope.PayloadActor)
	case bus.KindWorkerAssignmentNotify, bus.KindOutputAvailable:
		return k.validateBuilderWorkerActor(ctx, taskKey, scope.PayloadActor)
	case bus.KindOpenVerify:
		// A round the Task never opened is a missing stage prerequisite, not a
		// contradiction of the Task's identity: round 1 is the original
		// verification, while the current public V1 surface has no authority behind
		// a challenge round. §5.5 calls exactly this case a
		// "premature OPEN_VERIFY", which is what WRONG_STAGE is for.
		if scope.VerifyRound == nil || *scope.VerifyRound != types.VerifyRoundV1 {
			return fmt.Errorf("%w: OPEN_VERIFY verify_round", errBuilderEvidenceWrongStage)
		}
		if err := k.validateBuilderWorkerActor(ctx, taskKey, scope.PayloadActor); err != nil {
			return err
		}
		receipt, err := k.InferReceipt.Get(ctx, taskKey)
		if errors.Is(err, collections.ErrNotFound) {
			return fmt.Errorf("%w: OPEN_VERIFY requires an accepted InferReceipt", errBuilderEvidenceWrongStage)
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(receipt.TaskId, authority.core.TaskId) || receipt.WinnerWorker == "" {
			return fmt.Errorf("OPEN_VERIFY InferReceipt authority is inconsistent")
		}
		if scope.PayloadActor == nil {
			return fmt.Errorf("%w: OPEN_VERIFY Worker actor", errBuilderEvidenceScopeMismatch)
		}
		actor, err := k.canonicalBusOperator(*scope.PayloadActor)
		if err != nil || actor != receipt.WinnerWorker {
			return fmt.Errorf("%w: OPEN_VERIFY InferReceipt Worker", errBuilderEvidenceScopeMismatch)
		}
		return nil
	case bus.KindVerifierHandraise:
		if scope.VerifyRound == nil || *scope.VerifyRound != types.VerifyRoundV1 {
			return fmt.Errorf("%w: VERIFIER_HANDRAISE verify_round", errBuilderEvidenceWrongStage)
		}
		return k.validateBuilderCandidateActor(ctx, taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY, scope.PayloadActor)
	case bus.KindVerifierAssignmentNotify:
		return k.validateBuilderVerifierAssignment(ctx, taskKey, scope, false)
	case bus.KindVerifyResult:
		return k.validateBuilderVerifierAssignment(ctx, taskKey, scope, true)
	default:
		return fmt.Errorf("unsupported Builder evidence Bus kind %d", scope.Kind)
	}
}

func (k Keeper) validateBuilderOrderBroadcast(core types.TaskCoreState, envelope bus.VerifiedEvidenceEnvelope) error {
	if len(envelope.OrderBytes) == 0 {
		return fmt.Errorf("ORDER_BROADCAST did not expose exact TaskOrderV2 bytes")
	}
	var order types.TaskOrderV2
	if err := order.Unmarshal(envelope.OrderBytes); err != nil {
		return fmt.Errorf("decode ORDER_BROADCAST TaskOrderV2: %w", err)
	}
	digest, err := types.TaskOrderHash(order)
	if err != nil {
		return fmt.Errorf("recompute ORDER_BROADCAST task_hash: %w", err)
	}
	actor, err := k.canonicalBusOperator(order.UserAddress)
	if err != nil {
		return err
	}
	if !bytes.Equal(digest[:], core.AcceptedTaskHash) || !bytes.Equal(order.SessionId, core.SessionId) ||
		order.OrderSequence != core.OrderSequence || order.ModelId != core.ModelId || actor != core.UserAddress {
		return fmt.Errorf("%w: ORDER_BROADCAST TaskOrder authority", errBuilderEvidenceScopeMismatch)
	}
	if envelope.Scope.PayloadActor == nil {
		return fmt.Errorf("%w: ORDER_BROADCAST user actor", errBuilderEvidenceScopeMismatch)
	}
	scopeActor, err := k.canonicalBusOperator(*envelope.Scope.PayloadActor)
	if err != nil || scopeActor != core.UserAddress {
		return fmt.Errorf("%w: ORDER_BROADCAST payload actor", errBuilderEvidenceScopeMismatch)
	}
	return nil
}

func (k Keeper) validateBuilderWorkerActor(ctx context.Context, taskKey types.TaskKey, actor *string) error {
	if actor == nil {
		return fmt.Errorf("%w: Worker actor is absent", errBuilderEvidenceScopeMismatch)
	}
	canonical, err := k.canonicalBusOperator(*actor)
	if err != nil {
		return err
	}
	assignment, err := k.TaskAssignment.Get(ctx, taskKey)
	if errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("%w: finalized Worker assignment", errBuilderEvidenceWrongStage)
	}
	if err != nil {
		return err
	}
	if assignment.WinnerWorker != canonical {
		return fmt.Errorf("%w: Worker actor", errBuilderEvidenceScopeMismatch)
	}
	return nil
}

func (k Keeper) validateBuilderCandidateActor(ctx context.Context, taskKey types.TaskKey, stage types.TaskCandidateStage, actor *string) error {
	if actor == nil {
		return fmt.Errorf("%w: candidate actor is absent", errBuilderEvidenceScopeMismatch)
	}
	canonical, err := k.canonicalBusOperator(*actor)
	if err != nil {
		return err
	}
	iter, err := k.TaskCandidateFact.Iterate(ctx, collections.NewSuperPrefixedTripleRange[types.Hash32Key, int32, uint32](taskKey, int32(stage)))
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		fact, err := iter.Value()
		if err != nil {
			return err
		}
		if fact.OperatorAddress == canonical {
			return nil
		}
	}
	return fmt.Errorf("%w: retained candidate fact", errBuilderEvidenceWrongStage)
}

func (k Keeper) validateBuilderVerifierAssignment(ctx context.Context, taskKey types.TaskKey, scope bus.BusActionScopeV2, requireCommit bool) error {
	if scope.VerifyRound == nil || *scope.VerifyRound != types.VerifyRoundV1 {
		return fmt.Errorf("%w: verifier action verify_round", errBuilderEvidenceWrongStage)
	}
	assignment, err := k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, *scope.VerifyRound))
	if errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("%w: finalized Verifier assignment", errBuilderEvidenceWrongStage)
	}
	if err != nil {
		return err
	}
	if !requireCommit {
		return nil
	}
	if scope.PayloadActor == nil {
		return fmt.Errorf("%w: Verifier actor is absent", errBuilderEvidenceScopeMismatch)
	}
	actor, err := k.canonicalBusOperator(*scope.PayloadActor)
	if err != nil {
		return err
	}
	selected := false
	for _, verifier := range assignment.SelectedVerifiers {
		if verifier.OperatorAddress == actor {
			selected = true
			break
		}
	}
	if !selected {
		return fmt.Errorf("%w: selected Verifier", errBuilderEvidenceScopeMismatch)
	}
	commitKey, err := types.DeriveCommitKey(sdkChainID(ctx), taskKey, *scope.VerifyRound, actor)
	if err != nil {
		return err
	}
	hasCommit, err := k.CommitState.Has(ctx, types.NewCommitKey(commitKey[:]))
	if err != nil {
		return err
	}
	if !hasCommit {
		return fmt.Errorf("%w: accepted Verifier commit", errBuilderEvidenceWrongStage)
	}
	return nil
}

func (k Keeper) validateInvalidBuilderProtocolFault(ctx context.Context, authority builderEvidenceTaskAuthority, envelope bus.VerifiedEvidenceEnvelope, violation types.BuilderProtocolViolation) error {
	scopeErr := validateBuilderEvidenceCommonScope(authority, envelope)
	switch violation {
	case types.BuilderProtocolViolation_BUILDER_PROTOCOL_VIOLATION_WRONG_TASK_SCOPE:
		if errors.Is(scopeErr, errBuilderEvidenceScopeMismatch) {
			return nil
		}
		if scopeErr != nil {
			return scopeErr
		}
		return fmt.Errorf("verified envelope does not contain the asserted wrong-task-scope violation")
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
		return fmt.Errorf("verified envelope does not contain the asserted wrong-stage violation")
	default:
		return fmt.Errorf("protocol violation %s requires payload authority not exposed by BusActionScopeV2", violation.String())
	}
}
