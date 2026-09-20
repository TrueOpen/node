package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// BuilderSetReplacementResult reports what one accepted ReplaceBuilderSetV1 item
// did. APPLIED means a pending replacement now exists and will activate in
// BeginBlock at its effective_height; NOOP covers the two §9.6c no-op shapes -
// the exact same action replayed while its pending row is still there, and a
// proposal whose member set already is the current one.
type BuilderSetReplacementResult struct {
	Pending types.BuilderSetPendingReplacementState
	Status  shared.MutationStatusV1
}

// ExecuteReplaceBuilderSetV1 is the internal callback for one accepted x/gov
// item. Like the treasury and bridge actions it is deliberately absent from
// hub.v1.Msg, AutoCLI and Tx routing: §9.6c makes governance the only
// admission and current-pointer path in Phase 0, and MsgRunBuilderTerm stays
// FEATURE_DISABLED.
//
// This half only *schedules*. The contract separates acceptance from effect so
// that the builder set a Task was assigned against cannot change under it inside
// the same block; ActivateDueBuilderSetReplacements performs the switch in
// BeginBlock of effective_height, before any transaction of that block runs.
func (k Keeper) ExecuteReplaceBuilderSetV1(
	ctx context.Context,
	execution AcceptedGovernanceActionContext,
	action types.ReplaceBuilderSetV1,
) (BuilderSetReplacementResult, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() <= 0 || sdkCtx.ChainID() == "" {
		return BuilderSetReplacementResult{}, fmt.Errorf("builder set replacement requires a positive height and non-empty chain_id")
	}
	height := uint64(sdkCtx.BlockHeight())
	if err := k.requireAcceptedBuilderSetAction(execution, action.ProposalId); err != nil {
		return BuilderSetReplacementResult{}, err
	}
	digest, err := types.ReplaceBuilderSetActionDigest(sdkCtx.ChainID(), action)
	if err != nil {
		return BuilderSetReplacementResult{}, err
	}
	actionDigest := digest[:]

	current, err := k.CurrentBuilderSet.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return BuilderSetReplacementResult{}, fmt.Errorf("builder set replacement requires an existing current builder set")
	} else if err != nil {
		return BuilderSetReplacementResult{}, err
	}
	if current.BuilderSetVersion != action.ExpectedCurrentVersion ||
		!bytes.Equal(current.BuilderSetHash, action.ExpectedCurrentSetHash) {
		return BuilderSetReplacementResult{}, fmt.Errorf("builder set replacement expects version %d, chain is at version %d", action.ExpectedCurrentVersion, current.BuilderSetVersion)
	}

	// The pending row is an Item, so "at most one pending replacement" is a
	// consequence of the store shape. What still needs deciding is what a second
	// execution means: the same proposal item re-run under the same digest is the
	// exact replay §9.6c calls a no-op, everything else is a conflict.
	existing, err := k.PendingBuilderSetReplacement.Get(ctx)
	switch {
	case err == nil:
		if existing.ProposalId != action.ProposalId || !bytes.Equal(existing.ActionDigest, actionDigest) {
			return BuilderSetReplacementResult{}, fmt.Errorf("builder set replacement %d is already pending", existing.ProposalId)
		}
		return BuilderSetReplacementResult{Pending: existing, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
	case !errors.Is(err, collections.ErrNotFound):
		return BuilderSetReplacementResult{}, err
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return BuilderSetReplacementResult{}, err
	}
	memberBytes, err := types.ReplaceBuilderSetMemberBytes(action.Members)
	if err != nil {
		return BuilderSetReplacementResult{}, err
	}
	memberCount := uint32(len(memberBytes))
	if memberCount < params.Builder.BuildersPerTask || memberCount > params.Builder.BuilderSetCap {
		return BuilderSetReplacementResult{}, fmt.Errorf("builder set replacement member count %d is outside [%d,%d]", memberCount, params.Builder.BuildersPerTask, params.Builder.BuilderSetCap)
	}
	members := make([]string, len(action.Members))
	for index, member := range action.Members {
		raw, canonical, err := k.requireCanonicalAddress("builder set member", member)
		if err != nil {
			return BuilderSetReplacementResult{}, err
		}
		// The digest decoded the same string independently; if the two decodings
		// disagree the stored snapshot would not be the set that was hashed.
		if canonical != member || !bytes.Equal(raw, memberBytes[index]) {
			return BuilderSetReplacementResult{}, fmt.Errorf("builder set member %q is not the canonical encoding of its address bytes", member)
		}
		if err := k.requireAdmissibleBuilder(ctx, canonical); err != nil {
			return BuilderSetReplacementResult{}, err
		}
		members[index] = canonical
	}

	if _, err := k.BuilderSetByIDIndex.Get(ctx, action.NextBuilderSetId); err == nil {
		return BuilderSetReplacementResult{}, fmt.Errorf("builder_set_id %q already exists", action.NextBuilderSetId)
	} else if !errors.Is(err, collections.ErrNotFound) {
		return BuilderSetReplacementResult{}, err
	}
	if current.BuilderSetVersion == math.MaxUint64 {
		return BuilderSetReplacementResult{}, fmt.Errorf("builder set version overflow")
	}
	nextVersion := current.BuilderSetVersion + 1
	minEffectiveHeight, err := checkedAdd(height, params.Builder.BuilderSetUpdateLeadBlocks)
	if err != nil {
		return BuilderSetReplacementResult{}, fmt.Errorf("builder set replacement lead height overflow: %w", err)
	}
	if action.EffectiveHeight < minEffectiveHeight {
		return BuilderSetReplacementResult{}, fmt.Errorf("builder set replacement effective_height %d is earlier than the %d lead height", action.EffectiveHeight, minEffectiveHeight)
	}

	membersHash, err := BuilderSetMembersHash(memberBytes)
	if err != nil {
		return BuilderSetReplacementResult{}, err
	}
	if bytes.Equal(membersHash, current.BuilderSetMembersHash) {
		// §9.6c: an unchanged member set is expressed only by the x/gov execution
		// receipt. Creating a pending row here would burn a version and supersede
		// the current set for a replacement that changes nothing.
		return BuilderSetReplacementResult{Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
	}
	setHash, err := BuilderSetHash(sdkCtx.ChainID(), nextVersion, action.NextBuilderSetId, action.EffectiveHeight, memberCount, membersHash)
	if err != nil {
		return BuilderSetReplacementResult{}, err
	}

	pending := types.BuilderSetPendingReplacementState{
		ProposalId:                action.ProposalId,
		ActionDigest:              actionDigest,
		ExpectedCurrentVersion:    action.ExpectedCurrentVersion,
		ExpectedCurrentSetHash:    append([]byte(nil), action.ExpectedCurrentSetHash...),
		NextBuilderSetVersion:     nextVersion,
		NextBuilderSetId:          action.NextBuilderSetId,
		NextBuilderSetHash:        setHash,
		NextBuilderSetMembersHash: membersHash,
		NextActiveBuilders:        members,
		NextActiveBuilderCount:    memberCount,
		AcceptedHeight:            height,
		EffectiveHeight:           action.EffectiveHeight,
	}
	if err := k.PendingBuilderSetReplacement.Set(ctx, pending); err != nil {
		return BuilderSetReplacementResult{}, err
	}
	// The index is what BeginBlock reads; genesis writes the same pair, so the
	// two producers of a pending replacement stay indistinguishable downstream.
	if err := k.BuilderSetReplacementIndex.Set(ctx, types.NewBuilderSetReplacementKey(pending.EffectiveHeight, pending.NextBuilderSetVersion), pending.ProposalId); err != nil {
		return BuilderSetReplacementResult{}, err
	}
	return BuilderSetReplacementResult{Pending: pending, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

// requireAcceptedBuilderSetAction rejects anything that did not arrive through an
// accepted x/gov item. ReplaceBuilderSetV1 carries no item_index, so the locator
// is the proposal_id alone - exactly the bridge action shape, not the treasury
// one.
func (k Keeper) requireAcceptedBuilderSetAction(execution AcceptedGovernanceActionContext, proposalID uint64) error {
	if !execution.Accepted {
		return fmt.Errorf("builder set replacement requires an accepted proposal")
	}
	if proposalID == 0 || execution.ProposalID != proposalID {
		return fmt.Errorf("builder set replacement proposal_id %d does not match the execution locator %d", proposalID, execution.ProposalID)
	}
	authority, _, err := k.requireCanonicalAddress("governance authority", execution.AuthorityAddress)
	if err != nil {
		return err
	}
	if !bytes.Equal(authority, k.authority) {
		return fmt.Errorf("builder set replacement governance authority mismatch")
	}
	return nil
}

// requireAdmissibleBuilder is the §9.6c membership precondition: a Genesis Builder
// identity, an ACTIVE current service key and a descriptor row at the version the
// identity points at. It runs at acceptance rather than at activation because a
// proposal that names an unusable Builder must fail while it can still be voted
// on again, not halt BeginBlock a lead window later.
func (k Keeper) requireAdmissibleBuilder(ctx context.Context, builder string) error {
	state, found, err := k.loadBuilder(ctx, builder)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("builder set member %s has no builder identity", builder)
	}
	if !isRegisteredBuilderState(state) {
		return fmt.Errorf("builder set member %s does not hold an ACTIVE service key binding", builder)
	}
	descriptor, err := k.ServiceDescriptor.Get(ctx, types.NewParticipantKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, builder))
	if errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("builder set member %s has no service descriptor", builder)
	} else if err != nil {
		return err
	}
	if err := descriptor.Validate(); err != nil {
		return fmt.Errorf("builder set member %s has an invalid service descriptor: %w", builder, err)
	}
	if descriptor.DescriptorVersion != state.CurrentDescriptorVersion {
		return fmt.Errorf("builder set member %s descriptor version %d does not match its identity version %d", builder, descriptor.DescriptorVersion, state.CurrentDescriptorVersion)
	}
	return nil
}

// ActivateDueBuilderSetReplacements is the Keeper detailed design step 2. It runs in
// BeginBlock, not EndBlock, because every transaction of this block must already
// see the new set: a SignedOrder validated against the old members inside the
// block that promotes them would bind a Task to a set that no longer exists.
//
// The scan is driven by BuilderSetReplacementIndex rather than by the pending row
// alone so that a replacement whose effective_height passed while the chain was
// down still activates on the first block after restart.
func (k Keeper) ActivateDueBuilderSetReplacements(ctx context.Context, height uint64) error {
	iter, err := k.BuilderSetReplacementIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	due := make([]types.BuilderSetReplacementKeyPair, 0, 1)
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return err
		}
		if key.K1() > height {
			// The index ascends by effective_height, so the first future row ends it.
			break
		}
		due = append(due, key)
	}
	if err := iter.Close(); err != nil {
		return err
	}
	for _, key := range due {
		if err := k.activateBuilderSetReplacement(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// activateBuilderSetReplacement performs the whole §9.6c activation inside one
// cache transaction: new snapshot, admissions, current pointer, superseded height
// on the outgoing set, then the pending row and its index. Any step failing rolls
// the block's BeginBlock back rather than leaving a half-switched set, which is
// why nothing is written through ctx directly.
func (k Keeper) activateBuilderSetReplacement(ctx context.Context, key types.BuilderSetReplacementKeyPair) error {
	pending, err := k.PendingBuilderSetReplacement.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("builder set replacement index row (%d,%d) has no pending replacement", key.K1(), key.K2())
	} else if err != nil {
		return err
	}
	if pending.EffectiveHeight != key.K1() || pending.NextBuilderSetVersion != key.K2() {
		return fmt.Errorf("pending builder set replacement does not match its index row (%d,%d)", key.K1(), key.K2())
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)

	current, err := k.CurrentBuilderSet.Get(cache)
	if err != nil {
		return err
	}
	if current.BuilderSetVersion != pending.ExpectedCurrentVersion ||
		!bytes.Equal(current.BuilderSetHash, pending.ExpectedCurrentSetHash) {
		return fmt.Errorf("pending builder set replacement no longer matches current builder set version %d", current.BuilderSetVersion)
	}
	previous, err := k.BuilderSet.Get(cache, current.BuilderSetVersion)
	if err != nil {
		return err
	}

	next := types.BuilderSetState{
		BuilderSetVersion:     pending.NextBuilderSetVersion,
		BuilderSetId:          pending.NextBuilderSetId,
		BuilderSetHash:        pending.NextBuilderSetHash,
		BuilderSetMembersHash: pending.NextBuilderSetMembersHash,
		EffectiveHeight:       pending.EffectiveHeight,
		ActiveBuilders:        pending.NextActiveBuilders,
		ActiveBuilderCount:    pending.NextActiveBuilderCount,
		XSourceProposalId:     &types.BuilderSetState_SourceProposalId{SourceProposalId: pending.ProposalId},
		BodyStatus:            shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE,
	}
	// The snapshot goes through the same validator genesis import uses, so a
	// pending row that survived a migration cannot install a set whose stored hash
	// disagrees with its members.
	if err := k.validateBuilderSetState(cache, next, next.BuilderSetVersion); err != nil {
		return err
	}
	if err := k.BuilderSet.Set(cache, next.BuilderSetVersion, next); err != nil {
		return err
	}
	if err := k.BuilderSetByIDIndex.Set(cache, next.BuilderSetId, next.BuilderSetVersion); err != nil {
		return err
	}
	if err := k.BuilderSetByHeightIndex.Set(cache, types.NewBuilderSetByHeightKey(next.EffectiveHeight, next.BuilderSetVersion), next.BuilderSetId); err != nil {
		return err
	}

	// Admission rows are written at effective_height rather than at the height the
	// sweep happens to run, so a chain that restarts late reproduces byte-identical
	// state to one that never stopped.
	listed := make(map[string]struct{}, len(next.ActiveBuilders))
	for _, builder := range next.ActiveBuilders {
		listed[builder] = struct{}{}
		if err := k.setBuilderAdmission(cache, builder, types.BuilderStatus_BUILDER_STATUS_ADMITTED, next.BuilderSetVersion, pending.ProposalId, next.EffectiveHeight); err != nil {
			return err
		}
	}
	for _, builder := range previous.ActiveBuilders {
		if _, stays := listed[builder]; stays {
			continue
		}
		if err := k.setBuilderAdmission(cache, builder, types.BuilderStatus_BUILDER_STATUS_REVOKED, next.BuilderSetVersion, pending.ProposalId, next.EffectiveHeight); err != nil {
			return err
		}
	}

	if err := k.CurrentBuilderSet.Set(cache, types.CurrentBuilderSetState{
		// Phase 0 has exactly one mode and a replacement does not change it; carrying
		// the old pointer's value keeps the genesis round-trip check honest.
		Mode:                  current.Mode,
		BuilderSetVersion:     next.BuilderSetVersion,
		BuilderSetId:          next.BuilderSetId,
		BuilderSetHash:        next.BuilderSetHash,
		BuilderSetMembersHash: next.BuilderSetMembersHash,
		EffectiveHeight:       next.EffectiveHeight,
	}); err != nil {
		return err
	}

	previous.XSupersededHeight = &types.BuilderSetState_SupersededHeight{SupersededHeight: pending.EffectiveHeight}
	if err := k.BuilderSet.Set(cache, previous.BuilderSetVersion, previous); err != nil {
		return err
	}
	if err := k.PendingBuilderSetReplacement.Remove(cache); err != nil {
		return err
	}
	if err := k.BuilderSetReplacementIndex.Remove(cache, key); err != nil {
		return err
	}
	// A set that is superseded with no Task still referencing it is prunable right
	// now; ReleaseBuilderSetTaskRef only schedules on the transition to zero, so a
	// set that was already at zero would otherwise never be scheduled at all. The
	// helper re-checks eligibility, which is why it runs after the pending row is
	// gone.
	if err := k.scheduleBuilderSetBodyPrune(cache, previous); err != nil {
		return err
	}

	mustEmitHubEvent(cache, &types.EventBuilderSetUpdated{
		OldVersion:            previous.BuilderSetVersion,
		NewVersion:            next.BuilderSetVersion,
		BuilderSetId:          next.BuilderSetId,
		BuilderSetHash:        next.BuilderSetHash,
		BuilderSetMembersHash: next.BuilderSetMembersHash,
		EffectiveHeight:       next.EffectiveHeight,
	})
	write()
	return nil
}

func (k Keeper) setBuilderAdmission(
	ctx context.Context,
	builder string,
	status types.BuilderStatus,
	builderSetVersion, proposalID, height uint64,
) error {
	return k.BuilderAdmission.Set(ctx, builder, types.BuilderAdmissionState{
		BuilderAddress:           builder,
		Status:                   status,
		CurrentBuilderSetVersion: builderSetVersion,
		XSourceProposalId:        &types.BuilderAdmissionState_SourceProposalId{SourceProposalId: proposalID},
		UpdatedHeight:            height,
	})
}
