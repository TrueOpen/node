package types_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestValidateModelIDAcceptsNATSAndURIPathSafeIDs(t *testing.T) {
	for _, modelID := range []string{
		"a",
		"model-1",
		"model_1",
		"9model",
		"a" + strings.Repeat("b", 127),
	} {
		require.NoError(t, shared.ValidateModelID(modelID), modelID)
	}
}

func TestValidateModelIDRejectsUnsafeOrNonCanonicalIDs(t *testing.T) {
	for _, modelID := range []string{
		"",
		"Model-1",
		"-model",
		"_model",
		"model/id",
		"model.id",
		"model?id",
		"model#id",
		"model%2Fid",
		"model id",
		"model\\id",
		"\u6a21\u578b",
		"a" + strings.Repeat("b", 128),
	} {
		require.ErrorContains(t, shared.ValidateModelID(modelID), "model_id must match", modelID)
	}
}
