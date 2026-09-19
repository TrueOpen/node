package keeper

import (
	"bytes"
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) freezeWorkerCandidateFact(
	ctx context.Context,
	pool hubtypes.CandidatePoolSnapshotState,
	handraise types.WorkerHandraiseV1,
	orderValue uint64,
	acceptedHeight uint64,
) (types.TaskCandidateFactState, error) {
	if handraise.SchemaVersion != types.WorkerHandraiseSchemaVersionV1 || handraise.Duty != shared.Duty_DUTY_WORKER {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker handraise schema or duty is invalid")
	}
	if handraise.ChainId != sdk.UnwrapSDKContext(ctx).ChainID() || len(handraise.TaskId) != types.Hash32Len || len(handraise.TaskHash) != types.Hash32Len {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker handraise scope is invalid")
	}
	if !bytes.Equal(handraise.Member.CandidatePoolSnapshotId, pool.SnapshotId) || handraise.Member.Slot >= pool.SlotCapacity {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker handraise pool member scope mismatch")
	}
	if handraise.ExpiryHeight == 0 || acceptedHeight > handraise.ExpiryHeight {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker handraise expired")
	}
	operatorBytes, operator, err := k.canonicalAddress("worker_operator_address", handraise.Member.OperatorAddress)
	if err != nil {
		return types.TaskCandidateFactState{}, err
	}
	member, binding, err := k.hubKeeper.ResolveCandidatePoolMember(ctx, pool.SnapshotId, handraise.Member.Slot)
	if err != nil {
		return types.TaskCandidateFactState{}, fmt.Errorf("candidate pool member: %w", err)
	}
	if member.SlotVersion != handraise.Member.SlotVersion || member.OperatorAddress != operator ||
		binding.SlotVersion != member.SlotVersion || binding.OperatorAddress != operator || !bytes.Equal(member.BindingHash, binding.BindingHash) {
		return types.TaskCandidateFactState{}, fmt.Errorf("candidate pool member binding mismatch")
	}

	node, ok := k.hubKeeper.GetCortexNode(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(operatorBytes))
	if !ok || node.OperatorAddress != operator || node.ServiceKeyStatus != hubtypes.ServiceKeyStatusActive ||
		node.ServiceAuthorizationNonce != handraise.ServiceAuthorizationNonce {
		return types.TaskCandidateFactState{}, fmt.Errorf("current service binding mismatch")
	}
	profile, ok := k.hubKeeper.GetProfileState(sdk.UnwrapSDKContext(ctx), handraise.ModelId, handraise.ProfileVersion)
	if !ok || (profile.Status != hubtypes.ModelStatusRegistered && profile.Status != hubtypes.ModelStatusActive) ||
		!isParentModelOpenForProfile(k.hubKeeper.GetModelStatus(sdk.UnwrapSDKContext(ctx), handraise.ModelId)) ||
		k.hubKeeper.IsProfileFrozen(sdk.UnwrapSDKContext(ctx), handraise.ModelId, handraise.ProfileVersion) {
		return types.TaskCandidateFactState{}, fmt.Errorf("profile is not available")
	}
	capability, ok := k.hubKeeper.GetProfileCapability(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(operatorBytes), handraise.ModelId, handraise.ProfileVersion)
	if !ok || !capability.InferenceCapability || capability.CapabilityVersion == 0 {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker inference capability is unavailable")
	}
	support, ok := k.hubKeeper.GetModelSupport(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(operatorBytes), handraise.ModelId, handraise.ProfileVersion)
	if !ok || !support.DeclaredSupport || support.SupportVersion == 0 {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker profile support is unavailable")
	}
	bond, ok := k.hubKeeper.GetServiceBond(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(operatorBytes), orderValue)
	if !ok || !hubtypes.IsCandidateEligibleBondStatus(bond.Status) ||
		bond.PendingUnbonding >= bond.EffectiveActiveBond {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker bond is unavailable")
	}
	if profile.Status == hubtypes.ModelStatusActive && !support.SupportActive && bond.Status != hubtypes.ServiceBondStatusJailed {
		return types.TaskCandidateFactState{}, fmt.Errorf("active worker profile requires active support unless the operator is jailed")
	}
	if bond.RequiredTaskLiability == 0 || bond.EffectiveActiveBond < profile.MinStake || bond.AvailableBond < bond.RequiredTaskLiability {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker bond cannot cover stake and liability")
	}
	scoring, err := k.hubKeeper.GetRoleScoringSnapshot(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(operatorBytes), hubtypes.ServiceBondRoleWorker)
	if err != nil {
		return types.TaskCandidateFactState{}, err
	}
	jailFactor := candidateJailFactorPpm(scoring.JailCount)
	if jailFactor == 0 || k.hubKeeper.GetNodeTombstone(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(operatorBytes)) {
		return types.TaskCandidateFactState{}, fmt.Errorf("worker is hard-invalidated")
	}
	weight, err := workerCandidateWeightPpm(bond.EffectiveActiveBond, profile.MinStake, types.PerformanceScoreDefaultPpm, jailFactor)
	if err != nil {
		return types.TaskCandidateFactState{}, err
	}
	digest, err := types.WorkerHandraiseSigningDigest(handraise)
	if err != nil {
		return types.TaskCandidateFactState{}, err
	}
	if err := k.hubKeeper.VerifyCurrentCortexServiceDigest(ctx, operator, handraise.ServiceSignature, digest[:], acceptedHeight); err != nil {
		return types.TaskCandidateFactState{}, err
	}
	return types.TaskCandidateFactState{
		SchemaVersion: types.AssignmentCandidateSchemaVersionV1,
		TaskId:        append([]byte(nil), handraise.TaskId...), Stage: types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK,
		Slot: member.Slot, SlotVersion: member.SlotVersion, OperatorAddress: operator, Duty: shared.Duty_DUTY_WORKER,
		ActiveBondSnapshot: shared.NewAmount(bond.EffectiveActiveBond), AvailableBondSnapshot: shared.NewAmount(bond.AvailableBond),
		RequiredTaskLiabilitySnapshot: shared.NewAmount(bond.RequiredTaskLiability), MinStakeSnapshot: shared.NewAmount(profile.MinStake),
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
