package keeper

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

const busObjectiveEvidenceResponsibilitySchemaVersionV1 uint32 = 1

type orderedBusObjectiveEvidenceResponsibility struct {
	addressBytes []byte
	locator      shared.BusObjectiveEvidenceResponsibilityV1
}

func (k Keeper) taskBuilderEvidenceResponsibilityLocators(
	ctx context.Context,
	core types.TaskCoreState,
	selection types.TaskBuilderSelectionState,
	allowRevoked bool,
) ([]shared.BusObjectiveEvidenceResponsibilityV1, error) {
	if len(core.SessionId) != types.Hash32Len || len(core.TaskId) != types.Hash32Len ||
		!bytes.Equal(core.TaskId, selection.TaskId) {
		return nil, fmt.Errorf("Task Builder objective-evidence responsibility scope is inconsistent")
	}
	if err := validateActiveTaskBuilderSelection(sdk.UnwrapSDKContext(ctx).ChainID(), selection, core.TaskId); err != nil {
		return nil, fmt.Errorf("Task Builder objective-evidence selection: %w", err)
	}

	ordered := make([]orderedBusObjectiveEvidenceResponsibility, 0, len(selection.SelectedTaskBuilders))
	seen := make(map[string]struct{}, len(selection.SelectedTaskBuilders))
	for _, selectedBuilder := range selection.SelectedTaskBuilders {
		addressBytes, builder, err := k.canonicalAddress("selected Task Builder", selectedBuilder)
		if err != nil {
			return nil, err
		}
		identity := string(addressBytes)
		if _, duplicate := seen[identity]; duplicate {
			return nil, fmt.Errorf("Task Builder objective-evidence selection contains a duplicate Builder")
		}
		seen[identity] = struct{}{}

		var binding hubtypes.CurrentServiceKeySnapshot
		if allowRevoked {
			binding, err = k.hubKeeper.GetBuilderObjectiveEvidenceCurrentBinding(ctx, builder)
		} else {
			binding, err = k.hubKeeper.GetCurrentServiceKey(ctx, shared.ParticipantTypeBuilder, builder)
		}
		if err != nil {
			return nil, fmt.Errorf("current Builder service key is unavailable for objective-evidence responsibility %s: %w", builder, err)
		}
		if binding.ParticipantType != shared.ParticipantTypeBuilder || binding.OperatorAddress != builder ||
			binding.AuthorizationNonce == 0 {
			return nil, fmt.Errorf("current Builder service key identity is invalid for objective-evidence responsibility %s", builder)
		}
		if binding.Status != hubtypes.ServiceKeyStatusActive && !(allowRevoked && binding.Status == hubtypes.ServiceKeyStatusRevoked) {
			return nil, fmt.Errorf("current Builder service key status is invalid for objective-evidence responsibility %s", builder)
		}

		ordered = append(ordered, orderedBusObjectiveEvidenceResponsibility{
			addressBytes: append([]byte(nil), addressBytes...),
			locator: shared.BusObjectiveEvidenceResponsibilityV1{
				SchemaVersion:             busObjectiveEvidenceResponsibilitySchemaVersionV1,
				BuilderOperator:           builder,
				SessionId:                 append([]byte(nil), core.SessionId...),
				TaskId:                    append([]byte(nil), core.TaskId...),
				ServiceAuthorizationNonce: binding.AuthorizationNonce,
			},
		})
	}
	sort.Slice(ordered, func(i, j int) bool {
		return bytes.Compare(ordered[i].addressBytes, ordered[j].addressBytes) < 0
	})
	locators := make([]shared.BusObjectiveEvidenceResponsibilityV1, len(ordered))
	for index := range ordered {
		locators[index] = ordered[index].locator
	}
	return locators, nil
}

func validateBusObjectiveEvidenceResponsibilityReceipt(
	locator shared.BusObjectiveEvidenceResponsibilityV1,
	receipt shared.BusObjectiveEvidenceResponsibilityReceiptV1,
	wantActive bool,
) error {
	if len(receipt.ResponsibilityId) != types.Hash32Len || receipt.BuilderOperator != locator.BuilderOperator ||
		!bytes.Equal(receipt.TaskId, locator.TaskId) ||
		receipt.ServiceAuthorizationNonce != locator.ServiceAuthorizationNonce || receipt.Active != wantActive ||
		receipt.Status != shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED {
		return fmt.Errorf("Hub returned a non-canonical Builder objective-evidence responsibility receipt")
	}
	return nil
}

func (k Keeper) acquireTaskBuilderEvidenceResponsibilities(
	ctx context.Context,
	core types.TaskCoreState,
	selection types.TaskBuilderSelectionState,
) error {
	locators, err := k.taskBuilderEvidenceResponsibilityLocators(ctx, core, selection, false)
	if err != nil {
		return err
	}
	for _, locator := range locators {
		receipt, err := k.hubKeeper.AcquireBusObjectiveEvidenceResponsibility(ctx, locator)
		if err != nil {
			return err
		}
		if err := validateBusObjectiveEvidenceResponsibilityReceipt(locator, receipt, true); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) releaseTaskBuilderEvidenceResponsibilities(
	ctx context.Context,
	core types.TaskCoreState,
	selection types.TaskBuilderSelectionState,
) error {
	locators, err := k.taskBuilderEvidenceResponsibilityLocators(ctx, core, selection, true)
	if err != nil {
		return err
	}
	for _, locator := range locators {
		receipt, err := k.hubKeeper.ReleaseBusObjectiveEvidenceResponsibility(ctx, locator)
		if err != nil {
			return err
		}
		if err := validateBusObjectiveEvidenceResponsibilityReceipt(locator, receipt, false); err != nil {
			return err
		}
	}
	return nil
}
