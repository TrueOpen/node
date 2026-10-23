package contracttest

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestSessionKeyOrderSignaturesMatchPublishedWireVector(t *testing.T) {
	// Consume the exact module pin directly instead of maintaining another copy
	// of the nested signature/lifecycle document in Node's flat domain fixtures.
	moduleDir, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/TrueOpen/wire").Output()
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(moduleDir)), "testdata", "v1", "task", "session_key_authorization_v1.json"))
	require.NoError(t, err)
	type section struct {
		Message          map[string]string `json:"message"`
		Signature        string            `json:"signature_65"`
		Digest           string            `json:"signing_digest"`
		Address          string            `json:"recovered_address"`
		SignatureChainID string            `json:"signature_chain_id"`
	}
	var doc struct {
		Wallet    section `json:"wallet_order"`
		Delegated section `json:"delegated_order"`
		Payload   struct {
			Hash string `json:"digest_hex"`
		} `json:"order_payload"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	decode := func(text string) []byte { b, err := hex.DecodeString(text); require.NoError(t, err); return b }
	order := wireCoreTaskOrder(t)
	order.UserAddress = doc.Wallet.Message["user"]
	order.SessionId = decode(doc.Wallet.Message["sessionId"])
	order.OrderSequence, err = strconv.ParseUint(doc.Wallet.Message["orderSequence"], 10, 64)
	require.NoError(t, err)
	hash, err := tasktypes.TaskOrderHash(order)
	require.NoError(t, err)
	require.Equal(t, doc.Payload.Hash, hex.EncodeToString(hash[:]))
	require.Equal(t, doc.Wallet.Digest, doc.Delegated.Digest, "authorization changes the signer, not the order digest")
	for _, vector := range []section{doc.Wallet, doc.Delegated} {
		chainID, err := strconv.ParseUint(vector.SignatureChainID, 10, 64)
		require.NoError(t, err)
		signed := tasktypes.SignedOrderV2{Order: order, SignatureScheme: tasktypes.SignatureSchemeEIP712, SignatureChainId: chainID, UserSignature: decode(vector.Signature)}
		digest, err := tasktypes.BuildTaskOrderEIP712Digest(chainID, "uusdc", order, hash[:])
		require.NoError(t, err)
		require.Equal(t, vector.Digest, hex.EncodeToString(digest.SigningDigest[:]))
		recovered, err := tasktypes.RecoverSignedOrderV2EIP712("uusdc", signed, hash[:])
		require.NoError(t, err)
		require.Equal(t, vector.Address, hex.EncodeToString(recovered.Address))
	}
}
