package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/wire/bus"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// canonicalBuilderEvidence is the Hub-local execution form of the shared fact.
// It contains no Bus envelope, payload, raw signature, or Task-owned authority
// material. IDs are always recomputed from the local chain ID.
type canonicalBuilderEvidence struct {
	BuilderOperator string
	Kind            shared.BuilderEvidenceKind
	ScopeID         []byte
	CanonicalDigest []byte
	EvidenceID      []byte
	FaultID         []byte
	// FrozenSlashBps is the only Task-owned economic input on this boundary. It
	// is deliberately outside evidence_id and fault_id: the same fault must keep
	// one identity regardless of the rate, so a replay carrying a different rate
	// is a conflict rather than a second fault.
	FrozenSlashBps uint32
}

func (k Keeper) canonicalBuilderEvidenceFact(chainID string, fact shared.BuilderObjectiveEvidenceFactV2) (canonicalBuilderEvidence, error) {
	if chainID == "" || fact.SchemaVersion != 2 {
		return canonicalBuilderEvidence{}, fmt.Errorf("chain_id and builder objective evidence fact schema_version=2 are required")
	}
	_, builder, err := k.requireCanonicalAddress("builder_operator", fact.BuilderOperator)
	if err != nil {
		return canonicalBuilderEvidence{}, err
	}
	if builder != fact.BuilderOperator {
		return canonicalBuilderEvidence{}, fmt.Errorf("builder_operator is not canonical")
	}
	if len(fact.ScopeId) != 32 || len(fact.CanonicalEvidenceDigest) != 32 {
		return canonicalBuilderEvidence{}, fmt.Errorf("scope_id and canonical_evidence_digest must be raw32")
	}
	// The Task module froze this rate and already bounded it, but the Hub does
	// not assume a cross-module read: the fault kernel is the side that spends
	// the bond.
	if fact.FrozenSlashBps > types.BuilderFaultSlashBasisPointsMaximum {
		return canonicalBuilderEvidence{}, fmt.Errorf("frozen_slash_bps must be <= %d, got %d",
			types.BuilderFaultSlashBasisPointsMaximum, fact.FrozenSlashBps)
	}

	var content [32]byte
	copy(content[:], fact.CanonicalEvidenceDigest)
	evidenceID, err := bus.EvidenceID(chainID, builder, int32(fact.EvidenceKind), fact.ScopeId, content)
	if err != nil {
		return canonicalBuilderEvidence{}, err
	}
	faultID, err := bus.FaultID(chainID, builder, int32(fact.EvidenceKind), fact.ScopeId, content)
	if err != nil {
		return canonicalBuilderEvidence{}, err
	}
	return canonicalBuilderEvidence{
		BuilderOperator: builder,
		Kind:            fact.EvidenceKind,
		ScopeID:         append([]byte(nil), fact.ScopeId...),
		CanonicalDigest: append([]byte(nil), fact.CanonicalEvidenceDigest...),
		EvidenceID:      append([]byte(nil), evidenceID[:]...),
		FaultID:         append([]byte(nil), faultID[:]...),
		FrozenSlashBps:  fact.FrozenSlashBps,
	}, nil
}

// ApplyBuilderObjectiveEvidence is the Task -> Hub fault-kernel boundary fixed
// by the keeper contract section 5.5. Task has already verified the exact Bus bytes,
// proof key, signature, payload scope, and Task authority before this call.
func (k Keeper) ApplyBuilderObjectiveEvidence(ctx context.Context, fact shared.BuilderObjectiveEvidenceFactV2) (shared.BuilderObjectiveEvidenceReceiptV2, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)

	evidence, err := k.canonicalBuilderEvidenceFact(sdkCtx.ChainID(), fact)
	if err != nil {
		return shared.BuilderObjectiveEvidenceReceiptV2{}, err
	}
	if existing, replay, err := k.builderEvidenceReplay(cache, evidence); err != nil {
		return shared.BuilderObjectiveEvidenceReceiptV2{}, err
	} else if replay {
		return shared.BuilderObjectiveEvidenceReceiptV2{
			EvidenceId: append([]byte(nil), evidence.EvidenceID...),
			XFaultId: &shared.BuilderObjectiveEvidenceReceiptV2_FaultId{
				FaultId: append([]byte(nil), existing.FaultId...),
			},
			Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP,
		}, nil
	}

	// The rate is never caller-authored: the Task module froze it when the Task
	// first locked its Builders and carried it on the shared fact, so a later
	// governance change cannot re-price an already-accepted duty.
	_, _, applied, err := k.submitBuilderEvidenceFault(cache, evidence, sdkContextHeight(cacheCtx))
	if err != nil {
		return shared.BuilderObjectiveEvidenceReceiptV2{}, err
	}
	if !applied {
		return shared.BuilderObjectiveEvidenceReceiptV2{}, fmt.Errorf("builder evidence fault was not applied and was not an exact replay")
	}
	mustEmitHubEvent(cache, &types.EventBuilderEvidenceAccepted{
		EvidenceId:      append([]byte(nil), evidence.EvidenceID...),
		BuilderOperator: evidence.BuilderOperator,
		EvidenceKind:    evidence.Kind,
		XFaultIdOrEmpty: &types.EventBuilderEvidenceAccepted_FaultIdOrEmpty{
			FaultIdOrEmpty: append([]byte(nil), evidence.FaultID...),
		},
	})
	commit()
	return shared.BuilderObjectiveEvidenceReceiptV2{
		EvidenceId: append([]byte(nil), evidence.EvidenceID...),
		XFaultId: &shared.BuilderObjectiveEvidenceReceiptV2_FaultId{
			FaultId: append([]byte(nil), evidence.FaultID...),
		},
		Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}
