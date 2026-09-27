package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"sort"

	storetypes "cosmossdk.io/store/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

const taskGasIntentPrefix byte = 1

type taskGasReimbursementIntent struct {
	taskID    []byte
	itemIndex uint32
	kind      types.TaskReimbursementKindV1
}

// recordTaskGasReimbursementIntent marks one first-APPLIED maintenance action.
// Direct keeper tests do not carry TxBytes and intentionally skip the post-hook;
// every BaseApp execution carries the canonical transaction bytes.
func (k Keeper) recordTaskGasReimbursementIntent(
	ctx context.Context,
	taskID []byte,
	itemIndex uint32,
	kind types.TaskReimbursementKindV1,
) error {
	if len(taskID) != types.Hash32Len || kind < types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_WORKER_HANDRAISE ||
		kind > types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_RESULT {
		return fmt.Errorf("task gas reimbursement intent is invalid")
	}
	txBytes := sdkContextFrom(ctx).TxBytes()
	if len(txBytes) == 0 {
		return nil
	}
	txHash := sha256.Sum256(txBytes)
	key := taskGasIntentKey(txHash[:], taskID, itemIndex)
	value := make([]byte, 4)
	binary.BigEndian.PutUint32(value, uint32(kind))
	store := k.transientStoreService.OpenTransientStore(ctx)
	existing, err := store.Get(key)
	if err != nil {
		return err
	}
	if existing != nil {
		if bytes.Equal(existing, value) {
			return nil
		}
		return fmt.Errorf("conflicting task gas reimbursement intent")
	}
	return store.Set(key, value)
}

// ApplyTaskGasReimbursementIntents is called once by the application post
// handler after a successful single-Task-Msg transaction. It divides batch gas
// and fee pools by original item index and commits receipts atomically.
func (k Keeper) ApplyTaskGasReimbursementIntents(
	ctx context.Context,
	txHash []byte,
	feePayer string,
	gasBasis uint64,
	actualFeePaid uint64,
) error {
	if len(txHash) != types.Hash32Len {
		return fmt.Errorf("task gas reimbursement post context is invalid")
	}
	intents, err := k.loadTaskGasReimbursementIntents(ctx, txHash)
	if err != nil || len(intents) == 0 {
		return err
	}
	if gasBasis == 0 {
		return fmt.Errorf("task gas reimbursement gas basis is zero")
	}
	if _, _, err := k.canonicalAddress("fee_payer", feePayer); err != nil {
		return err
	}
	// §10.10b computes ONE Tx-level reimbursable pool and then splits it:
	//
	//   necessary_fee = ceil_div_u128(gas_basis * num / den)   -- one ceil, u128
	//   pool          = min(actual_fee_paid, necessary_fee, per_tx_cap)
	//   share_i       = pool/n, remainder distributed +1 by ascending item_index
	//   amount_i      = min(share_i, per_task_cap_remaining_i, tx_fee_reserve_i)
	//
	// The previous shape split gas_basis and actual_fee_paid across items first,
	// which broke the identity three ways: the ceil ran n times instead of once,
	// each receipt committed to its own share rather than the Tx totals the frame
	// is defined over, and — the funds bug — per_tx_cap was applied inside the
	// per-item min, so an n-item batch could draw n x per_tx_cap out of escrow.
	//
	// per_task_cap_remaining and tx_fee_reserve stay per-item with no
	// redistribution, exactly as the contract says.
	count := uint64(len(intents))
	necessary := uint64(math.MaxUint64)
	perTxCap := uint64(math.MaxUint64)
	for _, intent := range intents {
		// Only the two immutable, admission-frozen terms are read here. The mutable
		// halves — tx_fee_reserve_remaining and gas_reimbursed_total — are
		// deliberately NOT snapshotted: two items of the same batch may target the
		// same Task (loadTaskGasReimbursementIntents only requires item_index to be
		// unique), and a snapshot would make the second item debit a reserve the
		// first item had already spent, then overwrite the first item's write.
		budget, err := k.TaskBudget.Get(ctx, types.NewTaskKey(intent.taskID))
		if err != nil || budget.BudgetStatus != types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED {
			return fmt.Errorf("task gas reimbursement budget is unavailable")
		}
		rate := budget.MaxReimbursableFeePerGasSnapshot
		itemNecessary, err := ceilMulDivTaskGas(gasBasis, rate.Numerator, rate.Denominator)
		if err != nil {
			return err
		}
		itemCap, err := shared.ParseAmount(budget.MaxReimbursementPerTxSnapshot)
		if err != nil {
			return err
		}
		// CONTRACT-GAP: necessary_fee and per_tx_cap are Tx-level terms but both
		// are frozen per Task, so a batch spanning two Tasks has two candidate
		// values and §10.10b does not say which one governs. The minimum is taken
		// because it is the only choice that cannot over-reimburse under either
		// reading; a single-Task batch — the only shape Phase 0 actually produces —
		// is unaffected either way.
		necessary = min(necessary, itemNecessary)
		perTxCap = min(perTxCap, itemCap)
	}
	pool := min(actualFeePaid, necessary, perTxCap)
	quotient, remainder := pool/count, pool%count
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return err
	}
	for index, intent := range intents {
		share := quotient
		if uint64(index) < remainder {
			share++
		}
		if err := k.applyTaskGasReimbursement(ctx, txHash, feePayer, gasBasis, actualFeePaid,
			necessary, share, intent, height); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) loadTaskGasReimbursementIntents(ctx context.Context, txHash []byte) ([]taskGasReimbursementIntent, error) {
	prefix := append([]byte{taskGasIntentPrefix}, txHash...)
	store := k.transientStoreService.OpenTransientStore(ctx)
	iterator, err := store.Iterator(prefix, storetypes.PrefixEndBytes(prefix))
	if err != nil {
		return nil, err
	}
	defer iterator.Close()
	intents := make([]taskGasReimbursementIntent, 0)
	for ; iterator.Valid(); iterator.Next() {
		key, value := iterator.Key(), iterator.Value()
		if len(key) != 1+types.Hash32Len+types.Hash32Len+4 || len(value) != 4 {
			return nil, fmt.Errorf("task gas reimbursement intent encoding is invalid")
		}
		kind := types.TaskReimbursementKindV1(binary.BigEndian.Uint32(value))
		if kind < types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_WORKER_HANDRAISE ||
			kind > types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_RESULT {
			return nil, fmt.Errorf("task gas reimbursement intent kind is invalid")
		}
		intents = append(intents, taskGasReimbursementIntent{
			taskID:    append([]byte(nil), key[1+types.Hash32Len:1+2*types.Hash32Len]...),
			itemIndex: binary.BigEndian.Uint32(key[len(key)-4:]), kind: kind,
		})
	}
	sort.Slice(intents, func(i, j int) bool {
		if intents[i].itemIndex != intents[j].itemIndex {
			return intents[i].itemIndex < intents[j].itemIndex
		}
		return bytes.Compare(intents[i].taskID, intents[j].taskID) < 0
	})
	for index := 1; index < len(intents); index++ {
		if intents[index-1].itemIndex == intents[index].itemIndex {
			return nil, fmt.Errorf("task gas reimbursement batch repeats item_index")
		}
	}
	return intents, nil
}

func (k Keeper) applyTaskGasReimbursement(
	ctx context.Context,
	txHash []byte,
	feePayer string,
	gasBasis, actualFeePaid, necessary, share uint64,
	intent taskGasReimbursementIntent,
	height uint64,
) error {
	taskKey := types.NewTaskKey(intent.taskID)
	// Re-read rather than reuse the pre-pass snapshot: the reserve and the
	// running total this function debits are exactly what an earlier item of the
	// same batch may already have moved.
	budget, err := k.TaskBudget.Get(ctx, taskKey)
	if err != nil || budget.BudgetStatus != types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED {
		return fmt.Errorf("task gas reimbursement budget is unavailable")
	}
	key := types.NewTaskGasReimbursementKey(taskKey, txHash, intent.itemIndex)
	if _, err := k.ReadGasReimbursement(ctx, key); err == nil {
		return fmt.Errorf("task gas reimbursement receipt already exists without replay intent")
	} else if !errIsNotFound(err) {
		return err
	}
	perTaskCap, parseErr := shared.ParseAmount(budget.MaxReimbursementPerTaskSnapshot)
	if parseErr != nil {
		return parseErr
	}
	already, err := shared.ParseAmount(budget.GasReimbursedTotal)
	if err != nil || already > perTaskCap {
		return fmt.Errorf("task gas reimbursement total is invalid")
	}
	reserve, err := shared.ParseAmount(budget.TxFeeReserveRemaining)
	if err != nil {
		return err
	}
	// The Tx-level terms are already folded into `share`; only this Task's own
	// remaining cap and reserve truncate it further, and the truncated remainder
	// is not redistributed to the other items.
	amount := min(share, perTaskCap-already, reserve)
	receipt := types.TaskGasReimbursementV1{
		TaskId: append([]byte(nil), intent.taskID...), TxHash: append([]byte(nil), txHash...), ItemIndex: intent.itemIndex,
		ReimbursementKind: intent.kind, FeePayer: feePayer, GasBasis: gasBasis,
		ActualFeePaid: shared.NewAmount(actualFeePaid), NecessaryFee: shared.NewAmount(necessary),
		ReimbursedAmount: shared.NewAmount(amount), FeePolicyVersion: budget.FeePolicyVersionSnapshot,
		AcceptedHeight: height,
	}
	if err := k.WriteGasReimbursement(ctx, key, receipt); err != nil {
		return err
	}
	budget.TxFeeReserveRemaining = shared.NewAmount(reserve - amount)
	nextTotal, overflow := checkedAddUint64(already, amount)
	if overflow || budget.GasReimbursementCount == math.MaxUint32 {
		return fmt.Errorf("task gas reimbursement accounting overflows")
	}
	budget.GasReimbursedTotal = shared.NewAmount(nextTotal)
	budget.GasReimbursementCount++
	if err := k.TaskBudget.Set(ctx, taskKey, budget); err != nil {
		return err
	}
	return emitTypedEvent(ctx, &types.EventTaskGasReimbursementRecorded{
		TaskId: intent.taskID, TxHash: txHash, ItemIndex: intent.itemIndex,
		ReimbursementKind: intent.kind, FeePayer: feePayer, GasBasis: gasBasis,
		ReimbursedAmount: shared.NewAmount(amount),
	})
}

func taskGasIntentKey(txHash, taskID []byte, itemIndex uint32) []byte {
	key := make([]byte, 1+types.Hash32Len+types.Hash32Len+4)
	key[0] = taskGasIntentPrefix
	copy(key[1:], txHash)
	copy(key[1+types.Hash32Len:], taskID)
	binary.BigEndian.PutUint32(key[len(key)-4:], itemIndex)
	return key
}

func ceilMulDivTaskGas(left, right, denominator uint64) (uint64, error) {
	if denominator == 0 {
		return 0, fmt.Errorf("gas reimbursement rate denominator is zero")
	}
	hi, lo := bits.Mul64(left, right)
	if hi >= denominator {
		return 0, fmt.Errorf("gas reimbursement necessary fee overflows")
	}
	quotient, remainder := bits.Div64(hi, lo, denominator)
	if remainder != 0 {
		if quotient == math.MaxUint64 {
			return 0, fmt.Errorf("gas reimbursement necessary fee overflows")
		}
		quotient++
	}
	return quotient, nil
}
