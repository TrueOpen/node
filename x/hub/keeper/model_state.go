package keeper

import (
	"context"
	"fmt"

	"github.com/TrueOpen/node/x/hub/types"
)

func (k Keeper) setModelState(ctx context.Context, state types.ModelState) error {
	if err := state.Validate(); err != nil {
		return fmt.Errorf("validate model state: %w", err)
	}
	if err := k.Model.Set(ctx, state.ModelId, state); err != nil {
		return fmt.Errorf("set model state: %w", err)
	}
	return nil
}

func (k Keeper) setProfileState(ctx context.Context, state types.ProfileState) error {
	if err := state.Validate(); err != nil {
		return fmt.Errorf("validate profile state: %w", err)
	}
	if err := k.Profile.Set(ctx, types.NewProfileStateKey(state.ModelId, state.ProfileVersion), state); err != nil {
		return fmt.Errorf("set profile state: %w", err)
	}
	return nil
}
