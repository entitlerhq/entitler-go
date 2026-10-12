package entitler

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestInAppClients(t *testing.T) {
	f := &fakeHTTP{handle: func(r *http.Request, _ string) fakeAnswer {
		switch {
		case r.URL.Path == "/keys/self":
			return fakeAnswer{status: 200, body: `{"customer":"user_42","scopes":["billing:self","entitlements:read","unknown:scope"],"expiresAt":"2026-07-01T10:30:00.000Z"}`}
		case r.URL.Path == "/pricing":
			return fakeAnswer{status: 200, body: `{"customer":null,"defaultPlan":null,"products":[],"plans":[],"environment":{"id":"e","name":"n","kind":"test"},"track":{"id":"t","name":"n"},"release":null,"change":null,"testers":false,"experiment":null}`}
		case r.Method == "PUT":
			return fakeAnswer{status: 201, body: `{"id":"c","externalId":"p:s","environmentId":"e","createdAt":"2026-07-01T09:30:00.000Z","created":true}`}
		}
		return fakeAnswer{status: 200, body: checkBody("user_42", "f")}
	}}
	token := StaticToken(tokenFor(time.Now().Add(time.Hour)))
	client, err := NewClient(token, WithHTTPClient(f.client()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if client.Me().ID() != "" {
		t.Error("no id before the first answer")
	}
	if _, err := client.Me().Check(ctx, Key("f"), nil); err != nil || client.Me().ID() != "user_42" {
		t.Fatalf("%v %q", err, client.Me().ID())
	}
	if f.seen()[0].url != "https://api.entitler.dev/customers/me/entitlements/f" {
		t.Error(f.seen()[0].url)
	}
	visitor := client.Visitor()
	if len(visitor) != 32 || checkVisitor(visitor) != nil || f.seen()[0].header.Get("Entitler-Visitor") != visitor {
		t.Errorf("a generated visitor is sent on every request: %q", visitor)
	}
	if _, err := client.Me().Pricing(ctx, &PricingParams{Visitor: "call_visitor_0123456789"}); err == nil || f.seen()[1].header.Get("Entitler-Visitor") != "call_visitor_0123456789" {
		t.Error("a call's visitor replaces the client's for that request")
	}
	if _, err := client.Me().Usage(ctx, &UsageParams{AsOf: time.Now()}); !errors.Is(err, ErrInvalidArgument) {
		t.Error("in-app reads refuse asOf")
	}
	s, err := client.Scopes(ctx)
	if err != nil || len(s.List) != 2 || s.List[0] != ScopeEntitlementsRead || !s.Has(ScopeBillingSelf) || s.Has(ScopeUsageWrite) {
		t.Errorf("scopes %+v %v", s, err)
	}
	if !strings.HasPrefix(client.String(), "entitler.Client(customer token, ") {
		t.Error(client.String())
	}
	derived := client.With(WithTimeout(time.Second))
	if derived.Visitor() != visitor {
		t.Error("derived clients share the visitor")
	}

	identity, err := NewIdentityClient(" ent_pk_test_x\n", StaticToken("identity-token"), WithHTTPClient(f.client()),
		WithVisitor("kept_visitor_0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	defer identity.Close()
	r, err := identity.Register(ctx, &IdentityRegisterParams{IdempotencyKey: "signin-1"})
	if err != nil || !r.Created || r.ExternalID != "p:s" {
		t.Fatalf("%+v %v", r, err)
	}
	last := f.seen()[len(f.seen())-1]
	if last.url != "https://api.entitler.dev/customers/me" || last.body != "" || last.header.Get("Authorization") != "Bearer ent_pk_test_x" ||
		last.header.Get("Entitler-Identity-Token") != "identity-token" || last.header.Get("Entitler-Visitor") != "kept_visitor_0123456789" {
		t.Errorf("register %+v", last)
	}
	if identity.With().Visitor() != "kept_visitor_0123456789" {
		t.Error("with")
	}

	pc, err := NewPublishableClient("ent_pk_live_x", WithHTTPClient(f.client()))
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if _, err := pc.Pricing(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pc.With().Pricing(ctx, &PricingParams{Revalidate: true}); err != nil {
		t.Fatal(err)
	}
	if pc.String() != "entitler.PublishableClient(https://api.entitler.dev)" || pc.Visitor() == "" {
		t.Error(pc.String())
	}
	f.push(fakeAnswer{status: 200, body: `{"keys":[]}`})
	if _, err := pc.SnapshotKeys(ctx); err != nil {
		t.Error(err)
	}
	if _, err := NewIdentityClient("ent_pk_x", nil); err == nil || err.Error() != "provide the identity token your sign-in provider issued" {
		t.Error(err)
	}
	if _, err := NewClient(nil); err == nil || err.Error() != "provide a customer token minted by your server" {
		t.Error(err)
	}
	if _, err := NewPublishableClient("ent_pk_x", WithVisitor("x")); !errors.Is(err, ErrInvalidArgument) {
		t.Error(err)
	}
}

func TestVisitorIDs(t *testing.T) {
	re := regexp.MustCompile(VisitorIDPattern)
	seen := map[string]bool{}
	for range 100 {
		v := NewVisitorID()
		if len(v) != 32 || !re.MatchString(v) || seen[v] {
			t.Fatal(v)
		}
		seen[v] = true
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 65), "visitor with spaces!", "visitor_0123456789\n"} {
		if checkVisitor(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	srv := testServer(t, &fakeHTTP{})
	if srv.c.e.visitor != "" {
		t.Error("the server never generates a visitor")
	}
}

func TestValuesAndFeatures(t *testing.T) {
	if (On{}).String() != "on" || Amount(5).String() != "5" || (Unlimited{}).String() != "unlimited" {
		t.Error("value strings")
	}
	g := Group("team", "a", "b")
	inc := g.Includes()
	inc[0] = "x"
	if g.Includes()[0] != "a" || g.Type() != FeatureTypeGroup || g.Key() != "team" {
		t.Error("group")
	}
	for f, want := range map[Feature]FeatureType{OnOff("a"): FeatureTypeBoolean, Config("a"): FeatureTypeConfig, Metered("a"): FeatureTypeMetered, Key("a"): ""} {
		if f.Type() != want || f.Key() != "a" {
			t.Errorf("%T", f)
		}
	}
	e := &Entitlements{Items: []Entitlement{{Key: "a", Entitled: true}, {Key: "b"}}}
	if !e.Has(Key("a")) || e.Has(Key("b")) || e.Has(Key("c")) || e.Has(nil) {
		t.Error("has")
	}
	if got, ok := e.Get(Key("b")); !ok || got.Key != "b" {
		t.Error("get")
	}
	for _, p := range []PendingChange{PendingMove{}, PendingCancel{}, PendingBilling{}, PendingUnknown{Type: "x"}} {
		p.isPending()
		if p.PendingType() == "" {
			t.Error(p)
		}
	}
	tok := IssuedCustomerToken{Token: "secret-token", Customer: "u", ExpiresAt: time.Now()}
	if strings.Contains(tok.String(), "secret-token") || strings.Contains(fmt.Sprint(tok), "secret-token") {
		t.Error("a token's string form holds the token")
	}
}

func TestEveryDocumentedCodeIsAConstant(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "codes.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			s, _ := strconv.Unquote(lit.Value)
			known[s] = true
		}
		return true
	})
	for _, doc := range []string{"customers.json", "keys.json"} {
		data, err := os.ReadFile("testdata/openapi/" + doc)
		if err != nil {
			t.Fatal(err)
		}
		var spec struct {
			Components struct {
				Schemas map[string]json.RawMessage `json:"schemas"`
			} `json:"components"`
		}
		_ = json.Unmarshal(data, &spec)
		count := 0
		for _, raw := range spec.Components.Schemas {
			var s struct {
				Properties struct {
					Error struct {
						Properties struct {
							Code struct {
								Enum []string `json:"enum"`
							} `json:"code"`
						} `json:"properties"`
					} `json:"error"`
				} `json:"properties"`
			}
			_ = json.Unmarshal(raw, &s)
			for _, code := range s.Properties.Error.Properties.Code.Enum {
				count++
				if !known[code] {
					t.Errorf("%s: %s is not a constant", doc, code)
				}
			}
		}
		if count == 0 {
			t.Errorf("%s holds no codes", doc)
		}
	}
	for _, own := range []string{"http_error", "invalid_response", "connection_failed", "timed_out", "internal", "snapshot_key_unavailable"} {
		if !known[own] {
			t.Errorf("%s is missing", own)
		}
	}
}

type signer struct {
	key *ecdsa.PrivateKey
	kid string
}

func newSigner(t *testing.T, kid string) signer {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signer{key: key, kid: kid}
}

func (s signer) public() SnapshotKey {
	b, _ := s.key.PublicKey.Bytes()
	return SnapshotKey{Kty: "EC", Crv: "P-256", X: base64.RawURLEncoding.EncodeToString(b[1:33]),
		Y: base64.RawURLEncoding.EncodeToString(b[33:]), Kid: s.kid, Alg: "ES256", Use: "sig"}
}

func (s signer) sign(t *testing.T, header, claims map[string]any) string {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	signed := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	digest := sha256.Sum256([]byte(signed))
	r, sv, err := ecdsa.Sign(rand.Reader, s.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	sv.FillBytes(sig[32:])
	return signed + "." + enc.EncodeToString(sig)
}

func snapshotClaimsFor(iat, exp time.Time) map[string]any {
	return map[string]any{
		"iss": "https://api.entitler.dev/customers", "sub": "user_42", "environment": map[string]any{"id": "env_1"},
		"track": map[string]any{"id": "trk", "name": "All customers"}, "release": 2, "change": nil, "testers": false,
		"entitlements": []any{map[string]any{"key": "ai", "type": "metered", "value": 100, "entitled": true, "sources": []any{},
			"used": 5, "held": 0, "remaining": 95, "resetsAt": nil}},
		"iat": iat.Unix(), "exp": exp.Unix(),
	}
}

func TestVerifySnapshotWithKeysSignedHere(t *testing.T) {
	s := newSigner(t, "k1")
	now := time.Now()
	token := s.sign(t, map[string]any{"typ": "entitlements+jwt", "alg": "ES256", "kid": "k1"}, snapshotClaimsFor(now.Add(-time.Minute), now.Add(time.Hour)))
	expected := SnapshotExpectation{Keys: []SnapshotKey{newSigner(t, "k0").public(), s.public()}, Customer: "user_42", Environment: "env_1"}
	v, err := VerifySnapshot(token, expected)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Entitlements.Has(Key("ai")) || v.Entitlements.Items[0].Meter.Remaining != Amount(95) || v.Release == nil || *v.Release != 2 {
		t.Errorf("%+v", v)
	}
	expected.Now = time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := VerifySnapshot(token, expected); err == nil || err.Error() != "pass now as a valid instant" {
		t.Error(err)
	}
	expected.Now = now.Add(2 * time.Hour)
	var se *SnapshotError
	if _, err := VerifySnapshot(token, expected); !errors.As(err, &se) || se.Code != CodeSnapshotExpired {
		t.Error(err)
	}
	bad := expected
	bad.Now = time.Time{}
	bad.Keys = []SnapshotKey{{Kid: "k1", X: "!!", Y: "x"}}
	if _, err := VerifySnapshot(token, bad); !errors.As(err, &se) || !strings.HasPrefix(se.Message, "this snapshot was changed") {
		t.Error(err)
	}
	keys := SnapshotKeys{Keys: expected.Keys}
	data, err := json.Marshal(keys)
	if err != nil || !strings.HasPrefix(string(data), `{"keys":[{"kty":"EC"`) {
		t.Fatalf("%s %v", data, err)
	}
	var back SnapshotKeys
	if err := json.Unmarshal(data, &back); err != nil || back.Keys[1].Kid != "k1" {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"keys":[{"kid":"x"}]}`), &back); err == nil {
		t.Error("a key missing members decodes")
	}
}

func TestSignatureEncodings(t *testing.T) {
	s := newSigner(t, "k")
	if verifySignature(&SnapshotKey{X: "", Y: ""}, "a.b", make([]byte, 64)) {
		t.Error("a short key verifies")
	}
	zero := big.NewInt(0)
	_ = zero
	pub := s.public()
	if verifySignature(&pub, "a.b", make([]byte, 64)) {
		t.Error("an all-zero signature verifies")
	}
}
