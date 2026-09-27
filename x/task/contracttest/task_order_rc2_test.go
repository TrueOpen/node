package contracttest

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestTaskOrderHashMatchesWireRC2(t *testing.T) {
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
	digest, err := tasktypes.TaskOrderHash(order)
	require.NoError(t, err)
	require.Equal(t, "d48c7062a64fcf6932aee1a935d9b0f30a04d954f40caa87f5ccf32a3696d59c", hex.EncodeToString(digest[:]))
}
