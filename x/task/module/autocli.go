package module

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/TrueOpen/node/x/task/types"
)

// AutoCLIOptions implements the autocli.HasAutoCLIConfig interface for the Task
// module.
//
// Every entry below must name an RPC in the registered public Query/Msg service.
// AutoCLI resolves names at startup and panics on stale entries, so blocked and
// internal-only methods must not be listed.
//
// Selectors are Hash32 values (§1.1: 64-hex on the protobuf/REST boundary), so
// every positional task/session id is a single hex argument, not the deleted
// (session_id, task_id) pair (Ruling 23).
func (am AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service: types.Query_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "Params",
					Use:       "params",
					Short:     "Shows the task module parameters",
				},
				{
					RpcMethod:      "Task",
					Use:            "task [task-id]",
					Short:          "Shows the composite view of one task",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}},
				},
				{
					RpcMethod:      "TaskStage",
					Use:            "task-stage [task-id]",
					Short:          "Shows the stage and next-deadline projection of one task",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}},
				},
				{
					RpcMethod:      "TaskAssignment",
					Use:            "task-assignment [task-id]",
					Short:          "Shows the Worker assignment projection of one task",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}},
				},
				{
					RpcMethod:      "AssignmentRandomness",
					Use:            "assignment-randomness [task-id]",
					Short:          "Shows the frozen assignment randomness and winner draw digest",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}},
				},
				{
					RpcMethod:      "TaskBudget",
					Use:            "task-budget [task-id]",
					Short:          "Shows the authoritative per-task accounting row",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}},
				},
				{
					RpcMethod:      "TaskBuilders",
					Use:            "task-builders [task-id]",
					Short:          "Shows the frozen Task Builder selection of one task",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}},
				},
				{
					RpcMethod:      "InferReceipt",
					Use:            "infer-receipt [task-id]",
					Short:          "Shows one task's accepted inference receipt",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}},
				},
				{
					RpcMethod:      "VerifierCandidateWindow",
					Use:            "verifier-candidate-window [task-id] [verify-round]",
					Short:          "Shows one round's frozen verifier window",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}, {ProtoField: "verify_round"}},
				},
				{
					RpcMethod:      "VerifierAssignment",
					Use:            "verifier-assignment [task-id] [verify-round]",
					Short:          "Shows one frozen verifier assignment",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}, {ProtoField: "verify_round"}},
				},
				{
					RpcMethod:      "VerifyCommit",
					Use:            "verify-commit [task-id] [verify-round] [verifier-operator-address]",
					Short:          "Shows one verifier's commit row",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}, {ProtoField: "verify_round"}, {ProtoField: "verifier_operator_address"}},
				},
				{
					RpcMethod:      "ResultReceipt",
					Use:            "result-receipt [task-id] [verify-round] [verifier-operator-address]",
					Short:          "Shows one verifier's result credential",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}, {ProtoField: "verify_round"}, {ProtoField: "verifier_operator_address"}},
				},
				{
					RpcMethod:      "DataUnavailableReports",
					Use:            "data-unavailable-reports [task-id] [verify-round]",
					Short:          "Pages one round's verifier data-availability reports",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}, {ProtoField: "verify_round"}},
				},
				{
					RpcMethod:      "BuilderDataUnavailable",
					Use:            "builder-data-unavailable [task-id] [verify-round] [builder-operator-address]",
					Short:          "Shows one (task, round, builder) data-availability aggregate",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}, {ProtoField: "verify_round"}, {ProtoField: "builder_operator_address"}},
				},
				{
					RpcMethod:      "WorkerEvidence",
					Use:            "worker-evidence [task-id] [worker-operator-address] [seq]",
					Short:          "Shows one compact Worker objective-evidence receipt",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}, {ProtoField: "worker_operator_address"}, {ProtoField: "seq"}},
				},
				{
					RpcMethod:      "SettlementFacts",
					Use:            "settlement-facts [task-id]",
					Short:          "Shows one task's retained settlement facts",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}},
				},
				{
					RpcMethod:      "Settlement",
					Use:            "settlement [task-id]",
					Short:          "Shows one task's committed settlement and plan",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}},
				},
				{
					RpcMethod:      "VerificationRound",
					Use:            "verification-round [task-id] [verify-round]",
					Short:          "Shows one task verification round",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}, {ProtoField: "verify_round"}},
				},
				{
					RpcMethod:      "TaskGasReimbursements",
					Use:            "task-gas-reimbursements [task-id]",
					Short:          "Pages one task's gas reimbursement receipts",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}},
				},
				{
					RpcMethod:      "TaskFailureClass",
					Use:            "task-failure-class [task-id] [verify-round]",
					Short:          "Shows one task failure classification",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}, {ProtoField: "verify_round"}},
				},
				{
					RpcMethod:      "EvidenceCleanup",
					Use:            "evidence-cleanup [task-id]",
					Short:          "Shows the bounded cleanup progress of one task",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}},
				},
				{
					RpcMethod:      "EpochTaskSummary",
					Use:            "epoch-task-summary [epoch]",
					Short:          "Shows bounded epoch task summary progress or its dispatch receipt",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "epoch"}},
				},
				{
					RpcMethod:      "Session",
					Use:            "session [session-id]",
					Short:          "Shows one stream session",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "session_id"}},
				},
				{
					RpcMethod:      "SessionNonce",
					Use:            "session-nonce [user-address]",
					Short:          "Shows the next session nonce for one user",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "user_address"}},
				},
				{
					RpcMethod:      "SessionsByOwner",
					Use:            "sessions-by-owner [user-address]",
					Short:          "Lists active sessions for one user",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "user_address"}},
				},
				{
					RpcMethod:      "SessionTerminalSummary",
					Use:            "session-terminal-summary [session-id]",
					Short:          "Shows one collapsed session summary",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "session_id"}},
				},
				{
					RpcMethod:      "OrderSequence",
					Use:            "order-sequence [session-id] [order-sequence]",
					Short:          "Shows one order sequence audit row",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "session_id"}, {ProtoField: "order_sequence"}},
				},
				{
					RpcMethod:      "RoleActiveTasks",
					Use:            "role-active-tasks [operator-address] [duty]",
					Short:          "Lists active tasks for one Worker or Verifier",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "operator_address"}, {ProtoField: "duty"}},
				},
			},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service:              types.Msg_serviceDesc.ServiceName,
			EnhanceCustomCommand: true,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "UpdateTaskParams",
					Skip:      true, // authority gated
				},
				{
					RpcMethod: "CreateSession",
					Use:       "create-session",
					Short:     "Create a user-owned session with a chain-generated session id",
				},
				{
					RpcMethod:      "CancelOrder",
					Use:            "cancel-order [session-id] [order-sequence]",
					Short:          "Cancel the next unused order sequence without moving funds",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "session_id"}, {ProtoField: "order_sequence"}},
				},
				{
					// The signed order / handraise payloads are structured, so the
					// command takes them as flags rather than positionals.
					RpcMethod: "SubmitWorkerHandraises",
					Use:       "submit-worker-handraises",
					Short:     "Submit a Task Builder Worker handraise proposal (the single Task admission entry)",
				},
				{
					RpcMethod: "SubmitInferReceipt",
					Use:       "submit-infer-receipt",
					Short:     "Submit the winner Worker's inference receipt",
				},
				{
					RpcMethod: "SubmitVerifierHandraises",
					Use:       "submit-verifier-handraises",
					Short:     "Submit an Open Verify verifier handraise proposal",
				},
				{
					RpcMethod: "ReportDataUnavailable",
					Use:       "report-data-unavailable",
					Short:     "Record one selected Verifier's data-availability report",
				},
				{
					RpcMethod:      "SubmitBuilderEvidence",
					Use:            "submit-builder-evidence [submitter-address]",
					Short:          "Submit one typed objective Builder evidence object",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "submitter_address"}},
				},
				{
					RpcMethod:      "SubmitWorkerEvidence",
					Use:            "submit-worker-evidence [submitter-address]",
					Short:          "Submit one strict objective Worker evidence object",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "submitter_address"}},
				},
				{
					RpcMethod: "SubmitVerifyCommit",
					Use:       "submit-verify-commit",
					Short:     "Submit one verifier commit",
				},
				{
					RpcMethod: "BatchSubmitVerifyCommit",
					Use:       "batch-submit-verify-commit",
					Short:     "Submit a bounded batch of verifier commits",
				},
				{
					RpcMethod: "SubmitVerifyResult",
					Use:       "submit-verify-result",
					Short:     "Submit one verifier result receipt",
				},
				{
					RpcMethod: "BatchSubmitVerifyResult",
					Use:       "batch-submit-verify-result",
					Short:     "Submit a bounded batch of verifier result receipts",
				},
				{
					RpcMethod: "OpenChallengeRound",
					Use:       "open-challenge-round",
					Short:     "Open the funded verification round 2",
				},
				{
					RpcMethod:      "SettleTask",
					Use:            "settle-task [task-id]",
					Short:          "Settle one task through the single ordinary settlement contract",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "task_id"}},
				},
				{
					// §9.4 / §10.14: the single failure and expiry entry point.
					// The locator is a four-branch oneof, so it stays a flag.
					RpcMethod: "SweepDeadline",
					Use:       "sweep-deadline",
					Short:     "Trigger one already-expired deterministic sweep (the only failure/expiry entry)",
				},
			},
		},
	}
}
