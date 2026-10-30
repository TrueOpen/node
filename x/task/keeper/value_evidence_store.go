package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) valueEvidenceReceiptToStore(row types.VerifierValueEvidenceReceiptState) (internaltypes.ValueEvidenceReceiptStoreState, error) {
	address, err := k.sessionAddressToStore("value evidence target", row.TargetOperatorAddress)
	if err != nil {
		return internaltypes.ValueEvidenceReceiptStoreState{}, err
	}
	if row.SchemaVersion != 1 || len(row.TaskId) != 32 || !isPhase0VerifyRound(row.VerifyRound) || len(row.EvidenceDigest) != 32 || len(row.FaultId) != 32 || row.AcceptedHeight == 0 || (row.EvidenceKind != types.VerifierValueEvidenceKindV1_VERIFIER_VALUE_EVIDENCE_KIND_V1_VERIFIER_VALUE_MISMATCH || row.FaultedDuty != shared.DutyVerifier) && (row.EvidenceKind != types.VerifierValueEvidenceKindV1_VERIFIER_VALUE_EVIDENCE_KIND_V1_WORKER_VALUES_INVALID || row.FaultedDuty != shared.DutyWorker) {
		return internaltypes.ValueEvidenceReceiptStoreState{}, fmt.Errorf("invalid value evidence receipt")
	}
	return internaltypes.ValueEvidenceReceiptStoreState{SchemaVersion: 1, TaskId: bytes.Clone(row.TaskId), VerifyRound: row.VerifyRound, TargetOperatorAddress: address, Position: row.Position, EvidenceKind: int32(row.EvidenceKind), FaultedDuty: int32(row.FaultedDuty), EvidenceDigest: bytes.Clone(row.EvidenceDigest), FaultId: bytes.Clone(row.FaultId), AcceptedHeight: row.AcceptedHeight}, nil
}

// ProjectValueEvidenceReceiptStore decodes compact primary facts for public projections and cross-module checks.
func (k Keeper) ProjectValueEvidenceReceiptStore(row internaltypes.ValueEvidenceReceiptStoreState) (types.VerifierValueEvidenceReceiptState, error) {
	address, err := k.sessionAddressFromStore("value evidence target", row.TargetOperatorAddress)
	if err != nil {
		return types.VerifierValueEvidenceReceiptState{}, err
	}
	out := types.VerifierValueEvidenceReceiptState{SchemaVersion: row.SchemaVersion, TaskId: bytes.Clone(row.TaskId), VerifyRound: row.VerifyRound, TargetOperatorAddress: address, Position: row.Position, EvidenceKind: types.VerifierValueEvidenceKindV1(row.EvidenceKind), FaultedDuty: shared.Duty(row.FaultedDuty), EvidenceDigest: bytes.Clone(row.EvidenceDigest), FaultId: bytes.Clone(row.FaultId), AcceptedHeight: row.AcceptedHeight}
	_, err = k.valueEvidenceReceiptToStore(out)
	return out, err
}

func valueEvidenceReceiptKey(row internaltypes.ValueEvidenceReceiptStoreState) types.ValueEvidenceReceiptKey {
	return types.NewValueEvidenceReceiptKey(row.TaskId, row.VerifyRound, row.TargetOperatorAddress, row.Position)
}

func (k Keeper) firstValueEvidenceReceipts(ctx context.Context, taskID []byte) ([]types.VerifierValueEvidenceReceiptState, error) {
	params, err := k.ParamsForTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	cap := uint64(params.Evidence.MaxVerifierValueEvidencePositions) * (uint64(params.Challenge.ChallengeVerifierCount) + 5)
	iter, err := k.ValueEvidenceReceipt.Iterate(ctx, collections.NewPrefixedQuadRange[types.Hash32Key, uint32, types.AddrKey, uint64](taskID))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var out []types.VerifierValueEvidenceReceiptState
	seen := make(map[string]bool)
	faults := make(map[string]hubtypes.RoleFaultState)
	var count uint64
	for ; iter.Valid(); iter.Next() {
		count++
		if count > cap {
			return nil, fmt.Errorf("task value receipt count exceeds its derived bound")
		}
		row, err := iter.Value()
		if err != nil {
			return nil, err
		}
		key := string(shared.Uint32BE(row.VerifyRound)) + string(row.TargetOperatorAddress)
		fault, loaded := faults[key]
		if !loaded {
			fault, err = k.hubKeeper.ReadRoleFaultValue(ctx, row.FaultId)
			if err != nil {
				return nil, err
			}
			faults[key] = fault
		}
		public, err := k.ProjectValueEvidenceReceiptStore(row)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(fault.FaultId, row.FaultId) || !bytes.Equal(fault.TaskId, taskID) || fault.OperatorAddress != public.TargetOperatorAddress || fault.Duty != public.FaultedDuty || fault.RecordedHeight > row.AcceptedHeight {
			return nil, fmt.Errorf("value receipt disagrees with its original fault")
		}
		if seen[key] {
			continue
		}
		// Position order does not identify the first accepted proof, including
		// when several transactions are accepted in the same block.
		if !bytes.Equal(fault.EvidenceDigest, row.EvidenceDigest) || fault.RecordedHeight != row.AcceptedHeight {
			continue
		}
		seen[key] = true
		out = append(out, public)
	}
	if len(seen) != len(faults) {
		return nil, fmt.Errorf("value fault has no original receipt")
	}
	return out, nil
}

func (k Keeper) valueEvidenceForOperator(ctx context.Context, taskID []byte, operator string) (types.VerifierValueEvidenceReceiptState, bool, error) {
	rows, err := k.firstValueEvidenceReceipts(ctx, taskID)
	if err != nil {
		return types.VerifierValueEvidenceReceiptState{}, false, err
	}
	for _, row := range rows {
		if row.TargetOperatorAddress == operator {
			return row, true, nil
		}
	}
	return types.VerifierValueEvidenceReceiptState{}, false, nil
}

// valueEvidenceTargetReceipts reads the sole primary prefix; no mirrored count
// or index can drift from the receipts after replay or Genesis restoration.
func (k Keeper) valueEvidenceTargetReceipts(ctx context.Context, taskID []byte, round uint32, target []byte, cap uint32) ([]internaltypes.ValueEvidenceReceiptStoreState, error) {
	r := collections.NewSuperPrefixedQuadRange3[types.Hash32Key, uint32, types.AddrKey, uint64](taskID, round, target)
	iter, err := k.ValueEvidenceReceipt.Iterate(ctx, r)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var rows []internaltypes.ValueEvidenceReceiptStoreState
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		row := entry.Value
		if !bytes.Equal(entry.Key.K1(), row.TaskId) || entry.Key.K2() != row.VerifyRound || !bytes.Equal(entry.Key.K3(), row.TargetOperatorAddress) || entry.Key.K4() != row.Position {
			return nil, fmt.Errorf("value evidence receipt key mismatch")
		}
		if uint32(len(rows)) >= cap {
			return nil, fmt.Errorf("value evidence receipt prefix exceeds its cap")
		}
		if _, err = k.ProjectValueEvidenceReceiptStore(row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}
