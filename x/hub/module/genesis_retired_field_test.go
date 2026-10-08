package module

import (
	"encoding/json"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
)

// TestValidateGenesisRejectsRetiredEvmChainID pins the import side of ADR-0033:
// a genesis that still names phase0.evm_chain_id comes from a pre-ADR chain and
// must be refused, not silently read with the field dropped.
func TestValidateGenesisRejectsRetiredEvmChainID(t *testing.T) {
	cdc := codec.NewProtoCodec(codectypes.NewInterfaceRegistry())
	appModule := NewAppModule(cdc, keeper.Keeper{}, types.MsgServerDependencies{})

	defaults := appModule.DefaultGenesis(cdc)
	require.NoError(t, appModule.ValidateGenesis(cdc, nil, defaults))

	var doc map[string]any
	require.NoError(t, json.Unmarshal(defaults, &doc))
	phase0 := doc["params"].(map[string]any)["phase0"].(map[string]any)
	phase0["evm_chain_id"] = "31337"
	legacy, err := json.Marshal(doc)
	require.NoError(t, err)
	require.Error(t, appModule.ValidateGenesis(cdc, nil, legacy))
}
