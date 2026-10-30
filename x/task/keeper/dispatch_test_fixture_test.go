package keeper

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"

	corestore "cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	"github.com/TrueOpen/wire/dispatch"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/internal/testutil/sessionfixture"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

const dispatchTestDenom = "uusdc"
const dispatchTestPrefund = uint64(2_000_000)

type dispatchTestAuth struct{ account sdk.AccountI }

func (a dispatchTestAuth) GetAccount(_ context.Context, address sdk.AccAddress) sdk.AccountI {
	if bytes.Equal(a.account.GetAddress(), address) {
		return a.account
	}
	return nil
}

// The mock bank writes into the same SDK cache as the handler. A rejected item
// or failed enclosing Msg therefore rolls back actual mock custody as well.
type dispatchTestBank struct{ store corestore.KVStoreService }

func (b dispatchTestBank) balance(ctx context.Context, address sdk.AccAddress) uint64 {
	raw, err := b.store.OpenKVStore(ctx).Get(append([]byte("dispatch-test/balance/"), address...))
	if err != nil {
		panic(err)
	}
	if raw == nil {
		return 0
	}
	return binary.BigEndian.Uint64(raw)
}
func (b dispatchTestBank) setBalance(ctx context.Context, address sdk.AccAddress, value uint64) error {
	return b.store.OpenKVStore(ctx).Set(append([]byte("dispatch-test/balance/"), address...), shared.Uint64BE(value))
}
func (b dispatchTestBank) GetBalance(ctx context.Context, address sdk.AccAddress, denom string) sdk.Coin {
	return sdk.NewCoin(denom, sdkmath.NewIntFromUint64(b.balance(ctx, address)))
}
func (b dispatchTestBank) transfer(ctx context.Context, from, to sdk.AccAddress, coins sdk.Coins) error {
	amount := coins.AmountOf(dispatchTestDenom).Uint64()
	available := b.balance(ctx, from)
	if available < amount {
		return fmt.Errorf("mock bank funds are insufficient")
	}
	next, overflow := shared.CheckedAddUint64(b.balance(ctx, to), amount)
	if overflow {
		return fmt.Errorf("mock bank balance overflows")
	}
	if err := b.setBalance(ctx, from, available-amount); err != nil {
		return err
	}
	return b.setBalance(ctx, to, next)
}
func (b dispatchTestBank) SendCoinsFromAccountToModule(ctx context.Context, from sdk.AccAddress, module string, coins sdk.Coins) error {
	return b.transfer(ctx, from, authtypes.NewModuleAddress(module), coins)
}
func (b dispatchTestBank) SendCoinsFromModuleToAccount(ctx context.Context, module string, to sdk.AccAddress, coins sdk.Coins) error {
	return b.transfer(ctx, authtypes.NewModuleAddress(module), to, coins)
}
func (b dispatchTestBank) SendCoinsFromModuleToModule(ctx context.Context, from, to string, coins sdk.Coins) error {
	return b.transfer(ctx, authtypes.NewModuleAddress(from), authtypes.NewModuleAddress(to), coins)
}

type dispatchTestHub struct {
	internalStubHubKeeper
	store           corestore.KVStoreService
	builders        []string
	keys            map[string]*secp.PrivateKey
	bindings        map[string]hubtypes.CurrentServiceKeySnapshot
	vrf             ed25519.PrivateKey
	pool            hubtypes.CandidatePoolSnapshotState
	member          hubtypes.CandidatePoolMemberState
	extraMembers    []hubtypes.CandidatePoolMemberState
	reserveFailure  bool
	fatalBucketTask []byte
	faultFailure    bool
	queryMaxBytes   uint64
	bondStatus      hubtypes.ServiceBondStatus
}

func (h *dispatchTestHub) GetHubParams(ctx sdk.Context) hubtypes.HubParamsSnapshot {
	params := h.internalStubHubKeeper.GetHubParams(ctx)
	params.EpochLengthBlocks = 120
	params.BusinessDenom = dispatchTestDenom
	if h.queryMaxBytes != 0 {
		params.MaxQueryResponseBytes = h.queryMaxBytes
	}
	return params
}
func (h *dispatchTestHub) GetBuilderSetForHeight(sdk.Context, uint64) (hubtypes.BuilderSetState, error) {
	return hubtypes.BuilderSetState{BuilderSetId: "dispatch-test-set", BuilderSetHash: bytes32(0x44), ActiveBuilders: append([]string(nil), h.builders...), ActiveBuilderCount: uint32(len(h.builders)), BodyStatus: shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE}, nil
}
func (h *dispatchTestHub) GetBuilderSetByID(ctx sdk.Context, _ string) (hubtypes.BuilderSetState, error) {
	return h.GetBuilderSetForHeight(ctx, 0)
}
func (h *dispatchTestHub) GetCurrentServiceKey(_ context.Context, role, operator string) (hubtypes.CurrentServiceKeySnapshot, error) {
	if binding, ok := h.bindings[operator]; ok {
		return binding, nil
	}
	key := h.keys[operator]
	if key == nil {
		return hubtypes.CurrentServiceKeySnapshot{}, fmt.Errorf("mock service identity is missing")
	}
	return hubtypes.CurrentServiceKeySnapshot{ParticipantType: role, OperatorAddress: operator, ServiceAddress: operator, ServicePubkey: hex.EncodeToString(key.PubKey().SerializeCompressed()), AuthorizationNonce: 1, Status: hubtypes.ServiceKeyStatusActive}, nil
}
func (h *dispatchTestHub) GetDispatchServiceProofKey(ctx context.Context, role shared.ParticipantType, operator string) (hubtypes.CurrentServiceKeySnapshot, error) {
	name := shared.ParticipantTypeCortexNode
	if role == shared.ParticipantType_PARTICIPANT_TYPE_BUILDER {
		name = shared.ParticipantTypeBuilder
	}
	return h.GetCurrentServiceKey(ctx, name, operator)
}
func (h *dispatchTestHub) GetBuilderDispatchAuthority(ctx context.Context, operator string) (hubtypes.DispatchBuilderAuthority, error) {
	binding, err := h.GetCurrentServiceKey(ctx, shared.ParticipantTypeBuilder, operator)
	return hubtypes.DispatchBuilderAuthority{Binding: binding, CompensationBalance: 2_000_000}, err
}
func (h *dispatchTestHub) GetBuilderVrfKeyForEpoch(context.Context, string, uint64) ([]byte, error) {
	return bytes.Clone(h.vrf.Public().(ed25519.PublicKey)), nil
}
func (h *dispatchTestHub) GetBeaconForDomain(_ sdk.Context, _ string, height int64) (hubtypes.BeaconSnapshot, bool) {
	return hubtypes.BeaconSnapshot{Height: height, Randomness: bytes32(0x71), SourceTag: hubtypes.BeaconSourceProposerVRFV1, ProofDigest: bytes32(0x72)}, height == 120
}
func (h *dispatchTestHub) GetCandidatePoolSnapshot(_ sdk.Context, id []byte) (hubtypes.CandidatePoolSnapshotState, bool) {
	return h.pool, bytes.Equal(id, h.pool.SnapshotId)
}
func (h *dispatchTestHub) ResolveCandidatePoolMember(_ context.Context, id []byte, slot uint32) (hubtypes.CandidatePoolMemberState, hubtypes.CandidateSlotBindingState, error) {
	for _, member := range append([]hubtypes.CandidatePoolMemberState{h.member}, h.extraMembers...) {
		if bytes.Equal(id, h.pool.SnapshotId) && slot == member.Slot {
			return member, hubtypes.CandidateSlotBindingState{Slot: slot, SlotVersion: member.SlotVersion, OperatorAddress: member.OperatorAddress, BindingHash: member.BindingHash}, nil
		}
	}
	return hubtypes.CandidatePoolMemberState{}, hubtypes.CandidateSlotBindingState{}, fmt.Errorf("mock pool member is missing")
}

func (h *dispatchTestHub) GetModelStatus(sdk.Context, []byte) hubtypes.ModelStatus {
	return hubtypes.ModelStatusActive
}
func (h *dispatchTestHub) GetProfileState(sdk.Context, []byte, uint32) (hubtypes.ProfileStateSnapshot, bool) {
	return hubtypes.ProfileStateSnapshot{ResourceTier: 1, Status: hubtypes.ModelStatusActive, TaskTypes: []shared.TaskType{shared.TaskType_TASK_TYPE_CHAT, shared.TaskType_TASK_TYPE_TEXT_GENERATION}, MinStake: 1_000_000, PricingProfile: shared.PricingProfile{VerifyRatioBps: 1000, MinOrderValue: 1}, ExecutionSnapshotHash: bytes32(0x45), ExecutionSnapshot: shared.ProfileExecutionSnapshot{RuntimeClass: shared.RuntimeClassV1, VerificationProfile: shared.VerificationProfile{EvidenceSchemaHash: bytes32(0x46), JudgmentFunctionVersion: shared.JudgmentFunctionVersionV1, CanonicalEncodingVersion: shared.CanonicalEncodingVersionV1, MetricAggregateProofVersion: shared.MetricAggregateProofVersionV1, VerificationMode: shared.VerificationMode_VERIFICATION_MODE_SINGLE_SAMPLE, TokenScope: shared.TokenScope_TOKEN_SCOPE_ALL_GENERATED_OUTPUT_TOKENS}}}, true
}
func (h *dispatchTestHub) GetParameterBucketVersion(context.Context, shared.BucketKind, string, uint64) (hubtypes.ParameterBucketVersionState, error) {
	return hubtypes.ParameterBucketVersionState{Version: 9, EffectiveHeight: 1, TimeoutEntries: hubtypes.TimeoutBucketEntriesV1{Entries: []hubtypes.TimeoutBucketEntryV1{{UpperWorkUnitsInclusive: 10000, TimeoutBlocks: 40}}}}, nil
}
func (h *dispatchTestHub) GetServiceBond(_ sdk.Context, _ sdk.AccAddress, _ uint64) (hubtypes.ServiceBondSnapshot, bool) {
	status := h.bondStatus
	if status == hubtypes.ServiceBondStatus_SERVICE_BOND_STATUS_UNSPECIFIED {
		status = hubtypes.ServiceBondStatusActive
	}
	return hubtypes.ServiceBondSnapshot{Status: status, ActiveBond: 4_000_000, EffectiveActiveBond: 4_000_000, AvailableBond: 4_000_000, RequiredTaskLiability: 30_000, BondVersion: 1}, true
}
func (h *dispatchTestHub) GetModelCapability(sdk.Context, sdk.AccAddress, []byte) (hubtypes.ModelCapabilitySnapshot, bool) {
	return hubtypes.ModelCapabilitySnapshot{InferenceCapability: true, CapabilityVersion: 1}, true
}
func (h *dispatchTestHub) GetModelSupport(sdk.Context, sdk.AccAddress, []byte) (hubtypes.ModelSupportSnapshot, bool) {
	return hubtypes.ModelSupportSnapshot{DeclaredSupport: true, SupportActive: true, SuspendReason: hubtypes.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_NONE, SupportFreshUntilEpoch: 10, SupportVersion: 1}, true
}
func (h *dispatchTestHub) AcquireCandidatePoolTaskRef(ctx context.Context, taskID, _ []byte, _ uint64) (bool, error) {
	return true, h.store.OpenKVStore(ctx).Set(append([]byte("dispatch-test/pool-ref/"), taskID...), []byte{1})
}
func (h *dispatchTestHub) ReserveTaskLiabilityFromFrozenFact(ctx context.Context, request hubtypes.FrozenFactLiabilityRequest) (hubtypes.TaskLiabilityReservationState, error) {
	if h.reserveFailure {
		return hubtypes.TaskLiabilityReservationState{}, fmt.Errorf("mock liability reservation failed")
	}
	err := h.store.OpenKVStore(ctx).Set(append([]byte("dispatch-test/liability/"), request.TaskID...), []byte{1})
	return hubtypes.TaskLiabilityReservationState{}, err
}
func (h *dispatchTestHub) AcquireBuilderSetTaskRef(context.Context, []byte, string, []byte, uint64) (bool, error) {
	return true, nil
}
func (h *dispatchTestHub) AcquireParameterBucketTaskRef(ctx context.Context, _ shared.BucketKind, _ string, _ uint64, _ uint64) error {
	if len(h.fatalBucketTask) != 0 {
		present, err := h.store.OpenKVStore(ctx).Has(append([]byte("dispatch-test/liability/"), h.fatalBucketTask...))
		if err != nil {
			return err
		}
		if present {
			return fmt.Errorf("mock parameter bucket store failure")
		}
	}
	return nil
}

type dispatchFixture struct {
	*internalFixture
	hub         *dispatchTestHub
	bank        dispatchTestBank
	owner       string
	ownerRaw    []byte
	ownerKey    *secp.PrivateKey
	delegateKey *secp.PrivateKey
	worker      string
	sessionID   []byte
}

func newDispatchFixture(t *testing.T) *dispatchFixture {
	t.Helper()
	f := &dispatchFixture{internalFixture: initInternalFixture(t)}
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.Session.SessionWithdrawDelayBlocks = 30
	params.Session.SessionChangeDelayBlocks = 30
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID("dispatch-test").WithBlockHeight(121))
	f.ownerKey = secp.PrivKeyFromBytes(bytes32(1))
	f.delegateKey = secp.PrivKeyFromBytes(bytes32(3))
	owner := shared.EVMAddressFromSecp256k1PublicKey(f.ownerKey.PubKey())
	f.ownerRaw = bytes.Clone(owner[:])
	f.owner, err = f.keeper.addressCodec.BytesToString(owner[:])
	require.NoError(t, err)
	f.keeper.authKeeper = dispatchTestAuth{account: authtypes.NewBaseAccount(owner[:], &ethsecp256k1.PubKey{Key: f.ownerKey.PubKey().SerializeCompressed()}, 0, 0)}
	f.bank = dispatchTestBank{store: f.keeper.storeService}
	f.keeper.bankKeeper = f.bank
	require.NoError(t, f.bank.setBalance(f.ctx, sdk.AccAddress(owner[:]), 100_000_000))
	f.hub = &dispatchTestHub{store: f.keeper.storeService, keys: map[string]*secp.PrivateKey{}, vrf: ed25519.NewKeyFromSeed(bytes32(0x51))}
	for i := byte(0x11); i < 0x14; i++ {
		key := secp.PrivKeyFromBytes(bytes32(i))
		address := shared.EVMAddressFromSecp256k1PublicKey(key.PubKey())
		operator, err := f.keeper.addressCodec.BytesToString(address[:])
		require.NoError(t, err)
		f.hub.builders = append(f.hub.builders, operator)
		f.hub.keys[operator] = key
	}
	workerKey := secp.PrivKeyFromBytes(bytes32(0x21))
	worker := shared.EVMAddressFromSecp256k1PublicKey(workerKey.PubKey())
	f.worker, err = f.keeper.addressCodec.BytesToString(worker[:])
	require.NoError(t, err)
	f.hub.keys[f.worker] = workerKey
	f.hub.pool = hubtypes.CandidatePoolSnapshotState{SchemaVersion: 1, Epoch: 1, SnapshotId: bytes32(0x47), PoolHash: bytes32(0x48), Status: hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_ACTIVE, SlotCapacity: 8, ActiveCount: 1, ActiveBitmapHash: bytes32(0x49), MemberSetHash: bytes32(0x50), EffectiveHeight: 120, PublishedHeight: 120, ExpiresHeight: 240, WorkerStakeWeightPpm: 300_000, WorkerPerformanceWeightPpm: 700_000}
	f.hub.member = hubtypes.CandidatePoolMemberState{Epoch: 1, Slot: 1, SlotVersion: 1, OperatorAddress: f.worker, BindingHash: bytes32(0x52), WeightInputs: &hubtypes.CandidateWeightInputsV1{ActiveBond: shared.NewAmount(4_000_000), AvailableBond: shared.NewAmount(4_000_000), PerformanceScorePpm: 1_000_000, PerformanceMethodVersion: 1, CandidateJailFactorPpm: 1_000_000, InferenceCapable: true, Profiles: []*hubtypes.CandidateProfileWeightInputV1{{ModelId: bytes32(0x55), ProfileVersion: 1, ProfileSupportFresh: true, InferenceCapable: true, MinStakeSnapshot: shared.NewAmount(1_000_000)}}}}
	f.keeper.hubKeeper = f.hub
	delegate := shared.EVMAddressFromSecp256k1PublicKey(f.delegateKey.PubKey())
	response, err := NewMsgServerImpl(f.keeper).CreateSession(f.ctx, &types.MsgCreateSession{SignerAddress: f.owner, InitialBuilder: f.hub.builders[0], InitialDeposit: shared.NewAmount(dispatchTestPrefund), SessionKey: &types.SessionKeyGrantV1{SessionKey: delegate[:], ExpiryHeight: 500, SpendCap: shared.NewAmount(4_000_000)}})
	require.NoError(t, err)
	f.sessionID = response.SessionId
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(123))
	return f
}

func (f *dispatchFixture) signedOrder(t *testing.T, sequence uint64, key *secp.PrivateKey) types.SignedOrderV2 {
	t.Helper()
	order := sessionfixture.CoreOrder(t)
	order.ChainId = "dispatch-test"
	order.UserAddress = f.owner
	order.SessionId = bytes.Clone(f.sessionID)
	order.OrderSequence = sequence
	order.EarliestSubmitHeight = uint64(sdk.UnwrapSDKContext(f.ctx).BlockHeight())
	order.OrderExpireHeight = 600
	order.SessionAnchorHeight = order.EarliestSubmitHeight
	order.BuilderSetId = "dispatch-test-set"
	order.BuilderSetHash = bytes32(0x44)
	hash, err := types.TaskOrderHash(order)
	require.NoError(t, err)
	digest, err := types.BuildTaskOrderEIP712Digest(1, dispatchTestDenom, order, hash[:])
	require.NoError(t, err)
	compact := ecdsa.SignCompact(key, digest.SigningDigest[:], false)
	return types.SignedOrderV2{Order: order, SignatureScheme: types.SignatureSchemeEIP712, SignatureChainId: 1, UserSignature: append(append([]byte(nil), compact[1:]...), compact[0])}
}

func (f *dispatchFixture) entry(t *testing.T, signed types.SignedOrderV2, builder string) *types.DispatchEntryV1 {
	t.Helper()
	order := signed.Order
	taskID, err := types.DeriveTaskIDFromRawSession(order.SessionId, order.OrderSequence)
	require.NoError(t, err)
	taskHash, err := types.TaskOrderHash(order)
	require.NoError(t, err)
	stream, err := f.keeper.ReadStream(f.ctx, order.SessionId)
	require.NoError(t, err)
	alpha, err := dispatchMaterialDigest("task.v1.DispatchVrfInputV1", &types.DispatchVrfInputV1{ChainId: order.ChainId, Epoch: 1, EpochSeed: bytes32(0x71), SessionId: order.SessionId, OrderSequence: order.OrderSequence})
	require.NoError(t, err)
	proof, randomness, err := dispatch.ProveDispatchVRF(f.hub.vrf, alpha[:])
	require.NoError(t, err)
	member := &types.CandidateMemberRefV1{CandidatePoolSnapshotId: bytes.Clone(f.hub.pool.SnapshotId), Slot: 1, SlotVersion: 1, OperatorAddress: f.worker}
	ticket := &types.DispatchTicketV1{ServiceAuthorizationNonce: 1, SchemaVersion: 1, ChainId: order.ChainId, TaskId: taskID[:], TaskHash: taskHash[:], SessionId: order.SessionId, OrderSequence: order.OrderSequence, BuilderAddress: builder, ReferenceHeight: 120, CandidatePoolSnapshotId: member.CandidatePoolSnapshotId, VrfEpoch: 1, VrfOutput: randomness[:], VrfProof: proof, RandomnessScheme: types.DispatchRandomnessSchemeV1_DISPATCH_RANDOMNESS_SCHEME_V1_BUILDER_VRF, RosterHash: bytes32(0x61), HandraiseRootDigest: bytes32(0x62), DispatchRound: 0, Rank: 0, Target: member, AcceptDeadlineMs: 1000, InputHash: order.InputHash, BoundChangeHeight: stream.BoundChangeHeight, StickyAnchorSequence: order.OrderSequence, StickyRunCount: 1}
	signDispatchTestMaterial(t, "task.v1.DispatchTicketV1", ticket, f.hub.keys[builder], func(signature []byte) { ticket.BuilderServiceSignature = signature })
	ticketHash, err := dispatchMaterialDigest("task.v1.DispatchTicketV1", ticket)
	require.NoError(t, err)
	accept := &types.DispatchAcceptV1{SchemaVersion: 1, ChainId: order.ChainId, TaskId: taskID[:], TaskHash: taskHash[:], DispatchTicketHash: ticketHash[:], Member: member, ServiceAuthorizationNonce: 1}
	signDispatchTestMaterial(t, "task.v1.DispatchAcceptV1", accept, f.hub.keys[f.worker], func(signature []byte) { accept.ServiceSignature = signature })
	record := &types.DispatchRecordV1{ServiceAuthorizationNonce: 1, SchemaVersion: 1, ChainId: order.ChainId, TaskId: taskID[:], TaskHash: taskHash[:], SessionId: order.SessionId, OrderSequence: order.OrderSequence, BuilderAddress: builder, ReferenceHeight: 120, CandidatePoolSnapshotId: member.CandidatePoolSnapshotId, VrfEpoch: 1, VrfOutput: randomness[:], VrfProof: proof, RandomnessScheme: ticket.RandomnessScheme, BoundChangeHeight: ticket.BoundChangeHeight, StickyAnchorSequence: order.OrderSequence, StickyRunCount: 1, DispatchTicketHash: ticketHash[:], Rounds: []*types.DispatchRoundV1{{DispatchRound: 0, HandraiseRootDigest: ticket.HandraiseRootDigest, RosterHash: ticket.RosterHash, Attempts: []*types.DispatchAttemptV1{{DispatchTicketHash: ticketHash[:], Rank: 0, Member: member, Outcome: types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_ACCEPTED, RejectDigestOrZero32: make([]byte, 32)}}}}}
	signDispatchTestMaterial(t, "task.v1.DispatchRecordV1", record, f.hub.keys[builder], func(signature []byte) { record.BuilderServiceSignature = signature })
	return &types.DispatchEntryV1{SignedOrder: &signed, Record: record, WinnerTicket: ticket, Accept: accept}
}

func signDispatchTestMaterial(t *testing.T, name string, message interface{ Marshal() ([]byte, error) }, key *secp.PrivateKey, set func([]byte)) {
	t.Helper()
	set(make([]byte, 64))
	raw, err := message.Marshal()
	require.NoError(t, err)
	digest, err := dispatch.SigningDigest(name, raw)
	require.NoError(t, err)
	signature := dispatch.SignDigest(key, digest)
	set(signature)
}

func (h *dispatchTestHub) GetCandidatePoolLayout(context.Context, []byte) (uint32, uint32, uint32, bool) {
	return h.pool.SlotCapacity, 1, 1, true
}
func (h *dispatchTestHub) CandidatePoolSegmentBitmap(context.Context, []byte, uint32) ([]byte, error) {
	bitmap := byte(1 << h.member.Slot)
	for _, member := range h.extraMembers {
		bitmap |= 1 << member.Slot
	}
	return []byte{bitmap}, nil
}
func (h *dispatchTestHub) ApplyDispatchFault(ctx context.Context, req hubtypes.DispatchFaultRequest) (hubtypes.DispatchFaultState, bool, error) {
	if h.faultFailure {
		return req.Fault, false, fmt.Errorf("injected fault custody failure")
	}
	key := append([]byte("dispatch-test/fault/"), req.Fault.FaultId...)
	found, err := h.store.OpenKVStore(ctx).Has(key)
	if err != nil || found {
		return req.Fault, false, err
	}
	row := req.Fault
	row.PenaltyAmount, row.CompensatedAmount = shared.NewAmount(req.Penalty), shared.NewAmount(req.Compensation)
	raw, err := row.Marshal()
	if err == nil {
		err = h.store.OpenKVStore(ctx).Set(key, raw)
	}
	return row, true, err
}
func (h *dispatchTestHub) PayDispatchCompensation(ctx context.Context, builder, worker string, sessionID, taskID []byte, requested, height uint64) (uint64, error) {
	if h.faultFailure {
		return 0, fmt.Errorf("injected compensation custody failure")
	}
	return requested, h.store.OpenKVStore(ctx).Set(append([]byte("dispatch-test/paid/"), taskID...), shared.Uint64BE(requested))
}
func (f *dispatchFixture) resignEntry(t *testing.T, entry *types.DispatchEntryV1) {
	t.Helper()
	signDispatchTestMaterial(t, "task.v1.DispatchTicketV1", entry.WinnerTicket, f.hub.keys[entry.WinnerTicket.BuilderAddress], func(s []byte) { entry.WinnerTicket.BuilderServiceSignature = s })
	hash, err := dispatchMaterialDigest("task.v1.DispatchTicketV1", entry.WinnerTicket)
	require.NoError(t, err)
	entry.Accept.DispatchTicketHash = hash[:]
	signDispatchTestMaterial(t, "task.v1.DispatchAcceptV1", entry.Accept, f.hub.keys[f.worker], func(s []byte) { entry.Accept.ServiceSignature = s })
	entry.Record.DispatchTicketHash = hash[:]
	last := entry.Record.Rounds[len(entry.Record.Rounds)-1].Attempts
	last[len(last)-1].DispatchTicketHash = hash[:]
	signDispatchTestMaterial(t, "task.v1.DispatchRecordV1", entry.Record, f.hub.keys[entry.Record.BuilderAddress], func(s []byte) { entry.Record.BuilderServiceSignature = s })
}

func (f *dispatchFixture) committedRoster(t *testing.T, entry *types.DispatchEntryV1, delta uint64) *types.DispatchRosterV1 {
	t.Helper()
	weight, err := hubtypes.FrozenWorkerCandidateWeight(f.hub.pool, f.hub.member.WeightInputs, f.hub.member.WeightInputs.Profiles[0])
	require.NoError(t, err)
	members := []dispatch.RosterMember{{Slot: 1, SlotVersion: 1, OperatorAddress: f.worker, CandidateWeight: uint64(weight) + delta}}
	digest, err := dispatch.RosterDigest("dispatch-test", [32]byte(entry.Record.TaskId), 120, [32]byte(f.hub.pool.SnapshotId), members)
	require.NoError(t, err)
	entry.Record.Rounds[0].RosterHash = digest[:]
	entry.WinnerTicket.RosterHash = digest[:]
	f.resignEntry(t, entry)
	return &types.DispatchRosterV1{TaskId: entry.Record.TaskId, ReferenceHeight: 120, CandidatePoolSnapshotId: f.hub.pool.SnapshotId, Members: []*types.DispatchRosterMemberV1{{Slot: 1, SlotVersion: 1, OperatorAddress: f.worker, CandidateWeight: uint64(weight) + delta}}}
}
func (f *dispatchFixture) record(t *testing.T, entry *types.DispatchEntryV1) {
	t.Helper()
	response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[0].Status)
}
