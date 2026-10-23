package sessionfixture

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// CoreOrder reconstructs the complete public TaskOrder fixture.
func CoreOrder(t *testing.T) tasktypes.TaskOrderV3 {
	t.Helper()
	userBytes, err := hex.DecodeString("a0a1a2a3a4a5a6a7a8a9aaabacadaeafb0b1b2b3")
	require.NoError(t, err)
	user, err := bech32.ConvertAndEncode("trueopen", userBytes)
	require.NoError(t, err)
	order := tasktypes.TaskOrderV3{
		SchemaVersion: 3, ChainId: "trueopen-golden-1", UserAddress: user,
		SessionId: bytes.Repeat([]byte{0x11}, 32), OrderSequence: 42,
		ModelId: bytes.Repeat([]byte{0x55}, 32), ProfileVersion: 1,
		TaskType:  shared.TaskType_TASK_TYPE_CHAT,
		InputHash: bytes.Repeat([]byte{0x22}, 32), InputSizeBytes: 4096,
		InputBucket: 3, OutputBudgetBucket: 4,
		GenerationParams: tasktypes.GenerationParamsV1{
			GenerationParamsSchemaVersion: 1, MaxOutputTokens: 256, MaxOutputDuration: 30_000,
			DecodingParams: tasktypes.DecodingParamsV1{
				SamplingEnabled: true, TemperatureMilli: 700, TopPPpm: 950_000,
				TopK: 40, Seed: 8_675_309, PresencePenaltyMilli: -250,
				FrequencyPenaltyMilli: 125, RepetitionPenaltyPpm: 1_050_000,
				StopSequences: []string{"</s>", "STOP"}, StopTokenIds: []uint32{11, 220},
			},
		},
		PriceBid: shared.NewAmount(4_000_000), MaxFee: shared.NewAmount(150_000),
		AssignmentPriorityFee: shared.NewAmount(0), TxFeeReserve: shared.NewAmount(250),
		EarliestSubmitHeight: 1_000, OrderExpireHeight: 2_000,
		DeadlinePolicy:       tasktypes.DeadlinePolicyV1{LatencyClass: tasktypes.DeadlineLatencyClass(2)},
		TimeoutBucketVersion: 9, SessionAnchorHeight: 990,
		SessionAnchorBlockHash: bytes.Repeat([]byte{0x33}, 32),
		BuilderSetId:           "12", BuilderSetHash: bytes.Repeat([]byte{0x44}, 32),
		PayloadMode:        tasktypes.PayloadModeV1_PAYLOAD_MODE_V1_PLAINTEXT,
		InputKeyCommitment: make([]byte, 32),
	}
	return order
}

// State is the authorization view published by the pinned wire fixture.
type State struct {
	Status       string `json:"status"`
	Owner        string `json:"owner"`
	Key          string `json:"key"`
	Expiry       uint64 `json:"expiry"`
	SetHeight    uint64 `json:"set_height"`
	Cap          string `json:"cap"`
	Spent        string `json:"spent"`
	NextSequence uint64 `json:"next_sequence"`
}

type Admission struct {
	Name            string `json:"name"`
	Order           string `json:"order"`
	Height          uint64 `json:"current_height"`
	Anchor          uint64 `json:"anchor_height"`
	State           State  `json:"state"`
	Expected        string `json:"expected"`
	ExpectedSpent   string `json:"expected_spent"`
	AlreadyAdmitted bool   `json:"already_admitted"`
	LaterValid      *bool  `json:"later_admission_valid"`
}

type Grant struct {
	Key    string `json:"key"`
	Expiry uint64 `json:"expiry"`
	Cap    string `json:"cap"`
}
type Update struct {
	Name          string `json:"name"`
	Height        uint64 `json:"current_height"`
	MaxBlocks     uint64 `json:"current_max_blocks"`
	Signer        string `json:"signer"`
	Action        string `json:"action"`
	State         State  `json:"state"`
	Grant         Grant  `json:"grant"`
	Expected      string `json:"expected"`
	ExpectedState State  `json:"expected_state"`
}

type Section struct {
	Message   map[string]string `json:"message"`
	Signature string            `json:"signature_65"`
	ChainID   string            `json:"signature_chain_id"`
}

type Document struct {
	Admission []Admission `json:"admission_cases"`
	Updates   []Update    `json:"update_cases"`
	Wallet    Section     `json:"wallet_order"`
	Delegated Section     `json:"delegated_order"`
	Account   struct {
		PublicKey string `json:"pub_compressed"`
	} `json:"account"`
}

func Hex(t *testing.T, text string) []byte {
	t.Helper()
	b, err := hex.DecodeString(text)
	require.NoError(t, err)
	return b
}

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	moduleDir, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/TrueOpen/wire").Output()
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(moduleDir)), "testdata", "v1", "task", name))
	require.NoError(t, err)
	return raw
}

// Load reads authorization cases from the exact wire module pin.
func Load(t *testing.T) Document {
	t.Helper()
	var doc Document
	require.NoError(t, json.Unmarshal(fixtureBytes(t, "session_key_authorization_v1.json"), &doc))
	return doc
}

type AminoCase struct {
	Name      string `json:"name"`
	Canonical string `json:"canonical_amino_json"`
	Digest    string `json:"signing_digest"`
	Signature string `json:"signature_65"`
	Address   string `json:"recovered_address"`
}

// AminoCases reads canonical signing bytes and independent signature expectations
// from wire; Node must not maintain another set of expected signing bytes.
func AminoCases(t *testing.T) []AminoCase {
	t.Helper()
	var doc struct {
		Cases []AminoCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(fixtureBytes(t, "session_key_amino_v1.json"), &doc))
	require.Len(t, doc.Cases, 3)
	return doc.Cases
}

func (d Document) SignedOrder(t *testing.T, name string) tasktypes.SignedOrderV2 {
	t.Helper()
	section := d.Delegated
	if name == "wallet_order" {
		section = d.Wallet
	}
	order := CoreOrder(t)
	order.UserAddress = section.Message["user"]
	order.SessionId = Hex(t, section.Message["sessionId"])
	var err error
	order.OrderSequence, err = strconv.ParseUint(section.Message["orderSequence"], 10, 64)
	require.NoError(t, err)
	chain, err := strconv.ParseUint(section.ChainID, 10, 64)
	require.NoError(t, err)
	return tasktypes.SignedOrderV2{Order: order, SignatureScheme: tasktypes.SignatureSchemeEIP712, SignatureChainId: chain, UserSignature: Hex(t, section.Signature)}
}
