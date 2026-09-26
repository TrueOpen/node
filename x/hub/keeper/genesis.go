package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	prepared, err := k.prepareParameterBucketGenesis(ctx, genState)
	if err != nil {
		return err
	}
	genState = prepared
	prepared, err = types.PrepareCandidateSlotGenesis(genState)
	if err != nil {
		return err
	}
	genState = prepared
	if err := genState.Validate(); err != nil {
		return err
	}
	// App wiring installs the staking-backed provider before InitGenesis. Direct
	// keeper fixtures may omit it, but a production import must retain the
	// OPEN-time validator set through the inclusive freeze vote deadline.
	if k.validatorSnapshots != nil {
		if err := k.validateFreezeValidatorHistoryCoverage(ctx, genState.Params); err != nil {
			return err
		}
	}
	if err := k.validateGenesisIdentityBindings(genState); err != nil {
		return err
	}
	if err := validateModelProfileGenesisCommitments(ctx, genState); err != nil {
		return err
	}
	if err := k.assertFreshGenesisStore(ctx); err != nil {
		return err
	}
	if err := k.initBridgeGenesis(ctx, genState); err != nil {
		return err
	}
	if err := k.Params.Set(ctx, genState.Params); err != nil {
		return err
	}
	// GenesisState.params_meta (field 2) carries the params version bookkeeping
	// row. A fresh genesis leaves it at version 0, which is exactly the value the
	// first MsgUpdateHubParams must present as expected_version.
	if genState.ParamsMeta.ParamsVersion != 0 {
		if err := k.ParamsMeta.Set(ctx, genState.ParamsMeta); err != nil {
			return err
		}
	}
	// One epoch definition for the whole import: types.GenesisState.Validate
	// recomputes every epoch-dependent aggregate at types.GenesisEpoch, so the
	// index rebuild below must place rows relative to the same epoch or a document
	// that validates could still land in the wrong expiry/prune bucket.
	genesisEpoch := types.GenesisEpoch
	for _, state := range genState.Models {
		if err := k.setModelState(ctx, state); err != nil {
			return err
		}
	}
	for _, state := range genState.Profiles {
		if err := k.setProfileState(ctx, state); err != nil {
			return err
		}
		if err := k.RegistrationReceipt.Set(ctx, state.RegistrationDigest, types.RegistrationReceipt{ModelId: state.ModelId, ProfileVersion: state.ProfileVersion}); err != nil {
			return err
		}
	}
	for _, state := range genState.CortexNodes {
		if err := k.StoreCortexNode(ctx, state.OperatorAddress, state); err != nil {
			return err
		}
		if state.ServiceKeyStatus == types.ServiceKeyStatusActive {
			if err := k.StoreCurrentServiceAddressIndex(ctx, types.NewCurrentServiceAddressIndexKey(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, state.CurrentServiceAddress), types.CurrentServiceAddressIndexState{OperatorAddress: state.OperatorAddress, ServiceAuthorizationNonce: state.ServiceAuthorizationNonce}); err != nil {
				return err
			}
		}
	}
	for _, state := range genState.ServiceBonds {
		if err := k.WriteServiceBondValue(ctx, types.NewServiceBondKey(state.OperatorAddress), state); err != nil {
			return err
		}
		if state.EffectiveActiveBond != state.ActiveBond {
			if err := k.ServiceBondEffectiveIndex.Set(ctx, types.NewServiceBondEffectiveKey(state.EffectiveBondEpoch, state.OperatorAddress)); err != nil {
				return err
			}
		}
	}
	if err := k.EnsureServiceBondEffectiveIndexInvariant(ctx); err != nil {
		return fmt.Errorf("service bond effective index: %w", err)
	}
	for _, state := range genState.ServiceUnbondings {
		id := shared.Hash32Key(state.UnbondingId)
		if err := k.WriteUnbondingValue(ctx, types.NewUnbondingKey(state.OperatorAddress, id), state); err != nil {
			return err
		}
		if err := k.UnbondingByOperatorStatusIndex.Set(ctx, types.NewUnbondingByOperatorStatusKey(state.OperatorAddress, state.Status, state.MatureHeight, id)); err != nil {
			return err
		}
		if state.Status == types.UnbondingStatusOpen {
			if err := k.UnbondingMaturityIndex.Set(ctx, types.NewUnbondingMaturityIndexKey(state.MatureHeight, state.OperatorAddress, id)); err != nil {
				return err
			}
		}
	}
	for _, state := range genState.ServiceUnbondingReceipts {
		id := shared.Hash32Key(state.UnbondingId)
		// receipt_hash is the only proof a withdrawn unbonding ever existed once the
		// primary row is gone, and its preimage is chain-id bound. Importing the
		// stored bytes verbatim would let a document carry a hash that no node can
		// reproduce - including one lifted from a different chain - so recompute it
		// from the row and refuse the mismatch instead of persisting a receipt whose
		// digest nothing can re-derive.
		expected, err := k.unbondingReceiptHash(sdk.UnwrapSDKContext(ctx).ChainID(), state)
		if err != nil {
			return fmt.Errorf("unbonding receipt %s: %w", hexRef(id), err)
		}
		if !bytes.Equal(expected, state.ReceiptHash) {
			return fmt.Errorf("unbonding receipt %s receipt_hash does not match its canonical preimage", hexRef(id))
		}
		if err := k.WriteUnbondingReceiptValue(ctx, id, state); err != nil {
			return err
		}
		if err := k.UnbondingReceiptPruneIndex.Set(ctx, types.NewUnbondingReceiptPruneKey(state.TerminalHeight+genState.Params.Service.UnbondingReceiptRetentionBlocks, id)); err != nil {
			return err
		}
	}
	for _, state := range genState.ServiceDescriptors {
		if err := k.StoreServiceDescriptor(ctx, types.NewParticipantKey(state.ParticipantType, state.OperatorAddress), state); err != nil {
			return err
		}
	}
	for _, state := range genState.TaskLiabilityReservations {
		if err := k.WriteTaskLiabilityValue(ctx, types.NewTaskLiabilityReservationKey(state.TaskId, state.Duty, state.OperatorAddress), state); err != nil {
			return err
		}
		if state.Status == types.TaskLiabilityStatusReserved {
			if err := k.TaskLiabilityByTaskIndex.Set(ctx, types.NewTaskLiabilityByTaskKey(state.TaskId, state.Duty, state.OperatorAddress)); err != nil {
				return err
			}
			if err := k.ActiveLiabilityByOperatorIndex.Set(ctx, types.NewActiveLiabilityByOperatorKey(state.OperatorAddress, state.TaskId, state.Duty)); err != nil {
				return err
			}
		}
	}
	for _, state := range genState.ServiceKeyResponsibilities {
		key, err := serviceKeyResponsibilityKey(state.ParticipantType, state.OperatorAddress, state.ResponsibilityId)
		if err != nil {
			return err
		}
		indexKey, err := serviceKeyResponsibilityByTaskKey(state)
		if err != nil {
			return err
		}
		if err := k.WriteServiceKeyResponsibilityValue(ctx, key, state); err != nil {
			return err
		}
		if err := k.ServiceKeyResponsibilityByTaskIndex.Set(ctx, indexKey); err != nil {
			return err
		}
	}
	for _, state := range genState.ProfileCapabilities {
		if err := k.ProfileCapability.Set(ctx, types.NewProfileCapabilityKey(state.OperatorAddress, state.ModelId, state.ProfileVersion), state); err != nil {
			return err
		}
	}
	for _, state := range genState.ModelSupports {
		if err := k.ModelSupport.Set(ctx, types.NewModelSupportKey(state.OperatorAddress, state.ModelId, state.ProfileVersion), state); err != nil {
			return err
		}
		// P1-11: index rebuild has exactly one implementation shared with the
		// runtime. The previous inline block wrote the by-profile/by-operator
		// projections only for declared rows (so an undeclared row became
		// invisible to every operator-scoped scan), always wrote the expiry index
		// even for an already stale row, and never wrote the prune index (so
		// imported rows were never retained-and-collected).
		if err := k.WriteModelSupportIndexes(ctx, state, genesisEpoch, uint64(genState.Params.Support.ModelSupportRowRetentionEpochs)); err != nil {
			return err
		}
	}
	for _, state := range genState.SupportDeactivateCursors {
		if err := k.SupportDeactivateCursor.Set(ctx, types.NewProfileStateKey(state.ModelId, state.ProfileVersion), state); err != nil {
			return err
		}
	}
	for _, state := range genState.DailySupports {
		if err := k.DailySupport.Set(ctx, types.NewDailySupportKey(state.Epoch, state.OperatorAddress), state); err != nil {
			return err
		}
		expiryEpoch, err := checkedAdd(state.Epoch, uint64(genState.Params.Support.DailySupportRetentionEpochs))
		if err != nil {
			return err
		}
		if err := k.DailySupportExpiryIndex.Set(ctx, types.NewDailySupportExpiryIndexKey(expiryEpoch, state.OperatorAddress, state.Epoch)); err != nil {
			return err
		}
	}
	if err := k.initCandidatePoolGenesis(ctx, genState); err != nil {
		return err
	}

	for _, state := range genState.Builders {
		if err := k.StoreBuilder(ctx, state.BuilderAddress, state); err != nil {
			return err
		}
		if state.CurrentServiceKeyStatus == types.ServiceKeyStatusActive {
			key := types.NewCurrentServiceAddressIndexKey(
				shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
				state.CurrentServiceAddress,
			)
			value := types.CurrentServiceAddressIndexState{
				OperatorAddress:           state.BuilderAddress,
				ServiceAuthorizationNonce: state.ServiceAuthorizationNonce,
			}
			if err := k.StoreCurrentServiceAddressIndex(ctx, key, value); err != nil {
				return err
			}
		}
	}
	for _, state := range genState.BuilderAdmissions {
		if err := k.storeBuilderAdmission(ctx, state); err != nil {
			return err
		}
	}
	for _, state := range genState.BuilderSets {
		if err := k.StoreBuilderSet(ctx, state); err != nil {
			return err
		}
	}
	if genState.CurrentBuilderSet.BuilderSetVersion != 0 {
		if err := k.CurrentBuilderSet.Set(ctx, genState.CurrentBuilderSet); err != nil {
			return err
		}
	}
	if pending := genState.PendingBuilderSetReplacement; pending != nil {
		if err := k.StorePendingBuilderSetReplacement(ctx, *pending); err != nil {
			return err
		}
		key := types.NewBuilderSetReplacementKey(pending.EffectiveHeight, pending.NextBuilderSetVersion)
		if err := k.BuilderSetReplacementIndex.Set(ctx, key, pending.ProposalId); err != nil {
			return err
		}
	}
	builderSetRefCounts := make(map[uint64]uint32, len(genState.BuilderSets))
	for _, state := range genState.BuilderSetTaskRefs {
		taskKey := shared.Hash32Key(state.TaskId)
		if err := validateBuilderSetTaskRef(state, state.TaskId, state.BuilderSetVersion); err != nil {
			return fmt.Errorf("builder set task reference: %w", err)
		}
		key := types.NewBuilderSetTaskRefKey(taskKey, state.BuilderSetVersion)
		if err := k.BuilderSetTaskRef.Set(ctx, key, state); err != nil {
			return err
		}
		if builderSetRefCounts[state.BuilderSetVersion] == ^uint32(0) {
			return fmt.Errorf("builder set version %d task_ref_count overflow", state.BuilderSetVersion)
		}
		builderSetRefCounts[state.BuilderSetVersion]++
	}
	for _, state := range genState.BuilderSets {
		if state.TaskRefCount != builderSetRefCounts[state.BuilderSetVersion] {
			return fmt.Errorf(
				"builder set version %d task_ref_count %d does not match %d task reference rows",
				state.BuilderSetVersion, state.TaskRefCount, builderSetRefCounts[state.BuilderSetVersion],
			)
		}
	}
	if err := k.rebuildBuilderSetIndexes(ctx, genState.BuilderSets); err != nil {
		return fmt.Errorf("builder set indexes: %w", err)
	}

	for _, state := range genState.BuilderFaults {
		if err := state.Validate(); err != nil {
			return fmt.Errorf("builder fault: %w", err)
		}
		if err := k.WriteBuilderFaultValue(ctx, types.NewBuilderFaultKey(state.BuilderAddress, state.FaultId), state); err != nil {
			return err
		}
		if err := k.BuilderFaultPruneIndex.Set(ctx, types.NewBuilderFaultPruneKey(state.PruneHeight, state.BuilderAddress, state.FaultId)); err != nil {
			return err
		}
	}
	for _, state := range genState.SlashSummaries {
		if err := state.Validate(); err != nil {
			return fmt.Errorf("slash summary: %w", err)
		}
		sourceKey, err := types.SlashSummarySourceKey(state.SourceKind, state.SourceId)
		if err != nil {
			return fmt.Errorf("slash summary source: %w", err)
		}
		key := types.NewSlashSummaryKey(state.SourceKind, sourceKey, state.EffectIndex)
		if err := k.WriteSlashSummaryValue(ctx, key, state); err != nil {
			return err
		}
	}

	if err := k.initRoleFaultGenesis(ctx, genState.RoleFaults, genState.Params.Service.RecordRetentionBlocks); err != nil {
		return fmt.Errorf("role fault genesis: %w", err)
	}
	// No jail/tombstone import: jail_count, normal_action_count_since_jail and
	// the TOMBSTONED status ride on ServiceBondState (the data-structure contract)
	// and are imported with genState.ServiceBonds above.
	if err := k.Treasury.Set(ctx, genState.Treasury); err != nil {
		return err
	}
	if err := k.initTreasurySpendGenesis(ctx, genState); err != nil {
		return err
	}
	if err := k.EnsureTreasurySpendReceiptInvariant(ctx); err != nil {
		return fmt.Errorf("treasury spend receipt invariant: %w", err)
	}
	if err := k.initParameterBucketGenesis(ctx, genState); err != nil {
		return err
	}

	// Reward primaries and every derived index are validated and rebuilt together
	// so an export/import round trip cannot silently drop runner work.
	if err := k.initRewardGenesis(ctx, genState); err != nil {
		return err
	}
	if err := k.EnsureRewardEpochInvariant(ctx); err != nil {
		return fmt.Errorf("reward epoch invariant: %w", err)
	}
	for _, state := range genState.Earnings {
		if err := k.setEarnings(ctx, state); err != nil {
			return err
		}
	}
	if err := k.initBeaconGenesis(ctx, &genState); err != nil {
		return err
	}
	if err := k.initVrfKeyGenesis(ctx, genState); err != nil {
		return fmt.Errorf("vrf key genesis: %w", err)
	}
	for _, state := range genState.FreezeSignalBuildCursors {
		key := types.NewFreezeSignalBuildCursorKey(state.ModelId, state.ProfileVersion)
		if err := k.FreezeSignalBuildCursor.Set(ctx, key, state); err != nil {
			return err
		}
	}
	for _, state := range genState.FreezeSignalWindowBindings {
		key := types.NewFreezeSignalByWindowKey(state.ModelId, state.ProfileVersion, state.RiskWindowId)
		if err := k.FreezeSignalByWindow.Set(ctx, key, state); err != nil {
			return err
		}
	}
	for _, state := range genState.FreezeSignals {
		signalID := append([]byte(nil), state.FreezeSignalId...)
		if err := k.FreezeSignalState.Set(ctx, signalID, state); err != nil {
			return err
		}
		if err := k.FreezeSignalByProfileIndex.Set(ctx, types.NewFreezeSignalByProfileKey(state.ModelId, state.ProfileVersion, state.SignalStatus, state.RiskWindowEndHeight, signalID)); err != nil {
			return err
		}
		if state.SignalStatus == types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_OPEN {
			if err := k.FreezeSignalDeadlineIndex.Set(ctx, types.NewFreezeSignalDeadlineKey(state.VoteDeadlineHeight, signalID)); err != nil {
				return err
			}
		} else {
			pruneHeight, overflow := checkedAddHubUint64(state.ClosedHeight, genState.Params.Freeze.FreezeSignalDetailRetentionBlocks)
			if overflow {
				return fmt.Errorf("freeze signal detail prune height overflow")
			}
			if err := k.FreezeSignalPruneIndex.Set(ctx, types.NewFreezeSignalPruneKey(pruneHeight, signalID, types.FreezeSignalPrunePhase_FREEZE_SIGNAL_PRUNE_PHASE_VOTES)); err != nil {
				return err
			}
		}
	}
	for _, state := range genState.EmergencyFreezeVotes {
		key := types.NewEmergencyFreezeVoteKey(state.FreezeSignalId, state.ValidatorConsensusAddress)
		if err := k.EmergencyFreezeVoteState.Set(ctx, key, state); err != nil {
			return err
		}
	}
	for _, profile := range genState.Profiles {
		if profile.Status == types.ModelStatusDelisted {
			continue
		}
		if _, err := k.FreezeSignalBuildCursor.Get(ctx, types.NewFreezeSignalBuildCursorKey(profile.ModelId, profile.ProfileVersion)); err == nil {
			continue
		} else if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		if _, found, err := k.openFreezeSignalForProfile(ctx, profile.ModelId, profile.ProfileVersion); err != nil {
			return err
		} else if found {
			continue
		}
		if err := k.scheduleNextFreezeRiskWindow(ctx, profile); err != nil {
			return err
		}
	}
	for _, state := range genState.ServiceDescriptors {
		operatorBytes, operatorAddress, err := k.requireCanonicalAddress("service descriptor operator_address", state.OperatorAddress)
		if err != nil {
			return err
		}
		if operatorAddress != state.OperatorAddress {
			return fmt.Errorf("service descriptor operator_address is not canonical")
		}
		expected, err := types.CanonicalServiceDescriptorHash(
			state.ParticipantType, operatorBytes, state.DescriptorVersion, state.Endpoints, genState.Params.Service,
		)
		if err != nil {
			return fmt.Errorf("service descriptor: %w", err)
		}
		if !bytes.Equal(expected, state.DescriptorHash) {
			return fmt.Errorf("service descriptor hash does not match canonical endpoints")
		}
	}
	if err := k.EnsureSlashSummaryInvariant(ctx); err != nil {
		return fmt.Errorf("slash summary invariant: %w", err)
	}
	// The same runtime invariant that guards the identity index in FinalizeBlock has
	// to hold the moment the store is populated. Without this call a document whose
	// identity rows and index rows disagree imports cleanly and then halts every
	// node at height 1, which is a far worse failure mode than refusing the import:
	// a rejected genesis can be edited, a halted chain cannot.
	if err := k.EnsureCurrentServiceAddressIndexInvariant(ctx); err != nil {
		return fmt.Errorf("current service address index invariant: %w", err)
	}
	if err := k.EnsureFreezeRiskScheduleInvariant(ctx); err != nil {
		return fmt.Errorf("freeze risk schedule invariant: %w", err)
	}
	return nil
}

func validateModelProfileGenesisCommitments(ctx context.Context, genState types.GenesisState) error {
	chainID := sdk.UnwrapSDKContext(ctx).ChainID()
	businessDenom := genState.Params.Phase0.BusinessDenom
	seenDigests := make(map[string]string, len(genState.Profiles))
	for _, state := range genState.Profiles {
		if err := validateProfileStateWithParams(state, genState.Params); err != nil {
			return fmt.Errorf("profile %s/%d: %w", state.ModelId, state.ProfileVersion, err)
		}
		projection := profileProjectionFromState(state)
		projection.MinStake = sdk.NewCoin(businessDenom, sdkmath.NewIntFromUint64(state.MinStake))
		projection.RegistrationFee = sdk.NewCoin(businessDenom, sdkmath.NewIntFromUint64(state.RegistrationFeePaid))
		digest, _, err := types.ModelRegistrationDigest(chainID, state.ProposerAddress, projection)
		if err != nil {
			return fmt.Errorf("profile %s/%d registration digest: %w", state.ModelId, state.ProfileVersion, err)
		}
		if !bytes.Equal(digest, state.RegistrationDigest) {
			return fmt.Errorf("profile %s/%d registration_digest does not match its canonical preimage", state.ModelId, state.ProfileVersion)
		}
		digestKey := hex.EncodeToString(digest)
		profileKey := fmt.Sprintf("%s/%d", state.ModelId, state.ProfileVersion)
		if prior, exists := seenDigests[digestKey]; exists {
			return fmt.Errorf("registration digest is shared by profiles %s and %s", prior, profileKey)
		}
		seenDigests[digestKey] = profileKey
	}
	return nil
}

func (k Keeper) assertFreshGenesisStore(ctx context.Context) error {
	if _, err := k.Params.Get(ctx); err == nil {
		return fmt.Errorf("hub store is already initialized; delete/reset node data before applying fresh genesis")
	} else if !errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("check existing hub params: %w", err)
	}
	return nil
}

func (k Keeper) validateGenesisIdentityBindings(genState types.GenesisState) error {
	for _, state := range genState.CortexNodes {
		if _, _, err := k.requireCanonicalAddress("cortex node operator_address", state.OperatorAddress); err != nil {
			return err
		}
	}
	for _, state := range genState.Builders {
		if _, _, err := k.requireCanonicalAddress("builder address", state.BuilderAddress); err != nil {
			return err
		}
	}
	for _, state := range genState.ServiceKeyResponsibilities {
		if state.ResponsibilityKind == types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE {
			expected, err := workerOutputEvidenceResponsibilityID(state.SessionId, state.TaskId, state.OperatorAddress)
			if err != nil || !bytes.Equal(state.ResponsibilityId, expected) {
				return fmt.Errorf("WORKER_OUTPUT_EVIDENCE responsibility_id does not match its registered preimage")
			}
			continue
		}
		if state.ResponsibilityKind != types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE {
			continue
		}
		sessionID, err := hex.DecodeString(state.SessionId)
		if err != nil {
			return fmt.Errorf("decode BUS_OBJECTIVE_EVIDENCE session_id: %w", err)
		}
		taskID, err := hex.DecodeString(state.TaskId)
		if err != nil {
			return fmt.Errorf("decode BUS_OBJECTIVE_EVIDENCE task_id: %w", err)
		}
		prepared, err := k.prepareBusObjectiveEvidenceResponsibility(
			shared.BusObjectiveEvidenceResponsibilityV1{
				SchemaVersion:             busObjectiveEvidenceResponsibilitySchemaVersion,
				BuilderOperator:           state.OperatorAddress,
				SessionId:                 sessionID,
				TaskId:                    taskID,
				ServiceAuthorizationNonce: state.ServiceAuthorizationNonce,
			},
		)
		if err != nil {
			return fmt.Errorf("BUS_OBJECTIVE_EVIDENCE responsibility identity: %w", err)
		}
		if !bytes.Equal(state.ResponsibilityId, prepared.id) {
			return fmt.Errorf("BUS_OBJECTIVE_EVIDENCE responsibility_id does not match its registered preimage")
		}
	}
	return nil
}

func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	genesis := types.DefaultGenesis()
	var err error
	genesis.Params, err = k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	genesis.ParamsMeta, err = k.GetHubParamsMeta(ctx)
	if err != nil {
		return nil, err
	}
	genesis.Models, err = collectMapValues[string, types.ModelState](ctx, k.Model)
	if err != nil {
		return nil, err
	}
	genesis.Profiles, err = collectMapValues[types.ProfileStateKeyPair, types.ProfileState](ctx, k.Profile)
	if err != nil {
		return nil, err
	}
	genesis.CortexNodes, err = k.exportCortexNodes(ctx)
	if err != nil {
		return nil, err
	}
	genesis.ServiceBonds, err = k.exportServiceBonds(ctx)
	if err != nil {
		return nil, err
	}
	if err := k.EnsureServiceBondEffectiveIndexInvariant(ctx); err != nil {
		return nil, fmt.Errorf("service bond effective index: %w", err)
	}
	genesis.ServiceUnbondings, err = k.exportUnbondings(ctx)
	if err != nil {
		return nil, err
	}
	genesis.ServiceUnbondingReceipts, err = k.exportUnbondingReceipts(ctx)
	if err != nil {
		return nil, err
	}
	genesis.ServiceDescriptors, err = k.exportServiceDescriptors(ctx)
	if err != nil {
		return nil, err
	}
	genesis.TaskLiabilityReservations, err = k.exportTaskLiabilities(ctx)
	if err != nil {
		return nil, err
	}
	genesis.ServiceKeyResponsibilities, err = k.exportServiceKeyResponsibilities(ctx)
	if err != nil {
		return nil, err
	}
	genesis.ProfileCapabilities, err = collectMapValues[types.ProfileCapabilityKeyTriple, types.ProfileCapabilityState](ctx, k.ProfileCapability)
	if err != nil {
		return nil, err
	}
	genesis.ModelSupports, err = collectMapValues[types.ModelSupportKeyTriple, types.ModelSupportState](ctx, k.ModelSupport)
	if err != nil {
		return nil, err
	}
	genesis.DailySupports, err = collectMapValues[types.DailySupportKey, types.DailySupportState](ctx, k.DailySupport)
	if err != nil {
		return nil, err
	}
	genesis.SupportDeactivateCursors, err = collectMapValues[types.ProfileStateKeyPair, types.SupportDeactivateCursorState](ctx, k.SupportDeactivateCursor)
	if err != nil {
		return nil, err
	}
	if err := k.exportCandidatePoolGenesis(ctx, genesis); err != nil {
		return nil, err
	}

	genesis.Builders, err = k.exportBuilders(ctx)
	if err != nil {
		return nil, err
	}
	genesis.BuilderAdmissions, err = k.exportBuilderAdmissionGenesis(ctx)
	if err != nil {
		return nil, err
	}
	if current, getErr := k.CurrentBuilderSet.Get(ctx); getErr == nil {
		genesis.CurrentBuilderSet = current
	} else if !errors.Is(getErr, collections.ErrNotFound) {
		return nil, getErr
	}
	if pending, getErr := k.GetPendingBuilderSetReplacement(ctx); getErr == nil {
		genesis.PendingBuilderSetReplacement = &pending
	} else if !errors.Is(getErr, collections.ErrNotFound) {
		return nil, getErr
	}
	genesis.BuilderSets, err = k.exportBuilderSets(ctx)
	if err != nil {
		return nil, err
	}
	genesis.BuilderSetTaskRefs, err = collectMapValues[types.BuilderSetTaskRefKeyPair, types.BuilderSetTaskRefState](ctx, k.BuilderSetTaskRef)
	if err != nil {
		return nil, err
	}
	genesis.BuilderFaults, err = k.exportBuilderFaults(ctx)
	if err != nil {
		return nil, err
	}
	if err := k.EnsureBuilderFaultPruneInvariant(ctx); err != nil {
		return nil, fmt.Errorf("builder fault prune index: %w", err)
	}

	if err := k.validateRoleFaultPruneIndex(ctx, genesis.Params.Service.RecordRetentionBlocks, false); err != nil {
		return nil, fmt.Errorf("role fault prune index: %w", err)
	}
	// The by-task index is derived and therefore not exported, but it is proven
	// here so a broken index fails the export rather than being silently dropped
	// and rebuilt into a different fault vector on the next import.
	if err := k.EnsureRoleFaultByTaskIndexInvariant(ctx); err != nil {
		return nil, fmt.Errorf("role fault by task index: %w", err)
	}
	genesis.RoleFaults, err = k.exportRoleFaults(ctx)
	if err != nil {
		return nil, err
	}
	genesis.SlashSummaries, err = k.exportSlashSummaries(ctx)
	if err != nil {
		return nil, err
	}
	if treasury, err := k.Treasury.Get(ctx); err == nil {
		genesis.Treasury = treasury
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, err
	}
	genesis.TreasurySpendReceipts, err = k.exportTreasurySpendReceipts(ctx)
	if err != nil {
		return nil, err
	}
	genesis.TreasurySpendProposals, err = collectMapValues[uint64, types.TreasurySpendProposalState](ctx, k.TreasurySpendProposal)
	if err != nil {
		return nil, err
	}
	genesis.TreasurySpendEpochs, err = collectMapValues[uint64, types.TreasurySpendEpochState](ctx, k.TreasurySpendEpoch)
	if err != nil {
		return nil, err
	}
	genesis.TreasurySpendRecipientEpochs, err = k.exportTreasuryRecipientEpochs(ctx)
	if err != nil {
		return nil, err
	}
	genesis.TreasurySpendEpochCleanupCursors, err = k.exportTreasuryCleanupCursors(ctx)
	if err != nil {
		return nil, err
	}
	genesis.ParameterBucketVersions, err = collectMapValues[types.ParameterBucketVersionKeyTriple, types.ParameterBucketVersionState](ctx, k.ParameterBucketVersion)
	if err != nil {
		return nil, err
	}
	genesis.ParameterBucketCurrentPointers, err = collectMapValues[types.ParameterBucketPointerKeyPair, types.ParameterBucketCurrentPointerState](ctx, k.ParameterBucketCurrentPointer)
	if err != nil {
		return nil, err
	}
	genesis.ParameterBucketPendingPointers, err = collectMapValues[types.ParameterBucketPointerKeyPair, types.ParameterBucketPendingPointerState](ctx, k.ParameterBucketPendingPointer)
	if err != nil {
		return nil, err
	}

	if err := k.EnsureRewardEpochInvariant(ctx); err != nil {
		return nil, fmt.Errorf("reward epoch invariant: %w", err)
	}
	genesis.RewardCompetitionEpochs, err = collectMapValues[types.HardwareTierEpochKey, types.RewardCompetitionEpochState](ctx, k.RewardCompetitionEpoch)
	if err != nil {
		return nil, err
	}
	genesis.RewardEpochCursors, err = collectMapValues[types.RewardEpochCursorKeyPair, types.RewardEpochCursorState](ctx, k.RewardEpochCursor)
	if err != nil {
		return nil, err
	}
	genesis.RewardEpochPruneIndexes, err = k.exportRewardEpochPruneIndexes(ctx)
	if err != nil {
		return nil, err
	}
	genesis.RewardEpochPruneCursors, err = collectMapValues[types.RewardEpochCursorKeyPair, types.RewardEpochPruneCursorState](ctx, k.RewardEpochPruneCursor)
	if err != nil {
		return nil, err
	}
	genesis.RewardEpochAudits, err = collectMapValues[types.RewardEpochCursorKeyPair, types.RewardEpochAuditState](ctx, k.RewardEpochAudit)
	if err != nil {
		return nil, err
	}
	genesis.Earnings, err = k.exportEarningsGenesis(ctx)
	if err != nil {
		return nil, err
	}

	if err := k.exportBeaconGenesis(ctx, genesis); err != nil {
		return nil, err
	}
	if err := k.exportVrfKeyGenesis(ctx, genesis); err != nil {
		return nil, fmt.Errorf("vrf key genesis export: %w", err)
	}
	genesis.FreezeSignals, err = collectMapValues[[]byte, types.FreezeSignalState](ctx, k.FreezeSignalState)
	if err != nil {
		return nil, err
	}
	genesis.FreezeSignalBuildCursors, err = collectMapValues[types.FreezeSignalBuildCursorKey, types.FreezeSignalBuildCursorState](ctx, k.FreezeSignalBuildCursor)
	if err != nil {
		return nil, err
	}
	genesis.FreezeSignalWindowBindings, err = collectMapValues[types.FreezeSignalByWindowKey, types.FreezeSignalByWindowIndex](ctx, k.FreezeSignalByWindow)
	if err != nil {
		return nil, err
	}
	genesis.EmergencyFreezeVotes, err = collectMapValues[types.EmergencyFreezeVoteKey, types.EmergencyFreezeVoteState](ctx, k.EmergencyFreezeVoteState)
	if err != nil {
		return nil, err
	}
	if err := k.EnsureParameterBucketInvariant(ctx); err != nil {
		return nil, fmt.Errorf("parameter bucket invariant: %w", err)
	}
	if err := k.EnsureTreasurySpendReceiptInvariant(ctx); err != nil {
		return nil, fmt.Errorf("treasury spend receipt invariant: %w", err)
	}
	if err := k.EnsureSlashSummaryInvariant(ctx); err != nil {
		return nil, fmt.Errorf("slash summary invariant: %w", err)
	}
	if err := k.exportBridgeGenesis(ctx, genesis); err != nil {
		return nil, fmt.Errorf("bridge genesis export: %w", err)
	}
	if err := k.EnsureBridgeSupplyInvariant(ctx); err != nil {
		return nil, fmt.Errorf("bridge supply invariant: %w", err)
	}
	return genesis, nil
}

func collectMapValues[K, V any](ctx context.Context, m collections.Map[K, V]) ([]V, error) {
	iter, err := m.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	values := []V{}
	for ; iter.Valid(); iter.Next() {
		value, err := iter.Value()
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}
