package types_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// settlementVectorFile is the Wire v0.3 `round_settlement_v1` fixture. Per
// DOC-001 the published registry projection of
// TRUEOPEN_SETTLEMENT_FACTS_V1 is stale (it still lists the deleted
// registered_full_result_refs_hash), so this vector — not registry/v1/domains.json
// — is the authority for the settlement preimages.
const settlementVectorFile = "testdata/round_settlement_v1.json"

type settlementVector struct {
	Name        string `json:"name"`
	Domain      string `json:"domain"`
	PreimageHex string `json:"preimage_hex"`
	DigestHex   string `json:"digest_hex"`
}

func loadSettlementVector(t *testing.T, name string) settlementVector {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(settlementVectorFile))
	require.NoError(t, err)
	var set struct {
		Vectors []settlementVector `json:"vectors"`
	}
	require.NoError(t, json.Unmarshal(raw, &set))
	for _, vector := range set.Vectors {
		if vector.Name == name {
			return vector
		}
	}
	t.Fatalf("wire vector %q is missing from %s", name, settlementVectorFile)
	return settlementVector{}
}

func settlementAddress(t *testing.T, hexBytes string) string {
	t.Helper()
	raw, err := hex.DecodeString(hexBytes)
	require.NoError(t, err)
	encoded, err := bech32.ConvertAndEncode("trueopen", raw)
	require.NoError(t, err)
	return encoded
}

func settlementHash(t *testing.T, hexBytes string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(hexBytes)
	require.NoError(t, err)
	return raw
}

// SettlementBillHash is the one settlement-group domain whose preimage does not
// open with chain_id; the bill is scoped by infer_receipt_ref instead.
func TestSettlementBillHashMatchesWireVector(t *testing.T) {
	vector := loadSettlementVector(t, "settlement_bill_v1")

	got, err := tasktypes.SettlementBillHash(tasktypes.TaskSettlementBillV1{
		WorkerOperatorAddress: settlementAddress(t, "1a642f0e3c3af545e7acbd38b07251b3990914f1"),
		InferReceiptRef:       settlementHash(t, "33"+repeatHex("33", 31)),
		FeeRuleVersion:        2,
		GeneratedTokenCount:   321,
		WorkUnit:              321,
	})
	require.NoError(t, err)
	require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
}

// The 13-field nested frame is the whole point of this vector: DOC-001 records
// that the registry still projects a 14th field that Wire v0.3 deleted, so a
// producer that re-adds it has to fail here.
func TestSettlementFactsHashMatchesWireVector(t *testing.T) {
	vector := loadSettlementVector(t, "settlement_facts_v1")

	facts := tasktypes.SettlementFactsV1{
		TaskId:                        settlementHash(t, repeatHex("11", 32)),
		VerifyRound:                   2,
		SettlementHeight:              1400,
		SettlementFactsCutoffHeight:   1300,
		SettlementDutyBuilderOperator: settlementAddress(t, "1a642f0e3c3af545e7acbd38b07251b3990914f1"),
		ConsensusClusterHash:          settlementHash(t, repeatHex("55", 32)),
		Verdict:                       tasktypes.TaskVerdict(1),
		FailureClass:                  tasktypes.TaskFailureClass(1),
		FaultSummaryHash:              settlementHash(t, repeatHex("88", 32)),
		PaidRoles: []tasktypes.PaidRoleV1{{
			Duty:            shared.Duty(2),
			OperatorAddress: settlementAddress(t, "c0c1c2c3c4c5c6c7c8c9cacbcccdcecfd0d1d2d3"),
		}},
		InferReceiptRef:       settlementHash(t, repeatHex("33", 32)),
		ResultReceiptRefsHash: settlementHash(t, repeatHex("44", 32)),
		ChallengeCloseHeight:  1200,
	}

	got, err := tasktypes.SettlementFactsHash("trueopen-golden-1", settlementHash(t, repeatHex("bb", 32)), facts)
	require.NoError(t, err)
	require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
}

// VerifyRoundFactsHash is what closeVerificationRound freezes into the round
// row and what the settlement path later re-derives to prove a closed round
// still reproduces its own commitments. The vector pins all 13 fields including
// the two cluster hashes, so a reordering there fails here first.
func TestVerifyRoundFactsHashMatchesWireVector(t *testing.T) {
	vector := loadSettlementVector(t, "verify_round_facts_v1")

	round := tasktypes.VerificationRoundState{
		TaskId:                   settlementHash(t, repeatHex("11", 32)),
		TaskHash:                 settlementHash(t, repeatHex("22", 32)),
		VerifyRound:              2,
		RoundOpenHeight:          1000,
		RoundCloseDeadlineHeight: 1100,
		// closed_height guards that only a closed round is committed; it is a
		// precondition, not one of the 13 digest fields.
		XClosedHeight:   &tasktypes.VerificationRoundState_ClosedHeight{ClosedHeight: 1100},
		InferReceiptRef: settlementHash(t, repeatHex("33", 32)),
		XResultReceiptRefsHash: &tasktypes.VerificationRoundState_ResultReceiptRefsHash{
			ResultReceiptRefsHash: settlementHash(t, repeatHex("44", 32)),
		},
		XConsensusClusterHash: &tasktypes.VerificationRoundState_ConsensusClusterHash{
			ConsensusClusterHash: settlementHash(t, repeatHex("55", 32)),
		},
		GeneratedTokenCount:          321,
		XVerdict:                     &tasktypes.VerificationRoundState_Verdict{Verdict: tasktypes.TaskVerdict(1)},
		XFailureClass:                &tasktypes.VerificationRoundState_FailureClass{FailureClass: tasktypes.TaskFailureClass(1)},
		ProfileExecutionSnapshotHash: settlementHash(t, repeatHex("66", 32)),
		GenerationParamsDigest:       settlementHash(t, repeatHex("77", 32)),
	}

	got, err := tasktypes.VerifyRoundFactsHash("trueopen-golden-1", round)
	require.NoError(t, err)
	require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
}

// settlementVectorGasItem is the single gas receipt both the gas-vector and the
// plan-vector embed, so the two tests below cannot drift from each other.
func settlementVectorGasItem(t *testing.T) tasktypes.TaskGasReimbursementV1 {
	t.Helper()
	return tasktypes.TaskGasReimbursementV1{
		TaskId:            settlementHash(t, repeatHex("11", 32)),
		TxHash:            settlementHash(t, repeatHex("cc", 32)),
		ItemIndex:         0,
		ReimbursementKind: tasktypes.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_RESULT,
		FeePayer:          settlementAddress(t, "c0c1c2c3c4c5c6c7c8c9cacbcccdcecfd0d1d2d3"),
		GasBasis:          21_000,
		ActualFeePaid:     shared.NewAmount(30),
		NecessaryFee:      shared.NewAmount(20),
		ReimbursedAmount:  shared.NewAmount(20),
		FeePolicyVersion:  1,
		AcceptedHeight:    1200,
	}
}

// The gas vector is the array digest the plan embeds by value. Binding it
// separately means a framing change in the 11-field receipt frame fails here
// instead of only showing up as a different plan hash.
func TestGasReimbursementsHashMatchesWireVector(t *testing.T) {
	vector := loadSettlementVector(t, "gas_reimbursements_v1")

	got, err := tasktypes.GasReimbursementsHash(
		"trueopen-golden-1", settlementHash(t, repeatHex("11", 32)),
		[]tasktypes.TaskGasReimbursementV1{settlementVectorGasItem(t)},
	)
	require.NoError(t, err)
	require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
}

func TestTaskSettlementIDAcceptsOnlyRegisteredSettlementRounds(t *testing.T) {
	taskID := bytes.Repeat([]byte{0x21}, tasktypes.Hash32Len)
	taskHash := bytes.Repeat([]byte{0x22}, tasktypes.Hash32Len)
	preVerification, err := tasktypes.TaskSettlementID("trueopen-test", taskID, taskHash, 0, 10, 10)
	require.NoError(t, err)
	roundOne, err := tasktypes.TaskSettlementID("trueopen-test", taskID, taskHash, tasktypes.VerifyRoundV1, 10, 10)
	require.NoError(t, err)
	require.NotEqual(t, preVerification, roundOne)
	_, err = tasktypes.TaskSettlementID("trueopen-test", taskID, taskHash, tasktypes.ChallengeVerifyRoundV1+1, 10, 10)
	require.ErrorContains(t, err, "0, 1, or 2")
}

// SettlementPlanHash is the terminal commitment of BuildSettlementPlan: it
// carries the payout vector, the embedded gas array and the three balances. The
// vector's own amounts also close the §10.10a conservation identity
// (90 + 10 + 12 + 20 + 50 == 182), so this pins the shape and the arithmetic the
// plan builder has to reproduce.
func TestSettlementPlanHashMatchesWireVector(t *testing.T) {
	vector := loadSettlementVector(t, "settlement_plan_v1")
	gas := settlementVectorGasItem(t)

	plan := tasktypes.SettlementPlanV1{
		TaskId:               settlementHash(t, repeatHex("11", 32)),
		SettlementId:         settlementHash(t, repeatHex("bb", 32)),
		FeeRuleVersion:       2,
		EffectiveVerifyRound: 2,
		GeneratedTokenCount:  321,
		WorkUnit:             321,
		WorkerGross:          shared.NewAmount(100),
		WorkerMaintenance:    shared.NewAmount(10),
		WorkerNet:            shared.NewAmount(90),
		VerifierSlotGross:    shared.NewAmount(12),
		VerifierPayouts: []tasktypes.VerifierPayoutV1{{
			OperatorAddress:       settlementAddress(t, "1a642f0e3c3af545e7acbd38b07251b3990914f1"),
			SelectedVerifierIndex: 0,
			Gross:                 shared.NewAmount(12),
			Maintenance:           shared.NewAmount(2),
			Net:                   shared.NewAmount(10),
		}},
		GasReimbursements:      []tasktypes.TaskGasReimbursementV1{gas},
		GasReimbursementsHash:  settlementHash(t, "2b0307c665550120236cb703ef2acc083eb30573d886fed71eba267e00f5b1e3"),
		MaintenanceFee:         shared.NewAmount(12),
		RefundAmount:           shared.NewAmount(50),
		OriginalReservedAmount: shared.NewAmount(182),
		SettlementFactsHash:    settlementHash(t, repeatHex("dd", 32)),
		TaskRoundSummaryHash:   settlementHash(t, "dce75d04ddc83f79c001448876d9588dd77dd3c922d1c53b3df4af5ecaa32004"),
		SettlementBillHash:     settlementHash(t, "dd272ba6d02cdbcabf4e1655b8c9af03396d84603370b1d405e8f76ac5a04ce0"),
	}

	got, err := tasktypes.SettlementPlanHash("trueopen-golden-1", plan)
	require.NoError(t, err)
	require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
}

func repeatHex(pair string, count int) string {
	out := make([]byte, 0, len(pair)*count)
	for i := 0; i < count; i++ {
		out = append(out, pair...)
	}
	return string(out)
}
