package keeper

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	errorsmod "cosmossdk.io/errors"

	"github.com/TrueOpen/wire/bus"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// applyDataUnavailableBuilderFaults is the chain-owned half of the tag 5 rule.
//
// The commit deadline only retained CONFIRMED aggregates; nothing was charged
// there. This runner is where the round's availability faults are actually
// spent, and it runs inside the same settlement/finality cache transaction as
// the accounting, the refund and task_finality_height, so a settlement that
// fails afterwards charges no Builder either.
//
// The walk is bounded by the frozen Task Builder selection and ordered by
// builder_operator address-codec bytes, which is the same ordering the fault
// kernel and every digest already frame identities in. Ordering matters because
// each apply mutates a Builder counter, so a map-iteration order would make the
// resulting state non-deterministic across nodes.
func (k Keeper) applyDataUnavailableBuilderFaults(ctx context.Context, core types.TaskCoreState) error {
	taskKey := types.NewTaskKey(core.TaskId)
	selection, err := k.loadActiveTaskBuilderSelection(ctx, taskKey, core.TaskId)
	if errIsNotFound(err) {
		// No frozen selection means the commit deadline could not have written a
		// single aggregate, so there is nothing this round can have confirmed.
		return nil
	}
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	// A Task that never finalized an OPEN_VERIFY stage has no data-ready
	// attestation bitmap, and §5.5 charges only Builders inside that bitmap. The
	// absent row is therefore an empty attester set, not a broken invariant: a
	// Worker self-rescue and a Task that failed before OPEN_VERIFY both settle
	// through here and must charge nobody.
	attested, err := k.dataReadyAttesterSet(ctx, taskKey, core.TaskId, selection)
	if errIsNotFound(err) {
		return nil
	}
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}

	type faultedBuilder struct {
		operator    string
		codecPrefix []byte
	}
	faulted := make([]faultedBuilder, 0, len(selection.SelectedTaskBuilders))
	for index, builder := range selection.SelectedTaskBuilders {
		if !attested[index] {
			continue
		}
		aggregate, err := k.ReadDataUnavailableAggregate(
			ctx, types.NewVerifyActorKey(taskKey, types.VerifyRoundV1, builder),
		)
		if errIsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if aggregate.Status != types.BuilderDataUnavailableAggregateStatusV1_BUILDER_DATA_UNAVAILABLE_AGGREGATE_STATUS_V1_CONFIRMED {
			continue
		}
		codecPrefix, err := k.addressCodec.StringToBytes(builder)
		if err != nil {
			return errorsmod.Wrap(types.ErrInvariantBroken, fmt.Sprintf("selected Task Builder %q is not decodable", builder))
		}
		faulted = append(faulted, faultedBuilder{operator: builder, codecPrefix: codecPrefix})
	}
	sort.Slice(faulted, func(i, j int) bool {
		return bytes.Compare(faulted[i].codecPrefix, faulted[j].codecPrefix) < 0
	})

	for _, builder := range faulted {
		// The fact is rebuilt through the same constructor the public entry point
		// uses. That is what makes a later MsgSubmitBuilderEvidence for this Task an
		// exact replay that returns NOOP instead of a second, later-timed charge.
		fact, err := k.canonicalBuilderDataUnavailable(ctx, bus.DataUnavailableStateReferenceV1{
			TaskID:          append([]byte(nil), core.TaskId...),
			VerifyRound:     types.VerifyRoundV1,
			BuilderOperator: builder.operator,
		})
		if err != nil {
			return errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
		receipt, err := k.hubKeeper.ApplyBuilderObjectiveEvidence(ctx, fact)
		if err != nil {
			return errorsmod.Wrap(types.ErrInvalidSettlement, err.Error())
		}
		if receipt.Status != shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED &&
			receipt.Status != shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP {
			return errorsmod.Wrap(types.ErrInvariantBroken, "builder data-unavailable fault was neither applied nor an exact replay")
		}
	}
	return nil
}
