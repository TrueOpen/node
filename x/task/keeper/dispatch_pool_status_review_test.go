package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

func TestDispatchPreflightRequiresActivatedEpochPool(t *testing.T) {
	for _, poolStatus := range []hubtypes.CandidatePoolSnapshotStatus{
		hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_READY,
		hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_ACTIVE,
		hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_EXPIRED,
		hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_PRUNED,
	} {
		t.Run(poolStatus.String(), func(t *testing.T) {
			f := newDispatchFixture(t)
			entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
			f.hub.pool.Status = poolStatus
			_, err := f.keeper.validateDispatchVrf(f.ctx, dispatchOrderAdmission{order: entry.SignedOrder.Order, taskID: [32]byte(entry.Record.TaskId)}, entry.WinnerTicket)
			if poolStatus == hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_ACTIVE || poolStatus == hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_EXPIRED {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "referenced epoch pool is unavailable")
			}
		})
	}
}
