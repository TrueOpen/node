package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// AcceptedGovernanceActionContext is supplied by the x/gov execution adapter,
// never by a Tx request. The duplicated locator is intentional: Hub compares
// the trusted execution locator with the typed action before touching state.
type AcceptedGovernanceActionContext struct {
	AuthorityAddress string
	ProposalID       uint64
	ItemIndex        uint32
	Accepted         bool
}

// GovernanceActionReplayChecker reads whether an accepted proposal item can
// still be executed. Hub uses it only when a receipt reaches its prune height;
// it does not copy governance proposal state into the Hub store.
type GovernanceActionReplayChecker interface {
	IsGovernanceActionReExecutable(context.Context, uint64, uint32) (bool, error)
}

type governanceActionReplayCheckerHolder struct {
	checker GovernanceActionReplayChecker
}

type TreasurySpendExecutionResult struct {
	Receipt types.TreasurySpendReceiptState
	Status  shared.MutationStatusV1
}

func (k Keeper) initTreasurySpendGenesis(ctx context.Context, genesis types.GenesisState) error {
	for _, state := range genesis.TreasurySpendReceipts {
		if err := k.storeTreasurySpendReceipt(ctx, types.NewTreasurySpendReceiptKey(state.ProposalId, state.ItemIndex), state); err != nil {
			return err
		}
		if err := k.TreasurySpendReceiptPruneIndex.Set(ctx, types.NewTreasurySpendReceiptPruneKey(state.PruneHeight, state.ProposalId, state.ItemIndex)); err != nil {
			return err
		}
	}
	for _, state := range genesis.TreasurySpendProposals {
		if err := k.TreasurySpendProposal.Set(ctx, state.ProposalId, state); err != nil {
			return err
		}
	}
	for _, state := range genesis.TreasurySpendEpochs {
		if err := k.TreasurySpendEpoch.Set(ctx, state.RewardEpoch, state); err != nil {
			return err
		}
	}
	for _, state := range genesis.TreasurySpendRecipientEpochs {
		recipient, canonical, err := k.requireCanonicalAddress("treasury recipient accumulator", state.RecipientAddress)
		if err != nil || canonical != state.RecipientAddress {
			return fmt.Errorf("treasury recipient accumulator address is invalid")
		}
		if err := k.storeTreasuryRecipient(ctx, types.NewTreasurySpendRecipientEpochKey(state.RewardEpoch, recipient), state); err != nil {
			return err
		}
	}
	cursors := make(map[uint64]struct{}, len(genesis.TreasurySpendEpochCleanupCursors))
	for _, state := range genesis.TreasurySpendEpochCleanupCursors {
		cursors[state.RewardEpoch] = struct{}{}
		if err := k.storeTreasuryCleanupCursor(ctx, state); err != nil {
			return err
		}
	}
	for _, state := range genesis.TreasurySpendEpochs {
		if _, running := cursors[state.RewardEpoch]; running {
			continue
		}
		if err := k.TreasurySpendEpochCleanupIndex.Set(ctx, types.NewTreasurySpendEpochCleanupIndexKey(state.CleanupHeight, state.RewardEpoch)); err != nil {
			return err
		}
	}
	return nil
}

// WithGovernanceActionReplayChecker returns the module-owned Keeper copy that
// can safely retire TreasurySpend receipts after the x/gov replay window.
func (k Keeper) WithGovernanceActionReplayChecker(checker GovernanceActionReplayChecker) Keeper {
	if k.governanceActionReplayChecker == nil {
		k.governanceActionReplayChecker = &governanceActionReplayCheckerHolder{}
	}
	k.governanceActionReplayChecker.checker = checker
	return k
}

// ExecuteTreasurySpendV1 is the internal callback for one accepted x/gov item.
// It is deliberately absent from hub.v1.Msg, AutoCLI and Tx routing.
func (k Keeper) ExecuteTreasurySpendV1(
	ctx context.Context,
	execution AcceptedGovernanceActionContext,
	action types.ExecuteTreasurySpendV1,
) (TreasurySpendExecutionResult, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() <= 0 || sdkCtx.ChainID() == "" {
		return TreasurySpendExecutionResult{}, fmt.Errorf("treasury spend requires a positive height and non-empty chain_id")
	}
	height := uint64(sdkCtx.BlockHeight())
	digest, recipient, amount, err := k.validateTreasurySpendAction(sdkCtx, execution, action)
	if err != nil {
		return TreasurySpendExecutionResult{}, err
	}

	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	key := types.NewTreasurySpendReceiptKey(action.ProposalId, action.ItemIndex)
	existing, err := k.getTreasurySpendReceipt(cache, key)
	if err == nil {
		if existing.ProposalId != action.ProposalId || existing.ItemIndex != action.ItemIndex ||
			!bytes.Equal(existing.ActionDigest, digest) {
			return TreasurySpendExecutionResult{}, fmt.Errorf("treasury spend locator conflicts with its existing receipt")
		}
		return TreasurySpendExecutionResult{Receipt: existing, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return TreasurySpendExecutionResult{}, err
	}
	if k.governanceActionReplayChecker == nil || k.governanceActionReplayChecker.checker == nil {
		return TreasurySpendExecutionResult{}, fmt.Errorf("treasury spend governance replay checker is unavailable")
	}
	if height < action.NotBeforeHeight || height > action.ExpiryHeight {
		return TreasurySpendExecutionResult{}, fmt.Errorf("treasury spend height %d is outside [%d,%d]", height, action.NotBeforeHeight, action.ExpiryHeight)
	}

	params, err := k.Params.Get(cache)
	if err != nil {
		return TreasurySpendExecutionResult{}, err
	}
	rewardEpoch := epochForHeight(height, normalizedEpochLengthBlocks(params))
	_, epochEnd, err := epochHeightRange(rewardEpoch, normalizedEpochLengthBlocks(params))
	if err != nil {
		return TreasurySpendExecutionResult{}, fmt.Errorf("treasury spend epoch range: %w", err)
	}
	newEpochCleanupHeight, err := checkedAdd(epochEnd, params.Treasury.GovernanceActionReplayWindowBlocks)
	if err != nil {
		return TreasurySpendExecutionResult{}, fmt.Errorf("treasury spend epoch cleanup height overflow: %w", err)
	}
	pruneHeight, err := checkedAdd(height, params.Treasury.GovernanceActionReplayWindowBlocks)
	if err != nil {
		return TreasurySpendExecutionResult{}, fmt.Errorf("treasury spend prune height overflow: %w", err)
	}
	cleanupHeight, err := k.applyTreasurySpendCaps(
		cache, action.ProposalId, rewardEpoch, recipient, action.Recipient, amount, height, newEpochCleanupHeight, params.Treasury,
	)
	if err != nil {
		return TreasurySpendExecutionResult{}, err
	}
	pruneHeight = max(pruneHeight, cleanupHeight)
	treasury, err := k.getOrInitTreasuryState(cache)
	if err != nil {
		return TreasurySpendExecutionResult{}, err
	}
	balance, err := shared.ParseAmount(treasury.Balance)
	if err != nil {
		return TreasurySpendExecutionResult{}, fmt.Errorf("treasury balance: %w", err)
	}
	bankBalance := k.bankKeeper.GetBalance(cache, authtypes.NewModuleAddress(types.TreasuryModuleName), params.Phase0.BusinessDenom)
	if bankBalance.Denom != params.Phase0.BusinessDenom || !bankBalance.Amount.IsUint64() || bankBalance.Amount.Uint64() != balance {
		return TreasurySpendExecutionResult{}, fmt.Errorf("treasury logical balance does not match its module account")
	}
	if balance < amount {
		return TreasurySpendExecutionResult{}, fmt.Errorf("treasury balance %d is below spend amount %d", balance, amount)
	}
	if treasury.TreasuryVersion == math.MaxUint64 {
		return TreasurySpendExecutionResult{}, fmt.Errorf("treasury version overflow")
	}
	treasury.Balance = shared.NewAmount(balance - amount)
	treasury.TreasuryVersion++
	receipt := types.TreasurySpendReceiptState{
		ProposalId: action.ProposalId, ItemIndex: action.ItemIndex,
		ExecutedHeight: height, PruneHeight: pruneHeight,
		ActionDigest: append([]byte(nil), digest...), TreasuryVersionAfter: treasury.TreasuryVersion,
		Recipient: action.Recipient, Amount: shared.NewAmount(amount), RewardEpoch: rewardEpoch,
		PurposeCode: action.PurposeCode,
	}
	if err := k.Treasury.Set(cache, treasury); err != nil {
		return TreasurySpendExecutionResult{}, err
	}
	if err := k.storeTreasurySpendReceipt(cache, key, receipt); err != nil {
		return TreasurySpendExecutionResult{}, err
	}
	if err := k.TreasurySpendReceiptPruneIndex.Set(cache, types.NewTreasurySpendReceiptPruneKey(pruneHeight, action.ProposalId, action.ItemIndex)); err != nil {
		return TreasurySpendExecutionResult{}, err
	}
	mustEmitHubEvent(cache, &types.EventTreasuryAdjusted{
		ProposalId: action.ProposalId, ItemIndex: action.ItemIndex, Amount: shared.NewAmount(amount),
		PurposeCode: action.PurposeCode, XRecipientOrEmpty: &types.EventTreasuryAdjusted_RecipientOrEmpty{RecipientOrEmpty: action.Recipient},
		TreasuryVersion: treasury.TreasuryVersion,
	})
	if err := k.sendModuleToAccount(cache, types.TreasuryModuleName, recipient, amount); err != nil {
		return TreasurySpendExecutionResult{}, err
	}
	write()
	return TreasurySpendExecutionResult{Receipt: receipt, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

func (k Keeper) applyTreasurySpendCaps(
	ctx context.Context,
	proposalID uint64,
	rewardEpoch uint64,
	recipient []byte,
	recipientAddress string,
	amount uint64,
	height uint64,
	cleanupHeight uint64,
	params types.TreasuryParamsV1,
) (uint64, error) {
	proposalCap, err := shared.ParseAmount(params.TreasurySpendLimitPerProposal)
	if err != nil {
		return 0, fmt.Errorf("treasury proposal spend cap: %w", err)
	}
	epochCap, err := shared.ParseAmount(params.TreasurySpendLimitPerEpoch)
	if err != nil {
		return 0, fmt.Errorf("treasury epoch spend cap: %w", err)
	}
	recipientCap, err := shared.ParseAmount(params.TreasurySpendLimitPerRecipient)
	if err != nil {
		return 0, fmt.Errorf("treasury recipient spend cap: %w", err)
	}

	proposal, err := k.TreasurySpendProposal.Get(ctx, proposalID)
	if errors.Is(err, collections.ErrNotFound) {
		proposal = types.TreasurySpendProposalState{
			ProposalId: proposalID, SpentAmount: shared.NewAmount(0), UpdatedHeight: height,
		}
	} else if err != nil {
		return 0, err
	} else if proposal.ProposalId != proposalID || proposal.ActiveReceiptCount == 0 {
		return 0, fmt.Errorf("treasury proposal accumulator is invalid")
	}
	proposalSpent, err := checkedTreasurySpendTotal("proposal", proposal.SpentAmount, amount, proposalCap)
	if err != nil {
		return 0, err
	}
	if proposal.ActiveReceiptCount == math.MaxUint32 {
		return 0, fmt.Errorf("treasury proposal receipt count overflows")
	}
	proposal.SpentAmount = shared.NewAmount(proposalSpent)
	proposal.ActiveReceiptCount++
	proposal.UpdatedHeight = height

	epoch, err := k.TreasurySpendEpoch.Get(ctx, rewardEpoch)
	newEpoch := errors.Is(err, collections.ErrNotFound)
	if newEpoch {
		epoch = types.TreasurySpendEpochState{
			RewardEpoch: rewardEpoch, SpentAmount: shared.NewAmount(0), CleanupHeight: cleanupHeight,
		}
	} else if err != nil {
		return 0, err
	} else if epoch.RewardEpoch != rewardEpoch || epoch.CleanupHeight <= height {
		return 0, fmt.Errorf("treasury epoch accumulator is invalid")
	}
	epochSpent, err := checkedTreasurySpendTotal("epoch", epoch.SpentAmount, amount, epochCap)
	if err != nil {
		return 0, err
	}

	recipientKey := types.NewTreasurySpendRecipientEpochKey(rewardEpoch, recipient)
	recipientState, err := k.getTreasuryRecipient(ctx, recipientKey)
	newRecipient := errors.Is(err, collections.ErrNotFound)
	if newRecipient {
		recipientState = types.TreasurySpendRecipientEpochState{
			RewardEpoch: rewardEpoch, RecipientAddress: recipientAddress, SpentAmount: shared.NewAmount(0),
		}
		if epoch.RecipientRowCount == math.MaxUint32 {
			return 0, fmt.Errorf("treasury epoch recipient count overflows")
		}
		epoch.RecipientRowCount++
	} else if err != nil {
		return 0, err
	} else if recipientState.RewardEpoch != rewardEpoch || recipientState.RecipientAddress != recipientAddress {
		return 0, fmt.Errorf("treasury recipient accumulator is invalid")
	}
	recipientSpent, err := checkedTreasurySpendTotal("recipient", recipientState.SpentAmount, amount, recipientCap)
	if err != nil {
		return 0, err
	}
	epoch.SpentAmount = shared.NewAmount(epochSpent)
	recipientState.SpentAmount = shared.NewAmount(recipientSpent)

	if err := k.TreasurySpendProposal.Set(ctx, proposalID, proposal); err != nil {
		return 0, err
	}
	if err := k.TreasurySpendEpoch.Set(ctx, rewardEpoch, epoch); err != nil {
		return 0, err
	}
	if err := k.storeTreasuryRecipient(ctx, recipientKey, recipientState); err != nil {
		return 0, err
	}
	if newEpoch {
		if err := k.TreasurySpendEpochCleanupIndex.Set(ctx, types.NewTreasurySpendEpochCleanupIndexKey(cleanupHeight, rewardEpoch)); err != nil {
			return 0, err
		}
	}
	return epoch.CleanupHeight, nil
}

func checkedTreasurySpendTotal(name string, current shared.Amount, amount, cap uint64) (uint64, error) {
	value, err := shared.ParseAmount(current)
	if err != nil {
		return 0, fmt.Errorf("treasury %s spend total: %w", name, err)
	}
	next, err := checkedAdd(value, amount)
	if err != nil {
		return 0, fmt.Errorf("treasury %s spend total overflow: %w", name, err)
	}
	if next > cap {
		return 0, fmt.Errorf("treasury %s spend cap exceeded", name)
	}
	return next, nil
}

func (k Keeper) validateTreasurySpendAction(
	ctx sdk.Context,
	execution AcceptedGovernanceActionContext,
	action types.ExecuteTreasurySpendV1,
) ([]byte, sdk.AccAddress, uint64, error) {
	if !execution.Accepted {
		return nil, nil, 0, fmt.Errorf("treasury spend requires an accepted governance action")
	}
	authority, _, err := k.requireCanonicalAddress("governance authority", execution.AuthorityAddress)
	if err != nil {
		return nil, nil, 0, err
	}
	if !bytes.Equal(authority, k.authority) {
		return nil, nil, 0, fmt.Errorf("treasury spend governance authority mismatch")
	}
	if action.ProposalId == 0 || execution.ProposalID != action.ProposalId || execution.ItemIndex != action.ItemIndex {
		return nil, nil, 0, fmt.Errorf("treasury spend action locator does not match governance execution context")
	}
	recipientBytes, _, err := k.requireCanonicalAddress("treasury spend recipient", action.Recipient)
	if err != nil {
		return nil, nil, 0, err
	}
	if bytes.Equal(recipientBytes, authtypes.NewModuleAddress(types.TreasuryModuleName)) {
		return nil, nil, 0, fmt.Errorf("treasury cannot spend to itself")
	}
	amount, err := shared.ParseAmount(action.Amount)
	if err != nil || amount == 0 {
		return nil, nil, 0, fmt.Errorf("treasury spend amount must be a positive canonical Amount")
	}
	switch action.PurposeCode {
	case types.TreasuryPurposeCode_TREASURY_PURPOSE_CODE_INFRASTRUCTURE,
		types.TreasuryPurposeCode_TREASURY_PURPOSE_CODE_SECURITY_RESPONSE,
		types.TreasuryPurposeCode_TREASURY_PURPOSE_CODE_GOVERNANCE_OPERATION:
	default:
		return nil, nil, 0, fmt.Errorf("treasury spend purpose_code is not in the V1 closed set")
	}
	if action.ExpiryHeight == 0 || action.NotBeforeHeight > action.ExpiryHeight {
		return nil, nil, 0, fmt.Errorf("treasury spend height window is invalid")
	}
	digest, err := treasurySpendActionDigest(
		ctx.ChainID(), action.ProposalId, action.ItemIndex, recipientBytes,
		action.Amount, action.PurposeCode, action.NotBeforeHeight, action.ExpiryHeight,
	)
	if err != nil {
		return nil, nil, 0, err
	}
	return digest, sdk.AccAddress(recipientBytes), amount, nil
}

// treasurySpendActionDigest is the TRUEOPEN_TREASURY_SPEND_V1 action digest x/gov and
// the Hub recomputation must agree on.
//
// Everything above it in validateTreasurySpendAction is authorisation and bounds
// checking, which is Hub policy; the eight framed fields below are the frozen §9.6c
// preimage. Separating them lets a golden vector state that preimage directly, which
// matters here because not_before_height and expiry_height are adjacent Uint64BE and
// proposal_id is a third one - a swap among them is invisible to any binding that
// only sees encoder classes.
//
// The amount stays a nested one-field frame carrying the decimal ASCII of
// atomic_units, not a Uint64BE; that is the frozen §4.4 Amount shape and building it
// here keeps the exception next to the only preimage that uses it.
func treasurySpendActionDigest(
	chainID string, proposalID uint64, itemIndex uint32, recipient []byte, amount shared.Amount,
	purposeCode types.TreasuryPurposeCode, notBeforeHeight, expiryHeight uint64,
) ([]byte, error) {
	amountFrame, err := shared.CanonicalAmountFrameV1(amount)
	if err != nil {
		return nil, err
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainTreasurySpendV1)).Raw(
		[]byte(chainID), shared.Uint64BE(proposalID), shared.Uint32BE(itemIndex),
		recipient,
	).Nested(amountFrame).Raw(
		shared.EnumBE(uint32(purposeCode)), shared.Uint64BE(notBeforeHeight), shared.Uint64BE(expiryHeight),
	).Sum()
}

// ProcessTreasurySpendReceiptPrunes visits each due derived index row at most
// once per call. A still-re-executable item is moved forward by one current
// replay-window step instead of being revisited every block.
func (k Keeper) ProcessTreasurySpendReceiptPrunes(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, nil
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	keys := make([]types.TreasurySpendReceiptPruneKeyTriple, 0, limit)
	iter, err := k.TreasurySpendReceiptPruneIndex.Iterate(cache, nil)
	if err != nil {
		return 0, err
	}
	for ; iter.Valid() && uint64(len(keys)) < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return 0, err
		}
		if key.K1() > currentHeight {
			break
		}
		keys = append(keys, key)
	}
	if err := iter.Close(); err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 0, nil
	}
	if k.governanceActionReplayChecker == nil || k.governanceActionReplayChecker.checker == nil {
		return 0, fmt.Errorf("treasury spend governance replay checker is unavailable")
	}
	params, err := k.Params.Get(cache)
	if err != nil {
		return 0, err
	}
	var visited uint64
	for _, indexKey := range keys {
		visited++
		primaryKey := types.NewTreasurySpendReceiptKey(indexKey.K2(), indexKey.K3())
		receipt, err := k.getTreasurySpendReceipt(cache, primaryKey)
		if errors.Is(err, collections.ErrNotFound) {
			if err := k.TreasurySpendReceiptPruneIndex.Remove(cache, indexKey); err != nil {
				return visited, err
			}
			continue
		}
		if err != nil {
			return visited, fmt.Errorf("treasury spend prune index references missing receipt: %w", err)
		}
		if receipt.ProposalId != indexKey.K2() || receipt.ItemIndex != indexKey.K3() || receipt.PruneHeight != indexKey.K1() ||
			receipt.Validate() != nil {
			return visited, fmt.Errorf("treasury spend prune index identity mismatch")
		}
		reExecutable, err := k.governanceActionReplayChecker.checker.IsGovernanceActionReExecutable(cache, receipt.ProposalId, receipt.ItemIndex)
		if err != nil {
			return visited, err
		}
		cleanupComplete, err := k.treasuryEpochCleanupComplete(cache, receipt.RewardEpoch)
		if err != nil {
			return visited, err
		}
		if err := k.TreasurySpendReceiptPruneIndex.Remove(cache, indexKey); err != nil {
			return visited, err
		}
		if reExecutable || !cleanupComplete {
			receipt.PruneHeight, err = checkedAdd(receipt.PruneHeight, params.Treasury.GovernanceActionReplayWindowBlocks)
			if err != nil {
				return visited, fmt.Errorf("treasury spend receipt reschedule overflow: %w", err)
			}
			if err := k.storeTreasurySpendReceipt(cache, primaryKey, receipt); err != nil {
				return visited, err
			}
			if err := k.TreasurySpendReceiptPruneIndex.Set(cache, types.NewTreasurySpendReceiptPruneKey(receipt.PruneHeight, receipt.ProposalId, receipt.ItemIndex)); err != nil {
				return visited, err
			}
			continue
		}
		if err := k.pruneTreasurySpendReceipt(cache, receipt, currentHeight); err != nil {
			return visited, err
		}
	}
	write()
	return visited, nil
}

func (k Keeper) treasuryEpochCleanupComplete(ctx context.Context, rewardEpoch uint64) (bool, error) {
	hasEpoch, err := k.TreasurySpendEpoch.Has(ctx, rewardEpoch)
	if err != nil {
		return false, err
	}
	hasCursor, err := k.TreasurySpendEpochCleanupCursor.Has(ctx, rewardEpoch)
	if err != nil {
		return false, err
	}
	return !hasEpoch && !hasCursor, nil
}

func (k Keeper) pruneTreasurySpendReceipt(ctx context.Context, receipt types.TreasurySpendReceiptState, currentHeight uint64) error {
	amount, err := shared.ParseAmount(receipt.Amount)
	if err != nil || amount == 0 {
		return fmt.Errorf("treasury spend receipt amount is invalid")
	}
	proposal, err := k.TreasurySpendProposal.Get(ctx, receipt.ProposalId)
	if err != nil {
		return fmt.Errorf("treasury spend receipt proposal accumulator is unavailable: %w", err)
	}
	spent, err := shared.ParseAmount(proposal.SpentAmount)
	if err != nil || proposal.ProposalId != receipt.ProposalId || proposal.ActiveReceiptCount == 0 || spent < amount {
		return fmt.Errorf("treasury spend proposal accumulator cannot retire receipt")
	}
	proposal.SpentAmount = shared.NewAmount(spent - amount)
	proposal.ActiveReceiptCount--
	proposal.UpdatedHeight = currentHeight
	if proposal.ActiveReceiptCount == 0 {
		if spent != amount {
			return fmt.Errorf("empty treasury proposal accumulator retains spend")
		}
		if err := k.TreasurySpendProposal.Remove(ctx, receipt.ProposalId); err != nil {
			return err
		}
	} else if err := k.TreasurySpendProposal.Set(ctx, receipt.ProposalId, proposal); err != nil {
		return err
	}
	return k.TreasurySpendReceipt.Remove(ctx, types.NewTreasurySpendReceiptKey(receipt.ProposalId, receipt.ItemIndex))
}

// ProcessTreasurySpendEpochCleanups advances recipient cleanup before receipt
// pruning at the same height. Starting a cursor, deleting one recipient and
// closing a cursor each consume one visited unit.
func (k Keeper) ProcessTreasurySpendEpochCleanups(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, nil
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	var visited uint64
	for visited < limit {
		rewardEpoch, cursor, found, err := k.firstTreasurySpendCleanupCursor(cache)
		if err != nil {
			return visited, err
		}
		if found {
			if cursor.CleanupHeight > currentHeight {
				return visited, fmt.Errorf("treasury cleanup cursor started before its cleanup height")
			}
			if err := k.processTreasurySpendCleanupCursorStep(cache, rewardEpoch, cursor); err != nil {
				return visited, err
			}
			visited++
			continue
		}

		indexKey, found, err := k.firstDueTreasurySpendCleanupIndex(cache, currentHeight)
		if err != nil || !found {
			if err != nil {
				return visited, err
			}
			break
		}
		epoch, err := k.TreasurySpendEpoch.Get(cache, indexKey.K2())
		if errors.Is(err, collections.ErrNotFound) {
			if err := k.TreasurySpendEpochCleanupIndex.Remove(cache, indexKey); err != nil {
				return visited, err
			}
			visited++
			continue
		}
		if err != nil || epoch.RewardEpoch != indexKey.K2() || epoch.CleanupHeight != indexKey.K1() {
			return visited, fmt.Errorf("treasury cleanup index does not match its epoch state")
		}
		cursor = types.TreasurySpendEpochCleanupCursorState{
			RewardEpoch: epoch.RewardEpoch, CleanupHeight: epoch.CleanupHeight,
		}
		if err := k.TreasurySpendEpochCleanupIndex.Remove(cache, indexKey); err != nil {
			return visited, err
		}
		if err := k.storeTreasuryCleanupCursor(cache, cursor); err != nil {
			return visited, err
		}
		visited++
	}
	if visited != 0 {
		write()
	}
	return visited, nil
}

func (k Keeper) firstTreasurySpendCleanupCursor(ctx context.Context) (uint64, types.TreasurySpendEpochCleanupCursorState, bool, error) {
	iter, err := k.TreasurySpendEpochCleanupCursor.Iterate(ctx, nil)
	if err != nil {
		return 0, types.TreasurySpendEpochCleanupCursorState{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return 0, types.TreasurySpendEpochCleanupCursorState{}, false, nil
	}
	entry, err := iter.KeyValue()
	if err != nil {
		return 0, types.TreasurySpendEpochCleanupCursorState{}, false, err
	}
	state, err := k.treasuryCleanupCursorStorePublicProjection(entry.Value)
	return entry.Key, state, err == nil, err
}

func (k Keeper) firstDueTreasurySpendCleanupIndex(ctx context.Context, currentHeight uint64) (types.TreasurySpendEpochCleanupIndexKeyPair, bool, error) {
	iter, err := k.TreasurySpendEpochCleanupIndex.Iterate(ctx, nil)
	if err != nil {
		return types.TreasurySpendEpochCleanupIndexKeyPair{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.TreasurySpendEpochCleanupIndexKeyPair{}, false, nil
	}
	key, err := iter.Key()
	if err != nil || key.K1() > currentHeight {
		return types.TreasurySpendEpochCleanupIndexKeyPair{}, false, err
	}
	return key, true, nil
}

func (k Keeper) processTreasurySpendCleanupCursorStep(
	ctx context.Context,
	rewardEpoch uint64,
	cursor types.TreasurySpendEpochCleanupCursorState,
) error {
	if cursor.RewardEpoch != rewardEpoch {
		return fmt.Errorf("treasury cleanup cursor key and state disagree")
	}
	epoch, err := k.TreasurySpendEpoch.Get(ctx, rewardEpoch)
	if err != nil || epoch.CleanupHeight != cursor.CleanupHeight {
		return fmt.Errorf("treasury cleanup cursor has no matching epoch state")
	}
	rng := collections.NewPrefixedPairRange[uint64, []byte](rewardEpoch)
	if cursor.XLastRecipientAddress != nil {
		last, err := k.addressCodec.StringToBytes(cursor.GetLastRecipientAddress())
		if err != nil {
			return fmt.Errorf("treasury cleanup cursor address is invalid: %w", err)
		}
		rng = rng.StartExclusive(last)
	}
	iter, err := k.TreasurySpendRecipientEpoch.Iterate(ctx, rng)
	if err != nil {
		return err
	}
	defer iter.Close()
	if !iter.Valid() {
		if cursor.DeletedCount != uint64(epoch.RecipientRowCount) {
			return fmt.Errorf("treasury cleanup deleted count does not match epoch recipient count")
		}
		if err := k.TreasurySpendEpoch.Remove(ctx, rewardEpoch); err != nil {
			return err
		}
		return k.TreasurySpendEpochCleanupCursor.Remove(ctx, rewardEpoch)
	}
	entry, err := iter.KeyValue()
	if err != nil {
		return err
	}
	recipientState, err := k.treasuryRecipientStorePublicProjection(entry.Value)
	if err != nil || entry.Key.K1() != rewardEpoch || !bytes.Equal(entry.Key.K2(), entry.Value.RecipientAddress) || entry.Value.RewardEpoch != rewardEpoch {
		return fmt.Errorf("treasury recipient accumulator key and state disagree")
	}
	cursor.VisitedCount, err = checkedAdd(cursor.VisitedCount, 1)
	if err != nil {
		return fmt.Errorf("treasury cleanup visited count overflow: %w", err)
	}
	cursor.DeletedCount, err = checkedAdd(cursor.DeletedCount, 1)
	if err != nil {
		return fmt.Errorf("treasury cleanup deleted count overflow: %w", err)
	}
	cursor.XLastRecipientAddress = &types.TreasurySpendEpochCleanupCursorState_LastRecipientAddress{
		LastRecipientAddress: recipientState.RecipientAddress,
	}
	if err := k.TreasurySpendRecipientEpoch.Remove(ctx, entry.Key); err != nil {
		return err
	}
	return k.storeTreasuryCleanupCursor(ctx, cursor)
}

// EnsureTreasurySpendReceiptInvariant checks both directions of the derived
// prune index. It is used by Genesis export and can be called by app audits.
func (k Keeper) EnsureTreasurySpendReceiptInvariant(ctx context.Context) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	treasury, err := k.Treasury.Get(ctx)
	if err != nil {
		return err
	}
	versions := make(map[uint64]struct{})
	digests := make(map[string]struct{})
	receipts, err := k.TreasurySpendReceipt.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; receipts.Valid(); receipts.Next() {
		entry, err := receipts.KeyValue()
		if err != nil {
			receipts.Close()
			return err
		}
		row, err := k.treasurySpendReceiptStorePublicProjection(entry.Value)
		if err != nil {
			receipts.Close()
			return err
		}
		if entry.Key.K1() != row.ProposalId || entry.Key.K2() != row.ItemIndex || row.ProposalId == 0 ||
			row.ExecutedHeight == 0 || row.PruneHeight <= row.ExecutedHeight || len(row.ActionDigest) != 32 ||
			row.TreasuryVersionAfter == 0 || row.TreasuryVersionAfter > treasury.TreasuryVersion {
			receipts.Close()
			return fmt.Errorf("treasury spend receipt primary/state mismatch")
		}
		if _, duplicate := versions[row.TreasuryVersionAfter]; duplicate {
			receipts.Close()
			return fmt.Errorf("duplicate treasury spend version %d", row.TreasuryVersionAfter)
		}
		versions[row.TreasuryVersionAfter] = struct{}{}
		digestKey := string(row.ActionDigest)
		if _, duplicate := digests[digestKey]; duplicate {
			receipts.Close()
			return fmt.Errorf("duplicate treasury spend action digest")
		}
		digests[digestKey] = struct{}{}
		has, err := k.TreasurySpendReceiptPruneIndex.Has(ctx, types.NewTreasurySpendReceiptPruneKey(row.PruneHeight, row.ProposalId, row.ItemIndex))
		if err != nil || !has {
			receipts.Close()
			if err != nil {
				return err
			}
			return fmt.Errorf("treasury spend receipt is missing its prune index")
		}
	}
	if err := receipts.Close(); err != nil {
		return err
	}
	indexes, err := k.TreasurySpendReceiptPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; indexes.Valid(); indexes.Next() {
		key, err := indexes.Key()
		if err != nil {
			return err
		}
		row, err := k.getTreasurySpendReceipt(ctx, types.NewTreasurySpendReceiptKey(key.K2(), key.K3()))
		if err != nil {
			return fmt.Errorf("treasury spend prune index references missing receipt: %w", err)
		}
		if row.PruneHeight != key.K1() || row.ProposalId != key.K2() || row.ItemIndex != key.K3() {
			return fmt.Errorf("treasury spend prune index/receipt mismatch")
		}
	}
	if err := indexes.Close(); err != nil {
		return err
	}

	proposalRows, err := collectMapValues[uint64, types.TreasurySpendProposalState](ctx, k.TreasurySpendProposal)
	if err != nil {
		return err
	}
	epochRows, err := collectMapValues[uint64, types.TreasurySpendEpochState](ctx, k.TreasurySpendEpoch)
	if err != nil {
		return err
	}
	recipientRows, err := k.exportTreasuryRecipientEpochs(ctx)
	if err != nil {
		return err
	}
	cursorRows, err := k.exportTreasuryCleanupCursors(ctx)
	if err != nil {
		return err
	}
	receiptRows, err := k.exportTreasurySpendReceipts(ctx)
	if err != nil {
		return err
	}
	if err := types.ValidateTreasuryGenesisState(
		params, treasury, receiptRows, proposalRows, epochRows, recipientRows, cursorRows,
	); err != nil {
		return err
	}
	bankBalance := k.bankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(types.TreasuryModuleName), params.Phase0.BusinessDenom)
	logicalBalance, err := shared.ParseAmount(treasury.Balance)
	if err != nil || bankBalance.Denom != params.Phase0.BusinessDenom || !bankBalance.Amount.IsUint64() ||
		bankBalance.Amount.Uint64() != logicalBalance {
		return fmt.Errorf("treasury logical balance does not match its module account")
	}

	for _, epoch := range epochRows {
		hasCursor, err := k.TreasurySpendEpochCleanupCursor.Has(ctx, epoch.RewardEpoch)
		if err != nil {
			return err
		}
		hasIndex, err := k.TreasurySpendEpochCleanupIndex.Has(
			ctx, types.NewTreasurySpendEpochCleanupIndexKey(epoch.CleanupHeight, epoch.RewardEpoch),
		)
		if err != nil || hasCursor == hasIndex {
			return fmt.Errorf("treasury epoch must have exactly one cleanup index or cursor")
		}
	}
	cleanupIndexes, err := k.TreasurySpendEpochCleanupIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer cleanupIndexes.Close()
	for ; cleanupIndexes.Valid(); cleanupIndexes.Next() {
		key, err := cleanupIndexes.Key()
		if err != nil {
			return err
		}
		epoch, err := k.TreasurySpendEpoch.Get(ctx, key.K2())
		if err != nil || epoch.RewardEpoch != key.K2() || epoch.CleanupHeight != key.K1() {
			return fmt.Errorf("treasury cleanup index does not match an epoch state")
		}
		if has, err := k.TreasurySpendEpochCleanupCursor.Has(ctx, key.K2()); err != nil || has {
			return fmt.Errorf("treasury cleanup index and cursor overlap")
		}
	}

	recipientIter, err := k.TreasurySpendRecipientEpoch.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer recipientIter.Close()
	for ; recipientIter.Valid(); recipientIter.Next() {
		entry, err := recipientIter.KeyValue()
		if err != nil {
			return err
		}
		if entry.Key.K1() != entry.Value.RewardEpoch || !bytes.Equal(entry.Key.K2(), entry.Value.RecipientAddress) {
			return fmt.Errorf("treasury recipient accumulator key and state disagree")
		}
		if _, err := k.treasuryRecipientStorePublicProjection(entry.Value); err != nil {
			return fmt.Errorf("treasury recipient accumulator value is invalid: %w", err)
		}
		if cursor, err := k.getTreasuryCleanupCursor(ctx, entry.Value.RewardEpoch); err == nil && cursor.XLastRecipientAddress != nil {
			last, err := k.addressCodec.StringToBytes(cursor.GetLastRecipientAddress())
			if err != nil || bytes.Compare(entry.Value.RecipientAddress, last) <= 0 {
				return fmt.Errorf("treasury cleanup cursor retains an already-visited recipient")
			}
		} else if err != nil && !errors.Is(err, collections.ErrNotFound) {
			return err
		}
	}
	return nil
}
