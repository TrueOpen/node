package module

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisterModelProfileCommandUsesCanonicalProfileFile(t *testing.T) {
	command, _, err := AppModule{}.GetTxCmd().Find([]string{"register-model-profile"})
	require.NoError(t, err)
	require.NotNil(t, command.Flags().Lookup(profileFileFlag))
	require.Nil(t, command.Flags().Lookup("profile"))
	require.Nil(t, command.Flags().Lookup("registrant-signature"))
	require.NoError(t, command.Args(command, nil))
}
