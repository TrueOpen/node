package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"cosmossdk.io/collections"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	"github.com/TrueOpen/node/x/task/types"
)

var _ hubtypes.FreezeSignalTaskValidator = Keeper{}

// ScanFreezeSignalFailures walks only the selected profile's frozen finality
// window. LastIndexKey is the canonical encoded primary key, so a cursor cannot
// be replayed against another profile or window.
func (k Keeper) ScanFreezeSignalFailures(ctx context.Context, req hubtypes.FreezeSignalFailureScanRequest) (hubtypes.FreezeSignalFailureScanResult, error) {
	if req.ModelID == "" || req.ProfileVersion == 0 {
		return hubtypes.FreezeSignalFailureScanResult{}, fmt.Errorf("freeze failure selector is incomplete")
	}
	if req.RiskWindowStartHeight == 0 || req.RiskWindowEndHeight < req.RiskWindowStartHeight {
		return hubtypes.FreezeSignalFailureScanResult{}, fmt.Errorf("freeze failure window is invalid")
	}
	if req.Limit == 0 {
		return hubtypes.FreezeSignalFailureScanResult{}, fmt.Errorf("freeze failure scan limit must be positive")
	}

	zero := make([]byte, types.Hash32Len)
	maxHash := bytes.Repeat([]byte{0xff}, types.Hash32Len)
	start := collections.Join4(req.ModelID, req.ProfileVersion, req.RiskWindowStartHeight,
		collections.Join(int32(0), types.Hash32Key(zero)))
	end := collections.Join4(req.ModelID, req.ProfileVersion, req.RiskWindowEndHeight,
		collections.Join(int32(math.MaxInt32), types.Hash32Key(maxHash)))
	rng := new(collections.Range[types.TaskFailureClassByProfileWindowKey]).
		StartInclusive(start).EndInclusive(end)

	if len(req.LastIndexKey) != 0 {
		last, err := decodeFreezeFailureIndexKey(k.TaskFailureClassByProfileWindowIndex.KeyCodec(), req.LastIndexKey)
		if err != nil {
			return hubtypes.FreezeSignalFailureScanResult{}, err
		}
		if last.K1() != req.ModelID || last.K2() != req.ProfileVersion ||
			last.K3() < req.RiskWindowStartHeight || last.K3() > req.RiskWindowEndHeight {
			return hubtypes.FreezeSignalFailureScanResult{}, fmt.Errorf("freeze failure cursor does not match selector")
		}
		rng = new(collections.Range[types.TaskFailureClassByProfileWindowKey]).
			StartExclusive(last).EndInclusive(end)
	}

	iter, err := k.TaskFailureClassByProfileWindowIndex.Iterate(ctx, rng)
	if err != nil {
		return hubtypes.FreezeSignalFailureScanResult{}, err
	}
	defer iter.Close()

	result := hubtypes.FreezeSignalFailureScanResult{
		Failures: make([]hubtypes.FreezeSignalFailure, 0, req.Limit),
	}
	var last types.TaskFailureClassByProfileWindowKey
	for iter.Valid() && result.Visited < req.Limit {
		key, err := iter.Key()
		if err != nil {
			return hubtypes.FreezeSignalFailureScanResult{}, err
		}
		failure, err := k.failureForFreezeIndex(ctx, key)
		if err != nil {
			return hubtypes.FreezeSignalFailureScanResult{}, err
		}
		result.Failures = append(result.Failures, failure)
		result.Visited++
		last = key
		iter.Next()
	}
	result.Done = !iter.Valid()
	if result.Visited != 0 {
		result.LastIndexKey, err = encodeFreezeFailureIndexKey(k.TaskFailureClassByProfileWindowIndex.KeyCodec(), last)
		if err != nil {
			return hubtypes.FreezeSignalFailureScanResult{}, err
		}
	}
	return result, nil
}

func (k Keeper) failureForFreezeIndex(ctx context.Context, key types.TaskFailureClassByProfileWindowKey) (hubtypes.FreezeSignalFailure, error) {
	failureClass := types.TaskFailureClass(key.K4().K1())
	taskID := key.K4().K2()
	var found *types.TaskFailureClassState
	for _, round := range []uint32{types.VerifyRoundV1, types.ChallengeVerifyRoundV1} {
		state, err := k.TaskFailureClass.Get(ctx, types.NewVerifyRoundKey(taskID, round))
		if errors.Is(err, collections.ErrNotFound) {
			continue
		}
		if err != nil {
			return hubtypes.FreezeSignalFailure{}, err
		}
		if state.SupersededByVerifyRound == 0 && state.XTaskFinalityHeight != nil &&
			state.ModelId == key.K1() && state.ProfileVersion == key.K2() &&
			state.GetTaskFinalityHeight() == key.K3() && state.FailureClass == failureClass {
			copy := state
			if found != nil {
				return hubtypes.FreezeSignalFailure{}, fmt.Errorf("freeze failure index matches multiple classifications")
			}
			found = &copy
		}
	}
	if found == nil {
		return hubtypes.FreezeSignalFailure{}, fmt.Errorf("freeze failure index has no matching primary")
	}
	if len(found.EvidenceDigest) != types.Hash32Len {
		return hubtypes.FreezeSignalFailure{}, fmt.Errorf("freeze failure evidence_digest must be Hash32")
	}
	included, known := types.FreezeSignalEligibilityForTaskFailureClass(found.FailureClass)
	if !known || included != found.FreezeSignalEligible {
		return hubtypes.FreezeSignalFailure{}, fmt.Errorf("freeze failure eligibility is inconsistent")
	}
	settlementID := []byte(nil)
	if found.XSettlementId != nil {
		settlementID = append([]byte(nil), found.GetSettlementId()...)
		if len(settlementID) != types.Hash32Len {
			return hubtypes.FreezeSignalFailure{}, fmt.Errorf("freeze failure settlement_id must be Hash32")
		}
	}
	return hubtypes.FreezeSignalFailure{
		TaskID: append([]byte(nil), found.TaskId...), SettlementID: settlementID,
		FinalityHeight: found.GetTaskFinalityHeight(),
		FailureClass:   hubtypes.FreezeFailureClass(found.FailureClass),
		EvidenceDigest: append([]byte(nil), found.EvidenceDigest...), IncludedInRoot: included,
	}, nil
}

type freezeFailureIndexKeyCodec interface {
	Decode([]byte) (int, types.TaskFailureClassByProfileWindowKey, error)
	Size(types.TaskFailureClassByProfileWindowKey) int
	Encode([]byte, types.TaskFailureClassByProfileWindowKey) (int, error)
}

func decodeFreezeFailureIndexKey(codec freezeFailureIndexKeyCodec, encoded []byte) (types.TaskFailureClassByProfileWindowKey, error) {
	read, key, err := codec.Decode(encoded)
	if err != nil || read != len(encoded) {
		return types.TaskFailureClassByProfileWindowKey{}, fmt.Errorf("freeze failure cursor is not a canonical index key")
	}
	reencoded, err := encodeFreezeFailureIndexKey(codec, key)
	if err != nil || !bytes.Equal(reencoded, encoded) {
		return types.TaskFailureClassByProfileWindowKey{}, fmt.Errorf("freeze failure cursor is not canonically encoded")
	}
	return key, nil
}

func encodeFreezeFailureIndexKey(codec freezeFailureIndexKeyCodec, key types.TaskFailureClassByProfileWindowKey) ([]byte, error) {
	encoded := make([]byte, codec.Size(key))
	written, err := codec.Encode(encoded, key)
	if err != nil {
		return nil, err
	}
	return encoded[:written], nil
}
