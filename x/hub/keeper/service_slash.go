package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// This file holds the single service-bond slash entry point required by
// keeper_api_contract.md §10.0c ("the slash debit function must be a single
// unified internal function; every Cortex Node Worker/Verifier duty fault,
// successful challenge and objective forgery calls the same sequence").
//
// Before this consolidation the repository had two mutually unaware debit
// paths: SlashServiceBond capped itself at `active_bond - reserved_liability`
// (so an operator whose bond was fully committed to open task reservations
// could not be slashed at all for a normal fault) and
// closeTaskLiabilityReservation capped itself at `reserved_amount` against
// `active_bond`. Neither ever reached the unbonding queue or claimable
// earnings, and `unfilled_amount` had no writer at all.
//
// The waterfall below implements §10.0c steps 0..6 verbatim:
//
//	0. pending task earnings of the bound task (skipped for a plain fault)
//	1. ServiceBondState.active_bond          <- reserved_liability is NOT subtracted
//	2. UnbondingState rows in (mature_height, unbonding_id) order, at most
//	   params.Service.MaxOpenUnbondingEntriesPerOperator visited rows
//	3. EarningsState.claimable_amount
//	4. residual is recorded as unfilled_amount and the waterfall stops; it is
//	   explicitly NOT a protocol debt and is not covered by the Treasury
//	5. DeactivateModelSupport(SLASH_BELOW_MIN) for every profile whose
//	   min_stake is no longer met
//	6. bond_version += 1
//
// Step 1 deliberately ignores `reserved_liability`: reserved liability is the
// gate for unstake (§B.1.3 "Unstake may only use unreserved bond"), never the gate for
// slash. That single change is what makes a fully-reserved operator slashable.
// A consequence is that `reserved_liability` may transiently exceed
// `active_bond` after a slash while reservations are still open; that is the
// case types.AvailableBond reports as unavailable rather than wrapping.

// ApplyServiceSlashRequest is the only accepted input shape for a service-bond
// slash. Both registered V1 sources are task-bound, so TaskID is a required
// Hash32 and the waterfall first consumes that task's pending earnings before a
// responsible operator can claim them.
//
// Duty is attribution only: the bond is not split per duty (§6.4 "the deposit is
// not lock-split by profile, capability or task duty"), so the waterfall never
// reads it. It is
// carried here so the caller's event 52 payload and this request cannot drift.
type ApplyServiceSlashRequest struct {
	OperatorAddress string
	Duty            shared.Duty
	TaskID          []byte
	Requested       uint64
	Height          uint64

	// SummaryID is the immutable Hash32 replay handle exposed by the source
	// receipt. SourceKind/SourceID/EffectIndex form the authoritative store key.
	SummaryID   []byte
	SourceKind  types.SlashSourceKind
	SourceID    []byte
	EffectIndex uint64
	Destination types.SlashDestination
}

// applyServiceSlashMetadata is passed through liability closure so the only
// ledger debit entry point can also own replay identity and custody routing.
type applyServiceSlashMetadata struct {
	SummaryID   []byte
	SourceKind  types.SlashSourceKind
	SourceID    []byte
	EffectIndex uint64
	Destination types.SlashDestination
}

func newRoleFaultSlashMetadata(fault types.RoleFaultState) *applyServiceSlashMetadata {
	return &applyServiceSlashMetadata{
		SummaryID:   append([]byte(nil), fault.FaultId...),
		SourceKind:  types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT,
		SourceID:    append([]byte(nil), fault.FaultId...),
		Destination: types.SlashDestination_SLASH_DESTINATION_TREASURY,
	}
}

// ApplyServiceSlashResult reports the audited outcome of one waterfall run.
//
// Applied/Unfilled/BondVersion are the contract-visible values (§5.11 code 52
// `role_slashed` carries applied_amount / unfilled_amount / bond_version).
//
// The per-layer fields are persisted in SlashSummary and drive the mandatory
// custody transfer: layers 1 and 2 leave types.ServiceBondModuleName, while
// layers 0 and 3 leave types.RewardsModuleName.
type ApplyServiceSlashResult struct {
	Applied     uint64
	Unfilled    uint64
	BondVersion uint64

	PendingEarningsApplied uint64
	ActiveBondApplied      uint64
	UnbondingApplied       uint64
	ClaimableApplied       uint64
	UnbondingRowsVisited   uint32
}

// ServiceBondModuleDebit is the part of Applied that left the service bond
// module account (layers 1 and 2). It cannot overflow because both terms are
// summands of the already checked Applied total.
func (r ApplyServiceSlashResult) ServiceBondModuleDebit() uint64 {
	return r.ActiveBondApplied + r.UnbondingApplied
}

// RewardsModuleDebit is the part of Applied that left the rewards module
// account (layers 0 and 3).
func (r ApplyServiceSlashResult) RewardsModuleDebit() uint64 {
	return r.PendingEarningsApplied + r.ClaimableApplied
}

// ApplyServiceSlash is the atomic exported boundary. Internal fault/challenge
// flows that already own a larger cache transaction call
// applyServiceSlashInCache directly.
func (k Keeper) ApplyServiceSlash(ctx context.Context, req ApplyServiceSlashRequest) (ApplyServiceSlashResult, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	result, err := k.applyServiceSlashInCache(sdk.WrapSDKContext(cacheCtx), req)
	if err != nil {
		return ApplyServiceSlashResult{}, err
	}
	write()
	return result, nil
}

// applyServiceSlashInCache is the single service-bond debit kernel. The state
// waterfall, SlashSummary receipt, bank transfers and (for a Treasury
// destination) TreasuryState inflow all use the supplied cache context. No
// ledger-only variant exists.
func (k Keeper) applyServiceSlashInCache(ctx context.Context, req ApplyServiceSlashRequest) (ApplyServiceSlashResult, error) {
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if req.Height == 0 {
		return ApplyServiceSlashResult{}, fmt.Errorf("service slash height must be > 0")
	}
	if err := validateServiceSlashRequest(req); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	sourceID, err := types.SlashSummarySourceKey(req.SourceKind, req.SourceID)
	if err != nil {
		return ApplyServiceSlashResult{}, err
	}
	summaryKey := types.NewSlashSummaryKey(req.SourceKind, sourceID, req.EffectIndex)
	if existing, getErr := k.SlashSummary.Get(ctx, summaryKey); getErr == nil {
		return serviceSlashReplayResult(existing, req, operatorAddress)
	} else if !errors.Is(getErr, collections.ErrNotFound) {
		return ApplyServiceSlashResult{}, getErr
	}
	bond, err := k.GetServiceBondState(ctx, operatorAddress)
	if err != nil {
		return ApplyServiceSlashResult{}, err
	}
	result := ApplyServiceSlashResult{BondVersion: bond.BondVersion}
	remaining := req.Requested

	// Ruling 18/9: normalize effective_active_bond before the waterfall touches
	// active_bond. Layer 1 lowers active_bond, and the previous inline
	// one-directional clamp could only lower effective_active_bond, so a slash at
	// or after effective_bond_epoch left effective < active and broke the epoch
	// invariant (active=1000, effective=600, epoch=10, currentEpoch=10, slash 200
	// -> active=800, effective=600). ValidateServiceBondEpochConsistency then
	// rejects the exported genesis even though the chain kept running.
	epochLength, err := k.epochLengthBlocks(ctx)
	if err != nil {
		return ApplyServiceSlashResult{}, err
	}
	currentEpoch := epochForHeight(req.Height, epochLength)
	types.NormalizeEffectiveActiveBond(&bond, currentEpoch)

	// Layer 1: active_bond. reserved_liability is intentionally not subtracted.
	if remaining > 0 && bond.ActiveBond > 0 {
		applied := minUint64(remaining, bond.ActiveBond)
		bond.ActiveBond -= applied
		// Re-run the single normalization point against the post-debit amount:
		// the pre-waterfall call established the epoch invariant against the old
		// active_bond, this one re-establishes both invariants against the new one.
		types.NormalizeEffectiveActiveBond(&bond, currentEpoch)
		result.ActiveBondApplied = applied
		remaining -= applied
	}

	// Layer 2: bounded unbonding queue in (mature_height, unbonding_id) order.
	if remaining > 0 {
		applied, visited, err := k.slashServiceUnbondingQueue(
			ctx, sdk.UnwrapSDKContext(ctx).ChainID(), req.Height, &bond, operatorAddress, remaining,
		)
		if err != nil {
			return ApplyServiceSlashResult{}, err
		}
		result.UnbondingApplied = applied
		result.UnbondingRowsVisited = visited
		remaining -= applied
	}

	// Layer 3: claimable earnings.
	if remaining > 0 {
		applied, err := k.slashClaimableEarnings(ctx, operatorAddress, remaining, req.Height)
		if err != nil {
			return ApplyServiceSlashResult{}, err
		}
		result.ClaimableApplied = applied
		remaining -= applied
	}

	// Layer 4: audit the residual and stop. No protocol debt, no Treasury cover.
	result.Unfilled = remaining
	result.Applied, err = checkedSum4(
		result.PendingEarningsApplied, result.ActiveBondApplied,
		result.UnbondingApplied, result.ClaimableApplied,
	)
	if err != nil {
		return ApplyServiceSlashResult{}, fmt.Errorf("service slash applied amount overflow: %w", err)
	}
	if result.Applied > req.Requested {
		return ApplyServiceSlashResult{}, fmt.Errorf("service slash applied %d exceeds requested %d", result.Applied, req.Requested)
	}
	if result.Applied > 0 {
		// Layer 6: bond_version += 1, written together with the layer 1/2 effects.
		if bond.BondVersion == math.MaxUint64 {
			return ApplyServiceSlashResult{}, fmt.Errorf("service bond version overflow")
		}
		bond.BondVersion++
		applyPostSlashBondStatus(&bond)
		if err := bond.Validate(); err != nil {
			return ApplyServiceSlashResult{}, err
		}
		if err := k.ServiceBond.Set(ctx, types.NewServiceBondKey(operatorAddress), bond); err != nil {
			return ApplyServiceSlashResult{}, err
		}
		result.BondVersion = bond.BondVersion
		// Ruling 29a: min_stake changes do not affect global membership, but a slash
		// that removes the last effective active bond flips the §3.3 bond predicate.
		if err := k.syncCandidateSlotMembershipOnBondExit(ctx, operatorAddress, bond, req.Height); err != nil {
			return ApplyServiceSlashResult{}, err
		}
		if bond.Status == types.ServiceBondStatusExited {
			if err := k.removeCortexIdentityOnTerminalBond(ctx, operatorAddress); err != nil {
				return ApplyServiceSlashResult{}, err
			}
		}

		// Layer 5: remove profiles below min_stake and reweight every retained
		// declaration against the post-slash effective bond.
		if result.ActiveBondApplied > 0 {
			if err := k.reconcileSupportsAfterBondChange(
				ctx, operatorAddress, bond.ActiveBond, req.Height, types.ModelSupportDeactivateSlashBelowMin,
			); err != nil {
				return ApplyServiceSlashResult{}, err
			}
		}
	}

	summary := newSlashSummary(req, operatorAddress, result)
	if err := summary.Validate(); err != nil {
		return ApplyServiceSlashResult{}, fmt.Errorf("invalid slash summary: %w", err)
	}
	if err := k.SlashSummary.Set(ctx, summaryKey, summary); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if err := k.transferServiceSlashCustody(ctx, req.Destination, result); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if req.Destination == types.SlashDestination_SLASH_DESTINATION_TREASURY && result.Applied > 0 {
		epochLength, err := k.epochLengthBlocks(ctx)
		if err != nil {
			return ApplyServiceSlashResult{}, err
		}
		if err := k.addTreasuryInflow(ctx, epochForHeight(req.Height, epochLength), result.Applied, req.Height); err != nil {
			return ApplyServiceSlashResult{}, err
		}
	}
	return result, nil
}

func validateServiceSlashRequest(req ApplyServiceSlashRequest) error {
	if req.Requested == 0 {
		return fmt.Errorf("service slash requested amount must be > 0")
	}
	if !types.IsValidDuty(req.Duty) {
		return fmt.Errorf("service slash duty is invalid")
	}
	for name, value := range map[string][]byte{
		"summary_id": req.SummaryID,
		"task_id":    req.TaskID,
	} {
		if len(value) != 32 || bytes.Equal(value, make([]byte, 32)) {
			return fmt.Errorf("service slash %s must be a non-zero 32-byte hash", name)
		}
	}
	switch req.SourceKind {
	case types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT:
		if req.EffectIndex != 0 || !bytes.Equal(req.SummaryID, req.SourceID) ||
			req.Destination != types.SlashDestination_SLASH_DESTINATION_TREASURY {
			return fmt.Errorf("role-fault slash source identity is invalid")
		}
	case types.SlashSourceKind_SLASH_SOURCE_KIND_CHALLENGE_EFFECT:
		if req.EffectIndex == 0 || req.Destination != types.SlashDestination_SLASH_DESTINATION_CHALLENGE_EFFECT_POOL {
			return fmt.Errorf("challenge-effect slash requires a positive effect_index")
		}
	default:
		return fmt.Errorf("service slash source_kind is invalid")
	}
	if _, err := serviceSlashDestinationModule(req.Destination); err != nil {
		return err
	}
	if _, err := types.SlashSummarySourceKey(req.SourceKind, req.SourceID); err != nil {
		return err
	}
	return nil
}

func serviceSlashReplayResult(summary types.SlashSummaryState, req ApplyServiceSlashRequest, operatorAddress string) (ApplyServiceSlashResult, error) {
	requested, err := shared.ParseAmount(summary.RequestedAmount)
	if err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if err := summary.Validate(); err != nil ||
		!bytes.Equal(summary.SlashSummaryId, req.SummaryID) ||
		!bytes.Equal(summary.SourceId, req.SourceID) ||
		summary.SourceKind != req.SourceKind || summary.EffectIndex != req.EffectIndex ||
		summary.OperatorAddress != operatorAddress || summary.Duty != req.Duty ||
		!bytes.Equal(summary.GetTaskId(), req.TaskID) || requested != req.Requested ||
		summary.Destination != req.Destination {
		return ApplyServiceSlashResult{}, fmt.Errorf("service slash replay facts differ")
	}
	return slashSummaryResult(summary)
}

func slashSummaryResult(summary types.SlashSummaryState) (ApplyServiceSlashResult, error) {
	parse := func(name string, amount shared.Amount) (uint64, error) {
		value, err := shared.ParseAmount(amount)
		if err != nil {
			return 0, fmt.Errorf("slash summary %s: %w", name, err)
		}
		return value, nil
	}
	var result ApplyServiceSlashResult
	var err error
	if result.Applied, err = parse("applied_amount", summary.AppliedAmount); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if result.Unfilled, err = parse("unfilled_amount", summary.UnfilledAmount); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if result.PendingEarningsApplied, err = parse("pending_task_earnings_debit", summary.PendingTaskEarningsDebit); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if result.ActiveBondApplied, err = parse("active_bond_debit", summary.ActiveBondDebit); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if result.UnbondingApplied, err = parse("unbonding_debit", summary.UnbondingDebit); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if result.ClaimableApplied, err = parse("claimable_earnings_debit", summary.ClaimableEarningsDebit); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	result.UnbondingRowsVisited = summary.UnbondingRowsVisited
	result.BondVersion = summary.BondVersion
	return result, nil
}

func newSlashSummary(req ApplyServiceSlashRequest, operatorAddress string, result ApplyServiceSlashResult) types.SlashSummaryState {
	return types.SlashSummaryState{
		SlashSummaryId: append([]byte(nil), req.SummaryID...),
		SourceKind:     req.SourceKind, SourceId: append([]byte(nil), req.SourceID...), EffectIndex: req.EffectIndex,
		OperatorAddress: operatorAddress, Duty: req.Duty,
		XTaskId:                  &types.SlashSummaryState_TaskId{TaskId: append([]byte(nil), req.TaskID...)},
		RequestedAmount:          shared.NewAmount(req.Requested),
		PendingTaskEarningsDebit: shared.NewAmount(result.PendingEarningsApplied),
		ActiveBondDebit:          shared.NewAmount(result.ActiveBondApplied),
		UnbondingDebit:           shared.NewAmount(result.UnbondingApplied),
		ClaimableEarningsDebit:   shared.NewAmount(result.ClaimableApplied),
		AppliedAmount:            shared.NewAmount(result.Applied), UnfilledAmount: shared.NewAmount(result.Unfilled),
		Destination: req.Destination, UnbondingRowsVisited: result.UnbondingRowsVisited,
		AppliedHeight: req.Height, BondVersion: result.BondVersion,
	}
}

func serviceSlashDestinationModule(destination types.SlashDestination) (string, error) {
	switch destination {
	case types.SlashDestination_SLASH_DESTINATION_TREASURY:
		return types.TreasuryModuleName, nil
	case types.SlashDestination_SLASH_DESTINATION_CHALLENGE_EFFECT_POOL:
		return shared.TaskChallengeEffectModuleName, nil
	default:
		return "", fmt.Errorf("service slash destination is invalid")
	}
}

func (k Keeper) transferServiceSlashCustody(ctx context.Context, destination types.SlashDestination, result ApplyServiceSlashResult) error {
	destinationModule, err := serviceSlashDestinationModule(destination)
	if err != nil {
		return err
	}
	serviceDebit := result.ServiceBondModuleDebit()
	rewardsDebit := result.RewardsModuleDebit()
	if serviceDebit > result.Applied || rewardsDebit > result.Applied-serviceDebit {
		return fmt.Errorf("service slash layer debits do not sum to applied amount")
	}
	debits := []struct {
		module string
		amount uint64
	}{
		{module: types.ServiceBondModuleName, amount: serviceDebit},
		{module: types.RewardsModuleName, amount: rewardsDebit},
	}
	// Preflight every source before the first transfer. The SDK cache is still
	// the atomicity boundary, but this also prevents a partial move through a
	// custom BankKeeper implementation that reports a later insufficient source.
	for _, debit := range debits {
		if debit.amount == 0 {
			continue
		}
		if err := k.requireModuleBalance(ctx, debit.module, debit.amount); err != nil {
			return err
		}
	}
	for _, debit := range debits {
		if debit.amount == 0 {
			continue
		}
		if err := k.sendModuleToModule(ctx, debit.module, destinationModule, debit.amount); err != nil {
			return err
		}
	}
	return nil
}

// slashPendingTaskEarnings consumes the operator's still-pending task earnings
// for one task (§10.0c layer 0) and atomically drops the maturity index of any
// row it exhausts, so a responsible operator cannot mature and claim ahead of
// the deduction.
//
// The scan is bounded by params.Reward.MaxPendingEarningMaturePerBlock visited
// rows: the same budget that bounds pending-earning maturity work per block.
func (k Keeper) slashServiceUnbondingQueue(ctx context.Context, chainID string, height uint64, bond *types.ServiceBondState, operatorAddress string, requested uint64) (uint64, uint32, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, 0, err
	}
	limit := uint64(params.Service.MaxOpenUnbondingEntriesPerOperator)
	if limit == 0 {
		return 0, 0, fmt.Errorf("max_open_unbonding_entries_per_operator must be > 0")
	}
	rows, err := k.boundedServiceUnbondingRows(ctx, operatorAddress, limit)
	if err != nil {
		return 0, 0, err
	}
	remaining := requested
	var applied uint64
	var visited uint32
	for i := range rows {
		if remaining == 0 {
			break
		}
		visited++
		row := rows[i]
		if row.SlashAppliedAmount > row.Amount {
			return 0, 0, fmt.Errorf("service unbonding slash exceeds amount")
		}
		slashable := row.Amount - row.SlashAppliedAmount
		if slashable == 0 {
			continue
		}
		take := minUint64(remaining, slashable)
		row.SlashAppliedAmount, err = checkedAdd(row.SlashAppliedAmount, take)
		if err != nil {
			return 0, 0, fmt.Errorf("service unbonding slash overflow: %w", err)
		}
		if bond.PendingUnbondingTotal < take {
			return 0, 0, fmt.Errorf("service pending unbonding total underflow")
		}
		bond.PendingUnbondingTotal -= take
		if err := row.Validate(); err != nil {
			return 0, 0, err
		}
		if row.SlashAppliedAmount == row.Amount {
			if err := k.terminateServiceUnbonding(ctx, chainID, row, 0, height); err != nil {
				return 0, 0, err
			}
		} else if err := k.Unbonding.Set(ctx, types.NewUnbondingKey(operatorAddress, row.UnbondingId), row); err != nil {
			return 0, 0, err
		}
		remaining -= take
		applied += take
	}
	return applied, visited, nil
}

// boundedServiceUnbondingRows returns the operator's unbonding rows sorted by
// (mature_height, unbonding_id). §6.4 makes
// max_open_unbonding_entries_per_operator the synchronous access bound of the
// unified slash function, so more rows than that is an invariant error rather
// than a longer scan.
func (k Keeper) boundedServiceUnbondingRows(ctx context.Context, operatorAddress string, limit uint64) ([]types.UnbondingState, error) {
	iter, err := k.Unbonding.Iterate(ctx, collections.NewPrefixedPairRange[string, shared.Hash32Key](operatorAddress))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	rows := make([]types.UnbondingState, 0, limit)
	for ; iter.Valid(); iter.Next() {
		if uint64(len(rows)) == limit {
			return nil, fmt.Errorf(
				"operator %s holds more than %d unbonding rows; slash cannot scan unbounded state",
				operatorAddress, limit,
			)
		}
		row, err := iter.Value()
		if err != nil {
			return nil, err
		}
		if row.OperatorAddress != operatorAddress {
			return nil, fmt.Errorf("service unbonding owner mismatch")
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].MatureHeight != rows[j].MatureHeight {
			return rows[i].MatureHeight < rows[j].MatureHeight
		}
		return bytes.Compare(rows[i].UnbondingId, rows[j].UnbondingId) < 0
	})
	return rows, nil
}

// slashClaimableEarnings implements §10.0c layer 3. EarningsState.Validate
// requires claimable_amount to equal the sum of its claimable sub-ledgers, so the
// debit drains them in a fixed order instead of only lowering the total.
//
// V1 drains the three registered sub-ledgers in order: task fee, service, then
// builder. The residual check preserves the aggregate sum identity.
func (k Keeper) slashClaimableEarnings(ctx context.Context, operatorAddress string, requested, height uint64) (uint64, error) {
	earnings, err := k.getOrInitEarnings(ctx, operatorAddress)
	if err != nil {
		return 0, err
	}
	claimableTotal, err := shared.ParseAmount(earnings.ClaimableAmount)
	if err != nil {
		return 0, fmt.Errorf("earnings %s claimable_amount: %w", operatorAddress, err)
	}
	if claimableTotal == 0 {
		return 0, nil
	}
	applied := minUint64(requested, claimableTotal)
	if applied == 0 {
		return 0, nil
	}
	residual := applied
	for _, subledger := range []*shared.Amount{
		&earnings.ClaimableTaskFee,
		&earnings.ClaimableServiceReward,
		&earnings.ClaimableBuilderReward,
	} {
		if residual == 0 {
			break
		}
		current, err := shared.ParseAmount(*subledger)
		if err != nil {
			return 0, fmt.Errorf("earnings %s claimable sub-ledger: %w", operatorAddress, err)
		}
		take := minUint64(residual, current)
		*subledger = shared.NewAmount(current - take)
		residual -= take
	}
	if residual != 0 {
		return 0, fmt.Errorf("earnings %s claimable sub-ledgers do not cover claimable_amount", operatorAddress)
	}
	earnings.ClaimableAmount = shared.NewAmount(claimableTotal - applied)
	earnings.LastUpdatedHeight = height
	// A slash that drains every sub-ledger leaves an all-zero row, which
	// EarningsState.Validate rejects outright. The shared helper retires the row
	// in that case so the slash path and the claim path agree on what an empty
	// ledger means.
	if err := k.storeOrRemoveEmptyEarnings(ctx, earnings); err != nil {
		return 0, err
	}
	return applied, nil
}

// applyPostSlashBondStatus derives the terminal bond status after a debit.
// A bond that still carries open task reservations is not EXITED even at zero
// balance: §B.1.3 requires every reservation to be closed through
// CloseTaskLiabilityReservation before the operator is considered gone.
func applyPostSlashBondStatus(bond *types.ServiceBondState) {
	if bond.Status == types.ServiceBondStatusTombstoned {
		return
	}
	if bond.ActiveBond != 0 {
		return
	}
	if bond.PendingUnbondingTotal > 0 || bond.ReservedLiability > 0 {
		bond.Status = types.ServiceBondStatusUnbonding
		return
	}
	bond.Status = types.ServiceBondStatusExited
}

func checkedSum4(a, b, c, d uint64) (uint64, error) {
	sum, err := checkedAdd(a, b)
	if err != nil {
		return 0, err
	}
	sum, err = checkedAdd(sum, c)
	if err != nil {
		return 0, err
	}
	return checkedAdd(sum, d)
}
