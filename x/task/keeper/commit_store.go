package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) commitToStore(state types.CommitState) (internaltypes.CommitStoreState, error) {
	address, _, err := k.canonicalAddress("commit verifier", state.VerifierOperatorAddress)
	if err != nil {
		return internaltypes.CommitStoreState{}, err
	}
	return internaltypes.CommitStoreState{
		CommitKey:               append([]byte(nil), state.CommitKey...),
		TaskId:                  append([]byte(nil), state.TaskId...),
		VerifyRound:             state.VerifyRound,
		VerifierOperatorAddress: append([]byte(nil), address...),
		CommitHash:              append([]byte(nil), state.CommitHash...),
		CommitSigningDigest:     append([]byte(nil), state.CommitSigningDigest...),
		SignatureDigest:         append([]byte(nil), state.SignatureDigest...),
		CommitHeight:            state.CommitHeight,
		Status:                  int32(state.Status),
	}, nil
}

func (k Keeper) ProjectCommitStore(stored internaltypes.CommitStoreState) (types.CommitState, error) {
	address, err := k.sessionAddressFromStore("commit verifier", stored.VerifierOperatorAddress)
	if err != nil {
		return types.CommitState{}, err
	}
	if stored.Status != int32(types.CommitStatusV1_COMMIT_STATUS_V1_ACCEPTED) {
		return types.CommitState{}, fmt.Errorf("stored commit status is invalid")
	}
	return types.CommitState{
		CommitKey:               append([]byte(nil), stored.CommitKey...),
		TaskId:                  append([]byte(nil), stored.TaskId...),
		VerifyRound:             stored.VerifyRound,
		VerifierOperatorAddress: address,
		CommitHash:              append([]byte(nil), stored.CommitHash...),
		CommitSigningDigest:     append([]byte(nil), stored.CommitSigningDigest...),
		SignatureDigest:         append([]byte(nil), stored.SignatureDigest...),
		CommitHeight:            stored.CommitHeight,
		Status:                  types.CommitStatusV1(stored.Status),
	}, nil
}

func (k Keeper) ReadCommit(ctx context.Context, key types.CommitKey) (types.CommitState, error) {
	stored, err := k.CommitState.Get(ctx, key)
	if err != nil {
		return types.CommitState{}, err
	}
	if !bytes.Equal(key, stored.CommitKey) {
		return types.CommitState{}, fmt.Errorf("commit key/value mismatch")
	}
	return k.ProjectCommitStore(stored)
}

func (k Keeper) WriteCommit(ctx context.Context, key types.CommitKey, state types.CommitState) error {
	if !bytes.Equal(key, state.CommitKey) {
		return fmt.Errorf("commit key/value mismatch")
	}
	stored, err := k.commitToStore(state)
	if err != nil {
		return err
	}
	return k.CommitState.Set(ctx, key, stored)
}

func (k Keeper) exportCommits(ctx context.Context) ([]types.CommitState, error) {
	iter, err := k.CommitState.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.CommitState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(entry.Key, entry.Value.CommitKey) {
			return nil, fmt.Errorf("commit key/value mismatch")
		}
		state, err := k.ProjectCommitStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
