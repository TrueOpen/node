package keeper

import (
	"context"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

const (
	// InvariantEscrowReserved is the first of the two funding equations that
	// the data-structure contract (lines 845-846) declares the *only* authoritative
	// money statements of this module:
	//
	//	bank.balance(trueopen_escrow) == sum(TaskBudgetState.reserved_amount
	//	                                  where budget_status = RESERVED)
	InvariantEscrowReserved = "escrow_reserved"

	// InvariantSessionOpenPending is the second one:
	//
	//	StreamState.open_pending_count == count(TaskBudgetState
	//	    where budget_status = RESERVED and the budget belongs to that session)
	//
	// The session binding is not duplicated on TaskBudgetState (Ruling 23 keys it by
	// task_id alone), so it is resolved through the single authoritative
	// TaskCoreState.session_id.
	InvariantSessionOpenPending = "session_open_pending_count"

	// InvariantDeadlineIndexNonZeroHeight covers every height-keyed Task deadline
	// index.
	InvariantDeadlineIndexNonZeroHeight = "deadline_index_nonzero_height"
)

// Ruling 23 / §1.2: the fresh V1 store has no migration surface. The former
// store_schema_current invariant read StateVersionState + StoreMigrationState,
// both of which are deleted collections, so it is gone rather than stubbed.

type InvariantCheck struct {
	Name, Description string
	Check             func(context.Context) error
}

func (k Keeper) InvariantChecks() []InvariantCheck {
	return []InvariantCheck{
		{InvariantEscrowReserved, "task escrow balance equals the sum of RESERVED task budgets", k.EnsureEscrowReservedInvariant},
		{InvariantSessionOpenPending, "every stream open_pending_count equals its RESERVED task budget count", k.EnsureSessionOpenPendingInvariant},
		{InvariantDeadlineIndexNonZeroHeight, "no deadline index row sits at height 0", k.EnsureDeadlineIndexNonZeroHeightInvariant},
	}
}

// EnsureDeadlineIndexNonZeroHeightInvariant asserts the one property of the
// deadline indexes that no correct writer can ever violate: addDeadlineIndex
// refuses height 0, because a row at height 0 is due at every height and the
// sweep would re-select it for the life of the chain. N-18 found sixteen Genesis
// rebuild sites that bypassed that floor.
//
// Deliberately narrow. "The row's Task primary still exists" is the other
// tempting assertion, but this list is registered with x/crisis and can halt the
// chain, and the ordering between retiring an index row and removing a compacted
// primary is a per-path lifecycle detail rather than a stated contract rule. An
// invariant that halts on an inferred property is worse than no invariant.
func (k Keeper) EnsureDeadlineIndexNonZeroHeightInvariant(ctx context.Context) error {
	sets := []struct {
		name string
		set  collections.KeySet[types.DeadlineIndexKey]
	}{
		{"assignment_randomness_index", k.AssignmentRandomnessIndex},
		{"infer_deadline_index", k.InferDeadlineIndex},
		{"verify_open_deadline_index", k.VerifyOpenDeadlineIndex},
		{"commit_deadline_index", k.CommitDeadlineIndex},
		{"reveal_deadline_index", k.RevealDeadlineIndex},
		{"verify_deadline_index", k.VerifyDeadlineIndex},
		{"settlement_deadline_index", k.SettlementDeadlineIndex},
		{"challenge_window_close_index", k.ChallengeWindowCloseIndex},
		{"evidence_cleanup_index", k.EvidenceCleanupIndex},
		{"task_terminal_summary_prune_index", k.TaskTerminalSummaryPruneIndex},
	}
	for _, entry := range sets {
		iter, err := entry.set.Iterate(ctx, nil)
		if err != nil {
			return err
		}
		for ; iter.Valid(); iter.Next() {
			key, err := iter.Key()
			if err != nil {
				iter.Close()
				return err
			}
			if key.K1() == 0 {
				iter.Close()
				return errorsmod.Wrapf(types.ErrInvariantBroken,
					"%s holds a row at height 0, which is due at every height", entry.name)
			}
		}
		if err := iter.Close(); err != nil {
			return err
		}
	}
	return nil
}

// EnsureCurrentStoreSchema is retained as the module bootstrap hook only. Fresh
// genesis has no schema/migration state to compare against, so the check now
// only asserts that the module has been initialized at all.
func (k Keeper) EnsureCurrentStoreSchema(ctx context.Context) error {
	if _, err := k.Params.Get(ctx); err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "task params are missing; the store was never initialized from genesis")
	}
	return nil
}

// reservedTaskBudgetTotals walks TaskBudget once and returns both the global
// RESERVED total and the per-session RESERVED row count.
//
// The per-session map is keyed by hex32(session_id), not by the raw store key: a
// []byte cannot be a Go map key, and the hex form is also what the invariant
// messages below print. It is an in-memory aggregation key only — nothing here
// reaches the store with it.
func (k Keeper) reservedTaskBudgetTotals(ctx context.Context) (uint64, map[string]uint32, error) {
	total := uint64(0)
	perSession := map[string]uint32{}

	iter, err := k.TaskBudget.Iterate(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		taskKey, err := iter.Key()
		if err != nil {
			return 0, nil, err
		}
		state, err := iter.Value()
		if err != nil {
			return 0, nil, err
		}
		if state.BudgetStatus != types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED {
			continue
		}
		reserved, err := shared.ParseAmount(state.ReservedAmount)
		if err != nil {
			return 0, nil, errorsmod.Wrapf(types.ErrInvariantBroken, "task %s reserved_amount is not canonical", hex32(taskKey))
		}
		next, overflow := checkedAddUint64(total, reserved)
		if overflow {
			return 0, nil, errorsmod.Wrap(types.ErrInvariantBroken, "reserved task budget total overflow")
		}
		total = next

		core, err := k.TaskCore.Get(ctx, taskKey)
		if err != nil {
			return 0, nil, errorsmod.Wrapf(types.ErrInvariantBroken, "task %s has a RESERVED budget without a TaskCore row", hex32(taskKey))
		}
		sessionKey, err := sessionStoreKey(core.SessionId)
		if err != nil {
			return 0, nil, err
		}
		sessionHex := hex32(sessionKey)
		if perSession[sessionHex] == ^uint32(0) {
			return 0, nil, errorsmod.Wrap(types.ErrInvariantBroken, "session RESERVED budget count overflow")
		}
		perSession[sessionHex]++
	}
	return total, perSession, nil
}

// EnsureEscrowReservedInvariant is §6.2 line 845.
func (k Keeper) EnsureEscrowReservedInvariant(ctx context.Context) error {
	total, _, err := k.reservedTaskBudgetTotals(ctx)
	if err != nil {
		return err
	}
	denom := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).BusinessDenom
	if denom == "" {
		return errorsmod.Wrap(types.ErrInvariantBroken, "business denom is unavailable")
	}
	actual := k.bankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(shared.TaskEscrowModuleName), denom).Amount
	if !actual.Equal(sdkmath.NewIntFromUint64(total)) {
		return errorsmod.Wrapf(types.ErrInvariantBroken, "task escrow balance %s does not match the RESERVED task budget total %d", actual, total)
	}
	return nil
}

// EnsureSessionOpenPendingInvariant is §6.2 line 846. It is bidirectional: a
// stream whose counter disagrees with its RESERVED rows fails, and a RESERVED
// budget whose session has no stream row fails too (that combination is what
// would let a CLOSED session leave frozen funds behind).
func (k Keeper) EnsureSessionOpenPendingInvariant(ctx context.Context) error {
	_, perSession, err := k.reservedTaskBudgetTotals(ctx)
	if err != nil {
		return err
	}

	iter, err := k.Stream.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		sessionKey, err := iter.Key()
		if err != nil {
			return err
		}
		stored, err := iter.Value()
		if err != nil {
			return err
		}
		stream, err := k.ProjectStreamStore(stored)
		if err != nil {
			return err
		}
		sessionHex := hex32(sessionKey)
		reservedRows := perSession[sessionHex]
		if stream.OpenPendingCount != reservedRows {
			return errorsmod.Wrapf(types.ErrInvariantBroken,
				"session %s open_pending_count %d does not match its %d RESERVED task budgets",
				sessionHex, stream.OpenPendingCount, reservedRows)
		}
		// §6.2 line 851: IDLE/CLOSED requires open_pending_count == 0.
		if stream.OpenPendingCount != 0 && stream.Status != types.SessionStatus_SESSION_STATUS_ACTIVE {
			return errorsmod.Wrapf(types.ErrInvariantBroken,
				"session %s is %s with %d in-flight orders", sessionHex, stream.Status, stream.OpenPendingCount)
		}
		delete(perSession, sessionHex)
	}
	for sessionHex, reservedRows := range perSession {
		return errorsmod.Wrapf(types.ErrInvariantBroken,
			"session %s has %d RESERVED task budgets but no stream row", sessionHex, reservedRows)
	}
	return nil
}
