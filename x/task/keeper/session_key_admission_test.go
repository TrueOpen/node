package keeper

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"testing"

	corestore "cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	dcrecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type sessionAdmissionAuth struct{ account sdk.AccountI }

func (a sessionAdmissionAuth) GetAccount(_ context.Context, address sdk.AccAddress) sdk.AccountI {
	if bytes.Equal(a.account.GetAddress(), address) {
		return a.account
	}
	return nil
}

// Bank effects use the same SDK cache as admission, so discarding a failed
// proposal must roll back both escrow and allowance rather than only one ledger.
type sessionAdmissionBank struct {
	store corestore.KVStoreService
	fail  bool
}

var sessionAdmissionEscrowKey = []byte("test/session-key/escrow")

func (b sessionAdmissionBank) escrow(ctx context.Context) uint64 {
	raw, err := b.store.OpenKVStore(ctx).Get(sessionAdmissionEscrowKey)
	if err != nil {
		panic(err)
	}
	if len(raw) == 0 {
		return 0
	}
	return binary.BigEndian.Uint64(raw)
}
func (b sessionAdmissionBank) GetBalance(ctx context.Context, _ sdk.AccAddress, denom string) sdk.Coin {
	return sdk.NewCoin(denom, sdkmath.NewIntFromUint64(10000000-b.escrow(ctx)))
}
func (b sessionAdmissionBank) SendCoinsFromAccountToModule(ctx context.Context, _ sdk.AccAddress, _ string, coins sdk.Coins) error {
	if b.fail {
		return fmt.Errorf("injected bank failure")
	}
	return b.store.OpenKVStore(ctx).Set(sessionAdmissionEscrowKey, binary.BigEndian.AppendUint64(nil, b.escrow(ctx)+coins[0].Amount.Uint64()))
}
func (b sessionAdmissionBank) SendCoinsFromModuleToAccount(ctx context.Context, _ string, _ sdk.AccAddress, coins sdk.Coins) error {
	return b.store.OpenKVStore(ctx).Set(sessionAdmissionEscrowKey, binary.BigEndian.AppendUint64(nil, b.escrow(ctx)-coins[0].Amount.Uint64()))
}
func (sessionAdmissionBank) SendCoinsFromModuleToModule(context.Context, string, string, sdk.Coins) error {
	return nil
}

type sessionAdmissionHub struct {
	internalStubHubKeeper
	builders []string
	order    *types.TaskOrderV3
}

func (h sessionAdmissionHub) GetHubParams(ctx sdk.Context) hubtypes.HubParamsSnapshot {
	p := h.internalStubHubKeeper.GetHubParams(ctx)
	p.AssignmentBuilderProposalWindowBlocks = 10
	return p
}
func (h sessionAdmissionHub) GetBlockAnchorHash(context.Context, uint64) ([]byte, error) {
	if h.order != nil {
		return bytes.Clone(h.order.SessionAnchorBlockHash), nil
	}
	return bytes.Repeat([]byte{0x43}, 32), nil
}
func (h sessionAdmissionHub) GetBuilderSetForHeight(sdk.Context, uint64) (hubtypes.BuilderSetState, error) {
	id := "session-key-builders"
	if h.order != nil {
		id = h.order.BuilderSetId
	}
	return hubtypes.BuilderSetState{BuilderSetId: id, BuilderSetHash: bytes.Repeat([]byte{0x44}, 32), ActiveBuilders: h.builders, ActiveBuilderCount: uint32(len(h.builders)), BodyStatus: shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE}, nil
}
func (sessionAdmissionHub) GetModelStatus(sdk.Context, []byte) hubtypes.ModelStatus {
	return hubtypes.ModelStatusActive
}
func (sessionAdmissionHub) GetProfileState(sdk.Context, []byte, uint32) (hubtypes.ProfileStateSnapshot, bool) {
	return hubtypes.ProfileStateSnapshot{Status: hubtypes.ModelStatusActive, TaskTypes: []shared.TaskType{shared.TaskType_TASK_TYPE_TEXT_GENERATION, shared.TaskType_TASK_TYPE_CHAT}, MinStake: 1,
		PricingProfile: shared.PricingProfile{VerifyRatioBps: 1000, MinOrderValue: 1}, ExecutionSnapshotHash: bytes.Repeat([]byte{0x45}, 32),
		ExecutionSnapshot: shared.ProfileExecutionSnapshot{RuntimeClass: shared.RuntimeClassV1, VerificationProfile: shared.VerificationProfile{
			EvidenceSchemaHash: bytes.Repeat([]byte{0x46}, 32), JudgmentFunctionVersion: shared.JudgmentFunctionVersionV1,
			CanonicalEncodingVersion: shared.CanonicalEncodingVersionV1, MetricAggregateProofVersion: shared.MetricAggregateProofVersionV1,
			VerificationMode: shared.VerificationMode_VERIFICATION_MODE_SINGLE_SAMPLE, TokenScope: shared.TokenScope_TOKEN_SCOPE_ALL_GENERATED_OUTPUT_TOKENS,
		}}}, true
}
func (h sessionAdmissionHub) GetParameterBucketVersion(context.Context, shared.BucketKind, string, uint64) (hubtypes.ParameterBucketVersionState, error) {
	version := uint64(1)
	if h.order != nil {
		version = h.order.TimeoutBucketVersion
	}
	return hubtypes.ParameterBucketVersionState{Version: version, EffectiveHeight: 1, TimeoutEntries: hubtypes.TimeoutBucketEntriesV1{Entries: []hubtypes.TimeoutBucketEntryV1{{UpperWorkUnitsInclusive: 1000, TimeoutBlocks: 10}}}}, nil
}
func (sessionAdmissionHub) CurrentActiveCandidatePool(context.Context) (hubtypes.CandidatePoolSnapshotState, error) {
	return hubtypes.CandidatePoolSnapshotState{SnapshotId: bytes.Repeat([]byte{0x47}, 32), PoolHash: bytes.Repeat([]byte{0x48}, 32), Status: hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_ACTIVE}, nil
}
func (sessionAdmissionHub) AcquireCandidatePoolTaskRef(context.Context, []byte, []byte, uint64) (bool, error) {
	return true, nil
}
func (sessionAdmissionHub) AcquireBuilderSetTaskRef(context.Context, []byte, string, []byte, uint64) (bool, error) {
	return true, nil
}
func (sessionAdmissionHub) AcquireParameterBucketTaskRef(context.Context, shared.BucketKind, string, uint64, uint64) error {
	return nil
}

func sessionAdmissionFixture(t *testing.T) (*internalFixture, types.SignedOrderV2, sessionAdmissionBank) {
	t.Helper()
	f := initInternalFixture(t)
	f.ctx = sessionKeyContext(f, 100)
	ownerKey := dcrsecp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	ownerRaw := shared.EVMAddressFromSecp256k1PublicKey(ownerKey.PubKey())
	owner, err := f.keeper.addressCodec.BytesToString(ownerRaw[:])
	require.NoError(t, err)
	f.keeper.authKeeper = sessionAdmissionAuth{account: authtypes.NewBaseAccount(ownerRaw[:], &ethsecp256k1.PubKey{Key: ownerKey.PubKey().SerializeCompressed()}, 0, 0)}
	delegate := dcrsecp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{3}, 32))
	delegateRaw := shared.EVMAddressFromSecp256k1PublicKey(delegate.PubKey())
	created, err := NewMsgServerImpl(f.keeper).CreateSession(f.ctx, &types.MsgCreateSession{SignerAddress: owner, SessionKey: &types.SessionKeyGrantV1{
		SessionKey: delegateRaw[:], ExpiryHeight: 200, SpendCap: shared.NewAmount(4000)}})
	require.NoError(t, err)
	builders := make([]string, 3)
	for i := range builders {
		builders[i], err = f.keeper.addressCodec.BytesToString(bytes.Repeat([]byte{byte(0x50 + i)}, 20))
		require.NoError(t, err)
	}
	f.keeper.hubKeeper = sessionAdmissionHub{builders: builders}
	bank := sessionAdmissionBank{store: f.keeper.storeService}
	f.keeper.bankKeeper = bank
	order := types.TaskOrderV3{SchemaVersion: 3, ChainId: "session-key-test", UserAddress: owner, SessionId: created.SessionId, ModelId: bytes.Repeat([]byte{0x55}, 32), ProfileVersion: 1,
		TaskType: shared.TaskType_TASK_TYPE_TEXT_GENERATION, InputHash: bytes.Repeat([]byte{0x42}, 32), InputSizeBytes: 1, InputBucket: 1, OutputBudgetBucket: 1,
		GenerationParams: types.GenerationParamsV1{GenerationParamsSchemaVersion: types.GenerationParamsSchemaVersionV1, MaxOutputTokens: 1, MaxOutputDuration: 1,
			DecodingParams: types.DecodingParamsV1{TopPPpm: types.TopPPMMaxV1, RepetitionPenaltyPpm: 1000000}},
		PriceBid: shared.NewAmount(1000000000), MaxFee: shared.NewAmount(2000), AssignmentPriorityFee: shared.NewAmount(0), TxFeeReserve: shared.NewAmount(0),
		EarliestSubmitHeight: 100, OrderExpireHeight: 300, DeadlinePolicy: types.DeadlinePolicyV1{LatencyClass: types.DeadlineLatencyClass_DEADLINE_LATENCY_CLASS_STANDARD},
		TimeoutBucketVersion: 1, SessionAnchorHeight: 100, SessionAnchorBlockHash: bytes.Repeat([]byte{0x43}, 32), BuilderSetId: "session-key-builders", BuilderSetHash: bytes.Repeat([]byte{0x44}, 32),
		PayloadMode: types.PayloadModeV1_PAYLOAD_MODE_V1_PLAINTEXT, InputKeyCommitment: make([]byte, 32)}
	hash, err := types.TaskOrderHash(order)
	require.NoError(t, err)
	digest, err := types.BuildTaskOrderEIP712Digest(1, "uusdc", order, hash[:])
	require.NoError(t, err)
	compact := dcrecdsa.SignCompact(delegate, digest.SigningDigest[:], false)
	signed := types.SignedOrderV2{Order: order, SignatureScheme: types.SignatureSchemeEIP712, SignatureChainId: 1, UserSignature: append(append([]byte(nil), compact[1:]...), compact[0])}
	f.ctx = sessionKeyContext(f, 111)
	return f, signed, bank
}

func runSessionAdmission(t *testing.T, f *internalFixture, signed types.SignedOrderV2, commit bool) error {
	t.Helper()
	cacheCtx, write := sdk.UnwrapSDKContext(f.ctx).CacheContext()
	ctx := sdk.WrapSDKContext(cacheCtx)
	scope, err := resolveWorkerProposalScope(types.WorkerHandraiseScopeV1{Scope: &types.WorkerHandraiseScopeV1_SignedOrder{SignedOrder: &signed}})
	require.NoError(t, err)
	params, err := f.keeper.Params.Get(ctx)
	require.NoError(t, err)
	err = f.keeper.admitSignedWorkerOrder(ctx, &scope, types.WorkerHandraiseV1{Member: types.CandidateMemberRefV1{CandidatePoolSnapshotId: bytes.Repeat([]byte{0x47}, 32)}}, params, uint64(cacheCtx.BlockHeight()))
	if err == nil && commit {
		write()
	}
	return err
}

func TestSessionKeyAdmissionEscrowAndAllowanceAreAtomic(t *testing.T) {
	for _, scenario := range []string{"bank failure", "later proposal failure", "accepted"} {
		t.Run(scenario, func(t *testing.T) {
			f, signed, bank := sessionAdmissionFixture(t)
			if scenario == "bank failure" {
				failed := bank
				failed.fail = true
				f.keeper.bankKeeper = failed
			}
			err := runSessionAdmission(t, f, signed, scenario != "later proposal failure")
			if scenario == "bank failure" {
				require.ErrorContains(t, err, "injected bank failure")
			} else {
				require.NoError(t, err)
			}
			stream, readErr := f.keeper.ReadStream(f.ctx, signed.Order.SessionId)
			require.NoError(t, readErr)
			if scenario != "accepted" {
				require.Zero(t, stream.NextExpectedSequence)
				require.Zero(t, stream.OpenPendingCount)
				require.Equal(t, shared.NewAmount(0), stream.SessionKeySpent)
				require.Zero(t, bank.escrow(f.ctx))
				return
			}
			require.Equal(t, uint64(1), stream.NextExpectedSequence)
			require.Equal(t, uint32(1), stream.OpenPendingCount)
			require.Equal(t, shared.NewAmount(2000), stream.SessionKeySpent)

			require.Equal(t, uint64(2000), bank.escrow(f.ctx))
			// Activation must not make an accepted task reject its frozen legacy
			// parameter body merely because the new input limit was absent there.
			legacy, err := f.keeper.Params.Get(f.ctx)
			require.NoError(t, err)
			legacy.Session.MaxSessionKeyBlocks = 0
			require.NoError(t, f.keeper.Params.Set(f.ctx, legacy))
			require.NoError(t, f.keeper.ConfigureSessionKeyActivation(111))
			require.NoError(t, f.keeper.InitializeSessionKeyParams(f.ctx))
			// Accepted-time facts survive revocation, and replay never reserves twice.
			_, err = NewMsgServerImpl(f.keeper).UpdateSessionKey(f.ctx, &types.MsgUpdateSessionKey{SignerAddress: signed.Order.UserAddress, SessionId: signed.Order.SessionId, Action: &types.MsgUpdateSessionKey_Clear{Clear: &types.ClearSessionKeyV1{}}})
			require.NoError(t, err)
			require.NoError(t, runSessionAdmission(t, f, signed, true))
			require.Equal(t, uint64(2000), bank.escrow(f.ctx))
			stream, err = f.keeper.ReadStream(f.ctx, signed.Order.SessionId)
			require.NoError(t, err)
			require.Equal(t, shared.NewAmount(2000), stream.SessionKeySpent)
			taskID, err := types.DeriveTaskIDFromRawSession(signed.Order.SessionId, signed.Order.OrderSequence)
			require.NoError(t, err)
			core, err := f.keeper.TaskCore.Get(f.ctx, taskID[:])
			require.NoError(t, err)
			f.ctx = sessionKeyContext(f, 112)
			result, err := f.keeper.writePreVerificationFailure(f.ctx, core, 112,
				types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_ASSIGNMENT, types.TaskFailureClass_TASK_FAILURE_CLASS_NONE)
			require.NoError(t, err, "revocation must not block a funded task's terminal refund")
			require.True(t, result.Applied)
			require.Zero(t, bank.escrow(f.ctx))
			stream, err = f.keeper.ReadStream(f.ctx, signed.Order.SessionId)
			require.NoError(t, err)
			require.Equal(t, shared.NewAmount(2000), stream.SessionKeySpent, "refund never replenishes delegated allowance")
		})
	}
}

func TestSessionKeyRevocationOrderingUsesRealAdmission(t *testing.T) {
	for _, order := range []string{"clear then submit", "clear then same-key registration", "accept then clear"} {
		t.Run(order, func(t *testing.T) {
			f, signed, bank := sessionAdmissionFixture(t)
			server := NewMsgServerImpl(f.keeper)
			clear := &types.MsgUpdateSessionKey{SignerAddress: signed.Order.UserAddress, SessionId: signed.Order.SessionId, Action: &types.MsgUpdateSessionKey_Clear{Clear: &types.ClearSessionKeyV1{}}}
			if order == "accept then clear" {
				require.NoError(t, runSessionAdmission(t, f, signed, true))
			}
			_, err := server.UpdateSessionKey(f.ctx, clear)
			require.NoError(t, err)
			if order == "accept then clear" {
				require.NoError(t, runSessionAdmission(t, f, signed, true))
				require.Equal(t, uint64(2000), bank.escrow(f.ctx))
				return
			}
			if order == "clear then submit" {
				require.ErrorIs(t, runSessionAdmission(t, f, signed, true), types.ErrSessionKeyInvalid)
				require.Zero(t, bank.escrow(f.ctx))
				return
			}
			delegate := dcrsecp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{3}, 32))
			raw := shared.EVMAddressFromSecp256k1PublicKey(delegate.PubKey())
			_, err = server.UpdateSessionKey(f.ctx, sessionKeySetRequest(signed.Order.UserAddress, signed.Order.SessionId, raw[:], 220, 4000))
			require.NoError(t, err)
			require.ErrorIs(t, runSessionAdmission(t, f, signed, true), types.ErrSessionKeyInvalid, "old anchor cannot survive CLEAR/SET")
			stream, err := f.keeper.ReadStream(f.ctx, signed.Order.SessionId)
			require.NoError(t, err)
			require.Zero(t, stream.NextExpectedSequence)
			require.Equal(t, shared.NewAmount(0), stream.SessionKeySpent)
			require.Zero(t, bank.escrow(f.ctx))
			signed.Order.SessionAnchorHeight = 111
			hash, err := types.TaskOrderHash(signed.Order)
			require.NoError(t, err)
			digest, err := types.BuildTaskOrderEIP712Digest(1, "uusdc", signed.Order, hash[:])
			require.NoError(t, err)
			compact := dcrecdsa.SignCompact(delegate, digest.SigningDigest[:], false)
			signed.UserSignature = append(append([]byte(nil), compact[1:]...), compact[0])
			f.ctx = sessionKeyContext(f, 112)
			require.NoError(t, runSessionAdmission(t, f, signed, true))
			require.Equal(t, uint64(2000), bank.escrow(f.ctx))
		})
	}
}
