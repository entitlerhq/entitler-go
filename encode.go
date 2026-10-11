package entitler

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"
)

const hexDigits = "0123456789ABCDEF"

// encodeComponent percent-encodes a path segment or query value exactly as JavaScript's
// encodeURIComponent does: only ASCII letters, digits and -_.!~*'() stay as they are.
func encodeComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&15])
	}
	return b.String()
}

func blank(s string) bool {
	return strings.TrimSpace(s) == ""
}

// trimCredential trims spaces, tabs, carriage returns and line feeds, as construction does.
func trimCredential(s string) string {
	return strings.Trim(s, " \t\r\n")
}

// segment checks a path identifier and encodes it as one path segment. Ids are sent exactly as
// given, never trimmed.
func segment(id, blankMessage string) (string, error) {
	if blank(id) {
		return "", argError(blankMessage)
	}
	if strings.Trim(id, ".") == "" {
		return "", argError("pass an id that is not made only of dots")
	}
	if !utf8.ValidString(id) {
		return "", argError("pass an id that is valid Unicode")
	}
	return encodeComponent(id), nil
}

// queryValue checks a query value and encodes it.
func queryValue(v string) (string, error) {
	if !utf8.ValidString(v) {
		return "", argError("pass an id that is valid Unicode")
	}
	return encodeComponent(v), nil
}

const (
	keyMessage     = "pass idempotencyKey as 1 to 200 printable ASCII characters"
	holdKeyMessage = "pass idempotencyKey as 1 to 193 printable ASCII characters"
)

// validKey reports whether an idempotency key is 1 to max printable ASCII characters that neither
// starts nor ends with a space.
func validKey(key string, limit int) bool {
	if len(key) == 0 || len(key) > limit || key[0] == ' ' || key[len(key)-1] == ' ' {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			return false
		}
	}
	return true
}

// optionalKey answers the caller's key, checked, or a new UUID v4 when none is given.
func optionalKey(key string) (string, error) {
	if key == "" {
		return newUUID(), nil
	}
	if !validKey(key, 200) {
		return "", argError(keyMessage)
	}
	return key, nil
}

// requiredKey checks a key the method requires. Go cannot tell a missing string from an empty
// one, so an empty key fails the length rule.
func requiredKey(key string, limit int) (string, error) {
	if !validKey(key, limit) {
		if limit == 193 {
			return "", argError(holdKeyMessage)
		}
		return "", argError(keyMessage)
	}
	return key, nil
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// serialise writes strings, whole numbers, booleans, nil and slices of them as JavaScript's
// JSON.stringify does: no whitespace, and escaping only the characters JSON requires, so / and
// non-ASCII characters stay as they are.
func serialise(v any) string {
	var b strings.Builder
	writeJSON(&b, v)
	return b.String()
}

func writeJSON(b *strings.Builder, v any) {
	switch v := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
	case string:
		writeString(b, v)
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, item)
		}
		b.WriteByte(']')
	}
}

func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte(hexLower[r>>4])
			b.WriteByte(hexLower[r&15])
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

const hexLower = "0123456789abcdef"

// marshalBody encodes a request body without HTML escaping.
func marshalBody(v any) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}
