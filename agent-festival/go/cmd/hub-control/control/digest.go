package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/openjiuwen/agent-festival/go/internal/rfc8785"
)

// CanonVersion names the canonicalisation every κ in this module uses. It is
// part of the hashed material, so changing the rule can never make an old and
// a new digest collide silently.
const CanonVersion = "jcs-sha256-v1"

// Digest returns the canonical digest of a JSON-compatible value: RFC 8785
// canonical bytes, SHA-256, prefixed with the canonicalisation version. It is
// always computed here from the material itself; a caller-supplied digest is
// never trusted.
func Digest(v any) (string, []byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", nil, err
	}
	return DigestJSON(raw)
}

// DigestJSON is Digest for raw JSON bytes.
func DigestJSON(raw []byte) (string, []byte, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", nil, refuse(CodeInvalid, "not JSON: %v", err)
	}
	canon, err := rfc8785.Canonicalize(doc)
	if err != nil {
		return "", nil, refuse(CodeInvalid, "canonicalize: %v", err)
	}
	sum := sha256.Sum256(canon)
	return CanonVersion + ":" + hex.EncodeToString(sum[:]), canon, nil
}

// kappa binds material to the canonicalisation version and a purpose label,
// so the same bytes hashed for two different purposes never compare equal.
func kappa(purpose string, material any) (string, error) {
	d, _, err := Digest(map[string]any{"canon": CanonVersion, "purpose": purpose, "material": material})
	return d, err
}

func mustJSON(v any) string {
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshal audit detail: %v", err))
	}
	return string(b)
}
