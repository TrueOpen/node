package keeper

import (
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

// The private message mirrors every public field number and wire type except
// opener_address, which changes from optional string to optional bytes.
func (k Keeper) verificationRoundToStore(state types.VerificationRoundState) (internaltypes.VerificationRoundStoreState, error) {
	encoded, err := state.Marshal()
	if err != nil {
		return internaltypes.VerificationRoundStoreState{}, err
	}
	var stored internaltypes.VerificationRoundStoreState
	if err := stored.Unmarshal(encoded); err != nil {
		return internaltypes.VerificationRoundStoreState{}, err
	}
	if state.XOpenerAddress != nil {
		address, _, err := k.canonicalAddress("verification round opener", state.GetOpenerAddress())
		if err != nil {
			return internaltypes.VerificationRoundStoreState{}, err
		}
		stored.XOpenerAddress = &internaltypes.VerificationRoundStoreState_OpenerAddress{OpenerAddress: append([]byte(nil), address...)}
	}
	return stored, nil
}

func (k Keeper) ProjectVerificationRoundStore(stored internaltypes.VerificationRoundStoreState) (types.VerificationRoundState, error) {
	var address string
	if stored.XOpenerAddress != nil {
		var err error
		address, err = k.sessionAddressFromStore("verification round opener", stored.GetOpenerAddress())
		if err != nil {
			return types.VerificationRoundState{}, err
		}
		stored.XOpenerAddress = &internaltypes.VerificationRoundStoreState_OpenerAddress{OpenerAddress: []byte("placeholder")}
	}
	encoded, err := stored.Marshal()
	if err != nil {
		return types.VerificationRoundState{}, err
	}
	var state types.VerificationRoundState
	if err := state.Unmarshal(encoded); err != nil {
		return types.VerificationRoundState{}, err
	}
	if stored.XOpenerAddress != nil {
		state.XOpenerAddress = &types.VerificationRoundState_OpenerAddress{OpenerAddress: address}
	}
	return state, nil
}

func (k Keeper) ReadVerificationRound(ctx context.Context, key types.VerifyRoundKey) (types.VerificationRoundState, error) {
	stored, err := k.VerificationRound.Get(ctx, key)
	if err != nil {
		return types.VerificationRoundState{}, err
	}
	if !verifierAssignmentKeyMatches(key, stored.TaskId, stored.VerifyRound) {
		return types.VerificationRoundState{}, fmt.Errorf("verification round key/value mismatch")
	}
	return k.ProjectVerificationRoundStore(stored)
}

func (k Keeper) WriteVerificationRound(ctx context.Context, key types.VerifyRoundKey, state types.VerificationRoundState) error {
	if !verifierAssignmentKeyMatches(key, state.TaskId, state.VerifyRound) {
		return fmt.Errorf("verification round key/value mismatch")
	}
	stored, err := k.verificationRoundToStore(state)
	if err != nil {
		return err
	}
	return k.VerificationRound.Set(ctx, key, stored)
}

func (k Keeper) exportVerificationRounds(ctx context.Context) ([]types.VerificationRoundState, error) {
	iter, err := k.VerificationRound.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.VerificationRoundState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !verifierAssignmentKeyMatches(entry.Key, entry.Value.TaskId, entry.Value.VerifyRound) {
			return nil, fmt.Errorf("verification round key/value mismatch")
		}
		state, err := k.ProjectVerificationRoundStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
