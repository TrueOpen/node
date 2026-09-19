package keeper_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// Every fault_id, unbonding_id, responsibility_id, slash source_id and
// registration digest is a raw Hash32; operator_address and builder_address stay
// Bech32 text. encodeCustodyKey and signCompare are shared with
// custody_key_codec_test.go.
func TestServiceFaultCollectionsUseRawHash32Keys(t *testing.T) {
	f := initFixture(t)
	id := bytes.Repeat([]byte{0x71}, shared.Hash32KeySize)
	other := bytes.Repeat([]byte{0x72}, shared.Hash32KeySize)
	const operator = "trueopen1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"

	for _, tc := range []struct {
		name  string
		codec interface {
			Size(shared.Hash32Key) int
			Encode([]byte, shared.Hash32Key) (int, error)
		}
	}{
		{"role fault", f.keeper.RoleFault.KeyCodec()},
		{"unbonding receipt", f.keeper.UnbondingReceipt.KeyCodec()},
		{"registration receipt", f.keeper.RegistrationReceipt.KeyCodec()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded := make([]byte, tc.codec.Size(id))
			written, err := tc.codec.Encode(encoded, id)
			require.NoError(t, err)
			require.Equal(t, shared.Hash32KeySize, written)
			require.Equal(t, id, encoded)
			_, err = tc.codec.Encode(make([]byte, 64), []byte(hex.EncodeToString(id)))
			require.Error(t, err, "old lowercase-hex key must not remain writable")
		})
	}

	// Composite keys: assert the Hash32 lands at the expected offset and that a
	// non-terminal Hash32 costs exactly 32 bytes with no length prefix.
	byTask := encodeCustodyKey(t, f.keeper.RoleFaultByTaskIndex.KeyCodec(), types.NewRoleFaultByTaskKey(id, other))
	require.Len(t, byTask, 2*shared.Hash32KeySize)
	require.Equal(t, id, byTask[:shared.Hash32KeySize])
	require.Equal(t, other, byTask[shared.Hash32KeySize:])

	prune := encodeCustodyKey(t, f.keeper.RoleFaultPruneIndex.KeyCodec(), types.NewRoleFaultPruneKey(9, id))
	require.Len(t, prune, 8+shared.Hash32KeySize)

	summary := encodeCustodyKey(t, f.keeper.SlashSummary.KeyCodec(),
		types.NewSlashSummaryKey(types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT, id, 3))
	require.Len(t, summary, 4+shared.Hash32KeySize+8)
	require.Equal(t, id, summary[4:4+shared.Hash32KeySize])

	builderFault := encodeCustodyKey(t, f.keeper.BuilderFault.KeyCodec(), types.NewBuilderFaultKey(operator, id))
	require.Equal(t, id, builderFault[len(builderFault)-shared.Hash32KeySize:])

	unbonding := encodeCustodyKey(t, f.keeper.Unbonding.KeyCodec(), types.NewUnbondingKey(operator, id))
	require.Equal(t, id, unbonding[len(unbonding)-shared.Hash32KeySize:])

	quad := encodeCustodyKey(t, f.keeper.UnbondingByOperatorStatusIndex.KeyCodec(),
		types.NewUnbondingByOperatorStatusKey(operator, types.UnbondingStatusOpen, 5, id))
	require.Equal(t, id, quad[len(quad)-shared.Hash32KeySize:])

	responsibility := encodeCustodyKey(t, f.keeper.ServiceKeyResponsibility.KeyCodec(),
		types.NewServiceKeyResponsibilityKey(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operator, id))
	require.Equal(t, id, responsibility[len(responsibility)-shared.Hash32KeySize:])
}

// RoleFaultsForTask returns one task's fault vector in key order, and §6.6 frames
// that vector into fault_summary_hash -- so a reordering here is a consensus
// change, not a cosmetic one. Lower hex and raw memcmp agree; this pins it on the
// real composed codec rather than on the component.
func TestRoleFaultByTaskOrderSurvivesTheRawRetype(t *testing.T) {
	f := initFixture(t)
	taskID := bytes.Repeat([]byte{0x73}, shared.Hash32KeySize)
	left := append(bytes.Repeat([]byte{0x00}, 31), 0xff)
	right := append(bytes.Repeat([]byte{0x00}, 30), 0x01, 0x00)

	oldCodec := collections.PairKeyCodec(collections.StringKey, collections.StringKey)
	newCodec := f.keeper.RoleFaultByTaskIndex.KeyCodec()
	require.Equal(t,
		signCompare(bytes.Compare(
			encodeCustodyKey(t, oldCodec, collections.Join(hex.EncodeToString(taskID), hex.EncodeToString(left))),
			encodeCustodyKey(t, oldCodec, collections.Join(hex.EncodeToString(taskID), hex.EncodeToString(right))),
		)),
		signCompare(bytes.Compare(
			encodeCustodyKey(t, newCodec, types.NewRoleFaultByTaskKey(taskID, left)),
			encodeCustodyKey(t, newCodec, types.NewRoleFaultByTaskKey(taskID, right)),
		)),
	)

	// UnbondingMaturityIndex drives the EndBlocker sweep, whose per-block bound
	// makes "which row matures first" observable across blocks.
	oldMaturity := collections.TripleKeyCodec(collections.Uint64Key, collections.StringKey, collections.StringKey)
	newMaturity := f.keeper.UnbondingMaturityIndex.KeyCodec()
	const operator = "trueopen1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
	require.Equal(t,
		signCompare(bytes.Compare(
			encodeCustodyKey(t, oldMaturity, collections.Join3(uint64(7), operator, hex.EncodeToString(left))),
			encodeCustodyKey(t, oldMaturity, collections.Join3(uint64(7), operator, hex.EncodeToString(right))),
		)),
		signCompare(bytes.Compare(
			encodeCustodyKey(t, newMaturity, types.NewUnbondingMaturityIndexKey(7, operator, left)),
			encodeCustodyKey(t, newMaturity, types.NewUnbondingMaturityIndexKey(7, operator, right)),
		)),
	)
}

// The five key/value identity checks these keyspaces rely on had no coverage
// before this batch: the key and the row's own id were spelled differently
// (hex text vs raw bytes), so a disagreement was only ever checkable through a
// projection. Each subtest seeds a row whose store key names a different id than
// the row does.
func TestServiceFaultInvariantsRejectKeyValueMismatch(t *testing.T) {
	operator := hubAddress(t, 241)
	taskID := hubHashBytes("service-fault-task")
	evidence := hubHashBytes("service-fault-evidence")
	roleFault := func(faultID []byte) types.RoleFaultState {
		return types.RoleFaultState{
			FaultId: faultID, TaskId: taskID, OperatorAddress: operator,
			Duty: shared.DutyVerifier, FaultClass: types.FaultKind_FAULT_KIND_TIMEOUT,
			ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE,
			EvidenceDigest:       evidence, RecordedHeight: 10,
			Status: types.RoleFaultStatus_ROLE_FAULT_STATUS_CONFIRMED,
		}
	}
	key := hubHashBytes("service-fault-key")
	other := hubHashBytes("service-fault-other")

	t.Run("role fault primary vs slash summary sweep", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		require.NoError(t, f.keeper.RoleFault.Set(f.ctx, key, roleFault(other)))
		require.ErrorContains(t, f.keeper.EnsureSlashSummaryInvariant(f.ctx), "role fault does not match its store key")
	})

	t.Run("slash summary primary", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		// Internally valid: a role-fault summary requires slash_summary_id == source_id.
		// Only the store key disagrees, which is exactly the case the identity check
		// exists for -- and it has to stay Validate()-clean, because that call sits on
		// the same OR as the identity clause.
		summary := types.SlashSummaryState{
			SlashSummaryId: other, SourceKind: types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT,
			SourceId: other, EffectIndex: 0, OperatorAddress: operator, Duty: shared.DutyVerifier,
			XTaskId:                  &types.SlashSummaryState_TaskId{TaskId: taskID},
			RequestedAmount:          shared.NewAmount(1),
			PendingTaskEarningsDebit: shared.NewAmount(0),
			ActiveBondDebit:          shared.NewAmount(1),
			UnbondingDebit:           shared.NewAmount(0),
			ClaimableEarningsDebit:   shared.NewAmount(0),
			AppliedAmount:            shared.NewAmount(1),
			UnfilledAmount:           shared.NewAmount(0),
			Destination:              types.SlashDestination_SLASH_DESTINATION_TREASURY,
			AppliedHeight:            10,
			BondVersion:              1,
		}
		require.NoError(t, summary.Validate())
		require.NoError(t, f.keeper.SlashSummary.Set(f.ctx,
			types.NewSlashSummaryKey(types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT, key, 0), summary))
		require.ErrorContains(t, f.keeper.EnsureSlashSummaryInvariant(f.ctx), "slash summary does not match its store key")
	})

	t.Run("role fault by-task index", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		require.NoError(t, f.keeper.RoleFault.Set(f.ctx, key, roleFault(key)))
		require.NoError(t, f.keeper.RoleFaultByTaskIndex.Set(f.ctx, types.NewRoleFaultByTaskKey(other, key)))
		require.ErrorContains(t, f.keeper.EnsureRoleFaultByTaskIndexInvariant(f.ctx), "disagrees with primary")
	})

	t.Run("builder fault primary", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		// Same shape: the row is Validate()-clean and only the key names a different
		// fault_id, so a mutation of the identity clause is what turns this red.
		fault := types.BuilderFaultState{
			BuilderAddress: operator, FaultId: other,
			FaultKind:   types.BuilderFaultKind_BUILDER_FAULT_KIND_OBJECTIVE_DATA_UNAVAILABLE,
			FaultStatus: types.BuilderFaultStatus_BUILDER_FAULT_STATUS_RECORDED, FaultHeight: 4,
			EvidenceId: evidence, CanonicalEvidenceDigest: hubHashBytes("service-fault-canonical"),
			ScopeId: hubHashBytes("service-fault-scope"), PruneHeight: 5,
		}
		require.NoError(t, fault.Validate())
		require.NoError(t, f.keeper.BuilderFault.Set(f.ctx, types.NewBuilderFaultKey(operator, key), fault))
		require.ErrorContains(t, f.keeper.EnsureBuilderFaultPruneInvariant(f.ctx), "builder fault primary is invalid")
	})
}
