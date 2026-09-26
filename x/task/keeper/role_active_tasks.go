package keeper

import (
	"context"
	"strings"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"

	"github.com/TrueOpen/node/x/task/types"
)

// AddWorkerActiveTaskIndex records that a worker holds an in-flight task.
func (k Keeper) AddWorkerActiveTaskIndex(ctx context.Context, workerAddress string, taskID []byte) error {
	workerAddress = strings.TrimSpace(workerAddress)
	if workerAddress == "" {
		return errorsmod.Wrap(types.ErrInvalidAssignment, "worker address is required")
	}
	taskKey, err := taskStoreKey(taskID)
	if err != nil {
		return err
	}
	return k.WorkerActiveTaskIndex.Set(ctx, types.NewWorkerActiveTaskKey(workerAddress, taskKey))
}

// AddVerifierActiveJobIndex records that a verifier holds an in-flight job.
func (k Keeper) AddVerifierActiveJobIndex(ctx context.Context, verifierAddress string, taskID []byte) error {
	verifierAddress = strings.TrimSpace(verifierAddress)
	if verifierAddress == "" {
		return errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier address is required")
	}
	taskKey, err := taskStoreKey(taskID)
	if err != nil {
		return err
	}
	return k.VerifierActiveJobIndex.Set(ctx, types.NewVerifierActiveJobKey(verifierAddress, taskKey))
}

// RemoveRoleActiveTaskIndexes clears both role indexes of one task. It is driven
// by the authoritative assignment rows, so it never scans all operators
// (the data-structure contract: a task holds at most
// 1 + selected_verifier_count liabilities).
func (k Keeper) RemoveRoleActiveTaskIndexes(ctx context.Context, taskID []byte) error {
	taskKey, err := taskStoreKey(taskID)
	if err != nil {
		return err
	}

	assignment, err := k.ReadTaskAssignment(ctx, taskKey)
	if err != nil && !errIsNotFound(err) {
		return err
	}
	if err == nil {
		if worker := strings.TrimSpace(assignment.WinnerWorker); worker != "" {
			if err := removeRoleActiveTaskKey(ctx, k.WorkerActiveTaskIndex, types.NewWorkerActiveTaskKey(worker, taskKey)); err != nil {
				return err
			}
		}
	}

	// V1 has exactly one verify round, so the authoritative verifier vector is an
	// O(1) lookup followed by at most selected_verifier_count removals.
	verifiers, err := k.ReadVerifierAssignment(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
	if err != nil && !errIsNotFound(err) {
		return err
	}
	if err == nil {
		for _, selected := range verifiers.SelectedVerifiers {
			operator := strings.TrimSpace(selected.OperatorAddress)
			if operator == "" {
				return errorsmod.Wrap(types.ErrInvariantBroken, "verifier assignment contains an empty operator address")
			}
			if err := removeRoleActiveTaskKey(ctx, k.VerifierActiveJobIndex, types.NewVerifierActiveJobKey(operator, taskKey)); err != nil {
				return err
			}
		}
	}
	return nil
}

func removeRoleActiveTaskKey(ctx context.Context, set collections.KeySet[types.RoleActiveTaskKey], key types.RoleActiveTaskKey) error {
	has, err := set.Has(ctx, key)
	if err != nil || !has {
		return err
	}
	return set.Remove(ctx, key)
}
