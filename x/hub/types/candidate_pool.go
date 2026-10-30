package types

import (
	"fmt"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	CandidateWeightScale        = uint64(1_000_000)
	CandidateStakeCapMultiplier = uint64(3)
)

// ValidateWorkerWeightPolicy checks retained scoring facts independently of live params.
// A never-activated READY snapshot may expire without acquiring a policy.
func (s CandidatePoolSnapshotState) ValidateWorkerWeightPolicy() error {
	if s.Status == CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_READY && s.PublishedHeight != 0 {
		return fmt.Errorf("READY candidate snapshot has an activation height")
	}
	if s.PublishedHeight == 0 {
		if s.Status == CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_ACTIVE || s.WorkerStakeWeightPpm != 0 || s.WorkerPerformanceWeightPpm != 0 {
			return fmt.Errorf("unactivated candidate snapshot has an invalid Worker policy")
		}
		return nil
	}
	return (shared.WorkerCandidateWeightPolicy{
		StakeWeightPPM:       s.WorkerStakeWeightPpm,
		PerformanceWeightPPM: s.WorkerPerformanceWeightPpm,
	}).Validate()
}
