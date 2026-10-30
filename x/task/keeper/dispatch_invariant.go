package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	collectionscodec "cosmossdk.io/collections/codec"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func collectDispatchPrimaries[S any](ctx context.Context, table collections.Map[types.Hash32Key, S], identity func(S) []byte) ([]S, error) {
	var rows []S
	err := table.Walk(ctx, nil, func(key types.Hash32Key, row S) (bool, error) {
		if !bytes.Equal(key, identity(row)) {
			return false, fmt.Errorf("dispatch primary key/payload mismatch")
		}
		rows = append(rows, row)
		return false, nil
	})
	return rows, err
}

func indexKeyBytes[K any](codec collectionscodec.KeyCodec[K], key K) (string, error) {
	raw := make([]byte, codec.Size(key))
	n, err := codec.Encode(raw, key)
	if err != nil {
		return "", err
	}
	return string(raw[:n]), err
}

func checkDerivedKeySet[K any](ctx context.Context, set collections.KeySet[K], expected map[string]bool) error {
	iter, err := set.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return err
		}
		encoded, err := indexKeyBytes(set.KeyCodec(), key)
		if err != nil {
			return err
		}
		if !expected[encoded] {
			return fmt.Errorf("derived index has an orphan or conflicting row")
		}
		delete(expected, encoded)
	}
	if len(expected) != 0 {
		return fmt.Errorf("primary is missing a derived index row")
	}
	return nil
}

func checkDispatchPruneSet(ctx context.Context, set collections.KeySet[types.DeadlineIndexKey], minimum map[string]uint64) error {
	iter, err := set.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return err
		}
		floor, found := minimum[string(key.K2())]
		if !found || key.K1() < floor {
			return fmt.Errorf("dispatch prune index is orphaned, duplicated or precedes the admissibility floor")
		}
		delete(minimum, string(key.K2()))
	}
	if len(minimum) != 0 {
		return fmt.Errorf("dispatch terminal primary is missing its prune index")
	}
	return nil
}

// EnsureDispatchInvariant checks every compact primary against both directions
// of its indexes. Prune indexes may retain an older, longer schedule after a
// parameter update; they must still preserve every signed admissibility window.
func (k Keeper) EnsureDispatchInvariant(ctx context.Context) error {
	d := dispatchInvariantState{keeper: k,
		claimDeadlines:     map[string]bool{},
		challengeDeadlines: map[string]bool{},
		claimBuilders:      map[string]bool{},
		challengeBuilders:  map[string]bool{},
		recordPrunes:       map[string]uint64{},
		claimPrunes:        map[string]uint64{},
		challengePrunes:    map[string]uint64{},
		winnerPrunes:       map[string]uint64{},
		takeoverPrunes:     map[string]uint64{},
		pending:            map[string]uint64{},
		upheld:             map[string][]byte{},
		recordHashes:       map[string][]byte{},
	}
	for _, check := range []func(context.Context) error{d.collectRecords, d.collectClaims, d.collectChallenges, d.collectWinners, d.collectTakeovers, d.checkIndexes} {
		if err := check(ctx); err != nil {
			return err
		}
	}
	return nil
}

type dispatchInvariantState struct {
	keeper             Keeper
	claimDeadlines     map[string]bool
	challengeDeadlines map[string]bool
	claimBuilders      map[string]bool
	challengeBuilders  map[string]bool
	recordPrunes       map[string]uint64
	claimPrunes        map[string]uint64
	challengePrunes    map[string]uint64
	winnerPrunes       map[string]uint64
	takeoverPrunes     map[string]uint64
	pending            map[string]uint64
	upheld             map[string][]byte
	recordHashes       map[string][]byte
}

func addDispatchDeadlineExpectation(ctx context.Context, set collections.KeySet[types.DeadlineIndexKey], target map[string]bool, height uint64, id []byte) error {
	encoded, err := indexKeyBytes(set.KeyCodec(), collections.Join(height, id))
	if err != nil {
		return err
	}
	target[encoded] = true
	return nil
}
func addDispatchBuilderExpectation(ctx context.Context, set collections.KeySet[types.RoleActiveTaskKey], target map[string]bool, builder, id []byte) error {
	encoded, err := indexKeyBytes(set.KeyCodec(), collections.Join(builder, id))
	if err != nil {
		return err
	}
	target[encoded] = true
	return nil
}
func (d *dispatchInvariantState) addPending(id []byte) error {
	next, overflow := checkedAddUint64(d.pending[string(id)], 1)
	if overflow {
		return fmt.Errorf("dispatch pending invariant overflows")
	}
	d.pending[string(id)] = next
	return nil
}
func dispatchPruneFloor(height uint64) (uint64, error) {
	next, overflow := checkedHeightAdd(height, 1)
	if overflow {
		return 0, fmt.Errorf("dispatch retention floor overflows")
	}
	return next, nil
}

func (d *dispatchInvariantState) collectRecords(ctx context.Context) error {
	records, err := collectDispatchPrimaries(ctx, d.keeper.DispatchRecord, func(s internaltypes.DispatchRecordStoreState) []byte { return s.TaskId })
	if err != nil {
		return err
	}
	for _, state := range records {
		row, err := d.keeper.ProjectDispatchRecordStore(state)
		if err != nil {
			return err
		}
		if _, found := d.recordHashes[string(row.DispatchRecordHash)]; found {
			return fmt.Errorf("duplicate dispatch record hash")
		}
		d.recordHashes[string(row.DispatchRecordHash)] = row.TaskId
		floor, err := dispatchPruneFloor(row.ClaimWindowCloseHeight)
		if err != nil {
			return err
		}
		d.recordPrunes[string(row.TaskId)] = floor
	}
	return nil
}

func (d *dispatchInvariantState) collectClaims(ctx context.Context) error {
	claims, err := collectDispatchPrimaries(ctx, d.keeper.DispatchCompensationClaim, func(s internaltypes.DispatchCompensationClaimStoreState) []byte { return s.DispatchTicketHash })
	if err != nil {
		return err
	}
	for _, state := range claims {
		row, err := d.keeper.readDispatchClaim(ctx, state.DispatchTicketHash)
		if err != nil {
			return err
		}
		if row.Status == types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL {
			if _, pruned, err := d.keeper.dispatchClaimExcluded(ctx, row.TaskId, row.DispatchTicketHash); err != nil {
				return err
			} else if pruned {
				return fmt.Errorf("pending claim lost its retained winner exclusion facts")
			}
			// Recording may become admissible after this claim was registered.
			// The existing maturity transition rechecks exclusion before payment.
			if err := addDispatchDeadlineExpectation(ctx, d.keeper.DispatchClaimRebuttal, d.claimDeadlines, row.RebuttalDeadlineHeight, row.DispatchTicketHash); err != nil {
				return err
			}
			if err := addDispatchBuilderExpectation(ctx, d.keeper.DispatchOpenClaimByBuilder, d.claimBuilders, state.BuilderAddress, row.DispatchTicketHash); err != nil {
				return err
			}
			if err := d.addPending(row.TaskId); err != nil {
				return err
			}
		} else {
			floor, err := dispatchPruneFloor(max(row.ClaimWindowCloseHeight, row.GetResolvedHeight()))
			if err != nil {
				return err
			}
			d.claimPrunes[string(row.DispatchTicketHash)] = floor
		}
	}
	return nil
}

func (d *dispatchInvariantState) collectChallenges(ctx context.Context) error {
	challenges, err := collectDispatchPrimaries(ctx, d.keeper.DispatchChallenge, func(s internaltypes.DispatchChallengeStoreState) []byte { return s.ChallengeId })
	if err != nil {
		return err
	}
	for _, state := range challenges {
		row, err := d.keeper.readDispatchChallenge(ctx, state.ChallengeId)
		if err != nil {
			return err
		}
		if dispatchChallengePending(row.Status) {
			if _, err := d.keeper.readDispatchRecord(ctx, row.TaskId); err != nil {
				return fmt.Errorf("pending challenge lost its retained record: %w", err)
			}
			if _, err := d.keeper.TaskCore.Get(ctx, row.TaskId); err != nil {
				return fmt.Errorf("pending challenge lost its Task authority: %w", err)
			}
			if err := addDispatchDeadlineExpectation(ctx, d.keeper.DispatchChallengeResponse, d.challengeDeadlines, row.GetProofDeadlineHeight(), row.ChallengeId); err != nil {
				return err
			}
			if err := addDispatchBuilderExpectation(ctx, d.keeper.DispatchOpenChallengeByBuilder, d.challengeBuilders, state.BuilderAddress, row.ChallengeId); err != nil {
				return err
			}
			if err := d.addPending(row.TaskId); err != nil {
				return err
			}
		} else {
			floor, err := dispatchPruneFloor(row.GetResolvedHeight())
			if err != nil {
				return err
			}
			d.challengePrunes[string(row.ChallengeId)] = floor
		}
		if row.Status == types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_UPHELD {
			scope, err := d.keeper.dispatchUpheldScopeKey(row)
			if err != nil {
				return err
			}
			if _, found := d.upheld[string(scope)]; found {
				return fmt.Errorf("objective dispatch scope was upheld twice")
			}
			d.upheld[string(scope)] = row.ChallengeId
		}
	}
	return nil
}

func (d *dispatchInvariantState) collectWinners(ctx context.Context) error {
	winners, err := collectDispatchPrimaries(ctx, d.keeper.DispatchWinnerIneligible, func(s internaltypes.DispatchWinnerIneligibleStoreState) []byte { return s.DispatchTicketHash })
	if err != nil {
		return err
	}
	for _, state := range winners {
		row, err := d.keeper.ProjectDispatchWinnerIneligibleStore(state)
		if err != nil {
			return err
		}
		floor, err := dispatchPruneFloor(row.ClaimWindowCloseHeight)
		if err != nil {
			return err
		}
		d.winnerPrunes[string(row.DispatchTicketHash)] = floor
	}
	return nil
}

func (d *dispatchInvariantState) collectTakeovers(ctx context.Context) error {
	takeovers, err := collectDispatchPrimaries(ctx, d.keeper.TakeoverEntryReceipt, func(s internaltypes.TakeoverEntryReceiptStoreState) []byte { return s.TakeoverEntryDigest })
	if err != nil {
		return err
	}
	for _, state := range takeovers {
		row, err := d.keeper.ProjectTakeoverEntryReceiptStore(state)
		if err != nil {
			return err
		}
		floor, err := dispatchPruneFloor(max(row.ActivationHeight, row.OrderExpireHeight))
		if err != nil {
			return err
		}
		d.takeoverPrunes[string(row.TakeoverEntryDigest)] = floor
	}
	return nil
}

func (d *dispatchInvariantState) checkIndexes(ctx context.Context) error {
	for _, check := range []struct {
		set  collections.KeySet[types.DeadlineIndexKey]
		want map[string]bool
	}{{d.keeper.DispatchClaimRebuttal, d.claimDeadlines}, {d.keeper.DispatchChallengeResponse, d.challengeDeadlines}} {
		if err := checkDerivedKeySet(ctx, check.set, check.want); err != nil {
			return err
		}
	}
	for _, check := range []struct {
		set  collections.KeySet[types.RoleActiveTaskKey]
		want map[string]bool
	}{{d.keeper.DispatchOpenClaimByBuilder, d.claimBuilders}, {d.keeper.DispatchOpenChallengeByBuilder, d.challengeBuilders}} {
		if err := checkDerivedKeySet(ctx, check.set, check.want); err != nil {
			return err
		}
	}
	for _, check := range []struct {
		set  collections.KeySet[types.DeadlineIndexKey]
		want map[string]uint64
	}{{d.keeper.DispatchRecordPrune, d.recordPrunes}, {d.keeper.DispatchClaimPrune, d.claimPrunes}, {d.keeper.DispatchChallengePrune, d.challengePrunes}, {d.keeper.DispatchWinnerIneligiblePrune, d.winnerPrunes}, {d.keeper.TakeoverEntryReceiptPrune, d.takeoverPrunes}} {
		if err := checkDispatchPruneSet(ctx, check.set, check.want); err != nil {
			return err
		}
	}
	if err := d.keeper.DispatchRecordByHash.Walk(ctx, nil, func(hash []byte, id []byte) (bool, error) {
		want, found := d.recordHashes[string(hash)]
		if !found || !bytes.Equal(want, id) {
			return false, fmt.Errorf("dispatch hash index/primary mismatch")
		}
		delete(d.recordHashes, string(hash))
		return false, nil
	}); err != nil {
		return err
	}
	if len(d.recordHashes) != 0 {
		return fmt.Errorf("dispatch record is missing its hash index")
	}
	if err := d.keeper.DispatchPendingByTask.Walk(ctx, nil, func(id []byte, count uint64) (bool, error) {
		if count == 0 || d.pending[string(id)] != count {
			return false, fmt.Errorf("dispatch pending count/primary mismatch")
		}
		delete(d.pending, string(id))
		return false, nil
	}); err != nil {
		return err
	}
	if len(d.pending) != 0 {
		return fmt.Errorf("dispatch pending primary is missing its count")
	}
	if err := d.keeper.DispatchUpheldScope.Walk(ctx, nil, func(scope, id []byte) (bool, error) {
		want, found := d.upheld[string(scope)]
		if !found || !bytes.Equal(want, id) {
			return false, fmt.Errorf("dispatch upheld scope/primary mismatch")
		}
		delete(d.upheld, string(scope))
		return false, nil
	}); err != nil {
		return err
	}
	if len(d.upheld) != 0 {
		return fmt.Errorf("upheld challenge is missing its scope index")
	}
	return nil
}
