package types_test

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/internal/testutil/wirefixture"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

type bridgeWireField struct {
	Name   string            `json:"name"`
	Text   string            `json:"utf8"`
	Hex    string            `json:"hex"`
	Value  uint64            `json:"value"`
	Fields []bridgeWireField `json:"fields"`
}

type bridgeWireFields map[string]bridgeWireField

func (fields bridgeWireFields) field(t *testing.T, name string) bridgeWireField {
	t.Helper()
	field, found := fields[name]
	require.True(t, found, "published field %s must exist", name)
	return field
}

func bridgeWireFieldMap(fields []bridgeWireField) bridgeWireFields {
	out := make(bridgeWireFields, len(fields))
	for _, field := range fields {
		out[field.Name] = field
	}
	return out
}

// The typed public producers, rather than a test-side hash implementation, must
// reproduce the exact selected release's bytes, including nested route widths.
func TestBridgeProducersMatchPinnedWire(t *testing.T) {
	var document struct {
		Vectors []struct {
			Name   string            `json:"name"`
			Digest string            `json:"digest_hex"`
			Fields []bridgeWireField `json:"fields"`
		} `json:"vectors"`
	}
	require.NoError(t, json.Unmarshal(wirefixture.Read(t, "hub", "bridge_vrf_v1.json"), &document))
	checked := map[string]bool{}
	for _, vector := range document.Vectors {
		if vector.Name == "vrf_key_pop_v1" {
			continue
		} // Covered for both roles by TestVrfKeyPoPDigestMatchesPinnedWireForBothRoles.
		t.Run(vector.Name, func(t *testing.T) {
			fields := bridgeWireFieldMap(vector.Fields)
			chain := fields.field(t, "chain_id").Text
			var digest [32]byte
			var err error
			switch vector.Name {
			case "usdc_route_v1":
				digest, err = hubtypes.USDCRouteID(chain, bridgeWireRoute(t, fields))
			case "bridge_deployment_manifest_v1":
				digest, err = hubtypes.BridgeDeploymentManifestHash(chain, bridgeWireManifest(t, fields.field(t, "manifest").Fields))
			case "mint_bond_v1":
				digest, err = hubtypes.MintBondActionDigest(chain, hubtypes.MintBondV1{
					ProposalId:               fields.field(t, "proposal_id").Value,
					TargetOperator:           bridgeAddress(t, bridgeWireBytes(t, fields, "target_address_raw")),
					ConsensusPubkey:          bridgeWireBytes(t, fields, "consensus_pubkey"),
					DisclosureDigest:         bridgeWireBytes(t, fields, "disclosure_digest"),
					BridgeSignerAddressRaw20: bridgeWireBytes(t, fields, "bridge_signer_address_raw20"),
					BridgeSignerPopSignature: bridgeWireBytes(t, fields, "bridge_signer_pop_signature"),
				})
			case "burn_bond_v1":
				digest, err = hubtypes.BurnBondActionDigest(chain, hubtypes.BurnBondV1{
					ProposalId:     fields.field(t, "proposal_id").Value,
					TargetOperator: bridgeAddress(t, bridgeWireBytes(t, fields, "target_address_raw")),
				})
			default:
				t.Fatalf("unexecuted published producer %s", vector.Name)
			}
			require.NoError(t, err)
			require.Equal(t, vector.Digest, hex.EncodeToString(digest[:]))
			checked[vector.Name] = true
		})
	}
	require.Len(t, checked, 4)
}

func bridgeWireBytes(t *testing.T, fields bridgeWireFields, name string) []byte {
	t.Helper()
	return bridgeHex(t, fields.field(t, name).Hex)
}

func bridgeWireRoute(t *testing.T, fields bridgeWireFields) hubtypes.BridgeRouteV1 {
	t.Helper()
	route := hubtypes.BridgeRouteV1{
		HyperlaneLocalDomain:    uint32(fields.field(t, "hyperlane_local_domain").Value),
		OriginDomain:            uint32(fields.field(t, "origin_domain").Value),
		OriginTokenAddress:      bridgeWireBytes(t, fields, "origin_token_address"),
		OriginWarpRouterAddress: bridgeWireBytes(t, fields, "origin_warp_router_address"),
		OriginMailboxAddress:    bridgeWireBytes(t, fields, "origin_mailbox_address"),
		OriginDecimals:          uint32(fields.field(t, "origin_decimals").Value),
		BusinessDenom:           fields.field(t, "business_denom").Text,
		LocalMailboxId:          bridgeWireBytes(t, fields, "local_mailbox_id"),
		LocalWarpTokenId:        bridgeWireBytes(t, fields, "local_warp_token_id"),
		LocalOriginDenom:        fields.field(t, "local_origin_denom").Text,
	}
	if field, present := fields["usdc_route_id"]; present {
		route.UsdcRouteId = bridgeHex(t, field.Hex)
	}
	return route
}

func bridgeWireManifest(t *testing.T, raw []bridgeWireField) hubtypes.BridgeDeploymentManifestV1 {
	t.Helper()
	f := bridgeWireFieldMap(raw)
	m := hubtypes.BridgeDeploymentManifestV1{
		SchemaVersion:                     uint32(f.field(t, "schema_version").Value),
		Route:                             bridgeWireRoute(t, bridgeWireFieldMap(f.field(t, "route").Fields)),
		OriginEip155ChainId:               f.field(t, "origin_eip155_chain_id").Value,
		HyperlaneSourceRelease:            f.field(t, "hyperlane_source_release").Text,
		NodeCosmosSdkRelease:              f.field(t, "node_cosmos_sdk_release").Text,
		LocalIsmId:                        bridgeWireBytes(t, f, "local_ism_id"),
		EvmIsmAddress:                     bridgeWireBytes(t, f, "evm_ism_address"),
		LocalOwner:                        bridgeAddress(t, bridgeWireBytes(t, f, "local_owner")),
		EvmOwnerOrProxyAdmin:              bridgeWireBytes(t, f, "evm_owner_or_proxy_admin"),
		FinalitySource:                    hubtypes.BridgeOriginFinalitySourceV1(f.field(t, "finality_source").Value),
		FinalityParameter:                 f.field(t, "finality_parameter").Value,
		LocalSignerSetHash:                bridgeWireBytes(t, f, "local_signer_set_hash"),
		EvmSignerSetHash:                  bridgeWireBytes(t, f, "evm_signer_set_hash"),
		EvmMailboxCodeHash:                bridgeWireBytes(t, f, "evm_mailbox_code_hash"),
		EvmWarpRouterCodeHash:             bridgeWireBytes(t, f, "evm_warp_router_code_hash"),
		EvmIsmCodeHash:                    bridgeWireBytes(t, f, "evm_ism_code_hash"),
		EvmDeploymentTxHash:               bridgeWireBytes(t, f, "evm_deployment_tx_hash"),
		EvmDeploymentBlockNumber:          f.field(t, "evm_deployment_block_number").Value,
		EvmDeploymentBlockHash:            bridgeWireBytes(t, f, "evm_deployment_block_hash"),
		EvmIngressPauseController:         bridgeWireBytes(t, f, "evm_ingress_pause_controller"),
		EvmIngressPauseControllerCodeHash: bridgeWireBytes(t, f, "evm_ingress_pause_controller_code_hash"),
	}
	agents := f.field(t, "agents").Fields
	require.Equal(t, uint64(len(agents)-1), agents[0].Value)
	for _, entry := range agents[1:] {
		a := bridgeWireFieldMap(entry.Fields)
		m.Agents = append(m.Agents, hubtypes.BridgeAgentDeploymentV1{
			OperatorAddress:          bridgeAddress(t, bridgeWireBytes(t, a, "operator_address")),
			BridgeSignerAddressRaw20: bridgeWireBytes(t, a, "bridge_signer_address_raw20"),
			StorageLocation:          a.field(t, "storage_location").Text,
		})
	}
	relayers := f.field(t, "relayers").Fields
	require.Equal(t, uint64(len(relayers)-1), relayers[0].Value)
	for _, entry := range relayers[1:] {
		m.Relayers = append(m.Relayers, bridgeAddress(t, bridgeHex(t, entry.Hex)))
	}
	return m
}
