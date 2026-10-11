package entitler

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"time"
)

// SnapshotKey is one public key that signs snapshots (a JSON Web Key).
type SnapshotKey struct {
	// Kty is the key type, EC.
	Kty string
	// Crv is the curve, P-256.
	Crv string
	// X is the public point's x coordinate, base64url.
	X string
	// Y is the public point's y coordinate, base64url.
	Y string
	// Kid is the key's id.
	Kid string
	// Alg is the algorithm, ES256.
	Alg string
	// Use is the key's use, sig.
	Use string

	raw json.RawMessage
}

// SnapshotKeys is the key set that verifies snapshots. It encodes as the JSON key set Entitler
// sends, each key's members in Entitler's order, and decodes from it with json.Unmarshal, so an
// app can ship the keys `go tool entitler snapshot-keys` writes, for example with //go:embed.
type SnapshotKeys struct {
	// Keys are the keys.
	Keys []SnapshotKey
}

func decodeSnapshotKeys(o object) SnapshotKeys {
	keys := SnapshotKeys{Keys: []SnapshotKey{}}
	o.at("keys").list(func(n node) {
		k := n.object()
		key := SnapshotKey{
			Kty: k.at("kty").str(), Crv: k.at("crv").str(), X: k.at("x").str(), Y: k.at("y").str(),
			Kid: k.at("kid").str(), Alg: k.at("alg").str(), Use: k.at("use").str(),
		}
		keys.Keys = append(keys.Keys, key)
	})
	return keys
}

// MarshalJSON writes the key set as Entitler sent it.
func (k SnapshotKeys) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`{"keys":[`)
	for i, key := range k.Keys {
		if i > 0 {
			b.WriteByte(',')
		}
		if key.raw != nil {
			b.Write(key.raw)
			continue
		}
		b.WriteByte('{')
		for j, member := range [][2]string{{"kty", key.Kty}, {"crv", key.Crv}, {"x", key.X}, {"y", key.Y},
			{"kid", key.Kid}, {"alg", key.Alg}, {"use", key.Use}} {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(serialise(member[0]) + ":" + serialise(member[1]))
		}
		b.WriteByte('}')
	}
	b.WriteString(`]}`)
	return b.Bytes(), nil
}

// UnmarshalJSON reads a key set, refusing one that lacks a member a key needs.
func (k *SnapshotKeys) UnmarshalJSON(data []byte) error {
	keys, err := parseSnapshotKeys(data)
	if err != nil {
		return err
	}
	*k = keys
	return nil
}

// keepOrder keeps each key's original text, so its members stay in the order they were sent.
func keepOrder(data []byte, keys *SnapshotKeys) error {
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(data, &set); err != nil || len(set.Keys) != len(keys.Keys) {
		return nil
	}
	for i, raw := range set.Keys {
		var compact bytes.Buffer
		if json.Compact(&compact, raw) == nil {
			keys.Keys[i].raw = compact.Bytes()
		}
	}
	return nil
}

func parseSnapshotKeys(body []byte) (SnapshotKeys, error) {
	keys, err := decodeBody(body, decodeSnapshotKeys)
	if err != nil {
		return SnapshotKeys{}, err
	}
	err = keepOrder(body, &keys)
	return keys, err
}

func snapshotKeys(ctx context.Context, c caller) (*SnapshotKeys, error) {
	keys, _, err := fetchBody(ctx, c, &request{method: "GET", path: "/customers/snapshot-keys", open: true}, parseSnapshotKeys)
	if err != nil {
		return nil, err
	}
	return &keys, nil
}

// SnapshotExpectation is what a snapshot must match to verify.
//
// The customer is the signed-in person's external id from the app's own session, never from the
// stored snapshot; for an identity-token customer, <provider id>:<subject>, with the sign-in
// provider's Entitler id from the app's configuration. The environment is the environment id the
// app's build ships, beside its snapshot keys.
type SnapshotExpectation struct {
	// Keys are the keys that may have signed it: SnapshotKeys().Keys, shipped with the app.
	Keys []SnapshotKey
	// Customer is the signed-in customer's external id.
	Customer string
	// Environment is the app's environment id.
	Environment string
	// Issuer is the expected issuer; "" for https://api.entitler.dev/customers.
	Issuer string
	// Now is the instant to verify at; the zero time reads the system clock at each verification.
	Now time.Time
	// ClockSkewSeconds is how far ahead a snapshot's signing time may be, 0 to 300; nil for 60.
	ClockSkewSeconds *int
}

// VerifiedSnapshot is a verified snapshot: whose it is, where it is from, and the entitlements it
// signs, frozen (meters included) when it was signed.
type VerifiedSnapshot struct {
	// Customer is the customer's external id.
	Customer string
	// EnvironmentID is the environment's id.
	EnvironmentID string
	// Track is the customer's track.
	Track Track
	// Release is the release signed, or nil.
	Release *int64
	// Change is the change signed, or nil.
	Change *string
	// Testers is true on a track for testers.
	Testers bool
	// ExpiresAt is when it stops verifying.
	ExpiresAt time.Time
	// Entitlements are the entitlements signed, with AsOf the signing time and empty Upgrades,
	// since offline gating never offers a purchase.
	Entitlements *Entitlements
}

const (
	notASnapshot = "that is not an entitlements snapshot. Pass the token Snapshot() returned"
	firstSecond  = -62135596800
	lastSecond   = 253402300799
)

func snapshotInvalid(message string) error {
	return &SnapshotError{Code: CodeSnapshotInvalid, Message: message}
}

func urlSafe(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// VerifySnapshot verifies an entitlements snapshot offline, with no request: its ES256 signature
// against the keys given, its issuer, its lifetime, and that it is for the expected customer and
// environment. It returns *SnapshotError when a check fails.
//
// The keys decide which snapshots the app trusts: ship them with the app (written by
// `go tool entitler snapshot-keys` at build time) and replace them only with keys fetched from
// Entitler over HTTPS. Never store them beside the token or load them from the same record, since
// anyone who can edit that record could replace both. Entitler publishes a new signing key before
// signing with it and keeps retired keys published for 30 days after their last use; an emergency
// replacement withdraws old keys at once. So refresh the keys from SnapshotKeys whenever the app is
// online and keep them in the app's own trusted storage. A snapshot signed by a key the app has
// not fetched yet fails until it is next online.
func VerifySnapshot(token string, expected SnapshotExpectation) (*VerifiedSnapshot, error) {
	if blank(expected.Customer) || blank(expected.Environment) {
		return nil, argError("provide the customer and environment the snapshot must be for")
	}
	skew := 60
	if expected.ClockSkewSeconds != nil {
		skew = *expected.ClockSkewSeconds
		if skew < 0 || skew > 300 {
			return nil, argError("pass clockSkewSeconds as a whole number from 0 to 300")
		}
	}
	now := expected.Now
	if now.IsZero() {
		now = time.Now()
	} else if y := now.UTC().Year(); y < 1 || y > 9999 {
		return nil, argError("pass now as a valid instant")
	}
	issuer := expected.Issuer
	if issuer == "" {
		issuer = "https://api.entitler.dev/customers"
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 || !urlSafe(parts[0]) || !urlSafe(parts[1]) || !urlSafe(parts[2]) {
		return nil, snapshotInvalid(notASnapshot)
	}
	headerJSON, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	signature, err2 := base64.RawURLEncoding.DecodeString(parts[2])
	if err1 != nil || err2 != nil {
		return nil, snapshotInvalid(notASnapshot)
	}
	parsed, err := parseJSON(headerJSON)
	header, isObject := parsed.(map[string]any)
	if err != nil || !isObject || header["typ"] != "entitlements+jwt" || header["alg"] != "ES256" {
		return nil, snapshotInvalid(notASnapshot)
	}
	if _, crit := header["crit"]; crit {
		return nil, snapshotInvalid(notASnapshot)
	}

	kid, _ := header["kid"].(string)
	var key *SnapshotKey
	if _, isString := header["kid"].(string); isString {
		for i := range expected.Keys {
			if expected.Keys[i].Kid == kid {
				key = &expected.Keys[i]
				break
			}
		}
	}
	if key == nil {
		return nil, snapshotInvalid("none of the keys passed signed this snapshot. Fetch them again with SnapshotKeys()")
	}
	if !verifySignature(key, parts[0]+"."+parts[1], signature) {
		return nil, snapshotInvalid("this snapshot was changed after Entitler signed it")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, snapshotInvalid(notASnapshot)
	}
	claims, err := decodeBody(payload, decodeClaims)
	if err != nil {
		return nil, snapshotInvalid(notASnapshot)
	}
	if claims.iss != issuer {
		return nil, snapshotInvalid("this snapshot was not issued by " + issuer)
	}
	if time.Unix(claims.iat, 0).After(now.Add(time.Duration(skew) * time.Second)) {
		return nil, snapshotInvalid("this snapshot was signed for " + formatInstant(time.Unix(claims.iat, 0)) +
			", which is still to come. Fetch a new one while online")
	}
	if !now.Before(time.Unix(claims.exp, 0)) {
		return nil, &SnapshotError{Code: CodeSnapshotExpired,
			Message: "this snapshot expired at " + formatInstant(time.Unix(claims.exp, 0)) + ". Fetch a new one while online"}
	}
	if claims.sub != expected.Customer {
		return nil, snapshotInvalid("this snapshot is for another customer, not " + expected.Customer +
			". Fetch one for the signed-in customer while online")
	}
	if claims.environment != expected.Environment {
		return nil, snapshotInvalid("this snapshot is from another environment, not " + expected.Environment +
			". Fetch one from your app's environment while online")
	}
	return &VerifiedSnapshot{
		Customer:      claims.sub,
		EnvironmentID: claims.environment,
		Track:         claims.track,
		Release:       claims.release,
		Change:        claims.change,
		Testers:       claims.testers,
		ExpiresAt:     time.Unix(claims.exp, 0).UTC(),
		Entitlements: &Entitlements{
			Customer:    claims.sub,
			Items:       claims.entitlements,
			AsOf:        time.Unix(claims.iat, 0).UTC(),
			Environment: Environment{ID: claims.environment},
			Track:       claims.track,
			Release:     claims.release,
			Change:      claims.change,
			Testers:     claims.testers,
		},
	}, nil
}

func verifySignature(key *SnapshotKey, signed string, signature []byte) bool {
	x, errX := base64.RawURLEncoding.DecodeString(strings.TrimRight(key.X, "="))
	y, errY := base64.RawURLEncoding.DecodeString(strings.TrimRight(key.Y, "="))
	if errX != nil || errY != nil || len(x) != 32 || len(y) != 32 || len(signature) != 64 {
		return false
	}
	point := append(append([]byte{4}, x...), y...)
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(signed))
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	return ecdsa.Verify(pub, digest[:], r, s)
}

type snapshotClaims struct {
	iss, sub     string
	environment  string
	track        Track
	release      *int64
	change       *string
	testers      bool
	entitlements []Entitlement
	iat, exp     int64
}

func decodeClaims(o object) snapshotClaims {
	c := snapshotClaims{
		iss:         o.at("iss").str(),
		sub:         o.at("sub").str(),
		environment: o.at("environment").object().at("id").str(),
		track:       decodeTrack(o.at("track").object()),
		release:     o.at("release").intOrNull(),
		change:      o.at("change").strOrNull(),
		testers:     o.at("testers").boolean(),
		entitlements: objects(o.at("entitlements"), func(o object) Entitlement {
			return Entitlement{
				Key:      o.at("key").str(),
				Type:     FeatureType(o.at("type").str()),
				Value:    o.at("value").value(),
				Entitled: o.at("entitled").boolean(),
				Sources:  objects(o.at("sources"), decodeSource),
				Meter:    decodeMeter(o),
				Upgrades: []Upgrade{},
			}
		}),
		iat: o.at("iat").int(),
		exp: o.at("exp").int(),
	}
	for _, at := range []struct {
		name  string
		value int64
	}{{"iat", c.iat}, {"exp", c.exp}} {
		if at.value < firstSecond || at.value > lastSecond {
			o.r.fail(at.name, "expected seconds within the years 0001 to 9999")
		}
	}
	return c
}
