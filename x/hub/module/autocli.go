package module

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/TrueOpen/node/x/hub/types"
)

// AutoCLI resolves every RpcMethod against the live gRPC service descriptor and
// panics at command-tree construction on an unknown name. Keep this explicit
// command catalog aligned with the registered public service; blocked and
// internal-only methods must not be listed.

// AutoCLIOptions implements the autocli.HasAutoCLIConfig interface for the Hub module.
func (am AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service: types.Query_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "Params",
					Use:       "params",
					Short:     "Shows the hub module parameters",
				},
				{
					RpcMethod: "Beacon",
					Use:       "beacon",
					Short:     "Shows the recorded BeaconState at a given block height",
					FlagOptions: map[string]*autocliv1.FlagOptions{
						"height": {Name: "beacon-height"},
					},
				},
				{
					// Rename the state selector so it cannot collide with the SDK's
					// global historical-query --height flag.
					//
					// The request selects by a oneof. autocli's binder has no notion
					// of one and writes every field it has a value for, so the two
					// selectors overwrote each other; keepOnlyRequestedOneof in
					// cmd/noded/cmd/query_optional.go keeps the one the operator
					// actually typed.
					RpcMethod: "BuilderSet",
					Use:       "builder-set",
					Short:     "Shows the builder set selected by exactly one selector",
					FlagOptions: map[string]*autocliv1.FlagOptions{
						"height": {Name: "builder-set-height"},
					},
				},
				{
					RpcMethod:      "Builder",
					Use:            "builder [builder-address]",
					Short:          "Shows one builder identity",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "builder_address"}},
				},
				{
					// No positional args: the registry walk has no selector, and
					// paging is driven by the generated --page.* flags.
					RpcMethod: "Builders",
					Use:       "builders",
					Short:     "Lists the builder registry in canonical key order",
				},
				{
					// No positional args: the bridge is a chain-wide singleton.
					RpcMethod: "BridgeStatus",
					Use:       "bridge-status",
					Short:     "Shows the canonical USDC bridge route, limits, supply and invariants",
				},
				{
					RpcMethod:      "VrfKey",
					Use:            "vrf-key [operator-address]",
					Short:          "Shows a validator's active and pending VRF keys",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}},
				},
				{
					RpcMethod:      "ValidatorBridgeSigner",
					Use:            "validator-bridge-signer [operator-address]",
					Short:          "Shows a validator's current bridge signer binding",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}},
				},
				{
					RpcMethod:      "Earnings",
					Use:            "earnings [address]",
					Short:          "Shows claimable earnings for an address",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "address"}},
				},
				{
					RpcMethod:      "Model",
					Use:            "model [model-id]",
					Short:          "Shows a registered model",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "model_id"}},
				},
				{
					// No positional args: the registry walk has no selector, and
					// paging is driven by the generated --page.* flags.
					RpcMethod: "Models",
					Use:       "models",
					Short:     "Lists the model registry in canonical key order",
				},
				{
					RpcMethod:      "Profile",
					Use:            "profile [model-id] [profile-version]",
					Short:          "Shows a registered model profile",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "model_id"}, {ProtoField: "profile_version"}},
				},
				{
					RpcMethod:      "CortexNode",
					Use:            "cortex-node [operator-address]",
					Short:          "Shows one stable Cortex Node identity",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}},
				},
				{
					RpcMethod:      "ServiceBond",
					Use:            "service-bond [operator-address]",
					Short:          "Shows one node-level service bond",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}},
				},
				{
					RpcMethod:      "ServiceUnbondings",
					Use:            "service-unbondings [operator-address] [status]",
					Short:          "Lists one operator's unbondings by lifecycle status",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}, {ProtoField: "status"}},
				},
				{
					RpcMethod:      "CurrentServiceKey",
					Use:            "current-service-key [participant-type] [operator-address]",
					Short:          "Shows the current service key",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "participant_type"}, {ProtoField: "operator_address"}},
				},
				{
					RpcMethod:      "ServiceDescriptor",
					Use:            "service-descriptor [participant-type] [operator-address]",
					Short:          "Shows one versioned off-chain service descriptor commitment",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "participant_type"}, {ProtoField: "operator_address"}},
				},
				{
					RpcMethod:      "ServiceLifecycle",
					Use:            "service-lifecycle [operator-address]",
					Short:          "Shows one operator's identity and bond lifecycle",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}},
				},
				{
					RpcMethod:      "ProfileCapability",
					Use:            "profile-capability [operator-address] [model-id] [profile-version]",
					Short:          "Shows one operator's profile capability",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}, {ProtoField: "model_id"}, {ProtoField: "profile_version"}},
				},
				{
					RpcMethod:      "ModelSupport",
					Use:            "model-support [operator-address] [model-id] [profile-version]",
					Short:          "Shows one provider model/profile ModelSupport P30 ledger row",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}, {ProtoField: "model_id"}, {ProtoField: "profile_version"}},
				},
				{
					RpcMethod:      "DailySupport",
					Use:            "daily-support [operator-address] [epoch]",
					Short:          "Shows one retained daily support row",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}, {ProtoField: "epoch"}},
				},
				{
					RpcMethod: "CurrentCandidatePool",
					Use:       "current-candidate-pool",
					Short:     "Shows the current Hub-derived WORKER candidate pool",
				},
				{
					RpcMethod:      "CandidatePoolSnapshot",
					Use:            "candidate-pool-snapshot [snapshot-id]",
					Short:          "Shows one retained candidate pool snapshot",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "snapshot_id"}},
				},
				{
					RpcMethod:      "CandidatePoolMember",
					Use:            "candidate-pool-member [snapshot-id] [candidate-slot]",
					Short:          "Shows one stable candidate slot",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "snapshot_id"}, {ProtoField: "candidate_slot"}},
				},
				{
					RpcMethod:      "CandidatePoolMembers",
					Use:            "candidate-pool-members [snapshot-id]",
					Short:          "Lists one candidate pool snapshot's members",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "snapshot_id"}},
				},
				{
					RpcMethod:      "CompetitionEpoch",
					Use:            "competition-epoch [reward-bucket] [epoch]",
					Short:          "Shows the CompetitionGate histogram snapshot for a reward bucket + epoch",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "reward_bucket"}, {ProtoField: "epoch"}},
				},
				{
					RpcMethod:      "RewardEpochCursor",
					Use:            "reward-epoch-cursor [epoch] [reward-bucket]",
					Short:          "Shows the reward epoch cursor state for an epoch + bucket",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "epoch"}, {ProtoField: "reward_bucket"}},
				},
				{
					RpcMethod:      "RewardEpochAudit",
					Use:            "reward-epoch-audit [source-epoch] [reward-bucket]",
					Short:          "Shows the immutable audit root retained after reward epoch pruning",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "source_epoch"}, {ProtoField: "reward_bucket"}},
				},
				{
					RpcMethod: "Treasury",
					Use:       "treasury",
					Short:     "Shows the authoritative treasury ledger",
				},
				{
					RpcMethod:      "Fault",
					Use:            "fault [fault-id]",
					Short:          "Shows one retained role-fault receipt",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "fault_id"}},
				},
				{
					RpcMethod:      "FreezeSignal",
					Use:            "freeze-signal [freeze-signal-id]",
					Short:          "Shows one retained freeze signal",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "freeze_signal_id"}},
				},
				{
					RpcMethod:      "FreezeSignals",
					Use:            "freeze-signals [model-id] [profile-version] [status]",
					Short:          "Lists freeze signals for a profile",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "model_id"}, {ProtoField: "profile_version"}, {ProtoField: "status"}},
				},
				{
					RpcMethod:      "EmergencyFreezeVotes",
					Use:            "emergency-freeze-votes [freeze-signal-id]",
					Short:          "Lists validator votes for one freeze signal",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "freeze_signal_id"}},
				},
				{
					RpcMethod:      "TimeoutBucket",
					Use:            "timeout-bucket [bucket-key]",
					Short:          "Shows the current or exact-version timeout bucket state",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "bucket_key"}},
				},
			},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service:              types.Msg_serviceDesc.ServiceName,
			EnhanceCustomCommand: true,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "UpdateHubParams",
					Skip:      true, // authority gated
				},
				{
					RpcMethod: "SetModelStatus",
					Skip:      true, // authority gated
				},
				{
					RpcMethod: "SetProfileStatus",
					Skip:      true, // authority gated
				},
				{
					RpcMethod:      "ClaimEarnings",
					Use:            "claim-earnings [signer-address] [claim-class]",
					Short:          "Withdraw a claimable earnings entry for the signer",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "signer_address"}, {ProtoField: "claim_class"}},
				},
				{
					RpcMethod:      "RegisterBuilder",
					Use:            "register-builder [builder-operator-address] [service-pubkey] [service-key-proof]",
					Short:          "Registers a builder identity, service key, and descriptor",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "builder_operator_address"}, {ProtoField: "service_pubkey"}, {ProtoField: "service_key_proof"}},
				},
				{
					RpcMethod:      "RunBuilderTerm",
					Use:            "run-builder-term [target-term] [max-items] [submitter-address]",
					Short:          "Advances one due Builder term under a bounded item cap",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "target_term"}, {ProtoField: "max_items"}, {ProtoField: "submitter_address"}},
				},
				{
					RpcMethod:      "RegisterModelProfile",
					Use:            "register-model-profile [proposer-address]",
					Short:          "Registers an atomic model profile projection",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "proposer_address"}},
				},
				{
					RpcMethod:      "StakeService",
					Use:            "stake-service [operator-address]",
					Short:          "Creates or tops up a stable Cortex Node bond",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}},
				},
				{
					RpcMethod:      "BeginServiceUnstake",
					Use:            "begin-service-unstake [operator-address] [amount]",
					Short:          "Begin a slashable service-bond unbonding entry",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}, {ProtoField: "amount"}},
				},
				{
					RpcMethod:      "WithdrawServiceUnbonded",
					Use:            "withdraw-service-unbonded [operator-address]",
					Short:          "Release a matured service unbonding entry",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}},
				},
				{
					RpcMethod:      "RotateServiceKey",
					Use:            "rotate-service-key [operator-address] [participant-type] [new-service-pubkey] [expected-current-service-authorization-nonce] [new-service-key-proof]",
					Short:          "Atomically replaces the current service key",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}, {ProtoField: "participant_type"}, {ProtoField: "new_service_pubkey"}, {ProtoField: "expected_current_service_authorization_nonce"}, {ProtoField: "new_service_key_proof"}},
				},
				{
					RpcMethod:      "RevokeServiceKey",
					Use:            "revoke-service-key [operator-address] [participant-type] [expected-current-service-authorization-nonce] [reason-code]",
					Short:          "Revokes the current service key",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}, {ProtoField: "participant_type"}, {ProtoField: "expected_current_service_authorization_nonce"}, {ProtoField: "reason_code"}},
				},
				{
					RpcMethod:      "UpdateServiceDescriptor",
					Use:            "update-service-descriptor [operator-address] [participant-type] [expected-descriptor-version]",
					Short:          "Writes a new versioned service descriptor commitment",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}, {ProtoField: "participant_type"}, {ProtoField: "expected_descriptor_version"}},
				},
				{
					RpcMethod:      "DeclareModelSupport",
					Use:            "declare-model-support [operator-address] [model-id] [profile-version] [inference-capability] [verification-capability]",
					Short:          "Declare provisional support for one profile",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}, {ProtoField: "model_id"}, {ProtoField: "profile_version"}, {ProtoField: "inference_capability"}, {ProtoField: "verification_capability"}},
				},
				{
					RpcMethod: "RegisterVrfKey",
					Use:       "register-vrf-key",
					Short:     "Registers or rotates the validator's independent VRF key",
				},
				{
					RpcMethod:      "SubmitFreezeSignal",
					Use:            "submit-freeze-signal [submitter-address] [model-id] [profile-version]",
					Short:          "Submit an objective freeze signal for a profile",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "submitter_address"}, {ProtoField: "model_id"}, {ProtoField: "profile_version"}},
				},
				{
					RpcMethod:      "EmergencyFreezeVote",
					Use:            "emergency-freeze-vote [validator-address] [freeze-signal-id] [vote]",
					Short:          "Vote on an emergency freeze signal",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "validator_address"}, {ProtoField: "freeze_signal_id"}, {ProtoField: "vote"}},
				},
			},
		},
	}
}
