package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/TrueOpen/wire/dispatch"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (s *verificationHubStub) GetDispatchServiceProofKey(ctx context.Context, role shared.ParticipantType, operator string) (hubtypes.CurrentServiceKeySnapshot, error) {
	if role == shared.ParticipantType_PARTICIPANT_TYPE_BUILDER {
		return s.GetBuilderEvidenceProofKey(ctx, operator, 1, nil, nil)
	}
	return s.GetCurrentServiceKey(ctx, shared.ParticipantTypeCortexNode, operator)
}

func TestBuilderEvidenceV2LinkedWireVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/dispatch_v1.json")
	require.NoError(t, err)
	var fixture struct {
		Cases []struct {
			Name     string `json:"name"`
			Bytes    string `json:"evidence_bytes_hex"`
			Chain    string `json:"chain_id"`
			Operator string `json:"builder_operator"`
			Key      string `json:"public_key_hex"`
			Content  string `json:"content_digest_hex"`
			Evidence string `json:"evidence_id_hex"`
			Fault    string `json:"fault_id_hex"`
		} `json:"builder_evidence_cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Len(t, fixture.Cases, 2)
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			f := newVerificationFixture(t)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(row.Chain))
			f.hub.builderProofOperator = row.Operator
			f.hub.builderProofKey = mustDecodeBuilderEvidenceHex(t, row.Key)
			evidence := mustDecodeBuilderEvidenceHex(t, row.Bytes)
			verified, err := f.keeper.verifyBuilderEvidence(f.ctx, row.Chain, evidence)
			require.NoError(t, err)
			require.Equal(t, row.Content, hex.EncodeToString(verified.ContentDigest[:]))
			require.NotNil(t, verified.MessageA)
			message := verified.MessageA
			f.taskID = bytes.Clone(message.Scope.TaskID[:])
			f.taskKey = types.NewTaskKey(f.taskID)
			f.setTaskBuilders(t, row.Operator)
			model := bytes.Repeat([]byte{7}, 32)
			if message.Scope.ModelID != nil {
				model = bytes.Clone(message.Scope.ModelID[:])
			}
			core := types.TaskCoreState{TaskId: f.taskID, SessionId: bytes.Repeat([]byte{8}, 32), AcceptedTaskHash: message.Scope.TaskHash[:], ModelId: model, TaskPhase: types.TaskPhase_TASK_PHASE_INFER_PENDING}
			require.NoError(t, f.keeper.TaskCore.Set(f.ctx, f.taskKey, core))
			require.NoError(t, f.keeper.WriteTaskAssignment(f.ctx, f.taskKey, types.TaskAssignmentState{TaskId: f.taskID, WinnerWorker: message.Scope.PayloadActor}))
			if verified.Tag == dispatch.EvidenceTagEquivocation {
				require.NoError(t, f.keeper.WriteInferReceipt(f.ctx, f.taskKey, types.InferReceiptState{TaskId: f.taskID, WinnerWorker: message.Scope.PayloadActor}))
				_, err = f.keeper.publicBuilderObjectiveEvidence(f.ctx, dispatch.VerifiedBuilderEvidenceV2{Tag: dispatch.EvidenceTagInvalidStageSubmission, Kind: dispatch.EvidenceKindInvalidStageSubmission, MessageA: message, Violation: int32(types.BuilderProtocolViolation_BUILDER_PROTOCOL_VIOLATION_WRONG_STAGE)})
				require.Error(t, err, "a supported OPEN_VERIFY cannot be punished as premature")
			}
			fact, err := f.keeper.publicBuilderObjectiveEvidence(f.ctx, verified)
			require.NoError(t, err)
			require.Equal(t, row.Content, hex.EncodeToString(fact.CanonicalEvidenceDigest))
			require.Equal(t, row.Operator, fact.BuilderOperator)
			id, err := dispatch.EvidenceID(row.Chain, row.Operator, int32(fact.EvidenceKind), fact.ScopeId, verified.ContentDigest)
			require.NoError(t, err)
			require.Equal(t, row.Evidence, hex.EncodeToString(id[:]))
			fault, err := dispatch.FaultID(row.Chain, row.Operator, int32(fact.EvidenceKind), fact.ScopeId, verified.ContentDigest)
			require.NoError(t, err)
			require.Equal(t, row.Fault, hex.EncodeToString(fault[:]))
			core.AcceptedTaskHash = bytes.Repeat([]byte{0x81}, 32)
			require.NoError(t, f.keeper.TaskCore.Set(f.ctx, f.taskKey, core))
			_, err = f.keeper.publicBuilderObjectiveEvidence(f.ctx, verified)
			require.Error(t, err, "cryptography cannot replace accepted Task authority")
			bad := bytes.Clone(evidence)
			bad[len(bad)-1] ^= 1
			_, err = f.keeper.verifyBuilderEvidence(f.ctx, row.Chain, bad)
			require.Error(t, err)
		})
	}
}

func mustDecodeBuilderEvidenceHex(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	require.NoError(t, err)
	return raw
}
