package contracttest

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/internal/testutil/sessionfixture"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestTaskOrderHashMatchesPublishedWire(t *testing.T) {
	order := wireCoreTaskOrder(t)
	digest, err := tasktypes.TaskOrderHash(order)
	require.NoError(t, err)
	require.Equal(t, "057fee505c662a2480239af6ff4925b4ccce4b2f0caac392c6d91a6132a0ba6e", hex.EncodeToString(digest[:]))
}

func wireCoreTaskOrder(t *testing.T) tasktypes.TaskOrderV3 {
	return sessionfixture.CoreOrder(t)
}
