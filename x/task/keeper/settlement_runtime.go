package keeper

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// settlementInputs is the authoritative state one settlement is derived from.
// It is loaded once and then only read, so BuildSettlementFacts and
// BuildSettlementPlan cannot disagree about which rows they saw.
type settlementInputs struct {
	Core                  types.TaskCoreState
	Assignment            types.TaskAssignmentState
	BuilderSelection      types.TaskBuilderSelectionState
	Round1Assignment      types.VerifierAssignmentState
	Budget                types.TaskBudgetState
	Infer                 types.InferReceiptState
	Summary               types.TaskRoundSummaryState
	Round1                types.VerificationRoundState
	Round1Cluster         []types.ConsensusClusterMemberV1
	EffectiveRound        uint32
	EffectiveResult       roundCloseResult
	DutyBuilder           string
	CutoffHeight          uint64
	DisqualifiedOperators []string
	FaultSummaryHash      []byte
	FailureRows           []types.TaskFailureClassState
	// round1SelectedVerifierCount is the frozen denominator for Task-funded
	// verifier payouts. Challenge-round Verifiers are paid from round funding.
	round1SelectedVerifierCount uint32
}

// BuildSettlementFacts derives every non-amount settlement field from
// authoritative state (the API contract step 5). It is a pure function:
// Query preview, the Tx path, the deadline runner and Genesis validation all call
// this one implementation, and success here authorizes nothing — no state write
// and no fund movement.
func (k Keeper) BuildSettlementFacts(
	ctx context.Context,
	inputs settlementInputs,
	settlementHeight uint64,
) (types.SettlementFactsV1, []byte, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if inputs.CutoffHeight == 0 || settlementHeight < inputs.CutoffHeight {
		return types.SettlementFactsV1{}, nil, fmt.Errorf("settlement heights are invalid")
	}
	paidRoles, err := k.derivePaidRoles(inputs)
	if err != nil {
		return types.SettlementFactsV1{}, nil, err
	}
	facts := types.SettlementFactsV1{
		TaskId:                        append([]byte(nil), inputs.Core.TaskId...),
		VerifyRound:                   inputs.EffectiveRound,
		SettlementHeight:              settlementHeight,
		SettlementFactsCutoffHeight:   inputs.CutoffHeight,
		SettlementDutyBuilderOperator: inputs.DutyBuilder,
		ConsensusClusterHash:          append([]byte(nil), inputs.EffectiveResult.ConsensusClusterHash...),
		Verdict:                       inputs.EffectiveResult.Verdict,
		FailureClass:                  inputs.EffectiveResult.FailureClass,
		FaultSummaryHash:              append([]byte(nil), inputs.FaultSummaryHash...),
		PaidRoles:                     paidRoles,
		InferReceiptRef:               append([]byte(nil), inputs.Infer.InferReceiptHash...),
		ResultReceiptRefsHash:         append([]byte(nil), inputs.EffectiveResult.ResultReceiptRefsHash...),
		ChallengeCloseHeight:          inputs.Summary.GetChallengeCloseHeight(),
	}
	settlementID, err := types.TaskSettlementID(
		sdkCtx.ChainID(), inputs.Core.TaskId, inputs.Core.AcceptedTaskHash,
		inputs.EffectiveRound, inputs.CutoffHeight, settlementHeight,
	)
	if err != nil {
		return types.SettlementFactsV1{}, nil, err
	}
	return facts, settlementID[:], nil
}

// derivePaidRoles builds the (duty, operator) vector the facts commit to. §10.10a
// requires the tuple to be unique and sorted by (duty, operator bytes); only the
// worker that actually produced the billed receipt and the round-1 cluster
// members are paid roles.
func (k Keeper) derivePaidRoles(inputs settlementInputs) ([]types.PaidRoleV1, error) {
	roles := make([]types.PaidRoleV1, 0, len(inputs.Round1Cluster)+1)
	if inputs.EffectiveResult.Verdict == types.TaskVerdict_TASK_VERDICT_PASS &&
		inputs.Assignment.WinnerWorker != "" && !inputs.isDisqualified(inputs.Assignment.WinnerWorker) {
		roles = append(roles, types.PaidRoleV1{
			Duty: shared.DutyWorker, OperatorAddress: inputs.Assignment.WinnerWorker,
		})
	}
	for _, member := range inputs.Round1Cluster {
		if inputs.isDisqualified(member.VerifierOperatorAddress) {
			continue
		}
		roles = append(roles, types.PaidRoleV1{
			Duty: shared.DutyVerifier, OperatorAddress: member.VerifierOperatorAddress,
		})
	}
	keys := make([][]byte, len(roles))
	for i, role := range roles {
		operator, err := types.CanonicalOperatorAddressBytes("paid_role_operator", role.OperatorAddress)
		if err != nil {
			return nil, err
		}
		keys[i] = operator
	}
	order := make([]int, len(roles))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		left, right := order[a], order[b]
		if roles[left].Duty != roles[right].Duty {
			return roles[left].Duty < roles[right].Duty
		}
		return bytes.Compare(keys[left], keys[right]) < 0
	})
	sorted := make([]types.PaidRoleV1, 0, len(roles))
	for i, index := range order {
		if i > 0 {
			previous := order[i-1]
			if roles[previous].Duty == roles[index].Duty && bytes.Equal(keys[previous], keys[index]) {
				return nil, fmt.Errorf("paid role (%s) is not unique", roles[index].OperatorAddress)
			}
		}
		sorted = append(sorted, roles[index])
	}
	return sorted, nil
}

// BuildSettlementPlan applies the single frozen fee rule of §10.10a. Every step
// is checked and the closing identity
//
//	refund = apply_start_reserved - sum(net) - sum(maintenance) - sum(gas)
//
// must hold exactly with no underflow, so a plan that does not conserve the
// escrow cannot be built at all. The left-hand side is TaskBudgetState's
// reserved_amount at apply start, not original_reserved_amount: gas receipts only
// draw down tx_fee_reserve_remaining (§10.10b), so the two normally agree, but a
// task that already disbursed part of its escrow must settle against what is
// actually still held.
func (k Keeper) BuildSettlementPlan(
	ctx context.Context,
	inputs settlementInputs,
	facts types.SettlementFactsV1,
	settlementID []byte,
	reimbursements []types.TaskGasReimbursementV1,
) (types.SettlementPlanV1, types.TaskSettlementBillV1, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if inputs.Budget.FeeRuleVersion == 0 {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("task fee rule version is not frozen")
	}
	priceBid, err := shared.ParseAmount(inputs.Budget.PriceBid)
	if err != nil {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("price_bid: %w", err)
	}
	originalReserved, err := shared.ParseAmount(inputs.Budget.OriginalReservedAmount)
	if err != nil {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("original_reserved_amount: %w", err)
	}
	applyStartReserved, err := shared.ParseAmount(inputs.Budget.ReservedAmount)
	if err != nil {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("reserved_amount: %w", err)
	}
	if applyStartReserved > originalReserved {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("reserved_amount exceeds the original reservation")
	}

	// n is the count frozen by the threshold cluster; an unsettled or
	// non-consensus round bills nothing.
	n := inputs.EffectiveResult.GeneratedTokenCount
	if inputs.Budget.MaxOutputTokens == 0 || n > inputs.Budget.MaxOutputTokens {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("generated_token_count exceeds the frozen Task max_output_tokens")
	}
	workerGross, ok := mulDivFloorRound(n, priceBid, 1_000_000)
	if !ok {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("worker gross overflows")
	}
	workerPayableGross := uint64(0)
	if facts.Verdict == types.TaskVerdict_TASK_VERDICT_PASS && !inputs.isDisqualified(inputs.Assignment.WinnerWorker) {
		workerPayableGross = workerGross
	}
	verifyTotalGross := uint64(0)
	if len(inputs.Round1Cluster) > 0 {
		verifyTotalGross, ok = mulDivFloorRound(workerGross, uint64(inputs.Budget.VerifyRatioBpsSnapshot), uint64(types.BasisPointsMaximum))
		if !ok {
			return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("verifier total gross overflows")
		}
	}
	selectedCount := uint64(inputs.EffectiveResultSelectedCount())
	verifierSlotGross := uint64(0)
	if selectedCount > 0 {
		verifierSlotGross = verifyTotalGross / selectedCount
	}

	maintenanceBps := uint64(inputs.Budget.MaintenanceRateBpsSnapshot)
	workerMaintenance, ok := mulDivFloorRound(workerPayableGross, maintenanceBps, uint64(types.BasisPointsMaximum))
	if !ok {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("worker maintenance overflows")
	}
	// §10.10c requires every term checked. maintenance <= gross holds only while
	// maintenance_rate_bps_snapshot <= 10000 (the API contract:4995); an
	// out-of-range
	// snapshot would wrap this subtraction into a near-2^64 net and hand the
	// plan an amount the escrow can never cover.
	if workerMaintenance > workerPayableGross {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{},
			fmt.Errorf("worker maintenance %d exceeds its gross %d", workerMaintenance, workerPayableGross)
	}
	workerNet := workerPayableGross - workerMaintenance

	totalNet, totalMaintenance := workerNet, workerMaintenance
	payouts := make([]types.VerifierPayoutV1, 0, len(inputs.Round1Cluster))
	for _, member := range inputs.Round1Cluster {
		if inputs.isDisqualified(member.VerifierOperatorAddress) {
			continue
		}
		maintenance, ok := mulDivFloorRound(verifierSlotGross, maintenanceBps, uint64(types.BasisPointsMaximum))
		if !ok {
			return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("verifier maintenance overflows")
		}
		if maintenance > verifierSlotGross {
			return types.SettlementPlanV1{}, types.TaskSettlementBillV1{},
				fmt.Errorf("verifier maintenance %d exceeds its slot gross %d", maintenance, verifierSlotGross)
		}
		net := verifierSlotGross - maintenance
		totalNet, ok = checkedAddU64(totalNet, net)
		if !ok {
			return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("settlement net total overflows")
		}
		totalMaintenance, ok = checkedAddU64(totalMaintenance, maintenance)
		if !ok {
			return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("settlement maintenance total overflows")
		}
		payouts = append(payouts, types.VerifierPayoutV1{
			OperatorAddress: member.VerifierOperatorAddress, SelectedVerifierIndex: member.SelectedVerifierIndex,
			Gross:       shared.NewAmount(verifierSlotGross),
			Maintenance: shared.NewAmount(maintenance),
			Net:         shared.NewAmount(net),
		})
	}

	totalGas := uint64(0)
	for i, item := range reimbursements {
		amount, err := shared.ParseAmount(item.ReimbursedAmount)
		if err != nil {
			return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("gas reimbursement %d: %w", i, err)
		}
		totalGas, ok = checkedAddU64(totalGas, amount)
		if !ok {
			return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("gas reimbursement total overflows")
		}
	}

	spent, ok := checkedAddU64(totalNet, totalMaintenance)
	if !ok {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("settlement spend overflows")
	}
	spent, ok = checkedAddU64(spent, totalGas)
	if !ok {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("settlement spend overflows")
	}
	if spent > applyStartReserved {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("settlement spend exceeds the reserved escrow")
	}
	refund := applyStartReserved - spent

	bill := types.TaskSettlementBillV1{
		WorkerOperatorAddress: inputs.Assignment.WinnerWorker,
		InferReceiptRef:       append([]byte(nil), inputs.Infer.InferReceiptHash...),
		FeeRuleVersion:        inputs.Budget.FeeRuleVersion,
		GeneratedTokenCount:   n,
		WorkUnit:              n,
	}
	billHash, err := types.SettlementBillHash(bill)
	if err != nil {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, err
	}
	gasHash, err := types.GasReimbursementsHash(sdkCtx.ChainID(), inputs.Core.TaskId, reimbursements)
	if err != nil {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, err
	}
	factsHash, err := types.SettlementFactsHash(sdkCtx.ChainID(), settlementID, facts)
	if err != nil {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, err
	}
	summaryHash := inputs.Summary.GetTaskRoundSummaryHash()
	if len(summaryHash) != types.Hash32Len {
		return types.SettlementPlanV1{}, types.TaskSettlementBillV1{}, fmt.Errorf("task round summary hash is not frozen")
	}

	plan := types.SettlementPlanV1{
		TaskId: append([]byte(nil), inputs.Core.TaskId...), SettlementId: append([]byte(nil), settlementID...),
		FeeRuleVersion: inputs.Budget.FeeRuleVersion, EffectiveVerifyRound: inputs.EffectiveRound,
		GeneratedTokenCount: n, WorkUnit: n,
		WorkerGross: shared.NewAmount(workerPayableGross), WorkerMaintenance: shared.NewAmount(workerMaintenance),
		WorkerNet: shared.NewAmount(workerNet), VerifierSlotGross: shared.NewAmount(verifierSlotGross),
		VerifierPayouts: payouts, GasReimbursements: reimbursements, GasReimbursementsHash: gasHash[:],
		MaintenanceFee: shared.NewAmount(totalMaintenance), RefundAmount: shared.NewAmount(refund),
		OriginalReservedAmount: inputs.Budget.OriginalReservedAmount,
		SettlementFactsHash:    factsHash[:], TaskRoundSummaryHash: append([]byte(nil), summaryHash...),
		SettlementBillHash: billHash[:],
	}
	return plan, bill, nil
}

// EffectiveResultSelectedCount is the frozen slot count the verifier share is
// divided by. §10.10a divides the round's verify total by the *selected* count,
// not by the cluster size, so absent and minority slots forfeit their share and
// the remainder is never redistributed.
func (i settlementInputs) EffectiveResultSelectedCount() uint32 {
	return i.round1SelectedVerifierCount
}

func (i settlementInputs) isDisqualified(operator string) bool {
	for _, candidate := range i.DisqualifiedOperators {
		if candidate == operator {
			return true
		}
	}
	return false
}

func checkedAddU64(left, right uint64) (uint64, bool) {
	sum := left + right
	if sum < left {
		return 0, false
	}
	return sum, true
}
