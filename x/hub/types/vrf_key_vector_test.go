package types_test

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/TrueOpen/node/internal/testutil/wirefixture"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestVrfKeyPoPDigestMatchesPinnedWireForBothRoles(t *testing.T) {
	var document struct {
		Vectors []struct {
			Name   string `json:"name"`
			Digest string `json:"digest_hex"`
			Fields []struct {
				Name  string `json:"name"`
				UTF8  string `json:"utf8"`
				Hex   string `json:"hex"`
				Value uint64 `json:"value"`
			} `json:"fields"`
		} `json:"vectors"`
	}
	require.NoError(t, json.Unmarshal(wirefixture.Read(t, "hub", "bridge_vrf_v1.json"), &document))
	found := false
	for _, vector := range document.Vectors {
		if vector.Name != "vrf_key_pop_v1" {
			continue
		}
		found = true
		require.Len(t, vector.Fields, 4)
		require.Equal(t, []string{"chain_id", "operator_address", "vrf_pubkey", "vrf_authorization_nonce"}, []string{vector.Fields[0].Name, vector.Fields[1].Name, vector.Fields[2].Name, vector.Fields[3].Name})
		operator, err := hex.DecodeString(vector.Fields[1].Hex)
		require.NoError(t, err)
		pubkey, err := hex.DecodeString(vector.Fields[2].Hex)
		require.NoError(t, err)
		for _, role := range []shared.ParticipantType{shared.ParticipantType_PARTICIPANT_TYPE_VALIDATOR, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER} {
			t.Run(role.String(), func(t *testing.T) {
				digest, err := types.VrfKeyPoPDigest(vector.Fields[0].UTF8, role, sdk.AccAddress(operator).String(), pubkey, vector.Fields[3].Value)
				require.NoError(t, err)
				require.Equal(t, vector.Digest, hex.EncodeToString(digest[:]))
			})
		}
	}
	require.True(t, found, "the pinned release must supply the VRF possession vector")
}
