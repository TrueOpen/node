package node_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"
)

// A Bech32 checksum covers the HRP, so renaming a prefix in place leaves a
// string that decodes to nothing. That is what happened during the SingaXYZ ->
// TrueOpen migration: addresses were text-replaced from `singa1...` to
// `trueopen1...` without re-encoding, and five of them shipped that way. The
// wire repository hit the same defect in a published fixture and now rejects it
// in verify-fixtures; this is the equivalent guard for the sources here.
//
// It is deliberately a repository-wide scan rather than a check on one package:
// the addresses that went stale sat in test fixtures nobody decodes, which is
// exactly where a targeted test would not have looked. An address that is only
// ever used as an opaque map key is still worth keeping valid, because the next
// reader cannot tell it apart from one the chain will decode.
var bech32Pattern = regexp.MustCompile(`\btrueopen[a-z]*1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{20,}\b`)

func TestEveryBech32AddressInTheSourcesHasAValidChecksum(t *testing.T) {
	root, err := repositoryRoot()
	require.NoError(t, err)

	files, err := trackedFiles(root)
	require.NoError(t, err)

	type bad struct{ file, addr string }
	var failures []bad
	seen := map[string]bool{}

	for _, rel := range files {
		// The vendored descriptor is binary and the registry and fixtures are
		// reproduced from the pinned wire release, so a finding in them belongs
		// to that release rather than to this repository.
		if strings.HasPrefix(rel, "wire/") || strings.HasPrefix(rel, "registry/") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		for _, addr := range bech32Pattern.FindAllString(string(body), -1) {
			if _, _, err := bech32.DecodeAndConvert(addr); err != nil {
				failures = append(failures, bad{rel, addr})
			}
			seen[addr] = true
		}
	}

	require.NotEmpty(t, seen, "the scan found no addresses at all, so it is not proving anything")
	for _, f := range failures {
		t.Errorf("%s: %s does not decode; re-encode it for its HRP rather than editing the prefix as text", f.file, f.addr)
	}
}

func repositoryRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func trackedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}
