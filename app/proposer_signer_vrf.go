package app

// File-backed VRF ProposerSigner.
//
// the sampling protocol: the beacon uses a **separate VRF
// hot key**, kept apart from the consensus signing key, whose public key is
// registered on chain under the stable operator address .
// explains why the consensus key cannot be reused: ECVRF Prove needs
// `gamma = x * hash_to_curve(pk, alpha)`, while tmkms/HSM only exposes the
// fixed-base `R = r*B` and `S = r + H(...)*a`, so that primitive is out of
// reach. The private key is therefore read straight from
// `<home>/config/vrf_key.json`, independently of priv_validator_key.json; the
// exposure of one key does not affect the other.
//
// File format (0600 permissions, both fields hex):
//
//	{
//	  "vrf_pubkey":  "<32-byte hex>",
//	  "vrf_privkey": "<64-byte ed25519 seed||pub hex>"
//	}
//
// The private key never leaves this file; only Prove / VrfPubKey are exposed.
// A remote-signing scheme (should an ECVRF-capable HSM ever appear) only needs
// another ProposerSigner implementation, with no change to PrepareProposal.

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	nodeante "github.com/TrueOpen/node/app/ante"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// VrfKeyFileName is the fixed file name of the VRF hot key under
// `<home>/config/`.
const VrfKeyFileName = "vrf_key.json"

// vrfKeyFile is the on-disk representation of vrf_key.json.
type vrfKeyFile struct {
	VrfPubkey  string `json:"vrf_pubkey"`
	VrfPrivkey string `json:"vrf_privkey"`
}

// FileVrfProposerSigner is the default ProposerSigner implementation, backed by
// the VRF hot key on disk. It is safe for concurrent use once loaded.
type FileVrfProposerSigner struct {
	priv ed25519.PrivateKey
	pub  []byte
}

// LoadFileVrfProposerSigner reads vrf_key.json and returns a ProposerSigner.
//
// Deliberately strict: every inconsistency returns an error rather than being
// patched up in place:
//   - the file does not exist / cannot be read
//   - hex parsing fails, or a length is wrong
//   - the public key disagrees with the one derived from the private key (which
//     means the file was hand-edited or concatenated wrongly)
//
// The caller (wireBeaconHooks) decides whether a failure is fail-fast or
// degrades to a nil signer.
func LoadFileVrfProposerSigner(keyFilePath string) (*FileVrfProposerSigner, error) {
	if strings.TrimSpace(keyFilePath) == "" {
		return nil, errors.New("vrf_key.json path is empty")
	}
	raw, err := os.ReadFile(keyFilePath)
	if err != nil {
		return nil, fmt.Errorf("vrf_key.json not readable at %q: %w", keyFilePath, err)
	}
	var file vrfKeyFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse %q: %w", keyFilePath, err)
	}

	privBytes, err := hex.DecodeString(strings.TrimSpace(file.VrfPrivkey))
	if err != nil {
		return nil, fmt.Errorf("decode vrf_privkey in %q: %w", keyFilePath, err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("vrf_privkey in %q has wrong length: got %d, want %d",
			keyFilePath, len(privBytes), ed25519.PrivateKeySize)
	}
	pubBytes, err := hex.DecodeString(strings.TrimSpace(file.VrfPubkey))
	if err != nil {
		return nil, fmt.Errorf("decode vrf_pubkey in %q: %w", keyFilePath, err)
	}
	if len(pubBytes) != hubtypes.VrfPubkeyLen {
		return nil, fmt.Errorf("vrf_pubkey in %q has wrong length: got %d, want %d",
			keyFilePath, len(pubBytes), hubtypes.VrfPubkeyLen)
	}

	priv := ed25519.PrivateKey(privBytes)
	derived, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("vrf_privkey in %q did not yield an ed25519 public key", keyFilePath)
	}
	if !bytes.Equal(derived, pubBytes) {
		return nil, fmt.Errorf("vrf_pubkey in %q does not match the key derived from vrf_privkey", keyFilePath)
	}

	return &FileVrfProposerSigner{
		priv: priv,
		pub:  append([]byte(nil), pubBytes...),
	}, nil
}

// SaveVrfKeyFile generates a new VRF hot key, writes it to keyFilePath with
// 0600 permissions, and returns the public key. An existing file of the same
// name is an outright error — overwriting the hot key would instantly
// invalidate the public key already registered on chain, so operators must go
// through the MsgRegisterVrfKey rotation flow explicitly.
func SaveVrfKeyFile(keyFilePath string) ([]byte, error) {
	if strings.TrimSpace(keyFilePath) == "" {
		return nil, errors.New("vrf_key.json path is empty")
	}
	if _, err := os.Stat(keyFilePath); err == nil {
		return nil, fmt.Errorf("refusing to overwrite existing VRF key at %q", keyFilePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat %q: %w", keyFilePath, err)
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, fmt.Errorf("generate vrf key: %w", err)
	}
	blob, err := json.MarshalIndent(vrfKeyFile{
		VrfPubkey:  hex.EncodeToString(pub),
		VrfPrivkey: hex.EncodeToString(priv),
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode vrf key: %w", err)
	}
	if err := os.WriteFile(keyFilePath, append(blob, '\n'), 0o600); err != nil {
		return nil, fmt.Errorf("write %q: %w", keyFilePath, err)
	}
	return append([]byte(nil), pub...), nil
}

// Prove implements ProposerSigner.
func (s *FileVrfProposerSigner) Prove(input []byte) (proof, randomness []byte, err error) {
	return nodeante.Prove(s.priv, input)
}

// VrfPubKey implements ProposerSigner, returning the 32-byte VRF public key.
func (s *FileVrfProposerSigner) VrfPubKey() ([]byte, error) {
	if len(s.pub) != hubtypes.VrfPubkeyLen {
		return nil, fmt.Errorf("unexpected vrf pubkey length: got %d, want %d", len(s.pub), hubtypes.VrfPubkeyLen)
	}
	return append([]byte(nil), s.pub...), nil
}
