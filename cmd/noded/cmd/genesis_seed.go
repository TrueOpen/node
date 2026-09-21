package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	"github.com/spf13/cobra"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

const (
	genesisSeedVersion       = uint64(1)
	defaultGenesisSeedHeight = uint64(1)
	defaultSupportUntilEpoch = uint64(10_000)
	defaultBuilderSetID      = "genesis-1"
)

type genesisSeed struct {
	Version           uint64                `json:"version"`
	NodeConfig        genesisSeedNodeConfig `json:"node_config"`
	AccountBalance    uint64                `json:"account_balance"`
	SupportUntilEpoch uint64                `json:"support_until_epoch"`
	HubParams         json.RawMessage       `json:"hub_params"`
	TaskParams        json.RawMessage       `json:"task_params"`
	Builders          []genesisSeedBuilder  `json:"builders"`
	CortexNodes       []genesisSeedCortex   `json:"cortex_nodes"`
	Models            []genesisSeedModel    `json:"models"`
}

// NodeConfig is consumed by localnet tooling and is deliberately not projected
// into genesis state. It keeps local process settings separate from consensus
// parameters while allowing one localnet seed to describe a runnable setup.
type genesisSeedNodeConfig struct {
	TaskEventGRPC genesisSeedTaskEventGRPCConfig `json:"task_event_grpc"`
}

type genesisSeedTaskEventGRPCConfig struct {
	Enabled               bool `json:"enabled"`
	ProtocolEventsEnabled bool `json:"protocol_events_enabled"`
}

type genesisSeedBuilder struct {
	Address        string                        `json:"address"`
	ServiceAddress string                        `json:"service_address"`
	ServicePubKey  string                        `json:"service_pubkey"`
	Descriptor     *genesisSeedServiceDescriptor `json:"descriptor"`
}

type genesisSeedCortex struct {
	OperatorAddress   string                        `json:"operator_address"`
	ServiceAddress    string                        `json:"service_address"`
	ServicePubKey     string                        `json:"service_pubkey"`
	Bond              uint64                        `json:"bond"`
	Descriptor        *genesisSeedServiceDescriptor `json:"descriptor"`
	SupportedProfiles []genesisSeedProfileSupport   `json:"supported_profiles"`
}

type genesisSeedServiceDescriptor struct {
	Endpoints []genesisSeedServiceEndpoint `json:"endpoints"`
}

type genesisSeedServiceEndpoint struct {
	EndpointKind    string `json:"endpoint_kind"`
	URI             string `json:"uri"`
	ProtocolVersion string `json:"protocol_version"`
	TLSPubkeyHash   string `json:"tls_pubkey_hash,omitempty"`
}

type genesisSeedProfileSupport struct {
	ModelID        string `json:"model_id"`
	ProfileVersion uint32 `json:"profile_version"`
	Active         bool   `json:"active"`
}

type genesisSeedModel struct {
	ProposerAddress string `json:"proposer_address"`
	Profile         genesisSeedProfile
}

type genesisSeedProfile struct {
	BatchVerification       genesisSeedBatchVerification       `json:"batch_verification"`
	ChallengeOpenWindow     uint64                             `json:"challenge_open_window_blocks"`
	GenerationType          string                             `json:"generation_type"`
	ManifestHash            string                             `json:"manifest_hash"`
	MinStake                genesisSeedCoin                    `json:"min_stake"`
	ModelID                 string                             `json:"model_id"`
	PreviousProfileVersion  uint32                             `json:"previous_profile_version"`
	PricingProfile          genesisSeedPricingProfile          `json:"pricing_profile"`
	ProfileVersion          uint32                             `json:"profile_version"`
	RegistrationFee         genesisSeedCoin                    `json:"registration_fee"`
	RequiredTopK            uint32                             `json:"required_top_k"`
	ResourceTier            uint32                             `json:"resource_tier"`
	RuntimeClass            string                             `json:"runtime_class"`
	SchemaHash              string                             `json:"schema_hash"`
	TaskTypes               []string                           `json:"task_types"`
	TimeoutBootstrapProfile genesisSeedTimeoutBootstrapProfile `json:"timeout_bootstrap_profile"`
	TokenizerHash           string                             `json:"tokenizer_hash"`
	VerificationProfile     genesisSeedVerificationProfile     `json:"verification_profile"`
	VerificationThresholds  shared.VerificationThresholds      `json:"verification_thresholds"`
}

type genesisSeedCoin struct {
	Amount uint64 `json:"amount"`
	Denom  string `json:"denom"`
}

type genesisSeedBatchVerification struct {
	Enabled                       bool   `json:"enabled"`
	MinSampleCount                uint32 `json:"min_sample_count"`
	MinValidSampleCount           uint32 `json:"min_valid_sample_count"`
	PassMinSamplePassRatioBps     uint32 `json:"pass_min_sample_pass_ratio_bps"`
	RejectMinSampleRejectRatioBps uint32 `json:"reject_min_sample_reject_ratio_bps"`
}

type genesisSeedPricingProfile struct {
	InitialOutputPrice uint64 `json:"initial_output_price"`
	VerifyRatioBps     uint32 `json:"verify_ratio_bps"`
	MinOrderValue      uint64 `json:"min_order_value"`
}

type genesisSeedTimeoutBootstrapProfile struct {
	BootstrapValidUntilEpoch     uint64 `json:"bootstrap_valid_until_epoch"`
	CommitTimeoutBootstrapBlocks uint32 `json:"commit_timeout_bootstrap_blocks"`
	InferTimeoutBootstrapBlocks  uint32 `json:"infer_timeout_bootstrap_blocks"`
	VerifyTimeoutBootstrapBlocks uint32 `json:"verify_timeout_bootstrap_blocks"`
}

type genesisSeedVerificationProfile struct {
	CanonicalEncodingVersion      string                    `json:"canonical_encoding_version"`
	EvidenceSchema                genesisSeedEvidenceSchema `json:"evidence_schema"`
	IncludeGeneratedSpecialTokens bool                      `json:"include_generated_special_tokens"`
	IncludePaddingTokens          bool                      `json:"include_padding_tokens"`
	IncludePromptTokens           bool                      `json:"include_prompt_tokens"`
	JudgmentFunctionVersion       string                    `json:"judgment_function_version"`
	MetricAggregateProofVersion   string                    `json:"metric_aggregate_proof_version"`
	Metrics                       genesisSeedMetricSpec     `json:"metrics"`
	RequireFinishReason           bool                      `json:"require_finish_reason"`
	RequireOutputTokenIDs         bool                      `json:"require_output_token_ids"`
	TokenScope                    string                    `json:"token_scope"`
	VerificationMode              string                    `json:"verification_mode"`
	VerificationProfileID         uint32                    `json:"verification_profile_id"`
}

type genesisSeedEvidenceSchema struct {
	SchemaVersion         uint32                           `json:"schema_version"`
	RequiredInferEvidence []genesisSeedEvidenceRequirement `json:"required_infer_evidence"`
}

type genesisSeedEvidenceRequirement struct {
	EvidenceKind            string `json:"evidence_kind"`
	CommitmentSchemaVersion uint32 `json:"commitment_schema_version"`
	MaxEncodedSizeBytes     uint64 `json:"max_encoded_size_bytes"`
}

type genesisSeedMetricSpec struct {
	CompareLogprobDiff bool   `json:"compare_logprob_diff"`
	CompareRankDelta   bool   `json:"compare_rank_delta"`
	CompareTopKJaccard bool   `json:"compare_topk_jaccard"`
	CompareUnionJS     bool   `json:"compare_union_js"`
	ComparedTopK       uint32 `json:"compared_top_k"`
	NumericScale       string `json:"numeric_scale"`
}

func (m *genesisSeedModel) UnmarshalJSON(data []byte) error {
	var metadata struct {
		ProposerAddress string `json:"proposer_address"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return err
	}
	var profile genesisSeedProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return err
	}
	m.ProposerAddress, m.Profile = metadata.ProposerAddress, profile
	return nil
}

func newApplyGenesisSeedCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply-seed [seed-file]",
		Short: "Apply optional Builder, Cortex, and model state to genesis",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx := client.GetClientContextFromCmd(cmd)
			genesisPath := filepath.Join(clientCtx.HomeDir, "config", "genesis.json")
			return applyGenesisSeedFile(clientCtx.Codec, genesisPath, args[0])
		},
	}
}

func applyGenesisSeedFile(cdc codec.Codec, genesisPath, seedPath string) error {
	seed, err := readGenesisSeed(seedPath)
	if err != nil {
		return err
	}
	if len(seed.HubParams) == 0 && len(seed.TaskParams) == 0 &&
		len(seed.Builders) == 0 && len(seed.CortexNodes) == 0 && len(seed.Models) == 0 {
		return nil
	}
	raw, err := os.ReadFile(genesisPath)
	if err != nil {
		return fmt.Errorf("read genesis %s: %w", genesisPath, err)
	}
	updated, err := applyGenesisSeed(cdc, raw, seed)
	if err != nil {
		return err
	}
	info, err := os.Stat(genesisPath)
	if err != nil {
		return fmt.Errorf("stat genesis %s: %w", genesisPath, err)
	}
	if err := os.WriteFile(genesisPath, updated, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write genesis %s: %w", genesisPath, err)
	}
	return nil
}

func readGenesisSeed(path string) (genesisSeed, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return genesisSeed{}, fmt.Errorf("read genesis seed %s: %w", path, err)
	}
	var seed genesisSeed
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&seed); err != nil {
		return genesisSeed{}, fmt.Errorf("decode genesis seed %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return genesisSeed{}, fmt.Errorf("decode genesis seed %s: trailing JSON value", path)
		}
		return genesisSeed{}, fmt.Errorf("decode genesis seed %s: trailing data: %w", path, err)
	}
	if seed.Version != genesisSeedVersion {
		return genesisSeed{}, fmt.Errorf("genesis seed version must be %d", genesisSeedVersion)
	}
	if seed.SupportUntilEpoch == 0 {
		seed.SupportUntilEpoch = defaultSupportUntilEpoch
	}
	if err := validateGenesisSeed(seed); err != nil {
		return genesisSeed{}, err
	}
	return seed, nil
}

func validateGenesisSeed(seed genesisSeed) error {
	if seed.NodeConfig.TaskEventGRPC.ProtocolEventsEnabled && !seed.NodeConfig.TaskEventGRPC.Enabled {
		return fmt.Errorf("node_config.task_event_grpc.protocol_events_enabled requires enabled=true")
	}
	if err := validateGenesisParamsOverride("hub_params", seed.HubParams); err != nil {
		return err
	}
	if err := validateGenesisParamsOverride("task_params", seed.TaskParams); err != nil {
		return err
	}
	// Ruling 16: the minimum service deposit has one source,
	// params.Service.service_bond_min_initial (the API contract field 12).
	// Seed validation runs offline, so the default params carry the value; the
	// deleted keeper.MinServiceBond constant was an unregistered duplicate that had
	// already drifted to half this amount.
	serviceBondMinInitial, err := hubtypes.AmountToUint64(hubtypes.DefaultHubParams().Service.ServiceBondMinInitial, false)
	if err != nil {
		return fmt.Errorf("default service_bond_min_initial: %w", err)
	}
	operators := map[string]string{}
	serviceAddresses := map[string]string{}
	addOperator := func(kind, operatorAddress, serviceAddress, servicePubKey string) error {
		if err := requireGenesisAddress(kind, operatorAddress); err != nil {
			return err
		}
		if previous, exists := operators[operatorAddress]; exists {
			return fmt.Errorf("operator address %s is reused by %s and %s", operatorAddress, previous, kind)
		}
		operators[operatorAddress] = kind
		if err := requireGenesisAddress(kind+" service_address", serviceAddress); err != nil {
			return err
		}
		if previous, exists := serviceAddresses[serviceAddress]; exists {
			return fmt.Errorf("service_address %s is reused by %s and %s", serviceAddress, previous, kind)
		}
		serviceAddresses[serviceAddress] = kind
		pubKey, err := hubtypes.ParseSecp256k1PubKeyHex(kind+" service_pubkey", servicePubKey)
		if err != nil {
			return err
		}
		serviceAddressBytes, err := sdk.AccAddressFromBech32(serviceAddress)
		if err != nil || !bytes.Equal(hubtypes.ServicePubKeyAddress(pubKey), serviceAddressBytes) {
			return fmt.Errorf("%s service_pubkey does not derive service_address", kind)
		}
		return nil
	}
	for i, builder := range seed.Builders {
		kind := fmt.Sprintf("builders[%d]", i)
		if err := addOperator(kind, builder.Address, builder.ServiceAddress, builder.ServicePubKey); err != nil {
			return err
		}
		if err := validateGenesisSeedDescriptor(kind, builder.Descriptor, true); err != nil {
			return err
		}
	}
	supports := map[string]struct{}{}
	for i, node := range seed.CortexNodes {
		kind := fmt.Sprintf("cortex_nodes[%d]", i)
		if err := addOperator(kind, node.OperatorAddress, node.ServiceAddress, node.ServicePubKey); err != nil {
			return err
		}
		if err := requireGenesisAddress(kind+" operator_address", node.OperatorAddress); err != nil {
			return err
		}
		if node.Bond < serviceBondMinInitial {
			return fmt.Errorf("%s bond must be at least %d", kind, serviceBondMinInitial)
		}
		if seed.AccountBalance <= node.Bond {
			return fmt.Errorf("account_balance must be greater than %s bond %d", kind, node.Bond)
		}
		if err := validateGenesisSeedDescriptor(kind, node.Descriptor, false); err != nil {
			return err
		}
		for j, support := range node.SupportedProfiles {
			if strings.TrimSpace(support.ModelID) == "" || support.ModelID != strings.TrimSpace(support.ModelID) || support.ProfileVersion == 0 {
				return fmt.Errorf("%s.supported_profiles[%d] model_id and profile_version must be canonical", kind, j)
			}
			key := node.OperatorAddress + "\x00" + genesisProfileKey(support.ModelID, support.ProfileVersion)
			if _, exists := supports[key]; exists {
				return fmt.Errorf("duplicate supported profile %s/%d for %s", support.ModelID, support.ProfileVersion, node.OperatorAddress)
			}
			supports[key] = struct{}{}
		}
	}
	models := map[string]string{}
	profiles := map[string]struct{}{}
	for i, model := range seed.Models {
		profile := model.Profile
		if profile.ModelID == "" || profile.ModelID != strings.TrimSpace(profile.ModelID) {
			return fmt.Errorf("models[%d].model_id must be non-empty and canonical", i)
		}
		if proposer, exists := models[profile.ModelID]; exists && proposer != model.ProposerAddress {
			return fmt.Errorf("model_id %q has inconsistent proposer_address", profile.ModelID)
		}
		models[profile.ModelID] = model.ProposerAddress
		if err := requireGenesisAddress(fmt.Sprintf("models[%d].proposer_address", i), model.ProposerAddress); err != nil {
			return err
		}
		if profile.ProfileVersion == 0 {
			return fmt.Errorf("models[%d].profile_version must be greater than 0", i)
		}
		key := genesisProfileKey(profile.ModelID, profile.ProfileVersion)
		if _, exists := profiles[key]; exists {
			return fmt.Errorf("duplicate profile %s/%d", profile.ModelID, profile.ProfileVersion)
		}
		profiles[key] = struct{}{}
		projection, err := genesisSeedProjection(profile)
		if err != nil {
			return fmt.Errorf("models[%d]: %w", i, err)
		}
		if projection.MinStake.Amount.Uint64() < serviceBondMinInitial {
			return fmt.Errorf("profile %s/%d min_stake must be at least %d", profile.ModelID, profile.ProfileVersion, serviceBondMinInitial)
		}
		if seed.AccountBalance <= projection.RegistrationFee.Amount.Uint64() {
			return fmt.Errorf(
				"account_balance must be greater than profile %s/%d registration fee %d",
				profile.ModelID, profile.ProfileVersion, projection.RegistrationFee.Amount.Uint64(),
			)
		}
		if profile.ChallengeOpenWindow < hubtypes.ProfileChallengeOpenWindowMinBlocks ||
			profile.ChallengeOpenWindow > hubtypes.ProfileChallengeOpenWindowMaxBlocks {
			return fmt.Errorf("profile %s/%d challenge_open_window_blocks must be in [%d,%d]",
				profile.ModelID, profile.ProfileVersion,
				hubtypes.ProfileChallengeOpenWindowMinBlocks,
				hubtypes.ProfileChallengeOpenWindowMaxBlocks)
		}
	}
	return nil
}

func validateGenesisParamsOverride(name string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return fmt.Errorf("%s must be a JSON object: %w", name, err)
	}
	if values == nil {
		return fmt.Errorf("%s must be a JSON object", name)
	}
	return nil
}

func mergeGenesisParamsJSON(base, overrides []byte) ([]byte, error) {
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(overrides, &fields); err != nil {
		return nil, err
	}
	for name, value := range fields {
		var baseObject, overrideObject map[string]json.RawMessage
		if existing, ok := merged[name]; ok && json.Unmarshal(existing, &baseObject) == nil && baseObject != nil &&
			json.Unmarshal(value, &overrideObject) == nil && overrideObject != nil {
			nestedBase, err := json.Marshal(baseObject)
			if err != nil {
				return nil, err
			}
			nestedOverride, err := json.Marshal(overrideObject)
			if err != nil {
				return nil, err
			}
			value, err = mergeGenesisParamsJSON(nestedBase, nestedOverride)
			if err != nil {
				return nil, err
			}
		}
		merged[name] = value
	}
	return json.Marshal(merged)
}

func applyHubParamsOverride(cdc codec.Codec, params *hubtypes.HubParamsV2, overrides json.RawMessage) error {
	if len(overrides) == 0 {
		return nil
	}
	base, err := cdc.MarshalJSON(params)
	if err != nil {
		return fmt.Errorf("encode default hub_params: %w", err)
	}
	merged, err := mergeGenesisParamsJSON(base, overrides)
	if err != nil {
		return fmt.Errorf("merge hub_params: %w", err)
	}
	var updated hubtypes.HubParamsV2
	if err := cdc.UnmarshalJSON(merged, &updated); err != nil {
		return fmt.Errorf("decode hub_params: %w", err)
	}
	if err := updated.Validate(); err != nil {
		return fmt.Errorf("validate hub_params: %w", err)
	}
	*params = updated
	return nil
}

func applyTaskParamsOverride(cdc codec.Codec, params *tasktypes.TaskParamsV1, overrides json.RawMessage) error {
	if len(overrides) == 0 {
		return nil
	}
	base, err := cdc.MarshalJSON(params)
	if err != nil {
		return fmt.Errorf("encode default task_params: %w", err)
	}
	merged, err := mergeGenesisParamsJSON(base, overrides)
	if err != nil {
		return fmt.Errorf("merge task_params: %w", err)
	}
	var updated tasktypes.TaskParamsV1
	if err := cdc.UnmarshalJSON(merged, &updated); err != nil {
		return fmt.Errorf("decode task_params: %w", err)
	}
	if err := updated.Validate(); err != nil {
		return fmt.Errorf("validate task_params: %w", err)
	}
	*params = updated
	return nil
}

func requireGenesisAddress(field, value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must be a non-empty canonical address", field)
	}
	address, err := sdk.AccAddressFromBech32(value)
	if err != nil || address.String() != value {
		return fmt.Errorf("%s must be a canonical bech32 account address", field)
	}
	return nil
}

func applyGenesisSeed(cdc codec.Codec, rawGenesis []byte, seed genesisSeed) ([]byte, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(rawGenesis, &document); err != nil {
		return nil, fmt.Errorf("decode genesis document: %w", err)
	}
	seedHeight, err := genesisSeedApplicationHeight(document)
	if err != nil {
		return nil, err
	}
	var chainID string
	if err := json.Unmarshal(document["chain_id"], &chainID); err != nil || strings.TrimSpace(chainID) == "" {
		return nil, fmt.Errorf("genesis chain_id is required")
	}
	var appState map[string]json.RawMessage
	if err := json.Unmarshal(document["app_state"], &appState); err != nil {
		return nil, fmt.Errorf("decode genesis app_state: %w", err)
	}
	var hub hubtypes.GenesisState
	if err := cdc.UnmarshalJSON(appState[hubtypes.ModuleName], &hub); err != nil {
		return nil, fmt.Errorf("decode hub genesis: %w", err)
	}
	if hub.Params.Epoch.EpochLengthBlocks == 0 {
		hub.Params.Epoch.EpochLengthBlocks = hubtypes.DefaultEpochLengthBlocks
	}
	var bank banktypes.GenesisState
	if err := cdc.UnmarshalJSON(appState[banktypes.ModuleName], &bank); err != nil {
		return nil, fmt.Errorf("decode bank genesis: %w", err)
	}
	var auth authtypes.GenesisState
	if err := cdc.UnmarshalJSON(appState[authtypes.ModuleName], &auth); err != nil {
		return nil, fmt.Errorf("decode auth genesis: %w", err)
	}
	var task tasktypes.GenesisState
	if len(seed.TaskParams) > 0 {
		if err := cdc.UnmarshalJSON(appState[tasktypes.ModuleName], &task); err != nil {
			return nil, fmt.Errorf("decode task genesis: %w", err)
		}
	}
	if err := applyHubParamsOverride(cdc, &hub.Params, seed.HubParams); err != nil {
		return nil, err
	}
	businessDenom := hub.Params.Phase0.BusinessDenom
	if businessDenom == "" {
		return nil, fmt.Errorf("hub_params.phase0.business_denom is required")
	}
	if err := applySDKGenesisParams(cdc, appState, businessDenom); err != nil {
		return nil, err
	}
	if len(seed.TaskParams) > 0 {
		if err := applyTaskParamsOverride(cdc, &task.Params, seed.TaskParams); err != nil {
			return nil, err
		}
	}
	builderExists := make(map[string]bool, len(hub.Builders))
	for _, builder := range hub.Builders {
		builderExists[builder.BuilderAddress] = true
	}
	newBuilders := make([]genesisSeedBuilder, 0, len(seed.Builders))
	for _, builder := range seed.Builders {
		if builderExists[builder.Address] {
			continue
		}
		newBuilders = append(newBuilders, builder)
		builderExists[builder.Address] = true
	}

	nodeExists := make(map[string]bool, len(hub.CortexNodes))
	for _, node := range hub.CortexNodes {
		nodeExists[node.OperatorAddress] = true
	}
	newNodes := make([]genesisSeedCortex, 0, len(seed.CortexNodes))
	for _, node := range seed.CortexNodes {
		if nodeExists[node.OperatorAddress] {
			continue
		}
		newNodes = append(newNodes, node)
		nodeExists[node.OperatorAddress] = true
	}

	fundingAddresses := make(map[string]struct{}, len(newBuilders)*2+len(newNodes)*2)
	for _, builder := range newBuilders {
		fundingAddresses[builder.Address] = struct{}{}
		fundingAddresses[builder.ServiceAddress] = struct{}{}
	}
	for _, node := range newNodes {
		fundingAddresses[node.OperatorAddress] = struct{}{}
		fundingAddresses[node.ServiceAddress] = struct{}{}
	}
	existingProfiles := make(map[string]struct{}, len(hub.Profiles))
	for _, profile := range hub.Profiles {
		existingProfiles[genesisProfileKey(profile.ModelId, profile.ProfileVersion)] = struct{}{}
	}
	for _, model := range seed.Models {
		key := genesisProfileKey(model.Profile.ModelID, model.Profile.ProfileVersion)
		if _, exists := existingProfiles[key]; !exists {
			fundingAddresses[model.ProposerAddress] = struct{}{}
		}
	}
	if err := ensureGenesisAccountsFunded(&auth, &bank, fundingAddresses, businessDenom, seed.AccountBalance); err != nil {
		return nil, err
	}

	for _, builder := range newBuilders {
		if err := appendGenesisBuilder(&hub, builder, seedHeight); err != nil {
			return nil, err
		}
	}
	if len(hub.BuilderSets) == 0 && len(hub.Builders) > 0 {
		addresses := make([]string, 0, len(hub.Builders))
		for _, builder := range hub.Builders {
			if builder.CurrentServiceKeyStatus == hubtypes.ServiceKeyStatusActive {
				addresses = append(addresses, builder.BuilderAddress)
			}
		}
		sort.Slice(addresses, func(i, j int) bool {
			left := sdk.MustAccAddressFromBech32(addresses[i])
			right := sdk.MustAccAddressFromBech32(addresses[j])
			return bytes.Compare(left, right) < 0
		})
		if len(addresses) > 0 {
			memberAddresses := make([][]byte, 0, len(addresses))
			for _, address := range addresses {
				memberAddresses = append(memberAddresses, sdk.MustAccAddressFromBech32(address))
			}
			membersHash, err := hubkeeper.BuilderSetMembersHash(memberAddresses)
			if err != nil {
				return nil, fmt.Errorf("derive genesis BuilderSet members hash: %w", err)
			}
			const version = uint64(1)
			setHash, err := hubkeeper.BuilderSetHash(
				chainID, version, defaultBuilderSetID, seedHeight,
				uint32(len(addresses)), membersHash,
			)
			if err != nil {
				return nil, fmt.Errorf("derive genesis BuilderSet hash: %w", err)
			}
			hub.BuilderSets = append(hub.BuilderSets, hubtypes.BuilderSetState{
				BuilderSetVersion: version, BuilderSetId: defaultBuilderSetID,
				BuilderSetHash: setHash, BuilderSetMembersHash: membersHash,
				EffectiveHeight: seedHeight, ActiveBuilders: addresses,
				ActiveBuilderCount: uint32(len(addresses)),
				BodyStatus:         shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE,
			})
			hub.CurrentBuilderSet = hubtypes.CurrentBuilderSetState{
				Mode: "GOVERNED_FIXED_V1", BuilderSetVersion: version,
				BuilderSetId: defaultBuilderSetID, BuilderSetHash: setHash,
				BuilderSetMembersHash: membersHash, EffectiveHeight: seedHeight,
			}
			hub.BuilderAdmissions = make([]hubtypes.BuilderAdmissionState, 0, len(addresses))
			for _, address := range addresses {
				hub.BuilderAdmissions = append(hub.BuilderAdmissions, hubtypes.BuilderAdmissionState{
					BuilderAddress: address, Status: hubtypes.BuilderStatus_BUILDER_STATUS_ADMITTED,
					CurrentBuilderSetVersion: version, UpdatedHeight: seedHeight,
				})
			}
		}
	}

	for _, node := range newNodes {
		if err := moveGenesisFunds(&bank, node.OperatorAddress, hubtypes.ServiceBondModuleName, businessDenom, node.Bond); err != nil {
			return nil, fmt.Errorf("bond cortex node %s: %w", node.OperatorAddress, err)
		}
		if err := appendGenesisCortexNode(&hub, node, seedHeight); err != nil {
			return nil, err
		}
	}

	if err := appendMissingModels(&hub, &bank, seed.Models, chainID, seedHeight); err != nil {
		return nil, err
	}
	affectedProfiles, err := appendMissingSupports(&hub, seed, seedHeight)
	if err != nil {
		return nil, err
	}
	if err := reconcileGenesisModelStatuses(&hub, affectedProfiles, seedHeight); err != nil {
		return nil, err
	}
	hub, err = hubtypes.PrepareCandidateSlotGenesis(hub)
	if err != nil {
		return nil, fmt.Errorf("prepare seeded candidate slots: %w", err)
	}
	if err := hub.Validate(); err != nil {
		return nil, fmt.Errorf("validate seeded hub genesis: %w", err)
	}
	if err := bank.Validate(); err != nil {
		return nil, fmt.Errorf("validate seeded bank genesis: %w", err)
	}
	if err := authtypes.ValidateGenesis(auth); err != nil {
		return nil, fmt.Errorf("validate seeded auth genesis: %w", err)
	}
	if len(seed.TaskParams) > 0 {
		if err := task.Validate(); err != nil {
			return nil, fmt.Errorf("validate seeded task genesis: %w", err)
		}
	}
	sort.Slice(bank.Balances, func(i, j int) bool { return bank.Balances[i].Address < bank.Balances[j].Address })
	hubJSON, err := cdc.MarshalJSON(&hub)
	if err != nil {
		return nil, fmt.Errorf("encode hub genesis: %w", err)
	}
	bankJSON, err := cdc.MarshalJSON(&bank)
	if err != nil {
		return nil, fmt.Errorf("encode bank genesis: %w", err)
	}
	authJSON, err := cdc.MarshalJSON(&auth)
	if err != nil {
		return nil, fmt.Errorf("encode auth genesis: %w", err)
	}
	appState[hubtypes.ModuleName] = hubJSON
	appState[banktypes.ModuleName] = bankJSON
	appState[authtypes.ModuleName] = authJSON
	if len(seed.TaskParams) > 0 {
		taskJSON, err := cdc.MarshalJSON(&task)
		if err != nil {
			return nil, fmt.Errorf("encode task genesis: %w", err)
		}
		appState[tasktypes.ModuleName] = taskJSON
	}
	document["app_state"], err = json.Marshal(appState)
	if err != nil {
		return nil, fmt.Errorf("encode genesis app_state: %w", err)
	}
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode genesis document: %w", err)
	}
	return append(updated, '\n'), nil
}

func applySDKGenesisParams(cdc codec.Codec, appState map[string]json.RawMessage, businessDenom string) error {
	var governance govv1.GenesisState
	if err := cdc.UnmarshalJSON(appState[govtypes.ModuleName], &governance); err != nil {
		return fmt.Errorf("decode gov genesis: %w", err)
	}
	if governance.Params == nil {
		return fmt.Errorf("gov genesis params are required")
	}
	var err error
	governance.Params.MinDeposit, err = genesisDepositWithDenom(
		"gov min_deposit", governance.Params.MinDeposit, businessDenom,
	)
	if err != nil {
		return err
	}
	governance.Params.ExpeditedMinDeposit, err = genesisDepositWithDenom(
		"gov expedited_min_deposit", governance.Params.ExpeditedMinDeposit, businessDenom,
	)
	if err != nil {
		return err
	}
	// Keep the SDK v0.53.6 policy: only a veto result forfeits the deposit.
	// GovernedGovBankKeeper redirects that forfeiture to trueopen_treasury.
	governance.Params.BurnVoteQuorum = false
	governance.Params.BurnProposalDepositPrevote = false
	governance.Params.BurnVoteVeto = true
	if err := govv1.ValidateGenesis(&governance); err != nil {
		return fmt.Errorf("validate gov genesis: %w", err)
	}
	appState[govtypes.ModuleName], err = cdc.MarshalJSON(&governance)
	if err != nil {
		return fmt.Errorf("encode gov genesis: %w", err)
	}

	var slashing slashingtypes.GenesisState
	if err := cdc.UnmarshalJSON(appState[slashingtypes.ModuleName], &slashing); err != nil {
		return fmt.Errorf("decode slashing genesis: %w", err)
	}
	slashing.Params.SlashFractionDoubleSign = sdkmath.LegacyZeroDec()
	slashing.Params.SlashFractionDowntime = sdkmath.LegacyZeroDec()
	if err := slashingtypes.ValidateGenesis(slashing); err != nil {
		return fmt.Errorf("validate slashing genesis: %w", err)
	}
	appState[slashingtypes.ModuleName], err = cdc.MarshalJSON(&slashing)
	if err != nil {
		return fmt.Errorf("encode slashing genesis: %w", err)
	}
	return nil
}

func genesisDepositWithDenom(field string, deposit sdk.Coins, denom string) (sdk.Coins, error) {
	if len(deposit) != 1 || !deposit.IsValid() || !deposit[0].Amount.IsPositive() {
		return nil, fmt.Errorf("%s must contain exactly one positive coin", field)
	}
	return sdk.NewCoins(sdk.NewCoin(denom, deposit[0].Amount)), nil
}

func ensureGenesisAccountsFunded(
	auth *authtypes.GenesisState,
	bank *banktypes.GenesisState,
	addresses map[string]struct{},
	denom string,
	targetBalance uint64,
) error {
	if len(addresses) == 0 {
		return nil
	}
	accounts, err := authtypes.UnpackAccounts(auth.Accounts)
	if err != nil {
		return fmt.Errorf("unpack auth genesis accounts: %w", err)
	}
	accountExists := make(map[string]bool, len(accounts))
	for _, account := range accounts {
		accountExists[account.GetAddress().String()] = true
	}
	orderedAddresses := make([]string, 0, len(addresses))
	for address := range addresses {
		orderedAddresses = append(orderedAddresses, address)
	}
	sort.Strings(orderedAddresses)
	for _, address := range orderedAddresses {
		parsed, err := sdk.AccAddressFromBech32(address)
		if err != nil {
			return fmt.Errorf("parse genesis account %s: %w", address, err)
		}
		if !accountExists[address] {
			accounts = append(accounts, authtypes.NewBaseAccountWithAddress(parsed))
			accountExists[address] = true
		}
		topUpGenesisBalance(bank, address, denom, targetBalance)
	}
	accounts = authtypes.SanitizeGenesisAccounts(accounts)
	auth.Accounts, err = authtypes.PackAccounts(accounts)
	if err != nil {
		return fmt.Errorf("pack auth genesis accounts: %w", err)
	}
	return nil
}

func topUpGenesisBalance(bank *banktypes.GenesisState, address, denom string, targetBalance uint64) {
	target := sdkmath.NewIntFromUint64(targetBalance)
	for i := range bank.Balances {
		if bank.Balances[i].Address != address {
			continue
		}
		current := bank.Balances[i].Coins.AmountOf(denom)
		if current.GTE(target) {
			return
		}
		topUp := sdk.NewCoin(denom, target.Sub(current))
		bank.Balances[i].Coins = bank.Balances[i].Coins.Add(topUp)
		bank.Supply = bank.Supply.Add(topUp)
		return
	}
	coin := sdk.NewCoin(denom, target)
	bank.Balances = append(bank.Balances, banktypes.Balance{
		Address: address,
		Coins:   sdk.NewCoins(coin),
	})
	bank.Supply = bank.Supply.Add(coin)
}

func genesisSeedApplicationHeight(document map[string]json.RawMessage) (uint64, error) {
	raw, exists := document["initial_height"]
	if !exists || len(raw) == 0 {
		return defaultGenesisSeedHeight, nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		height, err := strconv.ParseUint(encoded, 10, 64)
		if err != nil || height == 0 {
			return 0, fmt.Errorf("genesis initial_height must be a positive integer")
		}
		return height, nil
	}
	var height uint64
	if err := json.Unmarshal(raw, &height); err != nil || height == 0 {
		return 0, fmt.Errorf("genesis initial_height must be a positive integer")
	}
	return height, nil
}

func moveGenesisFunds(bank *banktypes.GenesisState, fromAddress, moduleName, denom string, amount uint64) error {
	coin := sdk.NewCoin(denom, sdkmath.NewIntFromUint64(amount))
	found := false
	for i := range bank.Balances {
		if bank.Balances[i].Address != fromAddress {
			continue
		}
		found = true
		if bank.Balances[i].Coins.AmountOf(denom).LT(coin.Amount) {
			return fmt.Errorf("account %s has insufficient %s balance to transfer %d to module %s", fromAddress, denom, amount, moduleName)
		}
		bank.Balances[i].Coins = bank.Balances[i].Coins.Sub(coin)
		break
	}
	if !found {
		return fmt.Errorf("account %s is missing from bank genesis", fromAddress)
	}
	moduleAddress := authtypes.NewModuleAddress(moduleName).String()
	for i := range bank.Balances {
		if bank.Balances[i].Address == moduleAddress {
			bank.Balances[i].Coins = bank.Balances[i].Coins.Add(coin)
			return nil
		}
	}
	bank.Balances = append(bank.Balances, banktypes.Balance{Address: moduleAddress, Coins: sdk.NewCoins(coin)})
	return nil
}

func appendGenesisBuilder(hub *hubtypes.GenesisState, seed genesisSeedBuilder, height uint64) error {
	servicePubkey, _ := hex.DecodeString(seed.ServicePubKey)
	hub.Builders = append(hub.Builders, hubtypes.BuilderState{
		SchemaVersion:  1,
		BuilderAddress: seed.Address, CurrentServiceAddress: seed.ServiceAddress,
		CurrentServicePubkey: servicePubkey, ServiceAuthorizationNonce: 1,
		CurrentServiceKeyStatus:  hubtypes.ServiceKeyStatusActive,
		RegisteredHeight:         height,
		CurrentDescriptorVersion: 1,
	})
	descriptor, err := newGenesisServiceDescriptor(
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, seed.Address, seed.Descriptor, height, hub.Params.Service,
	)
	if err != nil {
		return err
	}
	hub.ServiceDescriptors = append(hub.ServiceDescriptors, descriptor)
	return nil
}

func appendGenesisCortexNode(hub *hubtypes.GenesisState, seed genesisSeedCortex, height uint64) error {
	descriptorConfigured := seed.Descriptor != nil
	node := hubtypes.CortexNodeState{
		SchemaVersion:   1,
		OperatorAddress: seed.OperatorAddress, CurrentServiceAddress: seed.ServiceAddress,
		ServiceAuthorizationNonce: 1, ServiceKeyStatus: hubtypes.ServiceKeyStatusActive,
		RegisteredHeight: height, UpdatedHeight: height,
	}
	node.CurrentServicePubkey, _ = hex.DecodeString(seed.ServicePubKey)
	if descriptorConfigured {
		node.CurrentDescriptorVersion = 1
	}
	hub.CortexNodes = append(hub.CortexNodes, node)
	hub.ServiceBonds = append(hub.ServiceBonds, hubtypes.ServiceBondState{
		OperatorAddress: seed.OperatorAddress,
		ActiveBond:      seed.Bond, EffectiveActiveBond: seed.Bond, BondVersion: 1, Status: hubtypes.ServiceBondStatusActive,
		LastStakeHeight: height,
	})
	if descriptorConfigured {
		descriptor, err := newGenesisServiceDescriptor(
			shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, seed.OperatorAddress, seed.Descriptor, height, hub.Params.Service,
		)
		if err != nil {
			return err
		}
		hub.ServiceDescriptors = append(hub.ServiceDescriptors, descriptor)
	}
	return nil
}

func newGenesisServiceDescriptor(
	participantType shared.ParticipantType, operatorAddress string, descriptor *genesisSeedServiceDescriptor,
	height uint64, params hubtypes.ServiceParamsV1,
) (hubtypes.ServiceDescriptorState, error) {
	endpoints, err := genesisServiceEndpoints(descriptor)
	if err != nil {
		return hubtypes.ServiceDescriptorState{}, err
	}
	operatorBytes, err := sdk.AccAddressFromBech32(operatorAddress)
	if err != nil {
		return hubtypes.ServiceDescriptorState{}, fmt.Errorf("descriptor operator_address: %w", err)
	}
	hash, err := hubtypes.CanonicalServiceDescriptorHash(participantType, operatorBytes, 1, endpoints, params)
	if err != nil {
		return hubtypes.ServiceDescriptorState{}, fmt.Errorf("service descriptor: %w", err)
	}
	return hubtypes.ServiceDescriptorState{
		ParticipantType: participantType, OperatorAddress: operatorAddress,
		DescriptorVersion: 1, EndpointCount: uint32(len(endpoints)), Endpoints: endpoints,
		DescriptorHash: hash, UpdatedHeight: height,
	}, nil
}

func validateGenesisSeedDescriptor(kind string, descriptor *genesisSeedServiceDescriptor, required bool) error {
	if descriptor == nil {
		if required {
			return fmt.Errorf("%s descriptor is required", kind)
		}
		return nil
	}
	endpoints, err := genesisServiceEndpoints(descriptor)
	if err != nil {
		return fmt.Errorf("%s descriptor: %w", kind, err)
	}
	if _, err := hubtypes.CanonicalServiceDescriptorEndpointFields(endpoints, hubtypes.DefaultHubParams().Service); err != nil {
		return fmt.Errorf("%s descriptor: %w", kind, err)
	}
	return nil
}

func genesisServiceEndpoints(descriptor *genesisSeedServiceDescriptor) ([]hubtypes.ServiceEndpointV1, error) {
	if descriptor == nil {
		return nil, fmt.Errorf("descriptor is required")
	}
	endpoints := make([]hubtypes.ServiceEndpointV1, len(descriptor.Endpoints))
	for i, seed := range descriptor.Endpoints {
		kind, exists := hubtypes.ServiceEndpointKind_value[seed.EndpointKind]
		if !exists || kind == int32(hubtypes.ServiceEndpointKind_SERVICE_ENDPOINT_KIND_UNSPECIFIED) {
			return nil, fmt.Errorf("endpoint %d has invalid endpoint_kind %q", i, seed.EndpointKind)
		}
		endpoints[i] = hubtypes.ServiceEndpointV1{
			EndpointKind:    hubtypes.ServiceEndpointKind(kind),
			Uri:             seed.URI,
			ProtocolVersion: seed.ProtocolVersion,
		}
		if seed.TLSPubkeyHash != "" {
			tlsHash, err := hex.DecodeString(seed.TLSPubkeyHash)
			if err != nil {
				return nil, fmt.Errorf("endpoint %d tls_pubkey_hash must be hex: %w", i, err)
			}
			endpoints[i].XTlsPubkeyHash = &hubtypes.ServiceEndpointV1_TlsPubkeyHash{TlsPubkeyHash: tlsHash}
		}
	}
	return endpoints, nil
}

func appendMissingModels(hub *hubtypes.GenesisState, bank *banktypes.GenesisState, models []genesisSeedModel, chainID string, height uint64) error {
	modelExists := make(map[string]bool, len(hub.Models))
	for _, model := range hub.Models {
		modelExists[model.ModelId] = true
	}
	profileExists := make(map[string]bool, len(hub.Profiles))
	for _, profile := range hub.Profiles {
		profileExists[genesisProfileKey(profile.ModelId, profile.ProfileVersion)] = true
	}
	for _, modelSeed := range models {
		profileSeed := modelSeed.Profile
		if !modelExists[profileSeed.ModelID] {
			hub.Models = append(hub.Models, hubtypes.ModelState{
				ModelId: profileSeed.ModelID, ProposerAddress: modelSeed.ProposerAddress, Status: hubtypes.ModelStatusRegistered,
				CreatedHeight: height, UpdatedHeight: height,
				StatusSource: hubtypes.ModelStatusSourceAutoProfile,
			})
			modelExists[profileSeed.ModelID] = true
		}
		key := genesisProfileKey(profileSeed.ModelID, profileSeed.ProfileVersion)
		if profileExists[key] {
			continue
		}
		projection, err := genesisSeedProjection(profileSeed)
		if err != nil {
			return err
		}
		businessDenom := hub.Params.Phase0.BusinessDenom
		if projection.MinStake.Denom != businessDenom || projection.RegistrationFee.Denom != businessDenom {
			return fmt.Errorf("profile %s/%d amounts must use business_denom %q",
				projection.ModelId, projection.ProfileVersion, businessDenom)
		}
		registrationDigest, _, err := hubtypes.ModelRegistrationDigest(chainID, modelSeed.ProposerAddress, projection)
		if err != nil {
			return err
		}
		minStake, registrationFee := projection.MinStake.Amount.Uint64(), projection.RegistrationFee.Amount.Uint64()
		if err := recordGenesisRegistrationFee(hub, bank, modelSeed.ProposerAddress, registrationFee, height); err != nil {
			return fmt.Errorf("register profile %s/%d: %w", projection.ModelId, projection.ProfileVersion, err)
		}
		profile := hubtypes.ProfileState{
			ModelId: projection.ModelId, ProfileVersion: projection.ProfileVersion,
			ManifestHash: projection.ManifestHash, TokenizerHash: projection.TokenizerHash,
			RuntimeClass: projection.RuntimeClass, RequiredTopK: projection.RequiredTopK, TaskTypes: projection.TaskTypes,
			GenerationType: projection.GenerationType, ResourceTier: projection.ResourceTier, MinStake: minStake,
			ChallengeOpenWindowBlocks: projection.ChallengeOpenWindowBlocks,
			VerificationProfile:       projection.VerificationProfile, VerificationThresholds: projection.VerificationThresholds,
			BatchVerification: projection.BatchVerification, PricingProfile: projection.PricingProfile,
			TimeoutBootstrapProfile: projection.TimeoutBootstrapProfile, SchemaHash: projection.SchemaHash,
			// the model registration contract "then set
			// ProfileState.ref_price = initial_output_price";
			// ValidateGenesis requires it to be positive, so a seed without it cannot boot.
			RefPrice: projection.PricingProfile.InitialOutputPrice,
			Status:   hubtypes.ModelStatusRegistered, StatusSource: hubtypes.ProfileStatusSourceAutoSupport,
			RegistrationFeePaid: registrationFee, PreviousProfileVersion: projection.PreviousProfileVersion,
			ProposerAddress: modelSeed.ProposerAddress, RegistrationDigest: registrationDigest,
			CreatedHeight: height, UpdatedHeight: height,
		}
		hub.Profiles = append(hub.Profiles, profile)
		for i := range hub.Models {
			if hub.Models[i].ModelId == projection.ModelId && hub.Models[i].LatestProfileVersion < projection.ProfileVersion {
				hub.Models[i].LatestProfileVersion = projection.ProfileVersion
				hub.Models[i].RegistrationFeePaid += registrationFee
			}
		}
		profileExists[key] = true
	}
	return nil
}

// recordGenesisRegistrationFee moves the profile registration fee into the
// treasury module account and credits the treasury singleton.
//
// The per-epoch treasury ledger this used to maintain is gone.
// TreasuryEpochState / GenesisState.treasury_epochs were deleted with the
// reward wire (§5.11 replaces the epoch-close summary with codes 80/81 and
// TreasuryState now carries only balance + treasury_version), so the seed can
// no longer pre-build epoch inflow rows, maintenance_rate_ppm or rule_version.
// Treasury runtime tests must assert that the seeded
// balance equals the sum of the registration fees actually moved and that the
// §9.6c governance receipts stay empty at genesis.
func recordGenesisRegistrationFee(
	hub *hubtypes.GenesisState,
	bank *banktypes.GenesisState,
	proposer string,
	amount, _ uint64,
) error {
	if err := moveGenesisFunds(bank, proposer, hubtypes.TreasuryModuleName, hub.Params.Phase0.BusinessDenom, amount); err != nil {
		return err
	}
	balance, err := shared.ParseAmount(hub.Treasury.Balance)
	if err != nil {
		return fmt.Errorf("treasury balance is not a canonical amount: %w", err)
	}
	total, err := checkedGenesisAdd(balance, amount)
	if err != nil {
		return fmt.Errorf("treasury balance overflow")
	}
	hub.Treasury.Balance = shared.NewAmount(total)
	hub.Treasury.TreasuryVersion++
	return nil
}

func genesisProfileKey(modelID string, profileVersion uint32) string {
	return modelID + "\x00" + strconv.FormatUint(uint64(profileVersion), 10)
}

func decodeGenesisSeedHash(field, value string, allowZero bool) ([]byte, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return nil, fmt.Errorf("%s must be a 32-byte hex value", field)
	}
	allZero := true
	for _, item := range decoded {
		if item != 0 {
			allZero = false
			break
		}
	}
	if allZero && !allowZero {
		return nil, fmt.Errorf("%s must be non-zero", field)
	}
	return decoded, nil
}

func genesisSeedProjection(seed genesisSeedProfile) (shared.ModelProfileProjection, error) {
	if seed.MinStake.Denom == "" || seed.MinStake.Amount == 0 {
		return shared.ModelProfileProjection{}, fmt.Errorf("min_stake must be a positive coin")
	}
	if seed.RegistrationFee.Denom == "" || seed.RegistrationFee.Amount == 0 {
		return shared.ModelProfileProjection{}, fmt.Errorf("registration_fee must be a positive coin")
	}
	manifestHash, err := decodeGenesisSeedHash("manifest_hash", seed.ManifestHash, false)
	if err != nil {
		return shared.ModelProfileProjection{}, err
	}
	tokenizerHash, err := decodeGenesisSeedHash("tokenizer_hash", seed.TokenizerHash, false)
	if err != nil {
		return shared.ModelProfileProjection{}, err
	}
	schemaHash, err := decodeGenesisSeedHash("schema_hash", seed.SchemaHash, false)
	if err != nil {
		return shared.ModelProfileProjection{}, err
	}
	taskTypes := make([]shared.TaskType, len(seed.TaskTypes))
	for i, value := range seed.TaskTypes {
		switch value {
		case "TEXT_GENERATION":
			taskTypes[i] = shared.TaskType_TASK_TYPE_TEXT_GENERATION
		case "CHAT":
			taskTypes[i] = shared.TaskType_TASK_TYPE_CHAT
		case "EMBEDDING":
			taskTypes[i] = shared.TaskType_TASK_TYPE_EMBEDDING
		case "CLASSIFICATION":
			taskTypes[i] = shared.TaskType_TASK_TYPE_CLASSIFICATION
		case "IMAGE_GENERATION":
			taskTypes[i] = shared.TaskType_TASK_TYPE_IMAGE_GENERATION
		case "MULTIMODAL":
			taskTypes[i] = shared.TaskType_TASK_TYPE_MULTIMODAL
		default:
			return shared.ModelProfileProjection{}, fmt.Errorf("unsupported task_type %q", value)
		}
	}
	generationType := shared.GenerationType_GENERATION_TYPE_UNSPECIFIED
	if seed.GenerationType == "SAMPLED" {
		generationType = shared.GenerationType_GENERATION_TYPE_SAMPLED
	}
	if seed.GenerationType == "DETERMINISTIC" {
		generationType = shared.GenerationType_GENERATION_TYPE_DETERMINISTIC
	}
	verificationMode := shared.VerificationMode_VERIFICATION_MODE_UNSPECIFIED
	if seed.VerificationProfile.VerificationMode == "SINGLE_SAMPLE" {
		verificationMode = shared.VerificationMode_VERIFICATION_MODE_SINGLE_SAMPLE
	}
	tokenScope := shared.TokenScope_TOKEN_SCOPE_UNSPECIFIED
	if seed.VerificationProfile.TokenScope == "ALL_GENERATED_OUTPUT_TOKENS" {
		tokenScope = shared.TokenScope_TOKEN_SCOPE_ALL_GENERATED_OUTPUT_TOKENS
	}
	numericScale := shared.NumericScale_NUMERIC_SCALE_UNSPECIFIED
	if seed.VerificationProfile.Metrics.NumericScale == "FP_1E6" {
		numericScale = shared.NumericScale_NUMERIC_SCALE_FP_1E6
	}
	evidenceRequirements := make([]shared.InferEvidenceRequirementV1, len(seed.VerificationProfile.EvidenceSchema.RequiredInferEvidence))
	for index, requirement := range seed.VerificationProfile.EvidenceSchema.RequiredInferEvidence {
		var kind shared.EvidenceKind
		switch requirement.EvidenceKind {
		case "WORKER_VALUE_OPENING":
			kind = shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING
		case "VERIFIER_VALUE_OPENING":
			kind = shared.EvidenceKind_EVIDENCE_KIND_VERIFIER_VALUE_OPENING
		case "SETTLEMENT_ROOT_OPENING":
			kind = shared.EvidenceKind_EVIDENCE_KIND_SETTLEMENT_ROOT_OPENING
		default:
			return shared.ModelProfileProjection{}, fmt.Errorf("unsupported evidence_kind %q", requirement.EvidenceKind)
		}
		evidenceRequirements[index] = shared.InferEvidenceRequirementV1{
			EvidenceKind: kind, CommitmentSchemaVersion: requirement.CommitmentSchemaVersion,
			MaxEncodedSizeBytes: requirement.MaxEncodedSizeBytes,
		}
	}
	projection := shared.ModelProfileProjection{
		ModelId: seed.ModelID, ProfileVersion: seed.ProfileVersion, ManifestHash: manifestHash, TokenizerHash: tokenizerHash,
		RuntimeClass: seed.RuntimeClass, RequiredTopK: seed.RequiredTopK, TaskTypes: taskTypes, GenerationType: generationType,
		ResourceTier: seed.ResourceTier, MinStake: sdk.NewCoin(seed.MinStake.Denom, sdkmath.NewIntFromUint64(seed.MinStake.Amount)),
		ChallengeOpenWindowBlocks: seed.ChallengeOpenWindow,
		VerificationProfile: shared.VerificationProfile{
			VerificationProfileId: seed.VerificationProfile.VerificationProfileID, JudgmentFunctionVersion: seed.VerificationProfile.JudgmentFunctionVersion,
			VerificationMode: verificationMode, TokenScope: tokenScope, IncludeGeneratedSpecialTokens: seed.VerificationProfile.IncludeGeneratedSpecialTokens,
			IncludePromptTokens: seed.VerificationProfile.IncludePromptTokens, IncludePaddingTokens: seed.VerificationProfile.IncludePaddingTokens,
			RequireOutputTokenIds: seed.VerificationProfile.RequireOutputTokenIDs, RequireFinishReason: seed.VerificationProfile.RequireFinishReason,
			Metrics:                     shared.MetricSpec{CompareLogprobDiff: seed.VerificationProfile.Metrics.CompareLogprobDiff, CompareRankDelta: seed.VerificationProfile.Metrics.CompareRankDelta, CompareTopkJaccard: seed.VerificationProfile.Metrics.CompareTopKJaccard, CompareUnionJs: seed.VerificationProfile.Metrics.CompareUnionJS, ComparedTopK: seed.VerificationProfile.Metrics.ComparedTopK, NumericScale: numericScale},
			CanonicalEncodingVersion:    seed.VerificationProfile.CanonicalEncodingVersion,
			MetricAggregateProofVersion: seed.VerificationProfile.MetricAggregateProofVersion,
			EvidenceSchema: shared.EvidenceSchemaV1{
				SchemaVersion:         seed.VerificationProfile.EvidenceSchema.SchemaVersion,
				RequiredInferEvidence: evidenceRequirements,
			},
		},
		VerificationThresholds: seed.VerificationThresholds,
		BatchVerification:      shared.BatchVerification{Enabled: seed.BatchVerification.Enabled, MinSampleCount: seed.BatchVerification.MinSampleCount, MinValidSampleCount: seed.BatchVerification.MinValidSampleCount, PassMinSamplePassRatioBps: seed.BatchVerification.PassMinSamplePassRatioBps, RejectMinSampleRejectRatioBps: seed.BatchVerification.RejectMinSampleRejectRatioBps},
		PricingProfile: shared.PricingProfile{
			InitialOutputPrice: seed.PricingProfile.InitialOutputPrice,
			VerifyRatioBps:     seed.PricingProfile.VerifyRatioBps,
			MinOrderValue:      seed.PricingProfile.MinOrderValue,
		},
		TimeoutBootstrapProfile: shared.TimeoutBootstrapProfile{InferTimeoutBootstrapBlocks: seed.TimeoutBootstrapProfile.InferTimeoutBootstrapBlocks, VerifyTimeoutBootstrapBlocks: seed.TimeoutBootstrapProfile.VerifyTimeoutBootstrapBlocks, CommitTimeoutBootstrapBlocks: seed.TimeoutBootstrapProfile.CommitTimeoutBootstrapBlocks, BootstrapValidUntilEpoch: seed.TimeoutBootstrapProfile.BootstrapValidUntilEpoch},
		SchemaHash:              schemaHash, PreviousProfileVersion: seed.PreviousProfileVersion,
		RegistrationFee: sdk.NewCoin(seed.RegistrationFee.Denom, sdkmath.NewIntFromUint64(seed.RegistrationFee.Amount)),
	}
	recomputedEvidenceSchemaHash, err := hubtypes.EvidenceSchemaHash(projection)
	if err != nil {
		return shared.ModelProfileProjection{}, err
	}
	projection.VerificationProfile.EvidenceSchemaHash = recomputedEvidenceSchemaHash
	return projection, nil
}

func appendMissingSupports(hub *hubtypes.GenesisState, seed genesisSeed, height uint64) (map[string]bool, error) {
	affectedProfiles := map[string]bool{}
	profiles := make(map[string]int, len(hub.Profiles))
	for i, profile := range hub.Profiles {
		profiles[genesisProfileKey(profile.ModelId, profile.ProfileVersion)] = i
	}
	bonds := make(map[string]hubtypes.ServiceBondState, len(hub.ServiceBonds))
	for _, bond := range hub.ServiceBonds {
		bonds[bond.OperatorAddress] = bond
	}
	capabilities := make(map[string]bool, len(hub.ProfileCapabilities))
	for _, state := range hub.ProfileCapabilities {
		capabilities[state.OperatorAddress+"\x00"+genesisProfileKey(state.ModelId, state.ProfileVersion)] = true
	}
	supports := make(map[string]bool, len(hub.ModelSupports))
	for _, state := range hub.ModelSupports {
		supports[state.OperatorAddress+"\x00"+genesisProfileKey(state.ModelId, state.ProfileVersion)] = true
	}
	dailySupports := make(map[string]bool, len(hub.DailySupports))
	for _, state := range hub.DailySupports {
		dailySupports[state.OperatorAddress] = true
	}
	for _, node := range seed.CortexNodes {
		bond, exists := bonds[node.OperatorAddress]
		if !exists {
			return nil, fmt.Errorf("cortex node %s has no service bond", node.OperatorAddress)
		}
		profileRefs := make([]hubtypes.ProfileKeyV1, 0, len(node.SupportedProfiles))
		for _, supportSeed := range node.SupportedProfiles {
			profileKey := genesisProfileKey(supportSeed.ModelID, supportSeed.ProfileVersion)
			profileIndex, exists := profiles[profileKey]
			if !exists {
				return nil, fmt.Errorf("support %s/%s/%d references missing profile", node.OperatorAddress, supportSeed.ModelID, supportSeed.ProfileVersion)
			}
			key := node.OperatorAddress + "\x00" + profileKey
			present := 0
			if capabilities[key] {
				present++
			}
			if supports[key] {
				present++
			}
			if present == 2 {
				profileRefs = append(profileRefs, hubtypes.ProfileKeyV1{ModelId: supportSeed.ModelID, ProfileVersion: supportSeed.ProfileVersion})
				continue
			}
			if present != 0 {
				return nil, fmt.Errorf("support %s is partially materialized in existing genesis", key)
			}
			profile := &hub.Profiles[profileIndex]
			if bond.ActiveBond < profile.MinStake {
				return nil, fmt.Errorf("cortex node %s bond is below profile %s/%d min_stake", node.OperatorAddress, profile.ModelId, profile.ProfileVersion)
			}
			stakeSnapshot, err := genesisSupportStakeWeight(bond.ActiveBond, profile.MinStake, uint64(hub.Params.Support.ActiveSupportStakeCapMultiplier))
			if err != nil {
				return nil, err
			}
			activeStake := uint64(0)
			activationKind := hubtypes.ModelSupportActivationNone
			var firstTaskID []byte
			if supportSeed.Active {
				profile.ActiveSupporterCount++
				profile.ActiveSupportStake, err = checkedGenesisAdd(profile.ActiveSupportStake, stakeSnapshot)
				if err != nil {
					return nil, err
				}
				activeStake = stakeSnapshot
				activationKind = hubtypes.ModelSupportActivationVerifierAssignedValid
				digest := sha256.Sum256([]byte("genesis-support/" + key))
				firstTaskID = digest[:]
			}
			profile.EligibleSupportStake, err = checkedGenesisAdd(profile.EligibleSupportStake, stakeSnapshot)
			if err != nil {
				return nil, err
			}
			hub.ProfileCapabilities = append(hub.ProfileCapabilities, hubtypes.ProfileCapabilityState{
				OperatorAddress: node.OperatorAddress, ModelId: supportSeed.ModelID,
				ProfileVersion: supportSeed.ProfileVersion, InferenceCapability: true, VerificationCapability: true,
				CapabilityVersion: 1,
			})
			hub.ModelSupports = append(hub.ModelSupports, hubtypes.ModelSupportState{
				OperatorAddress: node.OperatorAddress, ModelId: supportSeed.ModelID,
				ProfileVersion: supportSeed.ProfileVersion, DeclaredSupport: true,
				SupportActive: supportSeed.Active, SupportVersion: 1, FirstSupportTaskId: firstTaskID,
				SupportFreshUntilEpoch: seed.SupportUntilEpoch, ActiveSupportStakeSnapshot: activeStake,
				LastRefreshHeight: height, ActivationKind: activationKind, LastRefreshTaskId: firstTaskID,
				EligibleSupportStakeSnapshot: stakeSnapshot, FirstActivationDuty: shared.DutyVerifier,
			})
			profileRefs = append(profileRefs, hubtypes.ProfileKeyV1{ModelId: supportSeed.ModelID, ProfileVersion: supportSeed.ProfileVersion})
			capabilities[key], supports[key] = true, true
			affectedProfiles[profileKey] = true
		}
		if len(profileRefs) > 0 && !dailySupports[node.OperatorAddress] {
			sort.Slice(profileRefs, func(i, j int) bool {
				if profileRefs[i].ModelId != profileRefs[j].ModelId {
					return profileRefs[i].ModelId < profileRefs[j].ModelId
				}
				return profileRefs[i].ProfileVersion < profileRefs[j].ProfileVersion
			})
			signatureDigest := sha256.Sum256([]byte("genesis-daily-support/" + node.OperatorAddress))
			epoch := height / hub.Params.Epoch.EpochLengthBlocks
			profilesHash, err := hubtypes.CanonicalSupportedProfilesHashV1(profileRefs)
			if err != nil {
				return nil, err
			}
			hub.DailySupports = append(hub.DailySupports, hubtypes.DailySupportState{
				Epoch: epoch, OperatorAddress: node.OperatorAddress,
				SupportedProfilesHash: profilesHash,
				SignatureDigest:       signatureDigest[:], AcceptedHeight: height,
			})
			dailySupports[node.OperatorAddress] = true
		}
	}
	return affectedProfiles, nil
}

func reconcileGenesisModelStatuses(hub *hubtypes.GenesisState, affectedProfiles map[string]bool, height uint64) error {
	affectedModels := make(map[string]bool, len(affectedProfiles))
	for key := range affectedProfiles {
		modelID, _, _ := strings.Cut(key, "\x00")
		affectedModels[modelID] = true
	}
	activeProfiles := make(map[string]uint64)
	eligibleStakeByProfile := make(map[string]uint64)
	for _, support := range hub.ModelSupports {
		if support.DeclaredSupport {
			key := genesisProfileKey(support.ModelId, support.ProfileVersion)
			var err error
			eligibleStakeByProfile[key], err = checkedGenesisAdd(eligibleStakeByProfile[key], support.EligibleSupportStakeSnapshot)
			if err != nil {
				return fmt.Errorf("profile %s eligible support stake overflow: %w", key, err)
			}
		}
	}
	for i := range hub.Profiles {
		profile := &hub.Profiles[i]
		profileKey := genesisProfileKey(profile.ModelId, profile.ProfileVersion)
		if !affectedModels[profile.ModelId] {
			continue
		}
		if profile.StatusSource == hubtypes.ProfileStatusSourceUnspecified {
			profile.StatusSource = hubtypes.ProfileStatusSourceAutoSupport
		}
		if affectedProfiles[profileKey] &&
			profile.StatusSource == hubtypes.ProfileStatusSourceAutoSupport &&
			profile.Status != hubtypes.ModelStatusFrozen &&
			profile.Status != hubtypes.ModelStatusEmergencyFrozen &&
			profile.Status != hubtypes.ModelStatusDelisted {
			leftHi, leftLo := bits.Mul64(profile.ActiveSupportStake, uint64(hub.Params.Support.ActiveSupportStakeRatioDenominator))
			eligibleStake := eligibleStakeByProfile[profileKey]
			profile.EligibleSupportStake = eligibleStake
			rightHi, rightLo := bits.Mul64(eligibleStake, uint64(hub.Params.Support.ActiveSupportStakeRatioNumerator))
			stakeThresholdMet := eligibleStake > 0 &&
				(leftHi > rightHi || leftHi == rightHi && leftLo >= rightLo)
			if profile.ActiveSupporterCount >= hub.Params.Support.ActiveSupporterMinCount && stakeThresholdMet {
				profile.Status = hubtypes.ModelStatusActive
			} else {
				profile.Status = hubtypes.ModelStatusRegistered
			}
			profile.UpdatedHeight = height
		}
		if profile.Status == hubtypes.ModelStatusActive &&
			profile.StatusSource == hubtypes.ProfileStatusSourceAutoSupport {
			activeProfiles[profile.ModelId]++
		}
	}
	for i := range hub.Models {
		model := &hub.Models[i]
		if !affectedModels[model.ModelId] {
			continue
		}
		model.ActiveProfileCount = uint32(activeProfiles[model.ModelId])
		if model.StatusSource == hubtypes.ModelStatusSourceUnspecified {
			model.StatusSource = hubtypes.ModelStatusSourceAutoProfile
		}
		if model.StatusSource == hubtypes.ModelStatusSourceAutoProfile &&
			model.Status != hubtypes.ModelStatusFrozen &&
			model.Status != hubtypes.ModelStatusEmergencyFrozen &&
			model.Status != hubtypes.ModelStatusDelisted {
			if model.ActiveProfileCount > 0 {
				model.Status = hubtypes.ModelStatusActive
			} else {
				model.Status = hubtypes.ModelStatusRegistered
			}
			model.UpdatedHeight = height
		}
	}
	return nil
}

func genesisSupportStakeWeight(activeBond, minStake, capMultiplier uint64) (uint64, error) {
	if minStake != 0 && capMultiplier > math.MaxUint64/minStake {
		return 0, errors.New("support stake cap overflow")
	}
	capAmount := minStake * capMultiplier
	if activeBond < capAmount {
		return activeBond, nil
	}
	return capAmount, nil
}

func checkedGenesisAdd(left, right uint64) (uint64, error) {
	if math.MaxUint64-left < right {
		return 0, errors.New("genesis support aggregate overflow")
	}
	return left + right, nil
}
