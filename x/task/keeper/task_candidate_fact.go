package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// errCandidateNotApplicable marks a per-candidate handraise (Worker or
// Verifier) whose eligibility preconditions do not currently hold. The
// caller (msg_server_worker_handraises.go, msg_server_open_verify.go) skips
// that one candidate rather than failing the whole proposal.
//
// Every check inside freezeWorkerCandidateFact and freezeVerifierCandidateFact
// describes state that can legitimately change between when an operator signs
// its handraise off chain and when the proposal actually lands on chain: the
// profile or its parent model can be frozen or delisted, a capability or
// support declaration can be revoked or lapse, a service key can rotate or be
// revoked, a bond can be spent, jailed or tombstoned, or the handraise can
// simply expire while other candidates in the same batch are still good. A
// Task Builder collects handraises from many independent operators and has
// no way to guarantee all of them are still eligible by the time its
// proposal is included, so one stale or malicious handraise must not be able
// to void every other operator's otherwise-valid candidacy in the same
// batch - exactly the same reasoning the daily support batch already applies
// (see errSupportRefreshNotApplicable).
var errCandidateNotApplicable = errors.New("candidate handraise is not currently eligible")

func (k Keeper) freezeWorkerCandidateFact(
	ctx context.Context,
	pool hubtypes.CandidatePoolSnapshotState,
	handraise types.WorkerHandraiseV1,
	orderValue uint64,
	taskMinStake uint64,
	acceptedHeight uint64,
) (types.TaskCandidateFactState, error) {
	if taskMinStake == 0 {
		return types.TaskCandidateFactState{}, fmt.Errorf("task minimum stake snapshot is unavailable: %w", errCandidateNotApplicable)
	}
	if handraise.SchemaVersion != types.WorkerHandraiseSchemaVersionV1 || handraise.Duty != shared.Duty_DUTY_WORKER {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker handraise schema or duty is invalid: %w", errCandidateNotApplicable)
	}
	if handraise.ChainId != sdk.UnwrapSDKContext(ctx).ChainID() || len(handraise.TaskId) != types.Hash32Len || len(handraise.TaskHash) != types.Hash32Len {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker handraise scope is invalid: %w", errCandidateNotApplicable)
	}
	if !bytes.Equal(handraise.Member.CandidatePoolSnapshotId, pool.SnapshotId) || handraise.Member.Slot >= pool.SlotCapacity {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker handraise pool member scope mismatch: %w", errCandidateNotApplicable)
	}
	if handraise.ExpiryHeight == 0 || acceptedHeight > handraise.ExpiryHeight {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker handraise expired: %w", errCandidateNotApplicable)
	}
	operatorBytes, operator, err := k.canonicalAddress("worker_operator_address", handraise.Member.OperatorAddress)
	if err != nil {
		return types.TaskCandidateFactState{}, fmt.Errorf("%w: %w", errCandidateNotApplicable, err)
	}
	member, binding, err := k.hubKeeper.ResolveCandidatePoolMember(ctx, pool.SnapshotId, handraise.Member.Slot)
	if err != nil {
		return types.TaskCandidateFactState{}, fmt.Errorf("candidate pool member: %w: %w", err, errCandidateNotApplicable)
	}
	if member.SlotVersion != handraise.Member.SlotVersion || member.OperatorAddress != operator ||
		binding.SlotVersion != member.SlotVersion || binding.OperatorAddress != operator || !bytes.Equal(member.BindingHash, binding.BindingHash) {
		return types.TaskCandidateFactState{}, fmt.Errorf("candidate pool member binding mismatch: %w", errCandidateNotApplicable)
	}

	node, ok := k.hubKeeper.GetCortexNode(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(operatorBytes))
	if !ok || node.OperatorAddress != operator || node.ServiceKeyStatus != hubtypes.ServiceKeyStatusActive ||
		node.ServiceAuthorizationNonce != handraise.ServiceAuthorizationNonce {
		return types.TaskCandidateFactState{}, fmt.Errorf("current service binding mismatch: %w", errCandidateNotApplicable)
	}
	modelStatus := k.hubKeeper.GetModelStatus(sdk.UnwrapSDKContext(ctx), handraise.ModelId)
	profile, ok := k.hubKeeper.GetProfileState(sdk.UnwrapSDKContext(ctx), handraise.ModelId, handraise.ProfileVersion)
	if !ok || (profile.Status != hubtypes.ModelStatusRegistered && profile.Status != hubtypes.ModelStatusActive) ||
		!isParentModelOpenForProfile(modelStatus) ||
		k.hubKeeper.IsProfileFrozen(sdk.UnwrapSDKContext(ctx), handraise.ModelId, handraise.ProfileVersion) {
		return types.TaskCandidateFactState{}, fmt.Errorf("profile is not available: %w", errCandidateNotApplicable)
	}
	capability, ok := k.hubKeeper.GetModelCapability(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(operatorBytes), handraise.ModelId)
	if !ok || !capability.InferenceCapability || capability.CapabilityVersion == 0 {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker inference capability is unavailable: %w", errCandidateNotApplicable)
	}
	support, ok := k.hubKeeper.GetModelSupport(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(operatorBytes), handraise.ModelId)
	if !ok || !support.DeclaredSupport || support.SupportVersion == 0 {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker profile support is unavailable: %w", errCandidateNotApplicable)
	}
	bond, ok := k.hubKeeper.GetServiceBond(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(operatorBytes), orderValue)
	if !ok || !hubtypes.IsCandidateEligibleBondStatus(bond.Status) ||
		bond.PendingUnbonding >= bond.EffectiveActiveBond {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker bond is unavailable: %w", errCandidateNotApplicable)
	}
	if !candidateModelSupportAllowed(modelStatus, support, bond) {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker model support is not eligible for the current model status: %w", errCandidateNotApplicable)
	}
	if bond.RequiredTaskLiability == 0 || bond.EffectiveActiveBond < taskMinStake || bond.AvailableBond < bond.RequiredTaskLiability {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker bond cannot cover stake and liability: %w", errCandidateNotApplicable)
	}
	scoring, err := k.hubKeeper.GetRoleScoringSnapshot(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(operatorBytes), hubtypes.ServiceBondRoleWorker)
	if err != nil {
		return types.TaskCandidateFactState{}, fmt.Errorf("%w: %w", errCandidateNotApplicable, err)
	}
	jailFactor := candidateJailFactorPpm(scoring.JailCount)
	if jailFactor == 0 || k.hubKeeper.GetNodeTombstone(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(operatorBytes)) {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker is hard-invalidated: %w", errCandidateNotApplicable)
	}
	weight, err := workerCandidateWeightPpm(bond.EffectiveActiveBond, taskMinStake, types.PerformanceScoreDefaultPpm, jailFactor)
	if err != nil {
		return types.TaskCandidateFactState{}, fmt.Errorf("%w: %w", errCandidateNotApplicable, err)
	}
	digest, err := types.WorkerHandraiseSigningDigest(handraise)
	if err != nil {
		return types.TaskCandidateFactState{}, fmt.Errorf("%w: %w", errCandidateNotApplicable, err)
	}
	if err := k.hubKeeper.VerifyCurrentCortexServiceDigest(ctx, operator, handraise.ServiceSignature, digest[:], acceptedHeight); err != nil {
		return types.TaskCandidateFactState{}, fmt.Errorf("%w: %w", errCandidateNotApplicable, err)
	}
	return types.TaskCandidateFactState{
		SchemaVersion: types.AssignmentCandidateSchemaVersionV1,
		TaskId:        append([]byte(nil), handraise.TaskId...), Stage: types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK,
		Slot: member.Slot, SlotVersion: member.SlotVersion, OperatorAddress: operator, Duty: shared.Duty_DUTY_WORKER,
		ActiveBondSnapshot: shared.NewAmount(bond.EffectiveActiveBond), AvailableBondSnapshot: shared.NewAmount(bond.AvailableBond),
		RequiredTaskLiabilitySnapshot: shared.NewAmount(bond.RequiredTaskLiability), MinStakeSnapshot: shared.NewAmount(taskMinStake),
		PerformanceScoreSnapshotPpm: types.PerformanceScoreDefaultPpm, PerformanceMethodVersion: types.PerformanceMethodRawQ16V1,
		CandidateJailFactorSnapshotPpm: jailFactor, BondVersionSnapshot: bond.BondVersion,
		CapabilityVersionSnapshot: capability.CapabilityVersion, SupportVersionSnapshot: support.SupportVersion,
		CandidateWeight: weight, HandraiseSigningDigest: digest[:],
	}, nil
}

// isParentModelOpenForProfile is the §10.1 step 7 parent-model gate: the parent
// model must be REGISTERED or ACTIVE. FROZEN / EMERGENCY_FROZEN / DELISTED all
// reject new MsgSubmitWorkerHandraises.
func isParentModelOpenForProfile(status hubtypes.ModelProfileStatus) bool {
	return status == hubtypes.ModelStatusRegistered || status == hubtypes.ModelStatusActive
}
