package contracttest

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/internal/testutil/sessionfixture"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestTaskOrderHashMatchesWireRC2(t *testing.T) {
	order := wireCoreTaskOrder(t)
	digest, err := tasktypes.TaskOrderHash(order)
	require.NoError(t, err)
	require.Equal(t, "d48c7062a64fcf6932aee1a935d9b0f30a04d954f40caa87f5ccf32a3696d59c", hex.EncodeToString(digest[:]))
}

func wireCoreTaskOrder(t *testing.T) tasktypes.TaskOrderV3 {
	return sessionfixture.CoreOrder(t)
}
