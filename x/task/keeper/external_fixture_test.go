package keeper_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"sort"
	"testing"

	storetypes "cosmossdk.io/store/types"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/keeper"
	module "github.com/TrueOpen/node/x/task/module"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

type fixture struct {
	ctx    context.Context
	keeper keeper.Keeper
}

func initFixture(t *testing.T) *fixture {
	t.Helper()

	encCfg := moduletestutil.MakeTestEncodingConfig(module.AppModule{})
	addressCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	storeKey := storetypes.NewKVStoreKey(tasktypes.StoreKey)
	storeService := runtime.NewKVStoreService(storeKey)
	transientKey := storetypes.NewTransientStoreKey("transient_test")
	transientStoreService := runtime.NewTransientStoreService(transientKey)
	ctx := testutil.DefaultContextWithDB(t, storeKey, transientKey).Ctx
	authority := authtypes.NewModuleAddress(tasktypes.GovModuleName)

	k := keeper.NewKeeper(storeService, transientStoreService, encCfg.Codec, addressCodec, authority, taskExternalAuthKeeper{}, taskExternalBankKeeper{}, stubHubKeeper{})
	// Every registered hash binds chain_id (§1.3 rule 4), so the test context
	// must carry one or the canonical helpers correctly refuse to hash.
	return &fixture{ctx: sdk.WrapSDKContext(ctx.WithChainID("trueopen-test-1")), keeper: k}
}

// Canonical bech32 addresses: the API contract rule 4 / §2.2 step 3
// require every
// address field to survive the address codec, and the frozen
// TRUEOPEN_SELECTED_TASK_BUILDERS_V1 preimage uses the codec bytes.
var (
	genesisUser      = sdk.AccAddress(bytes.Repeat([]byte{0xa1}, 20)).String()
	genesisWorker    = sdk.AccAddress(bytes.Repeat([]byte{0xa2}, 20)).String()
	genesisBuilder   = sdk.AccAddress(bytes.Repeat([]byte{0xa3}, 20)).String()
	genesisVerifier  = sdk.AccAddress(bytes.Repeat([]byte{0xa4}, 20)).String()
	genesisVerifier2 = sdk.AccAddress(bytes.Repeat([]byte{0xa5}, 20)).String()
	genesisVerifier3 = sdk.AccAddress(bytes.Repeat([]byte{0xa6}, 20)).String()
)

func repeatByte(b byte) []byte { return bytes.Repeat([]byte{b}, tasktypes.Hash32Len) }

func taskGenesisV1(t *testing.T, chainID string) *tasktypes.GenesisState {
	t.Helper()

	genesis := tasktypes.DefaultGenesis()
	sessionID := repeatByte(0x11)
	taskID := repeatByte(0x22)
	poolSnapshotID := repeatByte(0x33)
	poolHash := repeatByte(0x44)
	unionBitmapHash := repeatByte(0x55)
	legalSetHash := repeatByte(0x66)
	builderSetHash := repeatByte(0x77)
	anchorHash := repeatByte(0x88)

	genesis.SessionNonces = []tasktypes.SessionNonceState{{UserAddress: genesisUser, NextSessionNonce: 1}}
	genesis.Streams = []tasktypes.StreamState{{
		SessionId:            sessionID,
		OwnerUserAddress:     genesisUser,
		NextExpectedSequence: 2,
		LastActiveHeight:     5,
		OpenPendingCount:     1,
		Status:               tasktypes.SessionStatus_SESSION_STATUS_ACTIVE,
	}}
	genesis.OrderSequences = []tasktypes.OrderSequenceState{{
		SessionId:      sessionID,
		OrderSequence:  1,
		Status:         tasktypes.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CONSUMED,
		TaskId:         taskID,
		ConsumedHeight: 5,
	}}
	genesis.TaskCores = []tasktypes.TaskCoreState{{
		TaskId:                           taskID,
		UserAddress:                      genesisUser,
		SessionId:                        sessionID,
		OrderSequence:                    1,
		AcceptedTaskHash:                 repeatByte(0x99),
		AcceptedInputHash:                repeatByte(0xaa),
		AcceptedOrderOpeningHash:         repeatByte(0xbb),
		ModelId:                          "model-a",
		ProfileVersion:                   1,
		TaskType:                         shared.TaskType_TASK_TYPE_TEXT_GENERATION,
		OrderValue:                       shared.NewAmount(800),
		TaskPhase:                        tasktypes.TaskPhase_TASK_PHASE_WORKER_ASSIGNED,
		AssignmentStatus:                 tasktypes.AssignmentStatus_ASSIGNMENT_STATUS_WORKER_ASSIGNED,
		ReceiptStatus:                    tasktypes.ReceiptStatus_RECEIPT_STATUS_NONE,
		VerificationStatus:               tasktypes.VerificationStatus_VERIFICATION_STATUS_NONE,
		SettlementStatus:                 tasktypes.SettlementStatus_SETTLEMENT_STATUS_NONE,
		FinalityStatus:                   shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_PENDING,
		CreatedHeight:                    5,
		UpdatedHeight:                    6,
		EvidenceRetentionBlocksSnapshot:  tasktypes.DefaultMaxEvidenceRetentionBlocks,
		ObjectiveForgerySlashBpsSnapshot: types.DefaultObjectiveForgerySlashBps,
	}}
	genesis.TaskAssignments = []tasktypes.TaskAssignmentState{{
		TaskId:                     taskID,
		AssignAcceptHeight:         5,
		AssignmentRandomnessHeight: 6,
		WinnerConfirmHeight:        6,
		InferDeadlineHeight:        46,
		InferTimeoutBlocks:         40,
		CandidatePoolSnapshotId:    poolSnapshotID,
		CandidatePoolHash:          poolHash,
		AssignmentCandidateSetHash: legalSetHash,
		WinnerWorker:               genesisWorker,
		WinnerDrawDigest:           repeatByte(0xcc),
		GenerationParamsDigest:     repeatByte(0xce),
	}}
	genesis.TaskBudgets = []tasktypes.TaskBudgetState{{
		TaskId:                 taskID,
		FeeRuleVersion:         1,
		OriginalReservedAmount: shared.NewAmount(1000),
		ReservedAmount:         shared.NewAmount(1000),
		TxFeeReserveRemaining:  shared.NewAmount(100),
		GasReimbursedTotal:     shared.NewAmount(0),
		BudgetStatus:           tasktypes.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED,
		// The frozen §10.1 identities hold on these numbers:
		// worker_max = mulDiv(640, 1_000_000, 1e6) = 640,
		// verify_max = mulDiv(640, 2500, 1e4) = 160, order_value = 800 = TaskCoreState.OrderValue,
		// and order_value + tx_fee_reserve = 900 <= max_fee = 1000.
		PriceBid:                         shared.NewAmount(1_000_000),
		WorkerMax:                        shared.NewAmount(640),
		VerifyMax:                        shared.NewAmount(160),
		VerifyRatioBpsSnapshot:           2500,
		MaxOutputTokens:                  640,
		MaintenanceRateBpsSnapshot:       500,
		FeePolicyVersionSnapshot:         1,
		MaxReimbursableFeePerGasSnapshot: tasktypes.FeePerGasRateV1{Numerator: 1, Denominator: 1},
		MaxReimbursementPerTxSnapshot:    shared.NewAmount(50),
		MaxReimbursementPerTaskSnapshot:  shared.NewAmount(100),
	}}
	genesis.TaskStageHandraiseUnions = []tasktypes.TaskStageHandraiseUnionState{{
		SchemaVersion:           1,
		TaskId:                  taskID,
		Stage:                   tasktypes.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK,
		CandidatePoolSnapshotId: poolSnapshotID,
		CandidatePoolHash:       poolHash,
		UnionCount:              1,
		AcceptedProposalCount:   1,
		Status:                  tasktypes.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZED,
		WindowCloseHeight:       6,
		UnionBitmapHash:         unionBitmapHash,
	}}
	genesis.TaskCandidateFacts = []tasktypes.TaskCandidateFactState{{
		SchemaVersion:                  1,
		TaskId:                         taskID,
		Stage:                          tasktypes.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK,
		Slot:                           7,
		SlotVersion:                    1,
		OperatorAddress:                genesisWorker,
		Duty:                           shared.Duty_DUTY_WORKER,
		ActiveBondSnapshot:             shared.NewAmount(1_000_000),
		AvailableBondSnapshot:          shared.NewAmount(900_000),
		RequiredTaskLiabilitySnapshot:  shared.NewAmount(30_000),
		MinStakeSnapshot:               shared.NewAmount(10_000),
		PerformanceScoreSnapshotPpm:    uint32(types.PerformanceScoreDefaultPpm),
		PerformanceMethodVersion:       uint32(types.PerformanceScoreMethodVersionV1),
		CandidateJailFactorSnapshotPpm: 1_000_000,
		BondVersionSnapshot:            1,
		CapabilityVersionSnapshot:      1,
		SupportVersionSnapshot:         1,
		CandidateWeight:                1_000_000,
		HandraiseSigningDigest:         repeatByte(0xdd),
	}}
	genesis.BuilderStageProposals = []tasktypes.BuilderStageProposalState{{
		SchemaVersion:    1,
		TaskId:           taskID,
		Stage:            tasktypes.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK,
		ProposalDigest:   repeatByte(0xee),
		ProposerOperator: genesisBuilder,
		AcceptedHeight:   5,
		NewMemberCount:   1,
	}}
	genesis.AssignmentCandidateSets = []tasktypes.AssignmentCandidateSetState{{
		SchemaVersion:              1,
		TaskId:                     taskID,
		TaskHash:                   repeatByte(0x99),
		CandidatePoolSnapshotId:    poolSnapshotID,
		CandidatePoolHash:          poolHash,
		UnionBitmapHash:            unionBitmapHash,
		CandidateCount:             1,
		AssignmentCandidateSetHash: legalSetHash,
	}}

	builders := []string{genesisBuilder}
	genesis.TaskBuilderSelections = []tasktypes.TaskBuilderSelectionState{{
		TaskId:                   taskID,
		SessionAnchorBlockHash:   anchorHash,
		BuilderSetId:             "1",
		BuilderSetHash:           builderSetHash,
		SelectedTaskBuilders:     builders,
		SelectedTaskBuilderCount: 1,
		SelectedTaskBuildersHash: expectedSelectedTaskBuildersHash(t, chainID, taskID, "1", builderSetHash, builders),
		CreatedHeight:            5,
		BodyStatus:               shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE,
	}}
	return genesis
}

func expectedSelectedTaskBuildersHash(t *testing.T, chainID string, taskID []byte, builderSetID string, builderSetHash []byte, builders []string) []byte {
	t.Helper()
	elements := make([][]byte, 0, len(builders)+1)
	elements = append(elements, shared.Uint32BE(uint32(len(builders))))
	for _, builder := range builders {
		operator, err := tasktypes.CanonicalOperatorAddressBytes("selected_task_builder", builder)
		require.NoError(t, err)
		elements = append(elements, operator)
	}
	return shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainSelectedTaskBuildersV1),
		[]byte(chainID), taskID, []byte(builderSetID), builderSetHash,
		shared.CanonicalFrameBytes(elements...),
	)
}

func genesisChainID(f *fixture) string { return sdk.UnwrapSDKContext(f.ctx).ChainID() }

func verifierReadyGenesisV1(t *testing.T, chainID string) *tasktypes.GenesisState {
	t.Helper()
	genesis := verifierSourceGenesisV1(t, chainID)
	window := &genesis.VerifierCandidateWindows[0]
	randomness := repeatByte(0x41)
	window.XWindowRandomnessBeacon = &tasktypes.VerifierCandidateWindowState_WindowRandomnessBeacon{WindowRandomnessBeacon: randomness}
	window.Status = tasktypes.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY
	window.GeneratedHeight = window.WindowRandomnessHeight
	type rankedCandidate struct {
		slot          uint32
		operator      string
		operatorBytes []byte
		rank          []byte
	}
	ranked := []rankedCandidate{{slot: 3, operator: genesisVerifier}, {slot: 4, operator: genesisVerifier2}, {slot: 5, operator: genesisVerifier3}}
	for i := range ranked {
		operatorBytes, err := tasktypes.CanonicalOperatorAddressBytes("verifier", ranked[i].operator)
		require.NoError(t, err)
		ranked[i].operatorBytes = operatorBytes
		ranked[i].rank = shared.CanonicalHashBytes(
			shared.MustDomain(shared.DomainVerifierWindowRankV1),
			[]byte(chainID), window.TaskId, shared.Uint32BE(window.VerifyRound), window.InferReceiptHash,
			window.CandidatePoolSnapshotId, window.CandidatePoolHash, window.VerifierWindowSourceHash,
			shared.Uint64BE(window.WindowRandomnessHeight), randomness, shared.Uint32BE(ranked[i].slot), shared.Uint64BE(1), operatorBytes,
		)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if comparison := bytes.Compare(ranked[i].rank, ranked[j].rank); comparison != 0 {
			return comparison < 0
		}
		return ranked[i].slot < ranked[j].slot
	})
	windowFields := [][]byte{
		[]byte(chainID), window.TaskId, shared.Uint32BE(window.VerifyRound), window.InferReceiptHash,
		window.CandidatePoolSnapshotId, window.CandidatePoolHash, window.VerifierWindowSourceHash,
		shared.Uint32BE(3), shared.Uint64BE(window.WindowRandomnessHeight), randomness, shared.Uint32BE(3),
	}
	windowElements := [][]byte{shared.Uint32BE(uint32(len(ranked)))}
	for i, candidate := range ranked {
		genesis.VerifierCandidateWindowMembers = append(genesis.VerifierCandidateWindowMembers, tasktypes.VerifierCandidateWindowMemberState{
			SchemaVersion: 1, TaskId: window.TaskId, VerifyRound: tasktypes.VerifyRoundV1, RankIndex: uint32(i), Slot: candidate.slot,
			SlotVersion: 1, OperatorAddress: candidate.operator, VerifierWindowRank: candidate.rank,
		})
		windowElements = append(windowElements, shared.CanonicalFrameBytes(
			shared.Uint32BE(uint32(i)), shared.Uint32BE(candidate.slot), shared.Uint64BE(1), candidate.operatorBytes, candidate.rank,
		))
	}
	windowFields = append(windowFields, shared.CanonicalFrameBytes(windowElements...))
	windowHash := shared.CanonicalHashBytes(shared.MustDomain(shared.DomainVerifierWindowV1), windowFields...)
	window.XVerifierWindowHash = &tasktypes.VerifierCandidateWindowState_VerifierWindowHash{VerifierWindowHash: windowHash}
	facts := make([]tasktypes.TaskCandidateFactState, 0, 3)
	for i, operator := range []string{genesisVerifier, genesisVerifier2, genesisVerifier3} {
		facts = append(facts, tasktypes.TaskCandidateFactState{
			SchemaVersion: 1, TaskId: window.TaskId, Stage: tasktypes.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
			Slot: uint32(i + 3), SlotVersion: 1, OperatorAddress: operator, Duty: shared.Duty_DUTY_VERIFIER,
			ActiveBondSnapshot: shared.NewAmount(100), AvailableBondSnapshot: shared.NewAmount(90),
			RequiredTaskLiabilitySnapshot: shared.NewAmount(10), MinStakeSnapshot: shared.NewAmount(20),
			PerformanceScoreSnapshotPpm: tasktypes.PerformanceScoreDefaultPpm, PerformanceMethodVersion: tasktypes.PerformanceMethodRawQ16V1,
			CandidateJailFactorSnapshotPpm: 1_000_000, BondVersionSnapshot: 1, CapabilityVersionSnapshot: 1, SupportVersionSnapshot: 1,
			CandidateWeight: uint32(300_000 + i*200_000), HandraiseSigningDigest: repeatByte(byte(0x42 + i)),
		})
	}
	unionBitmapHash := repeatByte(0x43)
	legalFields := [][]byte{
		[]byte(chainID), window.TaskId, shared.Uint32BE(window.VerifyRound), window.CandidatePoolSnapshotId, window.CandidatePoolHash,
		windowHash, unionBitmapHash, shared.Uint32BE(3),
	}
	legalElements := [][]byte{shared.Uint32BE(uint32(len(facts)))}
	for _, fact := range facts {
		operatorBytes, err := tasktypes.CanonicalOperatorAddressBytes("verifier", fact.OperatorAddress)
		require.NoError(t, err)
		legalElements = append(legalElements, shared.CanonicalFrameBytes(
			shared.Uint32BE(fact.Slot), shared.Uint64BE(1), operatorBytes, shared.Uint32BE(fact.CandidateWeight),
			shared.Uint64BE(100), shared.Uint64BE(90), shared.Uint64BE(10), shared.Uint64BE(20),
			shared.Uint32BE(fact.PerformanceScoreSnapshotPpm), shared.Uint32BE(fact.PerformanceMethodVersion),
			shared.Uint32BE(fact.CandidateJailFactorSnapshotPpm), shared.Uint64BE(1), shared.Uint64BE(1),
		))
	}
	legalFields = append(legalFields, shared.CanonicalFrameBytes(legalElements...))
	legalHash := shared.CanonicalHashBytes(shared.MustDomain(shared.DomainVerifierLegalSetV1), legalFields...)
	selectionBeacon := repeatByte(0x44)
	drawn := verifierDrawOrderV1(t, chainID, window.TaskId, legalHash, window.SelectionRandomnessHeight, selectionBeacon, facts)
	selectedFields := [][]byte{
		[]byte(chainID), window.TaskId, shared.Uint32BE(window.VerifyRound), legalHash,
		shared.Uint64BE(window.SelectionRandomnessHeight), selectionBeacon, shared.Uint32BE(3),
	}
	selected := make([]tasktypes.SelectedVerifierV1, 0, 3)
	selectedElements := [][]byte{shared.Uint32BE(uint32(len(drawn)))}
	for i, fact := range drawn {
		operatorBytes, err := tasktypes.CanonicalOperatorAddressBytes("verifier", fact.OperatorAddress)
		require.NoError(t, err)
		selectedElements = append(selectedElements, shared.CanonicalFrameBytes(
			shared.Uint32BE(uint32(i)), shared.Uint32BE(fact.Slot), shared.Uint64BE(fact.SlotVersion), operatorBytes,
		))
		selected = append(selected, tasktypes.SelectedVerifierV1{OperatorAddress: fact.OperatorAddress, Slot: fact.Slot, SlotVersion: fact.SlotVersion})
	}
	selectedFields = append(selectedFields, shared.CanonicalFrameBytes(selectedElements...))
	selectedHash := shared.CanonicalHashBytes(shared.MustDomain(shared.DomainSelectedVerifiersV1), selectedFields...)
	genesis.TaskStageHandraiseUnions = append(genesis.TaskStageHandraiseUnions, tasktypes.TaskStageHandraiseUnionState{
		SchemaVersion: 1, TaskId: window.TaskId, Stage: tasktypes.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
		CandidatePoolSnapshotId: window.CandidatePoolSnapshotId, CandidatePoolHash: window.CandidatePoolHash,
		UnionCount: 3, Status: tasktypes.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZED,
		WindowCloseHeight: window.HandraiseCloseHeight, UnionBitmapHash: unionBitmapHash,
		XVerifierLegalSetHash:      &tasktypes.TaskStageHandraiseUnionState_VerifierLegalSetHash{VerifierLegalSetHash: legalHash},
		XSelectionRandomnessHeight: &tasktypes.TaskStageHandraiseUnionState_SelectionRandomnessHeight{SelectionRandomnessHeight: window.SelectionRandomnessHeight},
	})
	genesis.TaskCandidateFacts = append(genesis.TaskCandidateFacts, facts...)
	genesis.VerifierAssignments = []tasktypes.VerifierAssignmentState{{
		TaskId: window.TaskId, VerifyRound: tasktypes.VerifyRoundV1, OpenVerifyHeight: 25,
		VerifierCandidateWindowHash: windowHash, VerifierLegalSetHash: legalHash, SelectedVerifiersHash: selectedHash,
		SelectionRandomnessHeight: window.SelectionRandomnessHeight, SelectionRandomnessBeacon: selectionBeacon,
		SelectedVerifiers: selected, SelectedVerifierCount: 3, VerifierHandraiseCount: 3,
		CommitDeadlineHeight: 30, VerifyDeadlineHeight: 40,
	}}
	genesis.TaskCores[0].TaskPhase = tasktypes.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED
	genesis.TaskCores[0].VerificationStatus = tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED
	return genesis
}

func verifierSourceGenesisV1(t *testing.T, chainID string) *tasktypes.GenesisState {
	t.Helper()
	genesis := taskGenesisV1(t, chainID)
	taskID := genesis.TaskCores[0].TaskId
	receiptHash := repeatByte(0x31)
	genesis.TaskCores[0].TaskPhase = tasktypes.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED
	genesis.TaskCores[0].ReceiptStatus = tasktypes.ReceiptStatus_RECEIPT_STATUS_RECEIPT_ACCEPTED
	genesis.TaskCores[0].VerificationStatus = tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING
	genesis.TaskAssignments[0].GenerationParamsDigest = repeatByte(0x32)
	emptyEvidenceHash, err := tasktypes.EvidenceCommitmentsHash(nil)
	require.NoError(t, err)
	genesis.InferReceipts = []tasktypes.InferReceiptState{{
		TaskId: taskID, WinnerWorker: genesisWorker, InferReceiptHash: receiptHash,
		GenerationParamsDigest: repeatByte(0x32), OutputHash: repeatByte(0x34), OutputSizeBytes: 64,
		EvidenceCommitmentsHash: emptyEvidenceHash[:], InferReceiptSigningDigest: receiptHash,
		SignatureDigest: repeatByte(0x36), ExpiryHeight: 100, ReceiptHeight: 10,
		GeneratedTokenCount: 1, OutputLeafCount: 1,
	}}
	window := tasktypes.VerifierCandidateWindowState{
		SchemaVersion: 1, TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1, InferReceiptHash: receiptHash,
		CandidatePoolSnapshotId: genesis.TaskAssignments[0].CandidatePoolSnapshotId,
		CandidatePoolHash:       genesis.TaskAssignments[0].CandidatePoolHash,
		EligibilityFrozenHeight: 10, EligibleCount: 3, EligibilitySegmentCount: 1,
		WindowRandomnessHeight: 15, BuilderProposalCloseHeight: 18, HandraiseCloseHeight: 20,
		SelectionRandomnessHeight: 25, AssignmentDeadlineHeight: 28, WindowSize: 3,
		Status: tasktypes.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN,
	}
	segment := tasktypes.VerifierCandidateEligibilitySegmentState{TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1, SegmentIndex: 0, Bitmap: []byte{0x38}}
	setVerifierSourceHashV1(chainID, &window, segment.Bitmap)
	genesis.VerifierCandidateWindows = []tasktypes.VerifierCandidateWindowState{window}
	genesis.VerifierCandidateEligibilitySegments = []tasktypes.VerifierCandidateEligibilitySegmentState{segment}
	return genesis
}

func verifierDrawOrderV1(t *testing.T, chainID string, taskID, legalHash []byte, height uint64, beacon []byte, facts []tasktypes.TaskCandidateFactState) []tasktypes.TaskCandidateFactState {
	t.Helper()
	remaining := append([]tasktypes.TaskCandidateFactState(nil), facts...)
	sort.Slice(remaining, func(i, j int) bool { return remaining[i].Slot < remaining[j].Slot })
	selected := make([]tasktypes.TaskCandidateFactState, 0, len(remaining))
	var counter uint64
	for len(remaining) != 0 {
		var total uint64
		for _, fact := range remaining {
			total += uint64(fact.CandidateWeight)
		}
		threshold := -total % total
		for attempt := uint64(0); attempt < 64; attempt++ {
			digest := shared.CanonicalHashBytes(
				shared.MustDomain(shared.DomainWeightedDrawV1), []byte(chainID), []byte("VERIFIER_SELECTION"),
				taskID, legalHash, shared.Uint64BE(height), beacon, shared.Uint64BE(counter),
			)
			x := binary.BigEndian.Uint64(digest[:8])
			if x >= threshold {
				target := x % total
				var cumulative uint64
				chosen := -1
				for i, fact := range remaining {
					cumulative += uint64(fact.CandidateWeight)
					if cumulative > target {
						chosen = i
						break
					}
				}
				require.NotEqual(t, -1, chosen)
				selected = append(selected, remaining[chosen])
				remaining = append(remaining[:chosen], remaining[chosen+1:]...)
				require.NotEqual(t, uint64(math.MaxUint64), counter)
				counter++
				break
			}
			counter++
		}
	}
	return selected
}

func setVerifierSourceHashV1(chainID string, window *tasktypes.VerifierCandidateWindowState, bitmap []byte) {
	segments := shared.CanonicalFrameBytes(
		shared.Uint32BE(1),
		shared.CanonicalFrameBytes(shared.Uint32BE(0), bitmap),
	)
	window.VerifierWindowSourceHash = shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainVerifierWindowSourceV1),
		[]byte(chainID), window.TaskId, shared.Uint32BE(window.VerifyRound), window.InferReceiptHash,
		window.CandidatePoolSnapshotId, window.CandidatePoolHash, shared.Uint64BE(window.EligibilityFrozenHeight),
		shared.Uint32BE(window.EligibleCount), shared.Uint32BE(window.EligibilitySegmentCount), segments,
	)
}
