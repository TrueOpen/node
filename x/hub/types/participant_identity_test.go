package types_test

import (
	"bytes"
	"crypto/sha256"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestServiceKeyResponsibilityAcceptsBusObjectiveEvidenceWithNonceState(t *testing.T) {
	require.True(t, types.IsValidServiceKeyResponsibilityKind(
		types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE,
	))
}

func testIdentity(t *testing.T, fill byte) (string, []byte) {
	t.Helper()
	secret := sha256.Sum256([]byte{fill, 0xa5, 0x5a})
	privateKey := secp256k1.GenPrivKeyFromSecret(secret[:])
	pubkey := privateKey.PubKey().(*secp256k1.PubKey)
	return sdk.AccAddress(types.ServicePubKeyAddress(pubkey)).String(), pubkey.Bytes()
}

func TestCortexNodeCurrentServiceKeyInvariants(t *testing.T) {
	address, pubkey := testIdentity(t, 0x41)
	state := types.CortexNodeState{
		SchemaVersion: 1, OperatorAddress: address, CurrentServiceAddress: address,
		CurrentServicePubkey: pubkey, ServiceAuthorizationNonce: 1,
		ServiceKeyStatus: types.ServiceKeyStatusActive, RegisteredHeight: 1, UpdatedHeight: 1,
	}
	require.NoError(t, state.Validate())

	wrongAddress, _ := testIdentity(t, 0x42)
	state.CurrentServiceAddress = wrongAddress
	require.ErrorContains(t, state.Validate(), "does not derive")
}

func TestTaskLiabilityLifecycleInvariants(t *testing.T) {
	address, _ := testIdentity(t, 0x43)
	base := types.TaskLiabilityReservationState{
		SchemaVersion: 1, TaskId: bytes.Repeat([]byte{1}, 32), OperatorAddress: address,
		Duty: shared.DutyWorker, BondVersion: 1, CapabilityVersion: 1,
		ReservedAmount: 10, Status: types.TaskLiabilityStatusReserved,
		CandidatePoolSnapshotId: bytes.Repeat([]byte{2}, 32), Slot: 0, SlotVersion: 1,
	}
	require.NoError(t, base.Validate())

	invalidDuty := base
	invalidDuty.Duty = shared.Duty_DUTY_UNSPECIFIED
	require.ErrorContains(t, invalidDuty.Validate(), "WORKER or VERIFIER")

	invalidTask := base
	invalidTask.TaskId = invalidTask.TaskId[:31]
	require.ErrorContains(t, invalidTask.Validate(), "32-byte")

	terminal := base
	terminal.Status = types.TaskLiabilityStatusReleased
	require.NoError(t, terminal.Validate())
}
