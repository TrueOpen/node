package app

import (
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	taskkeeper "github.com/TrueOpen/node/x/task/keeper"
)

// The Hub reads the settlement period of the Worker quota and the Worker's
// open obligations from the Task keeper. The app binds it by matching the
// concrete Task keeper to the Hub module's required input, so the keeper must
// keep implementing it by value; every test that boots the app proves the
// binding itself resolves.
func TestTaskKeeperProvidesTheWorkerSettlementView(t *testing.T) {
	var _ hubtypes.WorkerSettlementView = taskkeeper.Keeper{}
}
