package keeper

// §9.3a Genesis import/export for the VRF public key registry.
//
// Genesis protocol §4: "Genesis writes VrfKeyState(effective_from_epoch=0) for
// every entry". Without this section, no operator holds an active VRF public key
// once a fresh genesis starts the chain, ActiveVrfPubkeyForHeight always returns
// ErrNoActiveVrfKey, and under a production genesis with
// `vrf_required_from_height = 1` **the very first block cannot be produced**.
//
// The GenesisState comment (genesis.pb.go:104-108) states that the indexes are not
// imported: VrfKeyActivationIndex is rebuilt from the pending pairs of
// VrfKeyState, and VrfKeyPruneIndex from retired_at_epoch +
// max_vrf_key_history_epochs of the retained history; both are self-checked in
// both directions.

import (
	"context"
	"fmt"

	"github.com/TrueOpen/node/x/hub/types"
)

func (k Keeper) initVrfKeyGenesis(ctx context.Context, genState types.GenesisState) error {
	for _, state := range genState.VrfKeys {
		if err := k.StoreVrfKey(ctx, state); err != nil {
			return err
		}
		if state.XPendingVrfPubkey == nil {
			continue
		}
		if err := k.VrfKeyActivationIndex.Set(ctx, types.NewVrfKeyActivationKey(state.GetPendingFromEpoch(), state.OperatorAddress)); err != nil {
			return err
		}
	}
	for _, row := range genState.VrfKeyHistory {
		if err := k.StoreVrfKeyHistory(ctx, row); err != nil {
			return err
		}
		pruneEpoch, err := checkedAdd(row.RetiredAtEpoch, uint64(genState.Params.Beacon.MaxVrfKeyHistoryEpochs))
		if err != nil {
			return fmt.Errorf("VRF key history prune epoch overflows for operator %s: %w", row.OperatorAddress, err)
		}
		if err := k.VrfKeyPruneIndex.Set(ctx, types.NewVrfKeyPruneKey(pruneEpoch, row.OperatorAddress, row.EffectiveFromEpoch)); err != nil {
			return err
		}
	}
	if err := k.EnsureVrfKeyActivationIndexInvariant(ctx); err != nil {
		return err
	}
	return k.EnsureVrfKeyPruneIndexInvariant(ctx)
}

// vrfActivationLookupKey is a comparable projection of VrfKeyActivationKeyPair.
//
// collections.Pair cannot be used directly as a map key: its two fields are
// pointers (key1/key2 *K), and Go map equality compares the pointers rather than
// the pointed-to values, so using it as a key makes every lookup miss and silently
// degrades the bidirectional check into "every index row is reported as
// superfluous".
type vrfActivationLookupKey struct {
	epoch    uint64
	operator string
}

type vrfPruneLookupKey struct {
	pruneEpoch         uint64
	operator           string
	effectiveFromEpoch uint64
}

// EnsureVrfKeyActivationIndexInvariant checks the rebuilt activation index in both
// directions: every pending rotation has exactly one index row, and every index row
// corresponds to a pending rotation. A missing index row makes that rotation never
// activate; an extra one makes ActivateDueVrfKeys revisit a key with no reachable
// state on every block.
func (k Keeper) EnsureVrfKeyActivationIndexInvariant(ctx context.Context) error {
	expected := make(map[vrfActivationLookupKey]struct{})
	iter, err := k.VrfKey.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		stored, err := iter.Value()
		if err != nil {
			return err
		}
		state, err := k.vrfKeyStorePublicProjection(stored)
		if err != nil {
			return err
		}
		key, err := iter.Key()
		if err != nil || key != state.OperatorAddress {
			return fmt.Errorf("VRF key/address mismatch")
		}
		if state.XPendingVrfPubkey == nil {
			continue
		}
		expected[vrfActivationLookupKey{epoch: state.GetPendingFromEpoch(), operator: state.OperatorAddress}] = struct{}{}
	}

	indexIter, err := k.VrfKeyActivationIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer indexIter.Close()
	for ; indexIter.Valid(); indexIter.Next() {
		key, err := indexIter.Key()
		if err != nil {
			return err
		}
		lookup := vrfActivationLookupKey{epoch: key.K1(), operator: key.K2()}
		if _, ok := expected[lookup]; !ok {
			return fmt.Errorf("vrf key activation index has a row for operator %s at epoch %d with no pending rotation", lookup.operator, lookup.epoch)
		}
		delete(expected, lookup)
	}
	for lookup := range expected {
		return fmt.Errorf("vrf key activation index is missing operator %s at epoch %d", lookup.operator, lookup.epoch)
	}
	return nil
}

// EnsureVrfKeyPruneIndexInvariant proves both directions of the derived
// history retention schedule. A missing row leaks history forever; an extra or
// early row can delete audit material before its frozen retention expires.
func (k Keeper) EnsureVrfKeyPruneIndexInvariant(ctx context.Context) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	expected := make(map[vrfPruneLookupKey]struct{})
	iter, err := k.VrfKeyHistory.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		stored, err := iter.Value()
		if err != nil {
			return err
		}
		row, err := k.vrfKeyHistoryStorePublicProjection(stored)
		if err != nil {
			return err
		}
		key, err := iter.Key()
		if err != nil || key.K1() != row.OperatorAddress || key.K2() != row.EffectiveFromEpoch {
			return fmt.Errorf("VRF history key/address mismatch")
		}
		pruneEpoch, err := checkedAdd(row.RetiredAtEpoch, uint64(params.Beacon.MaxVrfKeyHistoryEpochs))
		if err != nil {
			return fmt.Errorf("VRF key history prune epoch overflows for operator %s: %w", row.OperatorAddress, err)
		}
		expected[vrfPruneLookupKey{
			pruneEpoch: pruneEpoch, operator: row.OperatorAddress, effectiveFromEpoch: row.EffectiveFromEpoch,
		}] = struct{}{}
	}

	indexIter, err := k.VrfKeyPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer indexIter.Close()
	for ; indexIter.Valid(); indexIter.Next() {
		key, err := indexIter.Key()
		if err != nil {
			return err
		}
		lookup := vrfPruneLookupKey{pruneEpoch: key.K1(), operator: key.K2(), effectiveFromEpoch: key.K3()}
		if _, ok := expected[lookup]; !ok {
			return fmt.Errorf("vrf key prune index has an extra row for operator %s effective at epoch %d", lookup.operator, lookup.effectiveFromEpoch)
		}
		delete(expected, lookup)
	}
	for lookup := range expected {
		return fmt.Errorf("vrf key prune index is missing operator %s effective at epoch %d", lookup.operator, lookup.effectiveFromEpoch)
	}
	return nil
}

func (k Keeper) exportVrfKeyGenesis(ctx context.Context, genesis *types.GenesisState) error {
	if err := k.EnsureVrfKeyActivationIndexInvariant(ctx); err != nil {
		return err
	}
	if err := k.EnsureVrfKeyPruneIndexInvariant(ctx); err != nil {
		return err
	}
	keys, err := k.exportVrfKeys(ctx)
	if err != nil {
		return err
	}
	genesis.VrfKeys = keys
	history, err := k.exportVrfKeyHistory(ctx)
	if err != nil {
		return err
	}
	genesis.VrfKeyHistory = history
	return nil
}
