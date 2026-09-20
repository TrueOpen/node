// Command servicekeyproof produces the service key material a Builder or Cortex
// registration transaction needs.
//
// `noded tx hub register-builder` takes a service pubkey and a
// service_key_proof: a proof-of-possession signature over a canonical digest
// binding the chain, the participant role, the operator address, the service
// pubkey and the key nonce. There is no CLI command that produces it and it
// cannot be assembled in shell, which makes live registration hard to test.
//
// This tool builds that digest with the SAME production helpers the chain
// verifies against (shared.CanonicalHashBytes over the
// TRUEOPEN_SERVICE_REGISTRATION_V1 domain, then a strict secp256k1 signature), and
// self-verifies the result before printing, so it can never emit a proof the
// chain would reject.
//
// It is a TEST/OPS tool: the service key it generates is printed in the clear.
// Never use a key produced here on a real network.
//
// Usage:
//
//	go run ./scripts/servicekeyproof --chain-id trueopen-localnet-1 --operator trueopen1... --generate
//	go run ./scripts/servicekeyproof --chain-id ... --operator ... --service-privkey-hex <64 hex>
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		chainID     = flag.String("chain-id", "", "chain id the registration tx will be broadcast to (required)")
		operator    = flag.String("operator", "", "operator address in bech32, e.g. trueopen1... (required)")
		participant = flag.String("participant", "builder", "participant role: builder | cortex")
		privHex     = flag.String("service-privkey-hex", "", "existing service private key, 64 hex chars")
		generate    = flag.Bool("generate", false, "generate a fresh service key and print it (dev only)")
		nonce       = flag.Uint64("nonce", 1, "service key nonce; registration uses 1")
		uri         = flag.String("endpoint-uri", "http://127.0.0.1:8080", "endpoint uri for the sample descriptor")
	)
	flag.Parse()

	if strings.TrimSpace(*chainID) == "" || strings.TrimSpace(*operator) == "" {
		flag.Usage()
		return fmt.Errorf("--chain-id and --operator are required")
	}

	ptype, endpointKind, err := participantOf(*participant)
	if err != nil {
		return err
	}

	operatorBytes, err := sdk.GetFromBech32(*operator, hubPrefixOf(*operator))
	if err != nil {
		return fmt.Errorf("--operator is not a valid bech32 address: %w", err)
	}

	priv, err := serviceKey(*privHex, *generate)
	if err != nil {
		return err
	}
	pub, ok := priv.PubKey().(*secp256k1.PubKey)
	if !ok {
		return fmt.Errorf("unexpected service pubkey type %T", priv.PubKey())
	}

	digest := registrationDigest(*chainID, ptype, operatorBytes, pub.Key, *nonce)

	proof, err := hubtypes.SignSecp256k1DigestForTest(priv, digest)
	if err != nil {
		return fmt.Errorf("signing the registration digest: %w", err)
	}
	// Refuse to emit anything the chain would reject: this is the same verifier
	// x/hub/keeper verifyServiceKeyProof calls.
	if err := hubtypes.VerifyStrictSecp256k1Digest(pub, digest, proof); err != nil {
		return fmt.Errorf("self-check failed, refusing to emit an invalid proof: %w", err)
	}
	proofHex := hex.EncodeToString(proof)

	descriptor, err := json.Marshal(map[string]any{
		"endpoints": []map[string]any{{
			"endpoint_kind":    endpointKind,
			"uri":              *uri,
			"protocol_version": "1",
		}},
	})
	if err != nil {
		return err
	}

	pubHex := hex.EncodeToString(pub.Key)
	fmt.Printf("chain_id          = %s\n", *chainID)
	fmt.Printf("participant       = %s\n", ptype)
	fmt.Printf("operator          = %s\n", *operator)
	fmt.Printf("nonce             = %d\n", *nonce)
	if *generate {
		fmt.Printf("service_privkey   = %s   # DEV ONLY - keep out of real networks\n",
			hex.EncodeToString(priv.Key))
	}
	fmt.Printf("service_pubkey    = %s\n", pubHex)
	fmt.Printf("service_key_proof = %s\n", proofHex)
	fmt.Printf("descriptor        = %s\n", descriptor)
	if ptype == shared.ParticipantType_PARTICIPANT_TYPE_BUILDER {
		fmt.Printf("\n# ready to paste (fill in --from / --home / --keyring-backend):\n")
		fmt.Printf("noded tx hub register-builder %s %s %s \\\n  --descriptor '%s' \\\n"+
			"  --chain-id %s --fees 0uusdc --gas auto --gas-adjustment 1.5 -y --from <key>\n",
			*operator, pubHex, proofHex, descriptor, *chainID)
	}
	return nil
}

// registrationDigest builds the proof-of-possession preimage digest exactly as
// x/hub/keeper RegisterBuilder (and service_provider_runtime for Cortex)
// does. Every argument is bound into the preimage; main_test.go pins the result
// and asserts each field changes it, so a framing change upstream cannot
// silently leave this tool emitting proofs the chain rejects.
func registrationDigest(
	chainID string,
	participant shared.ParticipantType,
	operatorBytes, servicePubkey []byte,
	nonce uint64,
) []byte {
	return shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainServiceRegistrationV1),
		[]byte(chainID),
		shared.EnumBE(uint32(participant)),
		operatorBytes,
		servicePubkey,
		shared.Uint64BE(nonce),
	)
}

func participantOf(name string) (shared.ParticipantType, string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "builder":
		return shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
			"SERVICE_ENDPOINT_KIND_NEXUS_GRPC", nil
	case "cortex":
		return shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
			"SERVICE_ENDPOINT_KIND_OBJECT_GATEWAY_HTTPS", nil
	default:
		return 0, "", fmt.Errorf("--participant must be builder or cortex, got %q", name)
	}
}

// hubPrefixOf returns the bech32 hrp of an address so the tool works on any
// prefix without hardcoding "trueopen".
func hubPrefixOf(addr string) string {
	if i := strings.LastIndex(addr, "1"); i > 0 {
		return addr[:i]
	}
	return "trueopen"
}

func serviceKey(privHex string, generate bool) (*secp256k1.PrivKey, error) {
	privHex = strings.TrimSpace(privHex)
	switch {
	case privHex != "" && generate:
		return nil, fmt.Errorf("use either --service-privkey-hex or --generate, not both")
	case privHex != "":
		raw, err := hex.DecodeString(privHex)
		if err != nil {
			return nil, fmt.Errorf("--service-privkey-hex must be hex: %w", err)
		}
		if len(raw) != 32 {
			return nil, fmt.Errorf("--service-privkey-hex must be 32 bytes (64 hex chars), got %d", len(raw))
		}
		return &secp256k1.PrivKey{Key: raw}, nil
	case generate:
		return secp256k1.GenPrivKey(), nil
	default:
		return nil, fmt.Errorf("pass --generate to create a service key, or --service-privkey-hex to use one")
	}
}
