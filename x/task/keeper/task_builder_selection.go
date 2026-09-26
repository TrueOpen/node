package keeper

import (
	"bytes"
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func validateActiveTaskBuilderSelection(chainID string, selection types.TaskBuilderSelectionState, taskID []byte) error {
	if !bytes.Equal(selection.TaskId, taskID) ||
		selection.BodyStatus != shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE ||
		selection.SelectedTaskBuilderCount == 0 ||
		selection.SelectedTaskBuilderCount != uint32(len(selection.SelectedTaskBuilders)) ||
		len(selection.SelectedTaskBuildersHash) != types.Hash32Len {
		return fmt.Errorf("active Task Builder selection is unavailable")
	}
	recomputed, err := types.SelectedTaskBuildersHash(
		chainID, selection.TaskId, selection.BuilderSetId,
		selection.BuilderSetHash, selection.SelectedTaskBuilders,
	)
	if err != nil || !bytes.Equal(recomputed, selection.SelectedTaskBuildersHash) {
		return fmt.Errorf("selected_task_builders_hash commitment mismatch")
	}
	return nil
}

func (k Keeper) loadActiveTaskBuilderSelection(ctx context.Context, taskKey types.TaskKey, taskID []byte) (types.TaskBuilderSelectionState, error) {
	selection, err := k.GetTaskBuilderSelection(ctx, taskKey)
	if err != nil {
		return types.TaskBuilderSelectionState{}, err
	}
	if err := validateActiveTaskBuilderSelection(sdk.UnwrapSDKContext(ctx).ChainID(), selection, taskID); err != nil {
		return types.TaskBuilderSelectionState{}, err
	}
	return selection, nil
}

func (k Keeper) authorizeOpenTaskBuilder(ctx context.Context, selection types.TaskBuilderSelectionState, submitter string) (string, error) {
	if err := validateActiveTaskBuilderSelection(sdk.UnwrapSDKContext(ctx).ChainID(), selection, selection.TaskId); err != nil {
		return "", err
	}
	for _, builder := range selection.SelectedTaskBuilders {
		if err := k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeBuilder, builder, submitter); err == nil {
			return builder, nil
		}
	}
	return "", fmt.Errorf("submitter is not a selected Task Builder current service")
}
