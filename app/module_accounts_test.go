package app

import (
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestTrueOpenModuleAccountsAreRegisteredAndBlocked(t *testing.T) {
	perms := GetMaccPerms()
	blocked := BlockedAddresses()

	cases := []struct {
		name        string
		wantBlocked bool
		wantNoPerms bool
	}{
		// hub-owned accounts.
		{name: hubtypes.ModuleName, wantBlocked: true, wantNoPerms: true},
		{name: hubtypes.RewardsModuleName, wantBlocked: true, wantNoPerms: true},
		{name: hubtypes.EmissionModuleName, wantBlocked: true, wantNoPerms: true},
		{name: hubtypes.TreasuryModuleName, wantBlocked: true, wantNoPerms: true},
		{name: hubtypes.ServiceBondModuleName, wantBlocked: true, wantNoPerms: true},
		// task-owned accounts.
		{name: tasktypes.ModuleName, wantBlocked: true, wantNoPerms: true},
		{name: tasktypes.EscrowModuleName, wantBlocked: true, wantNoPerms: true},
		{name: tasktypes.ChallengeEffectModuleName, wantBlocked: true, wantNoPerms: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPerms, ok := perms[tc.name]
			if !ok {
				t.Fatalf("module account %q is not registered", tc.name)
			}
			if tc.wantNoPerms && len(gotPerms) != 0 {
				t.Fatalf("module account %q permissions = %v, want none", tc.name, gotPerms)
			}
			if blocked[tc.name] != tc.wantBlocked {
				t.Fatalf("blocked[%q] = %v, want %v", tc.name, blocked[tc.name], tc.wantBlocked)
			}
		})
	}
}
