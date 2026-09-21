package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const serviceUnbondingIDBytes = 32

// Hard constraint 8: see service_provider_runtime.go — registry lookup, not bare literals.
var (
	unbondingIDDomain      = shared.MustDomain(shared.DomainUnbondingIDV1)
	unbondingReceiptDomain = shared.MustDomain(shared.DomainUnbondingReceiptV1)
)

type WithdrawServiceUnbondedResult struct {
	Bond            types.ServiceBondState
	LastUnbonding   types.UnbondingState
	WithdrawnAmount uint64
	WithdrawnItems  uint32
	Status          shared.MutationStatusV1
}

func (k Keeper) GetServiceBondState(ctx context.Context, operatorAddress string) (types.ServiceBondState, error) {
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return types.ServiceBondState{}, err
	}
	state, exists, err := k.loadServiceBond(ctx, operatorAddress)
	if err != nil {
		return types.ServiceBondState{}, err
	}
	if !exists {
		return types.ServiceBondState{}, fmt.Errorf("service bond %s not found", operatorAddress)
	}
	if state.OperatorAddress != operatorAddress {
		return types.ServiceBondState{}, fmt.Errorf("service bond does not match its store key")
	}
	if err := state.Validate(); err != nil {
		return types.ServiceBondState{}, fmt.Errorf("invalid service bond: %w", err)
	}
	return state, nil
}

func (k Keeper) BeginServiceUnstake(ctx context.Context, operatorAddress string, amount, height uint64) (types.ServiceBondState, types.UnbondingState, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	bond, unbonding, err := k.beginServiceUnstake(
		sdk.WrapSDKContext(cacheCtx), sdkCtx.ChainID(), operatorAddress, amount, height,
	)
	if err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	write()
	return bond, unbonding, nil
}

func (k Keeper) beginServiceUnstake(ctx context.Context, chainID, operatorAddress string, amount, height uint64) (types.ServiceBondState, types.UnbondingState, error) {
	operatorBytes, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	if chainID == "" || amount == 0 {
		return types.ServiceBondState{}, types.UnbondingState{}, fmt.Errorf("chain_id and positive amount are required")
	}
	if _, err := k.GetCortexNodeState(ctx, operatorAddress); err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	bond, err := k.GetServiceBondState(ctx, operatorAddress)
	if err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	if !canBeginServiceUnstakeFromBondStatus(bond.Status) {
		return types.ServiceBondState{}, types.UnbondingState{}, fmt.Errorf("service bond status %s cannot unstake", bond.Status.String())
	}
	epochLength, err := k.epochLengthBlocks(ctx)
	if err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	// §B.1.3: "Unstake may only use unreserved bond". Reading raw active_bond here (the
	// previous behaviour) let an operator unstake bond that open task-liability
	// reservations were already relying on, and disagreed with the candidate
	// eligibility reading of the same quantity.
	currentEpoch := epochForHeight(height, epochLength)
	availableBond, err := types.AvailableBond(bond, currentEpoch)
	if err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	if availableBond < amount {
		return types.ServiceBondState{}, types.UnbondingState{}, errorsmod.Wrap(types.ErrInsufficientServiceBond, "available bond is below unstake amount")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	openRows, err := k.countOutstandingServiceUnbondings(ctx, operatorAddress, params.Service.MaxOpenUnbondingEntriesPerOperator)
	if err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	if openRows >= uint64(params.Service.MaxOpenUnbondingEntriesPerOperator) {
		return types.ServiceBondState{}, types.UnbondingState{}, errorsmod.Wrap(types.ErrLimitExceeded, "max open unbonding entries reached")
	}
	matureHeight, err := checkedAdd(height, params.Service.ServiceUnbondingPeriodBlocks)
	if err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, fmt.Errorf("unbonding mature height overflow: %w", err)
	}
	if bond.BondVersion == math.MaxUint64 {
		return types.ServiceBondState{}, types.UnbondingState{}, fmt.Errorf("service bond version overflow")
	}
	bond.BondVersion++
	// Ruling 18/9: single normalization point, before and after the debit. Before, so
	// the epoch invariant holds against the pre-debit active_bond; after, so
	// effective_active_bond can never exceed the post-debit one. The inline
	// one-directional clamp this replaces could only lower the snapshot and
	// therefore left effective < active whenever currentEpoch had already reached
	// effective_bond_epoch.
	types.NormalizeEffectiveActiveBond(&bond, currentEpoch)
	bond.ActiveBond -= amount
	types.NormalizeEffectiveActiveBond(&bond, currentEpoch)
	bond.PendingUnbondingTotal, err = checkedAdd(bond.PendingUnbondingTotal, amount)
	if err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, fmt.Errorf("pending unbonding overflow: %w", err)
	}
	bond.LastUnstakeHeight = height
	if bond.ActiveBond == 0 && bond.Status != types.ServiceBondStatus_SERVICE_BOND_STATUS_TOMBSTONED {
		bond.Status = types.ServiceBondStatus_SERVICE_BOND_STATUS_UNBONDING
	}
	unbondingID, err := serviceUnbondingID(chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operatorBytes, bond.BondVersion, amount, height, matureHeight)
	if err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	unbonding := types.UnbondingState{
		UnbondingId: append([]byte(nil), unbondingID...), OperatorAddress: operatorAddress, Amount: amount,
		RequestHeight: height, MatureHeight: matureHeight, Status: types.UnbondingStatus_UNBONDING_STATUS_OPEN,
	}
	if err := bond.Validate(); err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	if err := unbonding.Validate(); err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	idKey := shared.Hash32Key(unbondingID)
	if err := k.ServiceBond.Set(ctx, types.NewServiceBondKey(operatorAddress), bond); err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	if err := k.reconcileSupportsAfterBondChange(
		ctx, operatorAddress, bond.ActiveBond, height, types.ModelSupportDeactivateBondBelowMin,
	); err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	bond, err = k.GetServiceBondState(ctx, operatorAddress)
	if err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	if err := k.Unbonding.Set(ctx, types.NewUnbondingKey(operatorAddress, idKey), unbonding); err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	if err := k.UnbondingMaturityIndex.Set(ctx, types.NewUnbondingMaturityIndexKey(matureHeight, operatorAddress, idKey)); err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	if err := k.UnbondingByOperatorStatusIndex.Set(ctx, types.NewUnbondingByOperatorStatusKey(operatorAddress, unbonding.Status, matureHeight, idKey)); err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	// Ruling 29 conditional keep: only a full exit changes the §3.3 membership
	// predicate; a partial unstake must not bump candidate_source_revision.
	if err := k.syncCandidateSlotMembershipOnBondExit(ctx, operatorAddress, bond, height); err != nil {
		return types.ServiceBondState{}, types.UnbondingState{}, err
	}
	emitServiceUnbondingStartedEvent(ctx, unbonding)
	return bond, unbonding, nil
}

func (k Keeper) withdrawServiceUnbondedByID(ctx context.Context, chainID, operatorAddress string, unbondingID []byte, height uint64) (WithdrawServiceUnbondedResult, error) {
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return WithdrawServiceUnbondedResult{}, err
	}
	if err := requireUnbondingID(unbondingID); err != nil {
		return WithdrawServiceUnbondedResult{}, err
	}
	idKey := shared.Hash32Key(unbondingID)
	unbonding, err := k.Unbonding.Get(ctx, types.NewUnbondingKey(operatorAddress, idKey))
	if errors.Is(err, collections.ErrNotFound) {
		receipt, receiptErr := k.UnbondingReceipt.Get(ctx, idKey)
		if receiptErr != nil {
			if errors.Is(receiptErr, collections.ErrNotFound) {
				return WithdrawServiceUnbondedResult{}, errorsmod.Wrap(types.ErrUnbondingNotFound, "service unbonding not found")
			}
			return WithdrawServiceUnbondedResult{}, receiptErr
		}
		if receipt.OperatorAddress != operatorAddress || !bytes.Equal(receipt.UnbondingId, unbondingID) {
			return WithdrawServiceUnbondedResult{}, fmt.Errorf("unbonding receipt owner mismatch")
		}
		bond, bondErr := k.GetServiceBondState(ctx, operatorAddress)
		return WithdrawServiceUnbondedResult{
			Bond: bond, WithdrawnAmount: receipt.WithdrawnAmount,
			Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP,
		}, bondErr
	}
	if err != nil {
		return WithdrawServiceUnbondedResult{}, err
	}
	// Exact receipt replay is resolved above without re-running current
	// responsibility/withdrawability gates. Those gates apply only while the
	// primary is still live and can move funds.
	if err := k.requireServiceUnbondingWithdrawable(ctx, operatorAddress); err != nil {
		return WithdrawServiceUnbondedResult{}, err
	}
	if height < unbonding.MatureHeight {
		return WithdrawServiceUnbondedResult{}, errorsmod.Wrap(types.ErrDeadlineNotReached, "unbonding is not mature")
	}
	return k.withdrawServiceUnbondingRows(ctx, chainID, operatorAddress, []types.UnbondingState{unbonding}, height)
}

func (k Keeper) withdrawServiceUnbondedBatch(ctx context.Context, chainID, operatorAddress string, maxItems uint32, height uint64) (WithdrawServiceUnbondedResult, error) {
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return WithdrawServiceUnbondedResult{}, err
	}
	if err := k.requireServiceUnbondingWithdrawable(ctx, operatorAddress); err != nil {
		return WithdrawServiceUnbondedResult{}, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return WithdrawServiceUnbondedResult{}, err
	}
	if maxItems == 0 {
		maxItems = params.Service.MaxUnbondingWithdrawItemsPerTx
	}
	if maxItems == 0 || maxItems > params.Service.MaxUnbondingWithdrawItemsPerTx {
		return WithdrawServiceUnbondedResult{}, errorsmod.Wrap(types.ErrLimitExceeded, "batch max_items exceeds max_unbonding_withdraw_items_per_tx")
	}
	rows, err := k.serviceUnbondingsForOperator(ctx, operatorAddress)
	if err != nil {
		return WithdrawServiceUnbondedResult{}, err
	}
	selected := make([]types.UnbondingState, 0, maxItems)
	var visited uint32
	for i := range rows {
		if visited == maxItems {
			break
		}
		visited++
		if rows[i].MatureHeight > height {
			continue
		}
		selected = append(selected, rows[i])
	}
	if len(selected) == 0 {
		bond, err := k.GetServiceBondState(ctx, operatorAddress)
		return WithdrawServiceUnbondedResult{Bond: bond, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, err
	}
	return k.withdrawServiceUnbondingRows(ctx, chainID, operatorAddress, selected, height)
}

func (k Keeper) withdrawServiceUnbondingRows(ctx context.Context, chainID, operatorAddress string, rows []types.UnbondingState, height uint64) (WithdrawServiceUnbondedResult, error) {
	bond, err := k.GetServiceBondState(ctx, operatorAddress)
	if err != nil {
		return WithdrawServiceUnbondedResult{}, err
	}
	if bond.BondVersion == math.MaxUint64 {
		return WithdrawServiceUnbondedResult{}, fmt.Errorf("service bond version overflow")
	}
	var result WithdrawServiceUnbondedResult
	result.Status = shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED
	for i := range rows {
		row := rows[i]
		if err := row.Validate(); err != nil {
			return WithdrawServiceUnbondedResult{}, err
		}
		if row.OperatorAddress != operatorAddress {
			return WithdrawServiceUnbondedResult{}, fmt.Errorf("service unbonding owner mismatch")
		}
		remaining, err := unwithdrawnServiceUnbondingAmount(row)
		if err != nil {
			return WithdrawServiceUnbondedResult{}, err
		}
		if bond.PendingUnbondingTotal < remaining {
			return WithdrawServiceUnbondedResult{}, fmt.Errorf("service pending unbonding total underflow")
		}
		bond.PendingUnbondingTotal -= remaining
		result.WithdrawnAmount, err = checkedAdd(result.WithdrawnAmount, remaining)
		if err != nil {
			return WithdrawServiceUnbondedResult{}, fmt.Errorf("withdrawn amount overflow: %w", err)
		}
		result.WithdrawnItems++
		result.LastUnbonding = row
		if err := k.terminateServiceUnbonding(ctx, chainID, row, remaining, height); err != nil {
			return WithdrawServiceUnbondedResult{}, err
		}
	}
	bond.BondVersion++
	if bond.ActiveBond == 0 && bond.PendingUnbondingTotal == 0 && bond.Status != types.ServiceBondStatus_SERVICE_BOND_STATUS_TOMBSTONED {
		bond.Status = types.ServiceBondStatus_SERVICE_BOND_STATUS_EXITED
	}
	if err := bond.Validate(); err != nil {
		return WithdrawServiceUnbondedResult{}, err
	}
	if err := k.ServiceBond.Set(ctx, types.NewServiceBondKey(operatorAddress), bond); err != nil {
		return WithdrawServiceUnbondedResult{}, err
	}
	// Ruling 29 conditional keep: withdrawal only changes membership when it
	// completes the exit (ACTIVE_BOND==0 && PENDING==0 -> EXITED).
	if err := k.syncCandidateSlotMembershipOnBondExit(ctx, operatorAddress, bond, height); err != nil {
		return WithdrawServiceUnbondedResult{}, err
	}
	// §3.3 line 225 clearing point 3: withdrawal removed unbonding rows, so a
	// pending unbonding slash hold may just have been lifted. Re-run the release
	// judgment in this same transaction.
	if err := k.retryCandidateSlotRelease(ctx, operatorAddress, height); err != nil {
		return WithdrawServiceUnbondedResult{}, err
	}
	if bond.Status == types.ServiceBondStatusExited {
		if err := k.removeCortexIdentityOnTerminalBond(ctx, operatorAddress); err != nil {
			return WithdrawServiceUnbondedResult{}, err
		}
	}
	result.Bond = bond
	if result.WithdrawnAmount > 0 {
		emitServiceUnbondedWithdrawnEvent(ctx, operatorAddress, result.WithdrawnAmount, result.WithdrawnItems, bond.BondVersion)
	}
	return result, nil
}

// terminateServiceUnbonding is the sole terminal writer for both withdrawal
// and slash exhaustion. It removes the primary and both derived indexes before
// writing the fixed-size exact-replay receipt and its retention index.
func (k Keeper) terminateServiceUnbonding(ctx context.Context, chainID string, row types.UnbondingState, withdrawnAmount, height uint64) error {
	if err := row.Validate(); err != nil {
		return err
	}
	if row.SlashAppliedAmount > row.Amount || withdrawnAmount != row.Amount-row.SlashAppliedAmount {
		return fmt.Errorf("terminal service unbonding amounts do not exhaust the row")
	}
	if height == 0 || chainID == "" {
		return fmt.Errorf("terminal service unbonding chain_id and height are required")
	}
	terminalStatus := types.UnbondingReceiptTerminalStatus_UNBONDING_RECEIPT_TERMINAL_STATUS_WITHDRAWN
	if withdrawnAmount == 0 {
		terminalStatus = types.UnbondingReceiptTerminalStatus_UNBONDING_RECEIPT_TERMINAL_STATUS_FULLY_SLASHED
	}
	receipt := types.UnbondingReceiptState{
		UnbondingId: append([]byte(nil), row.UnbondingId...), OperatorAddress: row.OperatorAddress,
		OriginalAmount: row.Amount, WithdrawnAmount: withdrawnAmount, SlashedAmount: row.SlashAppliedAmount,
		TerminalStatus: terminalStatus, TerminalHeight: height,
	}
	var err error
	receipt.ReceiptHash, err = k.unbondingReceiptHash(chainID, receipt)
	if err != nil {
		return err
	}
	if err := receipt.Validate(); err != nil {
		return fmt.Errorf("invalid unbonding receipt: %w", err)
	}
	idKey := shared.Hash32Key(row.UnbondingId)
	if has, err := k.UnbondingReceipt.Has(ctx, idKey); err != nil {
		return err
	} else if has {
		return fmt.Errorf("unbonding receipt already exists")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	pruneHeight, err := checkedAdd(height, params.Service.UnbondingReceiptRetentionBlocks)
	if err != nil {
		return fmt.Errorf("unbonding receipt prune height overflow: %w", err)
	}
	if err := k.Unbonding.Remove(ctx, types.NewUnbondingKey(row.OperatorAddress, idKey)); err != nil {
		return err
	}
	if err := k.removeUnbondingMaturityIndex(ctx, row.MatureHeight, row.OperatorAddress, idKey); err != nil {
		return err
	}
	if err := k.UnbondingByOperatorStatusIndex.Remove(ctx, types.NewUnbondingByOperatorStatusKey(row.OperatorAddress, row.Status, row.MatureHeight, idKey)); err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if err := k.UnbondingReceipt.Set(ctx, idKey, receipt); err != nil {
		return err
	}
	return k.UnbondingReceiptPruneIndex.Set(ctx, types.NewUnbondingReceiptPruneKey(pruneHeight, idKey))
}

func (k Keeper) requireServiceUnbondingWithdrawable(ctx context.Context, operatorAddress string) error {
	if _, err := k.GetCortexNodeState(ctx, operatorAddress); err != nil {
		return err
	}
	pending, err := k.hasPendingServiceKeyResponsibility(ctx, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operatorAddress)
	if err != nil {
		return err
	}
	if pending {
		return errorsmod.Wrap(types.ErrPendingResponsibility, "service unbonding cannot be withdrawn while responsibilities are pending")
	}
	// §10.0c step 3 also freezes withdrawal while any task liability is open;
	// that fact now lives on the participant counter rather than on a
	// TASK_LIABILITY responsibility row.
	node, err := k.GetCortexNodeState(ctx, operatorAddress)
	if err != nil {
		return err
	}
	if node.ActiveTaskLiabilityCount != 0 || node.PendingStageDutyCount != 0 {
		return errorsmod.Wrap(types.ErrPendingResponsibility, "service unbonding cannot be withdrawn while duties are open")
	}
	return nil
}

// countOutstandingServiceUnbondings counts every unwithdrawn primary row.
// Maturity changes slash timing, not whether the row consumes the per-operator
// hard cap; excluding MATURE rows made the slash scan unbounded and caused an
// exported genesis to reject its own state.
func (k Keeper) countOutstandingServiceUnbondings(ctx context.Context, operatorAddress string, limit uint32) (uint64, error) {
	iter, err := k.Unbonding.Iterate(ctx, collections.NewPrefixedPairRange[string, shared.Hash32Key](operatorAddress))
	if err != nil {
		return 0, err
	}
	defer iter.Close()
	var count uint64
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return 0, err
		}
		if entry.Key.K1() != operatorAddress || entry.Value.OperatorAddress != operatorAddress {
			return 0, fmt.Errorf("service unbonding does not match its operator prefix")
		}
		count++
		if count >= uint64(limit) {
			break
		}
	}
	return count, nil
}

func (k Keeper) serviceUnbondingsForOperator(ctx context.Context, operatorAddress string) ([]types.UnbondingState, error) {
	iter, err := k.Unbonding.Iterate(ctx, collections.NewPrefixedPairRange[string, shared.Hash32Key](operatorAddress))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	rows := make([]types.UnbondingState, 0)
	for ; iter.Valid(); iter.Next() {
		row, err := iter.Value()
		if err != nil {
			return nil, err
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

// RequiredServiceBondForProfile is min_stake(profile), the single bond
// requirement §6.4 attaches to a profile ("active_bond >= min_stake(profile)").
//
// It used to additionally floor the value at a hardcoded MinServiceBond of
// 500_000, which was a duplicate of params.Service.ServiceBondMinInitial and
// had already drifted from it (the params default is 1_000_000). The global
// floor only gates the first MsgStakeService (§10.0c step 3) and is read from
// params in prepareRegisterService; it is not a second per-profile minimum.
// This also matches the Task-side definition in
// x/task/keeper/candidate_eligibility.go.
func RequiredServiceBondForProfile(profile types.ProfileState) uint64 {
	return profile.MinStake
}

func (k Keeper) deactivateDeclaredSupports(ctx context.Context, operatorAddress, reason string, height uint64) error {
	return k.collectAndDeactivateSupports(ctx, operatorAddress, reason, height, func(types.ProfileState) bool { return true })
}

// suspendDeclaredSupportsForJail is deactivateDeclaredSupports' non-terminal
// counterpart: it discharges the same §6.1 genesis invariant on every declared
// row, but keeps the declaration itself so the operator stays a Worker candidate
// at the reduced candidate_jail_factor and can walk jail_count back to 0. See
// suspendModelSupportForJail for why the declaration must survive.
func (k Keeper) suspendDeclaredSupportsForJail(ctx context.Context, operatorAddress string, height uint64) error {
	supports, err := k.collectDeclaredSupports(ctx, operatorAddress, func(types.ProfileState) bool { return true })
	if err != nil {
		return err
	}
	for _, support := range supports {
		if err := k.suspendModelSupportForJail(ctx, support.OperatorAddress, support.ModelId, support.ProfileVersion, height); err != nil {
			return err
		}
	}
	return nil
}

// collectDeclaredSupports materialises the operator's declared rows before any
// of them is mutated. The scan is bounded by
// params.Support.MaxSupportedProfilesPerOperator (§6.1), unlike the
// profile->operators direction.
func (k Keeper) collectDeclaredSupports(ctx context.Context, operatorAddress string, shouldSelect func(types.ProfileState) bool) ([]types.ModelSupportState, error) {
	iter, err := k.ModelSupportByOperatorIndex.Iterate(ctx, collections.NewPrefixedTripleRange[string, string, uint32](strings.TrimSpace(operatorAddress)))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var supports []types.ModelSupportState
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		support, err := k.ModelSupport.Get(ctx, types.NewModelSupportKey(key.K1(), key.K2(), key.K3()))
		if err != nil {
			return nil, err
		}
		if !support.DeclaredSupport {
			continue
		}
		profile, err := k.GetProfile(ctx, support.ModelId, support.ProfileVersion)
		if err != nil {
			return nil, err
		}
		if shouldSelect(profile) {
			supports = append(supports, support)
		}
	}
	return supports, nil
}

func (k Keeper) collectAndDeactivateSupports(ctx context.Context, operatorAddress, reason string, height uint64, shouldDeactivate func(types.ProfileState) bool) error {
	supports, err := k.collectDeclaredSupports(ctx, operatorAddress, shouldDeactivate)
	if err != nil {
		return err
	}
	for _, support := range supports {
		if _, err := k.deactivateModelSupport(ctx, support.OperatorAddress, support.ModelId, support.ProfileVersion, reason, height); err != nil {
			return err
		}
	}
	return k.syncLiveServiceBondStatus(ctx, operatorAddress)
}

func (k Keeper) loadServiceBond(ctx context.Context, operatorAddress string) (types.ServiceBondState, bool, error) {
	state, err := k.ServiceBond.Get(ctx, types.NewServiceBondKey(strings.TrimSpace(operatorAddress)))
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.ServiceBondState{}, false, nil
		}
		return types.ServiceBondState{}, false, err
	}
	return state, true, nil
}

// The TrimSpace on unbonding_id is gone with the text key: a fixed-width Hash32
// cannot carry surrounding whitespace. operator_address keeps its trim because it
// is still Bech32 text.
func (k Keeper) removeUnbondingMaturityIndex(ctx context.Context, matureHeight uint64, operatorAddress string, unbondingID shared.Hash32Key) error {
	err := k.UnbondingMaturityIndex.Remove(ctx, types.NewUnbondingMaturityIndexKey(matureHeight, strings.TrimSpace(operatorAddress), unbondingID))
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	return nil
}

func unwithdrawnServiceUnbondingAmount(state types.UnbondingState) (uint64, error) {
	if state.SlashAppliedAmount > state.Amount {
		return 0, fmt.Errorf("service unbonding slash exceeds amount")
	}
	return state.Amount - state.SlashAppliedAmount, nil
}

// Ruling 17/24: operatorBytes is the address codec bytes required by
// the API contract,
// not the Bech32 text. §1.4 gives TRUEOPEN_UNBONDING_ID_V1 one preimage shared by
// service and Builder unbonding, so the caller must always pass codec bytes.
func serviceUnbondingID(chainID string, participantType shared.ParticipantType, operatorBytes []byte, bondVersion, amount, requestHeight, matureHeight uint64) ([]byte, error) {
	return shared.NewCanonicalHashBuilderV1(unbondingIDDomain).Raw(
		[]byte(chainID),
		shared.EnumBE(uint32(participantType)),
		operatorBytes,
		shared.Uint64BE(bondVersion),
		shared.Uint64BE(amount),
		shared.Uint64BE(requestHeight),
		shared.Uint64BE(matureHeight),
	).Sum()
}

// Ruling 17/24: the receipt digest frames the operator as address codec bytes
// (the API contract). It is a Keeper method purely so the single canonical decoder
// (requireCanonicalAddress) stays the only bech32 -> bytes path in the module; a
// second local decoder would be a second way to disagree about the preimage.
func (k Keeper) unbondingReceiptHash(chainID string, receipt types.UnbondingReceiptState) ([]byte, error) {
	operatorBytes, _, err := k.requireCanonicalAddress("operator_address", receipt.OperatorAddress)
	if err != nil {
		return nil, err
	}
	return shared.NewCanonicalHashBuilderV1(unbondingReceiptDomain).Raw(
		[]byte(chainID),
		receipt.UnbondingId,
		operatorBytes,
		shared.Uint64BE(receipt.OriginalAmount),
		shared.Uint64BE(receipt.WithdrawnAmount),
		shared.Uint64BE(receipt.SlashedAmount),
		shared.EnumBE(uint32(receipt.TerminalStatus)),
		shared.Uint64BE(receipt.TerminalHeight),
	).Sum()
}

func requireUnbondingID(unbondingID []byte) error {
	if len(unbondingID) != serviceUnbondingIDBytes || bytes.Equal(unbondingID, make([]byte, serviceUnbondingIDBytes)) {
		return fmt.Errorf("unbonding_id must be a non-zero 32-byte hash")
	}
	return nil
}

func normalizeServiceRole(role string) string { return strings.ToUpper(strings.TrimSpace(role)) }

func canBeginServiceUnstakeFromBondStatus(status types.ServiceBondStatus) bool {
	switch status {
	case types.ServiceBondStatus_SERVICE_BOND_STATUS_REGISTERED,
		types.ServiceBondStatus_SERVICE_BOND_STATUS_ACTIVE,
		types.ServiceBondStatus_SERVICE_BOND_STATUS_JAILED,
		types.ServiceBondStatus_SERVICE_BOND_STATUS_TOMBSTONED:
		return true
	default:
		return false
	}
}

func emitServiceStakeChangedEventWithAmount(ctx context.Context, bond types.ServiceBondState, amount uint64) {
	mustEmitHubEvent(ctx, &types.EventServiceStakeChanged{
		Operator: bond.OperatorAddress, Amount: shared.NewAmount(amount), ActiveBondAmount: shared.NewAmount(bond.ActiveBond),
		BondVersion: bond.BondVersion, EffectiveEpoch: bond.EffectiveBondEpoch,
	})
}

func emitServiceUnbondingStartedEvent(ctx context.Context, state types.UnbondingState) {
	mustEmitHubEvent(ctx, &types.EventServiceUnbondingStarted{
		UnbondingId: append([]byte(nil), state.UnbondingId...), Operator: state.OperatorAddress,
		Amount: shared.NewAmount(state.Amount), MatureHeight: state.MatureHeight,
	})
}

func emitServiceUnbondedWithdrawnEvent(ctx context.Context, operatorAddress string, amount uint64, items uint32, bondVersion uint64) {
	mustEmitHubEvent(ctx, &types.EventServiceUnbondingWithdrawn{
		Operator: operatorAddress, WithdrawnAmount: shared.NewAmount(amount), WithdrawnItems: items, BondVersion: bondVersion,
	})
}
