package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Audit standing of a panel Verifier: what the chain remembers about an
// operator's panel duty and what that does to its eligibility for the next
// panel. Three rules, each off while its parameters are zero:
//
//   - Allowance. The value of the tasks an operator was drawn for inside the
//     current allowance window, plus the task at hand, times
//     anchor_verifier_bond_multiple, must not exceed its effective bond. An
//     operator with no history is admitted by its bond alone.
//   - Missed votes. A member that had the whole vote window and cast no vote,
//     or cast UNAVAILABLE while every other member judged the material, missed
//     the round. Misses are counted once per settlement period. Each time the
//     count of periods with a miss inside anchor_verifier_miss_window_blocks
//     reaches another multiple of anchor_verifier_miss_trigger_count the
//     operator sits out one whole settlement period, starting at the next
//     period boundary.
//   - Wrong votes. A member whose PASS or REJECT is the opposite of the final
//     verdict a panel decided is deactivated: it is drawn for no further panel.
//
// A miss or a deactivation touches the Verifier alone. Nothing here is read as
// a statement about the Worker.

func (k Keeper) verifierStanding(ctx context.Context, operator string) (types.VerifierAuditStandingState, []byte, error) {
	raw, err := k.sessionAddressToStore("verifier_operator_address", operator)
	if err != nil {
		return types.VerifierAuditStandingState{}, nil, err
	}
	standing, err := k.VerifierAuditStanding.Get(ctx, raw)
	if err != nil && !errIsNotFound(err) {
		return types.VerifierAuditStandingState{}, nil, err
	}
	if standing.OperatorAddress == "" {
		standing = types.VerifierAuditStandingState{OperatorAddress: operator, AuditedValue: shared.NewAmount(0)}
	}
	return standing, raw, nil
}

// verifierAuditedValue is the value an operator was drawn for inside the
// allowance window that contains height. Windows are consecutive spans of
// window blocks; a standing last touched in an earlier span counts nothing.
func verifierAuditedValue(standing types.VerifierAuditStandingState, window, height uint64) uint64 {
	if window == 0 || standing.UpdatedHeight/window != height/window {
		return 0
	}
	value, err := shared.ParseAmount(standing.AuditedValue)
	if err != nil {
		return 0
	}
	return value
}

// verifierSuspended reports whether the operator sits out the settlement
// period that contains height: the one period before suspended_until_period.
func verifierSuspended(standing types.VerifierAuditStandingState, periodBlocks, height uint64) bool {
	return periodBlocks != 0 && standing.SuspendedUntilPeriod != 0 && height/periodBlocks+1 == standing.SuspendedUntilPeriod
}

// anchorVerifierStandingAllows applies the standing rules to an operator that
// is otherwise eligible for a panel of the task. A standing that cannot be
// read admits nobody.
func (k Keeper) anchorVerifierStandingAllows(ctx context.Context, operator string, orderValue, effectiveBond, height uint64, params types.TaskParamsV1) bool {
	standing, _, err := k.verifierStanding(ctx, operator)
	if err != nil || standing.Deactivated {
		return false
	}
	v := params.Verification
	if verifierSuspended(standing, v.BatchSettlementPeriodBlocks, height) {
		return false
	}
	if v.AnchorVerifierBondMultiple == 0 {
		return true
	}
	audited, overflow := checkedAddUint64(verifierAuditedValue(standing, v.AnchorVerifierAllowanceWindowBlocks, height), orderValue)
	if overflow {
		return false
	}
	required, overflow := checkedMulUint64(audited, uint64(v.AnchorVerifierBondMultiple))
	return !overflow && required <= effectiveBond
}

// noteAnchorPanelDrawn adds the task's value to the allowance use of every
// drawn member.
func (k Keeper) noteAnchorPanelDrawn(ctx context.Context, taskKey types.TaskKey, members []string, height uint64, params types.TaskParamsV1) error {
	window := params.Verification.AnchorVerifierAllowanceWindowBlocks
	if params.Verification.AnchorVerifierBondMultiple == 0 || window == 0 {
		return nil
	}
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return nil
	}
	orderValue, err := shared.ParseAmount(core.OrderValue)
	if err != nil {
		return nil
	}
	for _, member := range members {
		standing, raw, err := k.verifierStanding(ctx, member)
		if err != nil {
			continue
		}
		used, overflow := checkedAddUint64(verifierAuditedValue(standing, window, height), orderValue)
		if overflow {
			used = ^uint64(0)
		}
		standing.AuditedValue = shared.NewAmount(used)
		standing.UpdatedHeight = height
		if err := k.VerifierAuditStanding.Set(ctx, raw, standing); err != nil {
			return err
		}
	}
	return nil
}

// noteAnchorPanelMisses records the misses of a round that just closed. A
// member that cast no vote is counted only when the round ran to its vote
// deadline: a round decided early by the other members did not give it the
// whole window.
func (k Keeper) noteAnchorPanelMisses(ctx context.Context, round types.AnchorPanelRoundState, height uint64, params types.TaskParamsV1) error {
	v := params.Verification
	if v.BatchSettlementPeriodBlocks == 0 || v.AnchorVerifierMissTriggerCount == 0 || v.AnchorVerifierMissWindowBlocks == 0 {
		return nil
	}
	ranOut := round.VoteDeadlineHeight != 0 && height > round.VoteDeadlineHeight
	period := height / v.BatchSettlementPeriodBlocks
	for _, member := range anchorPanelMissedMembers(round) {
		if _, voted := anchorPanelVoteOf(round, member); !voted && !ranOut {
			continue
		}
		standing, raw, err := k.verifierStanding(ctx, member)
		if err != nil {
			continue
		}
		kept := make([]uint64, 0, len(standing.MissedPeriods)+1)
		listed := false
		for _, missed := range standing.MissedPeriods {
			if missed > period || (period-missed)*v.BatchSettlementPeriodBlocks >= v.AnchorVerifierMissWindowBlocks {
				continue
			}
			kept = append(kept, missed)
			listed = listed || missed == period
		}
		if !listed {
			kept = append(kept, period)
		}
		standing.MissedPeriods = kept
		triggers := uint32(len(kept)) / v.AnchorVerifierMissTriggerCount
		if !listed && triggers > standing.MissTriggersInWindow {
			// One whole period out, from the next period boundary.
			standing.SuspendedUntilPeriod = period + 2
		}
		standing.MissTriggersInWindow = triggers
		if standing.UpdatedHeight == 0 {
			standing.UpdatedHeight = height
		}
		if err := k.VerifierAuditStanding.Set(ctx, raw, standing); err != nil {
			return err
		}
	}
	return nil
}

// noteAnchorPanelWrongVotes deactivates the members whose vote is the opposite
// of a final verdict that a panel decided. A verdict that comes from anything
// else (a missed deadline, an expired late window, unavailable material, no
// conclusion) makes nobody wrong. It is safe to repeat.
func (k Keeper) noteAnchorPanelWrongVotes(ctx context.Context, taskKey types.TaskKey, state types.AnchorAuditState) error {
	if state.ResultHeight == 0 || !isAnchorDecision(state.TaskVerdict) {
		return nil
	}
	switch state.ResultSource {
	case types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VERIFIER_PANEL, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_REAUDIT:
	default:
		return nil
	}
	wrong := anchorPanelWrongVoters(state.PanelRounds, state.TaskVerdict)
	if len(wrong) == 0 {
		return nil
	}
	params, err := k.ParamsForTask(ctx, taskKey)
	if err != nil || params.Verification.AnchorVerifierWrongVoteSlashBps == 0 {
		return nil
	}
	for _, member := range wrong {
		standing, raw, err := k.verifierStanding(ctx, member)
		if err != nil || standing.Deactivated {
			continue
		}
		standing.Deactivated = true
		if standing.UpdatedHeight == 0 {
			standing.UpdatedHeight = state.ResultHeight
		}
		if err := k.VerifierAuditStanding.Set(ctx, raw, standing); err != nil {
			return err
		}
	}
	return nil
}

// VerifierAuditStanding returns an operator's audit standing and the value its
// current bond lets it audit per allowance window.
func (q queryServer) VerifierAuditStanding(ctx context.Context, req *types.QueryVerifierAuditStandingRequest) (*types.QueryVerifierAuditStandingResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "verifier audit standing request is required")
	}
	rawOperator, operator, err := q.k.canonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	standing, _, err := q.k.verifierStanding(ctx, operator)
	if err != nil {
		return nil, err
	}
	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	// What is reported is the use inside the current window.
	standing.AuditedValue = shared.NewAmount(verifierAuditedValue(standing, params.Verification.AnchorVerifierAllowanceWindowBlocks, height))
	out := &types.QueryVerifierAuditStandingResponse{Standing: standing, AllowanceLimit: shared.NewAmount(0)}
	if multiple := uint64(params.Verification.AnchorVerifierBondMultiple); multiple != 0 {
		if bond, found := q.k.hubKeeper.GetServiceBond(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(rawOperator), 1); found {
			out.AllowanceLimit = shared.NewAmount(bond.EffectiveActiveBond / multiple)
		}
	}
	return out, nil
}
