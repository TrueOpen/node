package keeper

import (
	"fmt"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/internal/testutil/domainfixture"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestHubKeeperDomainFixtureBindsProductionProducers(t *testing.T) {
	const path = "testdata/hub_keeper_domains_v1.json"
	vectors := domainfixture.ByName(t, path)
	bound := make(map[string]struct{}, len(vectors))
	bind := func(name, producer string, call func(domainfixture.Vector) ([]byte, error)) {
		t.Helper()
		vector, ok := vectors[name]
		require.True(t, ok, "producer binding %s has no vector", name)
		domainfixture.RequireProducer(t, vector, producer)
		got, err := call(vector)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, vector, got)
		bound[name] = struct{}{}
	}

	bind("builder_set_members_v1", "BuilderSetMembersHash", func(vector domainfixture.Vector) ([]byte, error) {
		elements := vector.Repeated(t, 1, 0, "members", "active_builder_count")
		addresses := make([][]byte, len(elements))
		for index, element := range elements {
			fields := element.Frame(t, fmt.Sprintf("%s member %d", vector.Name, index))
			require.Equal(t, uint64(index), fields[0].Uint(t, "rank_index"))
			addresses[index] = fields[1].Bytes(t, "builder_operator_address")
		}
		return BuilderSetMembersHash(addresses)
	})

	bind("beacon_aggregate_v1", "beaconAggregateDigest", func(vector domainfixture.Vector) ([]byte, error) {
		elements := vector.Repeated(t, 2, 1, "randomness_values", "beacon_count")
		randomness := make([][]byte, len(elements))
		for index, element := range elements {
			randomness[index] = element.Bytes(t, fmt.Sprintf("randomness_%d", index))
		}
		return beaconAggregateDigest(vector.Uint(t, 0, "start_height"), uint32(len(randomness)), randomness)
	})

	bind("daily_support_confirmation_id_v1", "dailySupportConfirmationID", func(vector domainfixture.Vector) ([]byte, error) {
		return dailySupportConfirmationID(
			vector.String(t, 0, "chain_id"), vector.Uint(t, 1, "epoch_index"),
			vector.Bytes(t, 2, "operator_address"),
		)
	})

	bind("freeze_failure_refs_v1", "freezeFailureRefsRoot", func(vector domainfixture.Vector) ([]byte, error) {
		return freezeFailureRefsRoot(
			vector.Bytes(t, 0, "previous_root"), vector.Bytes(t, 1, "task_id"),
			vector.Bytes(t, 2, "settlement_id_or_empty"), vector.Uint(t, 3, "task_finality_height"),
			types.FreezeFailureClass(vector.Uint(t, 4, "failure_class")),
			vector.Bytes(t, 5, "evidence_digest"),
		)
	})

	bind("freeze_signal_v1", "freezeSignalID", func(vector domainfixture.Vector) ([]byte, error) {
		return freezeSignalID(
			vector.String(t, 0, "chain_id"), vector.String(t, 1, "model_id"),
			uint32(vector.Uint(t, 2, "profile_version")), vector.Uint(t, 3, "risk_window_id"),
			vector.Uint(t, 4, "risk_window_start_height"), vector.Uint(t, 5, "risk_window_end_height"),
			uint32(vector.Uint(t, 6, "included_failure_task_ref_count")),
			vector.Bytes(t, 7, "included_failure_task_refs_hash"),
		)
	})

	bind("role_fault_v1", "roleFaultID", func(vector domainfixture.Vector) ([]byte, error) {
		return roleFaultID(
			vector.String(t, 0, "chain_id"), vector.Bytes(t, 1, "task_id"),
			vector.Bytes(t, 2, "operator_address"), shared.Duty(vector.Uint(t, 3, "duty")),
			types.FaultKind(vector.Uint(t, 4, "fault_class")), vector.Bytes(t, 5, "evidence_digest"),
		)
	})

	bind("treasury_spend_v1", "treasurySpendActionDigest", func(vector domainfixture.Vector) ([]byte, error) {
		amount := vector.Frame(t, 4, "amount").Field(t, 0, "atomic_units").String(t, "amount.atomic_units")
		return treasurySpendActionDigest(
			vector.String(t, 0, "chain_id"), vector.Uint(t, 1, "proposal_id"),
			uint32(vector.Uint(t, 2, "item_index")), vector.Bytes(t, 3, "recipient"),
			shared.Amount{AtomicUnits: amount}, types.TreasuryPurposeCode(vector.Uint(t, 5, "purpose_code")),
			vector.Uint(t, 6, "not_before_height"), vector.Uint(t, 7, "expiry_height"),
		)
	})

	domainfixture.RequireBoundSet(t, vectors, bound, nil)
}

func TestHubUnbondingFixtureBindsProductionProducers(t *testing.T) {
	const path = "../types/testdata/hub_domains_v1.json"
	vectors := domainfixture.ByName(t, path)
	id := vectors["unbonding_id_v1"]
	gotID, err := serviceUnbondingID(
		id.String(t, 0, "chain_id"), shared.ParticipantType(id.Uint(t, 1, "participant_type")),
		id.AddressBytes(t, 2, "operator_address"), id.Uint(t, 3, "new_bond_version"),
		id.Uint(t, 4, "amount"), id.Uint(t, 5, "request_height"), id.Uint(t, 6, "mature_height"),
	)
	require.NoError(t, err)
	domainfixture.RequireDigest(t, id, gotID)

	receiptVector := vectors["unbonding_receipt_v1"]
	receipt := types.UnbondingReceiptState{
		UnbondingId:     domainfixture.DecodeHex(t, "unbonding_id", receiptVector.Field(t, 1, "unbonding_id").Hex),
		OperatorAddress: receiptVector.Address(t, 2, "operator_address"),
		OriginalAmount:  receiptVector.Uint(t, 3, "original_amount"),
		WithdrawnAmount: receiptVector.Uint(t, 4, "withdrawn_amount"),
		SlashedAmount:   receiptVector.Uint(t, 5, "slashed_amount"),
		TerminalStatus:  types.UnbondingReceiptTerminalStatus(receiptVector.Uint(t, 6, "terminal_status")),
		TerminalHeight:  receiptVector.Uint(t, 7, "terminal_height"),
	}
	keeper := Keeper{addressCodec: address.NewBech32Codec("trueopen")}
	gotReceipt, err := keeper.unbondingReceiptHash(receiptVector.String(t, 0, "chain_id"), receipt)
	require.NoError(t, err)
	domainfixture.RequireDigest(t, receiptVector, gotReceipt)
}

func TestHubIdentityFixtureBindsProductionProducers(t *testing.T) {
	const path = "../types/testdata/hub_domains_v1.json"
	vectors := domainfixture.ByName(t, path)
	registration := vectors["service_registration_v1"]
	registrationDigest, err := serviceRegistrationDigest(
		registration.String(t, 0, "chain_id"),
		shared.ParticipantType(registration.Uint(t, 1, "participant_type")),
		registration.AddressBytes(t, 2, "operator_address"),
		registration.Bytes(t, 3, "service_pubkey"),
		registration.Uint(t, 4, "initial_service_authorization_nonce"),
	)
	require.NoError(t, err)
	domainfixture.RequireDigest(t, registration, registrationDigest)

	rotation := vectors["service_key_rotation_v1"]
	rotationDigest, err := serviceKeyRotationDigest(
		rotation.String(t, 0, "chain_id"),
		shared.ParticipantType(rotation.Uint(t, 1, "participant_type")),
		rotation.AddressBytes(t, 2, "operator_address"),
		rotation.Bytes(t, 3, "new_service_pubkey"),
		rotation.Uint(t, 4, "expected_current_service_authorization_nonce"),
		rotation.Uint(t, 5, "next_service_authorization_nonce"),
	)
	require.NoError(t, err)
	domainfixture.RequireDigest(t, rotation, rotationDigest)
}
