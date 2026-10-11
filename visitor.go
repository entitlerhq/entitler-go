package entitler

import (
	"crypto/rand"
	"encoding/base64"
)

// VisitorIDPattern is the pattern every visitor id matches: 16 to 64 letters, digits, hyphens or underscores.
const VisitorIDPattern = `^[A-Za-z0-9_-]{16,64}$`

// NewVisitorID answers a new visitor id: 24 cryptographically random bytes, base64url without
// padding (32 characters). A server keeps one for a signed-out person, for example in a
// first-party cookie, and passes it to pricing reads and to Register at sign-up, so the person sees
// the same experiment arm before and after signing up.
func NewVisitorID() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func checkVisitor(v string) error {
	ok := len(v) >= 16 && len(v) <= 64
	for i := 0; ok && i < len(v); i++ {
		c := v[i]
		ok = c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-'
	}
	if !ok {
		return argError("pass visitor as an id of 16 to 64 letters, numbers, hyphens or underscores")
	}
	return nil
}
