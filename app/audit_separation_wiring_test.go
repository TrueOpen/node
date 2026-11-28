package app

import (
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	taskkeeper "github.com/TrueOpen/node/x/task/keeper"
)

// The Hub refuses every audit capability declaration unless the block-producer
// identity lookup is bound. The app binds it by matching the concrete Task
// keeper to the Hub module's required input, so the keeper must keep
// implementing it by value; every test that boots the app proves the binding
// itself resolves.
func TestTaskKeeperProvidesTheConsensusIdentityLookup(t *testing.T) {
	var _ hubtypes.ConsensusIdentityChecker = taskkeeper.Keeper{}
}
