package keeper_test

import (
	"bytes"
	"sort"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	builderSetTestChainID    = "trueopen-builder-set-test"
	builderSetTestAcceptedAt = uint64(10)
	builderSetTestGenesisID  = "genesis-builder-set"
	builderSetTestNextID     = "governed-builder-set-2"
)

type builderSetReplacementFixture struct {
	*fixture
	// members is sorted by address bytes, which is the order §9.6c requires every
	// member list to be in. The genesis set holds members[0:3]; a replacement that
	// drops members[0] and adds members[3] is the smallest change that exercises
	// both the ADMITTED and the REVOKED branch.
	members []string
	leadFor uint64
}

func newBuilderSetReplacementFixture(t *testing.T) *builderSetReplacementFixture {
	t.Helper()
	f := initFixture(t)
	sdkCtx := sdk.UnwrapSDKContext(f.ctx).
		WithBlockHeight(int64(builderSetTestAcceptedAt)).
		WithChainID(builderSetTestChainID)
	f.ctx = sdk.WrapSDKContext(sdkCtx)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))

	members := make([]string, 0, 4)
	for _, fill := range []byte{0x61, 0x62, 0x63, 0x64} {
		identity := hubIdentity(t, fill)
		registerBuilderIdentityForTest(t, f, identity, builderSetTestAcceptedAt)
		members = append(members, identity.Address)
	}
	sort.Slice(members, func(i, j int) bool {
		return bytes.Compare(hubAddressBytes(t, members[i]), hubAddressBytes(t, members[j])) < 0
	})

	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	fixture := &builderSetReplacementFixture{
		fixture: f, members: members,
		leadFor: builderSetTestAcceptedAt + params.Builder.BuilderSetUpdateLeadBlocks,
	}
	fixture.seedGenesisBuilderSet(t)
	return fixture
}

func TestBuilderAdmissionStoresRawAddress(t *testing.T) {
	f := newBuilderSetReplacementFixture(t)
	member := f.members[0]
	stored, err := f.keeper.BuilderAdmission.Get(f.ctx, member)
	require.NoError(t, err)
	encoded, err := stored.Marshal()
	require.NoError(t, err)
	require.False(t, bytes.Contains(encoded, []byte(member)))
	require.True(t, bytes.Contains(encoded, hubAddressBytes(t, member)))
}

func TestBuilderAdmissionGenesisRoundTrip(t *testing.T) {
	f := newBuilderSetReplacementFixture(t)
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, exported.BuilderAdmissions, 3)
	for _, state := range exported.BuilderAdmissions {
		require.NotEmpty(t, state.BuilderAddress)
		require.Equal(t, types.BuilderStatus_BUILDER_STATUS_ADMITTED, state.Status)
		require.Nil(t, state.XSourceProposalId)
	}

	restarted := initFixture(t)
	restarted.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restarted.ctx).WithChainID(builderSetTestChainID))
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.Equal(t, exported.BuilderAdmissions, reexported.BuilderAdmissions)
}

func TestBuilderAdmissionProposalPresenceRoundTrip(t *testing.T) {
	f := newBuilderSetReplacementFixture(t)
	_, err := f.keeper.ExecuteReplaceBuilderSetV1(f.ctx, f.accepted(7), f.action(t, 7))
	require.NoError(t, err)
	due := f.atHeight(f.leadFor)
	require.NoError(t, due.keeper.BeginBlocker(due.ctx))

	exported, err := due.keeper.ExportGenesis(due.ctx)
	require.NoError(t, err)
	require.Len(t, exported.BuilderAdmissions, 4)
	for _, state := range exported.BuilderAdmissions {
		require.Equal(t, uint64(7), state.GetSourceProposalId())
		require.NotNil(t, state.XSourceProposalId)
	}

	restarted := initFixture(t)
	restarted.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restarted.ctx).WithChainID(builderSetTestChainID))
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.Equal(t, exported.BuilderAdmissions, reexported.BuilderAdmissions)
}

func TestBuilderSetValuesStoreRawMemberAddresses(t *testing.T) {
	f := newBuilderSetReplacementFixture(t)
	set, err := f.keeper.BuilderSet.Get(f.ctx, 1)
	require.NoError(t, err)
	encodedSet, err := set.Marshal()
	require.NoError(t, err)
	for _, member := range f.members[:3] {
		require.False(t, bytes.Contains(encodedSet, []byte(member)))
		require.True(t, bytes.Contains(encodedSet, hubAddressBytes(t, member)))
	}

	_, err = f.keeper.ExecuteReplaceBuilderSetV1(f.ctx, f.accepted(7), f.action(t, 7))
	require.NoError(t, err)
	pending, err := f.keeper.PendingBuilderSetReplacement.Get(f.ctx)
	require.NoError(t, err)
	encodedPending, err := pending.Marshal()
	require.NoError(t, err)
	for _, member := range f.members[1:] {
		require.False(t, bytes.Contains(encodedPending, []byte(member)))
		require.True(t, bytes.Contains(encodedPending, hubAddressBytes(t, member)))
	}
}

// seedGenesisBuilderSet installs the version 1 set the way InitGenesis would:
// the snapshot, both derived indexes, the admission rows and the current pointer.
func (f *builderSetReplacementFixture) seedGenesisBuilderSet(t *testing.T) {
	t.Helper()
	genesisMembers := f.members[:3]
	membersHash, err := keeper.BuilderSetMembersHash(hubAddressBytesList(t, genesisMembers))
	require.NoError(t, err)
	setHash, err := keeper.BuilderSetHash(builderSetTestChainID, 1, builderSetTestGenesisID, 1, 3, membersHash)
	require.NoError(t, err)

	set := types.BuilderSetState{
		BuilderSetVersion: 1, BuilderSetId: builderSetTestGenesisID,
		BuilderSetHash: setHash, BuilderSetMembersHash: membersHash,
		EffectiveHeight: 1, ActiveBuilders: genesisMembers, ActiveBuilderCount: 3,
		BodyStatus: shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE,
	}
	require.NoError(t, f.keeper.StoreBuilderSet(f.ctx, set))
	require.NoError(t, f.keeper.BuilderSetByIDIndex.Set(f.ctx, set.BuilderSetId, set.BuilderSetVersion))
	require.NoError(t, f.keeper.BuilderSetByHeightIndex.Set(
		f.ctx, types.NewBuilderSetByHeightKey(set.EffectiveHeight, set.BuilderSetVersion), set.BuilderSetId,
	))
	for _, member := range genesisMembers {
		require.NoError(t, f.keeper.BuilderAdmission.Set(f.ctx, member, internaltypes.BuilderAdmissionStoreState{
			BuilderAddress: hubAddressBytes(t, member), Status: int32(types.BuilderStatus_BUILDER_STATUS_ADMITTED),
			CurrentBuilderSetVersion: 1, UpdatedHeight: 1,
		}))
	}
	require.NoError(t, f.keeper.CurrentBuilderSet.Set(f.ctx, types.CurrentBuilderSetState{
		Mode: "GOVERNED_FIXED_V1", BuilderSetVersion: 1, BuilderSetId: set.BuilderSetId,
		BuilderSetHash: setHash, BuilderSetMembersHash: membersHash, EffectiveHeight: 1,
	}))
}

func (f *builderSetReplacementFixture) currentSetHash(t *testing.T) []byte {
	t.Helper()
	current, err := f.keeper.CurrentBuilderSet.Get(f.ctx)
	require.NoError(t, err)
	return current.BuilderSetHash
}

// action returns the replacement that drops members[0] and admits members[3].
func (f *builderSetReplacementFixture) action(t *testing.T, proposalID uint64) types.ReplaceBuilderSetV1 {
	t.Helper()
	return types.ReplaceBuilderSetV1{
		ProposalId: proposalID, ExpectedCurrentVersion: 1,
		ExpectedCurrentSetHash: f.currentSetHash(t), NextBuilderSetId: builderSetTestNextID,
		Members: f.members[1:], EffectiveHeight: f.leadFor,
	}
}

func (f *builderSetReplacementFixture) accepted(proposalID uint64) keeper.AcceptedGovernanceActionContext {
	return keeper.AcceptedGovernanceActionContext{
		AuthorityAddress: sdk.AccAddress(f.keeper.GetAuthority()).String(),
		ProposalID:       proposalID, Accepted: true,
	}
}

func (f *builderSetReplacementFixture) atHeight(height uint64) *builderSetReplacementFixture {
	sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height))
	return &builderSetReplacementFixture{
		fixture: &fixture{ctx: sdk.WrapSDKContext(sdkCtx), keeper: f.keeper, bank: f.bank, auth: f.auth},
		members: f.members, leadFor: f.leadFor,
	}
}

func hubAddressBytes(t *testing.T, address string) []byte {
	t.Helper()
	raw, err := sdk.AccAddressFromBech32(address)
	require.NoError(t, err)
	return raw
}

func hubAddressBytesList(t *testing.T, addresses []string) [][]byte {
	t.Helper()
	raw := make([][]byte, len(addresses))
	for index, address := range addresses {
		raw[index] = hubAddressBytes(t, address)
	}
	return raw
}

// TestExecuteReplaceBuilderSetV1SchedulesWithoutSwitching covers the half of
// §9.6c that is easy to get backwards: acceptance must not move the current
// pointer. A Task assigned earlier in the same block is bound to a builder set
// version, so a replacement that took effect at acceptance height would change
// the set out from under it.
func TestExecuteReplaceBuilderSetV1SchedulesWithoutSwitching(t *testing.T) {
	f := newBuilderSetReplacementFixture(t)

	result, err := f.keeper.ExecuteReplaceBuilderSetV1(f.ctx, f.accepted(7), f.action(t, 7))
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Status)
	require.Equal(t, uint64(2), result.Pending.NextBuilderSetVersion)
	require.Equal(t, f.leadFor, result.Pending.EffectiveHeight)
	require.Equal(t, builderSetTestAcceptedAt, result.Pending.AcceptedHeight)
	require.Equal(t, f.members[1:], result.Pending.NextActiveBuilders)

	current, err := f.keeper.CurrentBuilderSet.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), current.BuilderSetVersion, "acceptance must not switch the current set")

	_, err = f.keeper.GetBuilderSet(f.ctx, 2)
	require.ErrorIs(t, err, collections.ErrNotFound, "the next snapshot exists only from effective_height")

	proposalID, err := f.keeper.BuilderSetReplacementIndex.Get(f.ctx, types.NewBuilderSetReplacementKey(f.leadFor, 2))
	require.NoError(t, err)
	require.Equal(t, uint64(7), proposalID, "the index row is what BeginBlock scans")
}

// TestExecuteReplaceBuilderSetV1DistinguishesReplayFromConflict pins the two
// outcomes of a second execution while a replacement is pending. They differ only
// by the stored action digest, which is the reason the digest is persisted at all.
func TestExecuteReplaceBuilderSetV1DistinguishesReplayFromConflict(t *testing.T) {
	f := newBuilderSetReplacementFixture(t)
	action := f.action(t, 7)
	first, err := f.keeper.ExecuteReplaceBuilderSetV1(f.ctx, f.accepted(7), action)
	require.NoError(t, err)

	replay, err := f.keeper.ExecuteReplaceBuilderSetV1(f.ctx, f.accepted(7), action)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replay.Status)
	require.Equal(t, first.Pending, replay.Pending)

	sameProposalDifferentBody := action
	sameProposalDifferentBody.NextBuilderSetId = "governed-builder-set-2b"
	_, err = f.keeper.ExecuteReplaceBuilderSetV1(f.ctx, f.accepted(7), sameProposalDifferentBody)
	require.ErrorContains(t, err, "already pending")

	otherProposal := f.action(t, 8)
	_, err = f.keeper.ExecuteReplaceBuilderSetV1(f.ctx, f.accepted(8), otherProposal)
	require.ErrorContains(t, err, "already pending")
}

// TestExecuteReplaceBuilderSetV1RejectsUnusableActions walks the preconditions
// §9.6c states, each one against an otherwise valid action.
func TestExecuteReplaceBuilderSetV1RejectsUnusableActions(t *testing.T) {
	for name, testCase := range map[string]struct {
		mutate  func(*builderSetReplacementFixture, *types.ReplaceBuilderSetV1)
		context func(*builderSetReplacementFixture) keeper.AcceptedGovernanceActionContext
		message string
	}{
		"rejected proposal": {
			context: func(f *builderSetReplacementFixture) keeper.AcceptedGovernanceActionContext {
				execution := f.accepted(7)
				execution.Accepted = false
				return execution
			},
			message: "accepted proposal",
		},
		"locator mismatch": {
			context: func(f *builderSetReplacementFixture) keeper.AcceptedGovernanceActionContext {
				return f.accepted(8)
			},
			message: "does not match the execution locator",
		},
		"foreign authority": {
			context: func(f *builderSetReplacementFixture) keeper.AcceptedGovernanceActionContext {
				execution := f.accepted(7)
				execution.AuthorityAddress = f.members[0]
				return execution
			},
			message: "authority mismatch",
		},
		"stale expected version": {
			mutate:  func(_ *builderSetReplacementFixture, a *types.ReplaceBuilderSetV1) { a.ExpectedCurrentVersion = 2 },
			message: "chain is at version 1",
		},
		"stale expected hash": {
			mutate: func(_ *builderSetReplacementFixture, a *types.ReplaceBuilderSetV1) {
				a.ExpectedCurrentSetHash = bytes.Repeat([]byte{0x09}, shared.Hash32KeySize)
			},
			message: "chain is at version 1",
		},
		"effective height inside the lead window": {
			mutate:  func(f *builderSetReplacementFixture, a *types.ReplaceBuilderSetV1) { a.EffectiveHeight = f.leadFor - 1 },
			message: "lead height",
		},
		"member count below builders_per_task": {
			mutate:  func(f *builderSetReplacementFixture, a *types.ReplaceBuilderSetV1) { a.Members = f.members[1:3] },
			message: "outside",
		},
		"unsorted members": {
			mutate: func(f *builderSetReplacementFixture, a *types.ReplaceBuilderSetV1) {
				a.Members = []string{f.members[2], f.members[1], f.members[3]}
			},
			message: "strictly ascending",
		},
		// Dropping the identity rather than substituting a foreign address keeps the
		// member list in §9.6c order, so the ordering rule cannot mask this one.
		"member without a builder identity": {
			mutate: func(f *builderSetReplacementFixture, _ *types.ReplaceBuilderSetV1) {
				require.NoError(t, f.keeper.Builder.Remove(f.ctx, f.members[2]))
			},
			message: "no builder identity",
		},
		"member whose service key is not ACTIVE": {
			mutate: func(f *builderSetReplacementFixture, _ *types.ReplaceBuilderSetV1) {
				state, err := f.keeper.GetBuilderState(f.ctx, f.members[2])
				require.NoError(t, err)
				state.CurrentServiceKeyStatus = types.ServiceKeyStatusRevoked
				require.NoError(t, f.keeper.StoreBuilder(f.ctx, f.members[2], state))
			},
			message: "ACTIVE service key binding",
		},
		"member without a service descriptor": {
			mutate: func(f *builderSetReplacementFixture, _ *types.ReplaceBuilderSetV1) {
				require.NoError(t, f.keeper.ServiceDescriptor.Remove(
					f.ctx, types.NewParticipantKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, f.members[2]),
				))
			},
			message: "no service descriptor",
		},
		"builder_set_id already taken": {
			mutate: func(_ *builderSetReplacementFixture, a *types.ReplaceBuilderSetV1) {
				a.NextBuilderSetId = builderSetTestGenesisID
			},
			message: "already exists",
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBuilderSetReplacementFixture(t)
			action := f.action(t, 7)
			if testCase.mutate != nil {
				testCase.mutate(f, &action)
			}
			execution := f.accepted(7)
			if testCase.context != nil {
				execution = testCase.context(f)
			}
			_, err := f.keeper.ExecuteReplaceBuilderSetV1(f.ctx, execution, action)
			require.ErrorContains(t, err, testCase.message)

			_, err = f.keeper.GetPendingBuilderSetReplacement(f.ctx)
			require.ErrorIs(t, err, collections.ErrNotFound, "a rejected action must not leave a pending row")
		})
	}
}

// TestExecuteReplaceBuilderSetV1TreatsAnUnchangedMemberSetAsNoop covers the
// §9.6c rule that costs a version if it is missed: re-proposing the sitting
// members must not create a pending row, because activating it would supersede
// the current set and start its retention clock for no change at all.
func TestExecuteReplaceBuilderSetV1TreatsAnUnchangedMemberSetAsNoop(t *testing.T) {
	f := newBuilderSetReplacementFixture(t)
	action := f.action(t, 7)
	action.Members = f.members[:3]

	result, err := f.keeper.ExecuteReplaceBuilderSetV1(f.ctx, f.accepted(7), action)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, result.Status)
	require.Zero(t, result.Pending.NextBuilderSetVersion)

	_, err = f.keeper.GetPendingBuilderSetReplacement(f.ctx)
	require.ErrorIs(t, err, collections.ErrNotFound)
	has, err := f.keeper.BuilderSetReplacementIndex.Has(f.ctx, types.NewBuilderSetReplacementKey(f.leadFor, 2))
	require.NoError(t, err)
	require.False(t, has)
}

// TestActivateDueBuilderSetReplacementsSwitchesAtEffectiveHeight is the whole
// §15.1 step 2 transaction: the new snapshot, both derived indexes, the admission
// flips in both directions, the current pointer, superseded_height on the outgoing
// set and the retirement of the pending row plus its index.
func TestActivateDueBuilderSetReplacementsSwitchesAtEffectiveHeight(t *testing.T) {
	f := newBuilderSetReplacementFixture(t)
	pending, err := f.keeper.ExecuteReplaceBuilderSetV1(f.ctx, f.accepted(7), f.action(t, 7))
	require.NoError(t, err)

	// One block before: nothing may move.
	early := f.atHeight(f.leadFor - 1)
	require.NoError(t, early.keeper.BeginBlocker(early.ctx))
	current, err := early.keeper.CurrentBuilderSet.Get(early.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), current.BuilderSetVersion)

	due := f.atHeight(f.leadFor)
	eventStart := len(sdk.UnwrapSDKContext(due.ctx).EventManager().Events())
	require.NoError(t, due.keeper.BeginBlocker(due.ctx))

	current, err = due.keeper.CurrentBuilderSet.Get(due.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), current.BuilderSetVersion)
	require.Equal(t, builderSetTestNextID, current.BuilderSetId)
	require.Equal(t, "GOVERNED_FIXED_V1", current.Mode)
	require.Equal(t, pending.Pending.NextBuilderSetHash, current.BuilderSetHash)
	require.Equal(t, f.leadFor, current.EffectiveHeight)

	next, err := due.keeper.GetBuilderSet(due.ctx, 2)
	require.NoError(t, err)
	require.Equal(t, shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE, next.BodyStatus)
	require.Equal(t, f.members[1:], next.ActiveBuilders)
	require.Equal(t, uint32(3), next.ActiveBuilderCount)
	require.Equal(t, uint64(7), next.GetSourceProposalId())
	require.Nil(t, next.GetXSupersededHeight())

	version, err := due.keeper.BuilderSetByIDIndex.Get(due.ctx, builderSetTestNextID)
	require.NoError(t, err)
	require.Equal(t, uint64(2), version)
	setID, err := due.keeper.BuilderSetByHeightIndex.Get(due.ctx, types.NewBuilderSetByHeightKey(f.leadFor, 2))
	require.NoError(t, err)
	require.Equal(t, builderSetTestNextID, setID)

	previous, err := due.keeper.GetBuilderSet(due.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, f.leadFor, previous.GetSupersededHeight(),
		"superseded_height is effective_height, not the height the sweep happened to run")

	for _, member := range f.members[1:] {
		admission, err := due.keeper.BuilderAdmission.Get(due.ctx, member)
		require.NoError(t, err)
		require.Equal(t, int32(types.BuilderStatus_BUILDER_STATUS_ADMITTED), admission.Status)
		require.Equal(t, uint64(2), admission.CurrentBuilderSetVersion)
		require.True(t, admission.HasSourceProposalId)
		require.Equal(t, uint64(7), admission.SourceProposalId)
		require.Equal(t, f.leadFor, admission.UpdatedHeight)
	}
	removed, err := due.keeper.BuilderAdmission.Get(due.ctx, f.members[0])
	require.NoError(t, err)
	require.Equal(t, int32(types.BuilderStatus_BUILDER_STATUS_REVOKED), removed.Status)

	_, err = due.keeper.GetPendingBuilderSetReplacement(due.ctx)
	require.ErrorIs(t, err, collections.ErrNotFound)
	has, err := due.keeper.BuilderSetReplacementIndex.Has(due.ctx, types.NewBuilderSetReplacementKey(f.leadFor, 2))
	require.NoError(t, err)
	require.False(t, has, "the index row must be retired or BeginBlock revisits it every block")

	events := sdk.UnwrapSDKContext(due.ctx).EventManager().Events()[eventStart:]
	require.True(t, hasEventType(events, proto.MessageName(&types.EventBuilderSetUpdated{})))

	// Re-running the sweep is what proves the rows are gone rather than shadowed.
	later := f.atHeight(f.leadFor + 1)
	require.NoError(t, later.keeper.BeginBlocker(later.ctx))
	current, err = later.keeper.CurrentBuilderSet.Get(later.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), current.BuilderSetVersion)
}

// TestActivateDueBuilderSetReplacementsSchedulesTheOutgoingBodyPrune guards a gap
// the ref counter cannot close on its own: ReleaseBuilderSetTaskRef only schedules
// a body prune on the transition to zero refs, so a set that was already at zero
// when it was superseded would never be scheduled by anyone.
func TestActivateDueBuilderSetReplacementsSchedulesTheOutgoingBodyPrune(t *testing.T) {
	f := newBuilderSetReplacementFixture(t)
	_, err := f.keeper.ExecuteReplaceBuilderSetV1(f.ctx, f.accepted(7), f.action(t, 7))
	require.NoError(t, err)

	due := f.atHeight(f.leadFor)
	require.NoError(t, due.keeper.BeginBlocker(due.ctx))

	iter, err := due.keeper.BuilderSetPruneIndex.Iterate(due.ctx, nil)
	require.NoError(t, err)
	defer iter.Close()
	scheduled := make([]types.BuilderSetPruneKeyTriple, 0, 1)
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		require.NoError(t, err)
		scheduled = append(scheduled, key)
	}
	require.Len(t, scheduled, 1)
	require.Equal(t, uint64(1), scheduled[0].K2(), "the superseded set is the one that becomes prunable")
	require.Equal(t, uint32(types.BuilderSetPrunePhase_BUILDER_SET_PRUNE_PHASE_BODY), scheduled[0].K3())
}

// TestActivateDueBuilderSetReplacementsRejectsAnOrphanedIndexRow pins the
// bidirectional pending/index correspondence. Silently dropping the row would let
// BeginBlock paper over a store divergence that §15.1 step 4 says must stop the
// block instead.
func TestActivateDueBuilderSetReplacementsRejectsAnOrphanedIndexRow(t *testing.T) {
	f := newBuilderSetReplacementFixture(t)
	_, err := f.keeper.ExecuteReplaceBuilderSetV1(f.ctx, f.accepted(7), f.action(t, 7))
	require.NoError(t, err)
	require.NoError(t, f.keeper.PendingBuilderSetReplacement.Remove(f.ctx))

	due := f.atHeight(f.leadFor)
	require.ErrorContains(t, due.keeper.BeginBlocker(due.ctx), "has no pending replacement")
}
