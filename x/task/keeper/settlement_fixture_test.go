package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// settlementApplyHubStub is the Hub double shared by the task-reference release
// tests. It records the release counters those tests assert on and otherwise
// defers to internalStubHubKeeper; the Wire v0.3 cutover removed the settlement
// apply/finality surface the original fixture wrapped, so nothing beyond the
// release accounting survives here.
type settlementApplyHubStub struct {
	internalStubHubKeeper
	profile                       hubtypes.ProfileStateSnapshot
	liabilityReleases             uint32
	serviceResponsibilityReleases uint32
	roleFaults                    []hubtypes.TaskRoleFaultFact
}

// ApplyTaskRoleFault records the applied fault vector and otherwise behaves
// exactly like the embedded stub, so settlement tests can assert which verifiers
// a given settlement height faults.
func (s *settlementApplyHubStub) ApplyTaskRoleFault(_ context.Context, fact hubtypes.TaskRoleFaultFact) (hubtypes.RoleFaultState, error) {
	s.roleFaults = append(s.roleFaults, fact)
	return stubAppliedRoleFault(fact), nil
}

func (s *settlementApplyHubStub) GetHubParams(sdk.Context) hubtypes.HubParamsSnapshot {
	params := hubtypes.DefaultHubParams()
	return hubtypes.HubParamsSnapshot{
		EpochLengthBlocks:                 params.Epoch.EpochLengthBlocks,
		SettlementBuilderGraceBlocks:      params.Builder.SettlementBuilderGraceBlocks,
		FreezeFailureIndexRetentionBlocks: params.Freeze.FreezeFailureIndexRetentionBlocks,
	}
}

func (s *settlementApplyHubStub) GetProfileState(sdk.Context, string, uint32) (hubtypes.ProfileStateSnapshot, bool) {
	return s.profile, true
}

func (s *settlementApplyHubStub) ReleaseTaskLiabilities(context.Context, string, string, uint64) error {
	s.liabilityReleases++
	return nil
}

func (s *settlementApplyHubStub) ReleaseServiceKeyResponsibilities(context.Context, string, string) error {
	s.serviceResponsibilityReleases++
	return nil
}
