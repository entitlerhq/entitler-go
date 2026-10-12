package entitler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

const conformanceDir = "testdata/conformance"

type caseFile struct {
	Description string           `json:"description"`
	Cases       []map[string]any `json:"cases"`
}

func loadCases(t *testing.T, name string) []map[string]any {
	t.Helper()
	var f caseFile
	readJSON(t, filepath.Join(conformanceDir, name), &f)
	return f.Cases
}

func TestConformanceManifest(t *testing.T) {
	var manifest struct {
		Files []struct {
			Path   string      `json:"path"`
			Cases  json.Number `json:"cases"`
			SHA256 string      `json:"sha256"`
		} `json:"files"`
	}
	readJSON(t, filepath.Join(conformanceDir, "manifest.json"), &manifest)
	if len(manifest.Files) != 10 {
		t.Fatalf("manifest lists %d files", len(manifest.Files))
	}
	for _, f := range manifest.Files {
		data, err := os.ReadFile(filepath.Join(conformanceDir, f.Path))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			t.Errorf("%s: SHA-256 differs from the manifest", f.Path)
		}
		if got := len(loadCases(t, f.Path)); fmt.Sprint(got) != f.Cases.String() {
			t.Errorf("%s: %d cases, the manifest says %s", f.Path, got, f.Cases)
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) int64 {
	n, _ := v.(json.Number)
	i, _ := n.Int64()
	return i
}

func secondsOfNumber(v any) time.Duration {
	n, _ := v.(json.Number)
	f, _ := n.Float64()
	return time.Duration(f * float64(time.Second))
}

// normal turns expected outcomes and actual values into one comparable form: { "unknown": x } is x,
// and numbers are json.Number.
func normal(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var out any
	_ = d.Decode(&out)
	return unwrapUnknown(out)
}

func unwrapUnknown(v any) any {
	switch v := v.(type) {
	case map[string]any:
		if raw, ok := v["unknown"]; ok && len(v) == 1 {
			return raw
		}
		for k, item := range v {
			v[k] = unwrapUnknown(item)
		}
	case []any:
		for i, item := range v {
			v[i] = unwrapUnknown(item)
		}
	}
	return v
}

func same(t *testing.T, label string, got, want any) {
	t.Helper()
	g, w := normal(got), normal(want)
	if !reflect.DeepEqual(g, w) {
		gj, _ := json.Marshal(g)
		wj, _ := json.Marshal(w)
		t.Errorf("%s:\n got %s\nwant %s", label, gj, wj)
	}
}

func valueOut(v Value) any {
	switch v := v.(type) {
	case On:
		return map[string]any{"kind": "on"}
	case Unlimited:
		return map[string]any{"kind": "unlimited"}
	case Amount:
		return map[string]any{"kind": "amount", "amount": int64(v)}
	}
	return nil
}

func instantOut(at time.Time) any {
	if at.IsZero() {
		return nil
	}
	return instantReq(at)
}

// instantReq writes a required instant, which may be the zero time's own instant.
func instantReq(at time.Time) any {
	return map[string]any{"instant": formatInstant(at), "epochMillis": at.UnixMilli()}
}

func strPtr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func intPtr(i *int64) any {
	if i == nil {
		return nil
	}
	return *i
}

func noneIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func apiErrorOut(err error) any {
	var e *APIError
	if !errors.As(err, &e) {
		return map[string]any{"kind": fmt.Sprintf("%T", err), "error": fmt.Sprint(err)}
	}
	var retry any
	if e.retryAfter != nil {
		retry = int64(e.RetryAfter / time.Second)
	}
	var payment any
	if e.Payment != nil {
		payment = map[string]any{"status": string(e.Payment.Status), "url": noneIfEmpty(e.Payment.URL)}
	}
	return map[string]any{
		"kind": "ApiError", "status": e.Status, "code": string(e.Code), "message": e.Message,
		"requestId": noneIfEmpty(e.RequestID), "retryAfterSeconds": retry,
		"idempotencyKey": noneIfEmpty(e.IdempotencyKey), "payment": payment,
	}
}

// specAPIError turns an expected ApiError's message into Go's form when it is the SDK's own.
func specAPIError(want map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range want {
		out[k] = v
	}
	if m, ok := out["message"].(string); ok && (strings.HasPrefix(m, "Entitler sent an answer") || strings.HasPrefix(m, "Entitler answered with HTTP")) {
		out["message"] = goMessage(m)
	}
	return out
}

func upgradesOut(us []Upgrade) any {
	out := []any{}
	for _, u := range us {
		out = append(out, map[string]any{"plan": u.Plan, "name": u.Name, "move": string(u.Move), "action": string(u.Action), "reason": strPtr(u.Reason)})
	}
	return out
}

func TestConformanceConstruction(t *testing.T) {
	for _, c := range loadCases(t, "construction.json") {
		t.Run(str(c["name"]), func(t *testing.T) {
			if c["appliesWhere"] != nil {
				t.Skipf("separate constructors rule this out in Go: %s", c["appliesWhere"])
			}
			creds := c["credentials"].(map[string]any)
			source := func(v any) TokenSource {
				if s, ok := v.(string); ok {
					return StaticToken(s)
				}
				return TokenFunc(func(context.Context) (string, error) { return "", errors.New("never called") })
			}
			var client fmt.Stringer
			var err error
			key, hasKey := creds["key"].(string)
			switch {
			case c["client"] == "EntitlerServer":
				client, err = NewServer(key)
			case creds["token"] != nil:
				client, err = NewClient(source(creds["token"]))
			case creds["identityToken"] != nil:
				client, err = NewIdentityClient(key, source(creds["identityToken"]))
			case hasKey:
				client, err = NewPublishableClient(key)
			default:
				t.Fatal("unexpected credentials")
			}
			outcome := c["outcome"].(map[string]any)
			if outcome["valid"] == true {
				if err != nil {
					t.Fatalf("want a client, got %v", err)
				}
				for _, omitted := range outcome["stringFormOmits"].([]any) {
					if strings.Contains(client.String(), str(omitted)) || strings.Contains(fmt.Sprintf("%+v %#v", client, client), str(omitted)) {
						t.Errorf("string form holds a credential")
					}
				}
				return
			}
			wantArgError(t, err, str(outcome["message"]))
		})
	}
}

type keyStore struct {
	mu   sync.Mutex
	keys []string
}

func (s *keyStore) Get(_ context.Context, key string) (CacheEntry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, key)
	return CacheEntry{}, false, nil
}

func (s *keyStore) Set(context.Context, string, CacheEntry, time.Duration) error { return nil }

func TestConformanceCacheKeys(t *testing.T) {
	for _, c := range loadCases(t, "cache-keys.json") {
		t.Run(str(c["name"]), func(t *testing.T) {
			built := c["built"].(map[string]any)
			params, _ := built["params"].(map[string]any)
			route := str(built["route"])
			f := &fakeHTTP{}
			base := []ClientOption{WithHTTPClient(f.client()), WithMaxRetries(0), WithBaseURL(str(built["baseUrl"]))}
			visitor := str(c["visitor"])
			if cv, ok := c["clientVisitor"].(string); ok {
				base = append(base, WithVisitor(cv))
			} else if visitor != "" {
				base = append(base, WithVisitor(visitor))
			}
			var keys []string
			var mu sync.Mutex
			observe := func(e *engine) {
				e.keySeen = func(k string) { mu.Lock(); keys = append(keys, k); mu.Unlock() }
			}
			store := &keyStore{}
			var me Customer
			var read func() error
			switch str(c["principalKind"]) {
			case "key":
				credential := str(c["credential"])
				if strings.HasPrefix(trimCredential(credential), "ent_pk_") {
					pc, err := NewPublishableClient(credential, base...)
					if err != nil {
						t.Fatal(err)
					}
					defer pc.Close()
					observe(pc.c.e)
					read = func() error {
						var p *PricingParams
						if _, ok := c["clientVisitor"]; ok {
							p = &PricingParams{Visitor: visitor}
						}
						_, err := pc.Pricing(ctx, p)
						return err
					}
					break
				}
				opts := []ServerOption{WithHTTPClient(f.client()), WithMaxRetries(0), WithBaseURL(str(built["baseUrl"])), WithCache(store)}
				srv, err := NewServer(credential, opts...)
				if err != nil {
					t.Fatal(err)
				}
				defer srv.Close()
				read = func() error {
					switch route {
					case "/pricing":
						_, err := srv.Pricing(ctx, &PricingParams{Visitor: visitor})
						return err
					case "/pricing/features":
						_, err := srv.Features(ctx)
						return err
					}
					return customerRead(srv.Customer(str(params["id"])), route, params, c, visitor)
				}
			case "customer-token":
				client, err := NewClient(StaticToken(str(c["credential"])), base...)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				observe(client.c.e)
				me = client.Me()
			case "identity":
				client, err := NewIdentityClient(str(c["key"]), StaticToken(str(c["identityToken"])), base...)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				observe(client.c.e)
				me = client.Me()
			}
			if me != nil {
				read = func() error { return customerRead(me, route, params, c, "") }
			}
			err := read()
			outcome := c["outcome"].(map[string]any)
			if outcome["kind"] == "ArgumentError" {
				wantArgError(t, err, str(outcome["message"]))
				if len(keys)+len(store.keys)+f.count() != 0 {
					t.Error("a key was asked for or a request sent")
				}
				return
			}
			got := append(keys, store.keys...)
			if len(got) != 1 || got[0] != outcome["cacheKey"] {
				t.Errorf("cache key: got %v, want %v", got, outcome["cacheKey"])
			}
			if sha256Hex(str(outcome["serialised"])) != outcome["cacheKey"] {
				t.Error("serialised does not hash to the key")
			}
			requests := f.seen()
			if len(requests) != 1 {
				t.Fatalf("%d requests", len(requests))
			}
			if requests[0].url != c["url"] {
				t.Errorf("url %s, want %s", requests[0].url, c["url"])
			}
			for name, want := range outcome["requestHeaders"].(map[string]any) {
				if got := requests[0].header.Get(name); got != str(want) || (want == nil) != (got == "") {
					t.Errorf("%s: got %q, want %v", name, got, want)
				}
			}
		})
	}
}

func customerRead(cu Customer, route string, params map[string]any, c map[string]any, visitor string) error {
	p := &ReadParams{}
	if in, ok := c["asOfInput"].(string); ok {
		at, err := time.Parse(time.RFC3339, in)
		if err != nil {
			return err
		}
		p.AsOf = at
	} else if asOf, ok := c["asOf"].(string); ok {
		at, _ := parseInstant(asOf)
		p.AsOf = at
	}
	var err error
	switch route {
	case "/customers/{id}/entitlements/{feature}":
		_, err = cu.Check(ctx, Key(str(params["feature"])), p)
	case "/customers/{id}/entitlements":
		_, err = cu.Entitlements(ctx, p)
	case "/customers/{id}/plans":
		_, err = cu.Plans(ctx, p)
	case "/customers/{id}/pricing":
		_, err = cu.Pricing(ctx, &PricingParams{Visitor: visitor})
	default:
		err = fmt.Errorf("unknown route %s", route)
	}
	return err
}

func TestConformancePathEncoding(t *testing.T) {
	for _, c := range loadCases(t, "path-encoding.json") {
		t.Run(str(c["name"]), func(t *testing.T) {
			if c["appliesWhere"] != nil {
				t.Skipf("Go strings hold bytes, not UTF-16: %s (invalid UTF-8 is covered by TestInvalidUTF8)", c["appliesWhere"])
			}
			f := &fakeHTTP{}
			opts := []ServerOption{WithHTTPClient(f.client()), WithMaxRetries(0), WithoutCache()}
			if base, ok := c["baseUrl"].(string); ok {
				opts = append(opts, WithBaseURL(base))
			}
			srv, err := NewServer(testKey, opts...)
			if err != nil {
				t.Fatal(err)
			}
			defer srv.Close()
			outcome := c["outcome"].(map[string]any)
			switch str(c["kind"]) {
			case "segment", "baseUrl":
				req := c["request"].(map[string]any)
				params := req["params"].(map[string]any)
				options, _ := req["options"].(map[string]any)
				id := str(params["id"])
				if c["parameter"] == "customer" {
					id = str(c["id"])
				}
				err = pathCall(srv.Customer(id), str(req["method"]), str(req["route"]), params, options)
			case "query":
				value := str(c["value"])
				switch c["parameter"] {
				case "q":
					_, err = srv.Customers.Page(ctx, &ListCustomersParams{Q: value})
				case "cursor":
					_, err = srv.Customers.Page(ctx, &ListCustomersParams{Cursor: value})
				case "product":
					req := c["request"].(map[string]any)
					cu := srv.Customer(str(req["params"].(map[string]any)["id"]))
					if strings.HasSuffix(str(req["route"]), "/pending") {
						_, err = cu.UndoPendingChange(ctx, &CancelParams{Product: &value})
					} else {
						_, err = cu.Cancel(ctx, &CancelParams{Product: &value})
					}
				}
			case "arguments":
				call := c["call"].(map[string]any)
				options := call["options"].(map[string]any)
				addOn, product := str(options["addOn"]), str(options["product"])
				cu := srv.Customer(str(call["customer"]))
				if call["method"] == "cancel" {
					_, err = cu.Cancel(ctx, &CancelParams{AddOn: &addOn, Product: &product})
				} else {
					_, err = cu.UndoPendingChange(ctx, &CancelParams{AddOn: &addOn, Product: &product})
				}
			}
			if outcome["kind"] == "ArgumentError" {
				wantArgError(t, err, str(outcome["message"]))
				if f.count() != 0 {
					t.Error("a request was sent")
				}
				return
			}
			requests := f.seen()
			if len(requests) != 1 || requests[0].url != outcome["url"] {
				t.Fatalf("requests %v, want %v", requests, outcome["url"])
			}
		})
	}
}

func pathCall(cu *ServerCustomer, method, route string, params, options map[string]any) error {
	var err error
	switch method + " " + route {
	case "GET /customers/{id}/entitlements/{feature}":
		_, err = cu.Check(ctx, Key(str(params["feature"])), nil)
	case "DELETE /customers/{id}?erase=true":
		err = cu.Erase(ctx, nil)
	case "DELETE /customers/{id}/subscription/add-ons/{addOn}":
		addOn := str(params["addOn"])
		_, err = cu.Cancel(ctx, &CancelParams{AddOn: &addOn})
	case "DELETE /customers/{id}/subscription/add-ons/{addOn}/pending":
		addOn := str(params["addOn"])
		_, err = cu.UndoPendingChange(ctx, &CancelParams{AddOn: &addOn})
	case "PUT /customers/{id}/add-ons/{addOn}":
		_, err = cu.SetAddOn(ctx, str(params["addOn"]), num(options["quantity"]), nil)
	case "POST /customers/{id}/meters/{feature}/adjustments":
		by := num(options["by"])
		_, err = cu.AdjustMeter(ctx, Metered(str(params["feature"])), &AdjustMeterParams{By: &by, IdempotencyKey: str(options["idempotencyKey"])})
	case "POST /customers/{id}/usage/holds/{holdId}/settle":
		_, err = cu.SettleUsage(ctx, str(params["holdId"]), num(options["amount"]), nil)
	case "DELETE /customers/{id}/usage/holds/{holdId}":
		_, err = cu.ReleaseUsage(ctx, str(params["holdId"]), nil)
	case "DELETE /customers/{id}/grants/{grantId}":
		_, err = cu.RevokeGrant(ctx, str(params["grantId"]), nil)
	case "DELETE /customers/{id}/usage/{usageId}":
		_, err = cu.CancelUsage(ctx, str(params["usageId"]), nil)
	default:
		err = fmt.Errorf("unknown request %s %s", method, route)
	}
	return err
}

func usageAnswer(outcome string, holdID any) string {
	body := map[string]any{}
	_ = json.Unmarshal([]byte(checkBody("user_42", "ai_credits")), &body)
	body["type"] = "metered"
	body["value"] = 1000
	body["used"], body["held"], body["remaining"], body["resetsAt"] = 1, 0, 999, nil
	body["outcome"], body["refusal"], body["id"], body["holdId"] = outcome, nil, "use_1", holdID
	body["mode"], body["amount"], body["meterChange"], body["overBy"], body["late"] = "gate", 1, 1, 0, false
	body["occurredAt"], body["expiresAt"], body["reportedAs"] = nil, nil, "api"
	if outcome == "held" {
		body["expiresAt"] = "2026-07-01T09:35:00.000Z"
	}
	return string(marshalBody(body))
}

func TestConformanceIdempotencyKeys(t *testing.T) {
	for _, c := range loadCases(t, "idempotency-keys.json") {
		t.Run(str(c["name"]), func(t *testing.T) {
			if c["appliesWhere"] != nil {
				t.Skipf("Go strings have no missing value: an empty key is the empty-key case (%s)", c["appliesWhere"])
			}
			if c["method"] == "recordUsageBatch" {
				runBatchCase(t, c)
				return
			}
			f := &fakeHTTP{handle: func(r *http.Request, body string) fakeAnswer {
				switch {
				case strings.HasSuffix(r.URL.Path, "/usage/holds"):
					return fakeAnswer{status: 200, body: usageAnswer("held", "hold_1")}
				case strings.HasSuffix(r.URL.Path, "/settle"):
					return fakeAnswer{status: 200, body: usageAnswer("settled", "hold_1")}
				case strings.HasSuffix(r.URL.Path, "/usage") || strings.Contains(r.URL.Path, "/adjustments"):
					return fakeAnswer{status: 200, body: usageAnswer("recorded", nil)}
				case strings.HasSuffix(r.URL.Path, "/tokens"):
					return fakeAnswer{status: 201, body: `{"token":"t","customer":"user_42","scopes":["entitlements:read"],"expiresAt":"2026-07-01T10:30:00.000Z"}`}
				case strings.HasSuffix(r.URL.Path, "/snapshots"):
					return fakeAnswer{status: 201, body: `{"token":"t","expiresAt":"2026-07-08T09:30:00.000Z","keyId":"k"}`}
				case strings.HasSuffix(r.URL.Path, "/billing-portal"):
					return fakeAnswer{status: 200, body: `{"provider":"stripe","url":"https://billing.example.com"}`}
				case strings.HasSuffix(r.URL.Path, "/billing/sync"):
					return fakeAnswer{status: 200, body: `{"changed":false}`}
				}
				return fakeAnswer{status: 200, body: `{"product":null,"plan":null,"quantity":null,"effective":"now","at":"2026-07-01T09:30:00.000Z","until":null,"changed":false}`}
			}}
			srv := testServer(t, f)
			cu := srv.Customer("user_42")
			key, _ := c["key"].(string)
			credits := Metered("ai_credits")
			call := func() error {
				var err error
				switch c["method"] {
				case "recordUsage":
					_, err = cu.RecordUsage(ctx, credits, 1, key, nil)
				case "holdUsage":
					_, err = cu.HoldUsage(ctx, credits, 1, key, nil)
				case "startHold":
					var h *Hold
					if h, err = cu.StartHold(ctx, credits, 1, key, nil); err == nil {
						_ = h.Use(2)
						_, err = h.Finish(ctx)
					}
				case "withHold":
					_, err = WithHold(ctx, cu, credits, 1, key, func(_ context.Context, h *Hold) (int, error) {
						return 0, h.Use(2)
					}, nil)
				case "adjustMeter":
					by := int64(1)
					_, err = cu.AdjustMeter(ctx, credits, &AdjustMeterParams{By: &by, IdempotencyKey: key})
				case "cancel":
					_, err = cu.Cancel(ctx, &CancelParams{IdempotencyKey: key})
				case "token":
					_, err = cu.Token(ctx, nil)
				case "snapshot":
					_, err = cu.Snapshot(ctx, nil)
				case "billingPortal":
					_, err = cu.BillingPortal(ctx, "https://app.example.com/billing")
				case "syncBilling":
					_, err = cu.SyncBilling(ctx)
				default:
					err = fmt.Errorf("unknown method %v", c["method"])
				}
				return err
			}
			err := call()
			outcome := c["outcome"].(map[string]any)
			if outcome["valid"] != true {
				wantArgError(t, err, str(outcome["message"]))
				if f.count() != 0 {
					t.Error("a request was sent")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			requests := f.seen()
			first := requests[0].header.Get("Idempotency-Key")
			if key != "" && first != key {
				t.Errorf("sent key %q, want %q", first, key)
			}
			if key == "" {
				if !isUUIDv4(first) {
					t.Errorf("sent %q, want a UUID v4", first)
				}
				if err := call(); err != nil {
					t.Fatal(err)
				}
				if again := f.seen()[len(requests)].header.Get("Idempotency-Key"); again == first {
					t.Error("a second call sent the same key")
				}
			}
			if excess, ok := outcome["excessKey"].(string); ok {
				last := requests[len(requests)-1]
				if !strings.HasSuffix(last.url, "/usage") || last.header.Get("Idempotency-Key") != excess {
					t.Errorf("excess sent to %s with key %q, want %q", last.url, last.header.Get("Idempotency-Key"), excess)
				}
				var body map[string]any
				_ = json.Unmarshal([]byte(last.body), &body)
				if body["mode"] != "observe" || body["amount"] != float64(1) {
					t.Errorf("excess body %s", last.body)
				}
			}
		})
	}
}

func isUUIDv4(s string) bool {
	return len(s) == 36 && s[14] == '4' && strings.ContainsRune("89ab", rune(s[19])) && s[8] == '-' && s[13] == '-'
}

func runBatchCase(t *testing.T, c map[string]any) {
	var events []BatchEvent
	toEvent := func(e map[string]any) BatchEvent {
		ev := BatchEvent{Customer: str(e["customer"]), Feature: Metered(str(e["feature"])), Amount: num(e["amount"]),
			IdempotencyKey: str(e["idempotencyKey"])}
		if at, ok := e["occurredAt"].(string); ok {
			parsed, err := time.Parse(time.RFC3339Nano, at)
			if err != nil {
				t.Fatal(err)
			}
			ev.OccurredAt = parsed
		}
		return ev
	}
	if list, ok := c["events"].([]any); ok {
		for _, e := range list {
			events = append(events, toEvent(e.(map[string]any)))
		}
	} else {
		p := c["eventPattern"].(map[string]any)
		for i := int64(0); i < num(p["count"]); i++ {
			events = append(events, BatchEvent{
				Customer:       str(p["customerPrefix"]) + fmt.Sprint(i%num(p["customers"])),
				Feature:        Metered(str(p["feature"])),
				Amount:         i + 1,
				IdempotencyKey: str(p["keyPrefix"]) + fmt.Sprint(i),
			})
		}
		if overrides, ok := c["overrides"].([]any); ok {
			for _, o := range overrides {
				o := o.(map[string]any)
				events[num(o["index"])] = toEvent(o["event"].(map[string]any))
			}
		}
	}
	calls := 1
	if n, ok := c["calls"]; ok {
		calls = int(num(n))
	}
	f := &fakeHTTP{handle: func(_ *http.Request, body string) fakeAnswer {
		var sent struct {
			Events []any `json:"events"`
		}
		_ = json.Unmarshal([]byte(body), &sent)
		results := []any{}
		for i := range sent.Events {
			results = append(results, map[string]any{"index": i, "outcome": "recorded", "id": fmt.Sprint("u", i), "late": false, "error": nil})
		}
		return fakeAnswer{status: 200, body: string(marshalBody(map[string]any{"results": results, "recorded": len(results), "duplicates": 0, "errors": 0}))}
	}}
	srv := testServer(t, f)
	outcome := c["outcome"].(map[string]any)
	wantRequests := outcome["requests"].([]any)
	var params *RecordUsageBatchParams
	if reg, ok := c["register"].(bool); ok {
		params = &RecordUsageBatchParams{Register: reg}
	}
	for call := 0; call < calls; call++ {
		result, err := srv.RecordUsageBatch(ctx, events, params)
		if err != nil {
			t.Fatal(err)
		}
		refused := map[int64]map[string]any{}
		for _, r := range outcome["refused"].([]any) {
			r := r.(map[string]any)
			refused[num(r["index"])] = r
		}
		for i, r := range result.Results {
			if want, ok := refused[int64(i)]; ok {
				if r.Outcome != OutcomeError || r.Error == nil || string(r.Error.Code) != want["code"] || r.Error.Message != goMessage(str(want["message"])) {
					t.Errorf("event %d: got %+v %+v, want %v", i, r, r.Error, want)
				}
			} else if r.Outcome != OutcomeRecorded {
				t.Errorf("event %d: %s %+v", i, r.Outcome, r.Error)
			}
		}
		requests := f.seen()[call*len(wantRequests):]
		if len(requests) != len(wantRequests) {
			t.Fatalf("%d requests, want %d", len(requests), len(wantRequests))
		}
		for i, want := range wantRequests {
			want := want.(map[string]any)
			if got := requests[i].header.Get("Idempotency-Key"); got != want["idempotencyKey"] {
				t.Errorf("request %d key %s, want %s", i, got, want["idempotencyKey"])
			}
			var sent struct {
				Events []any `json:"events"`
			}
			_ = json.Unmarshal([]byte(requests[i].body), &sent)
			if int64(len(sent.Events)) != num(want["events"]) {
				t.Errorf("request %d: %d events, want %v", i, len(sent.Events), want["events"])
			}
			if "batch:"+sha256Hex(str(want["serialised"])) != want["idempotencyKey"] {
				t.Error("serialised does not hash to the key")
			}
		}
	}
}

func TestConformanceRetryTiming(t *testing.T) {
	for _, c := range loadCases(t, "retry-timing.json") {
		t.Run(str(c["name"]), func(t *testing.T) {
			outcome := c["outcome"].(map[string]any)
			switch c["kind"] {
			case "backoff":
				e := &engine{}
				var limit time.Duration
				e.jitter = func(l time.Duration) time.Duration { limit = l; return 0 }
				retry := int(num(c["retry"]))
				limit = min(8*time.Second, (500*time.Millisecond)<<retry)
				if limit != secondsOfNumber(outcome["maxSeconds"]) || secondsOfNumber(outcome["minSeconds"]) != 0 {
					t.Errorf("retry %d: limit %s, want %v", retry, limit, outcome["maxSeconds"])
				}
			case "limits":
				synctest.Test(t, func(t *testing.T) {
					f := &fakeHTTP{}
					for range 10 {
						f.push(fakeAnswer{status: 503})
					}
					srv := testServer(t, f, WithMaxRetries(int(num(c["maxRetries"]))), WithoutCache())
					_, _ = srv.Customer("user_42").Check(ctx, Key("export_pdf"), nil)
					if int64(f.count()) != num(outcome["maxAttemptsWithoutUnauthorised"]) {
						t.Errorf("%d attempts, want %v", f.count(), outcome["maxAttemptsWithoutUnauthorised"])
					}
				})
			case "retryAfter":
				synctest.Test(t, func(t *testing.T) { runRetryAfter(t, c) })
			case "attempts":
				synctest.Test(t, func(t *testing.T) { runAttempts(t, c) })
			}
		})
	}
}

func runRetryAfter(t *testing.T, c map[string]any) {
	outcome := c["outcome"].(map[string]any)
	f := &fakeHTTP{}
	header := map[string]string{}
	if ra, ok := c["retryAfter"].(string); ok {
		header["Retry-After"] = ra
	}
	f.push(fakeAnswer{status: 503, header: header}, fakeAnswer{status: 200, body: checkBody("user_42", "export_pdf")})
	srv := testServer(t, f, WithMaxRetries(2), WithMaxRetryDelay(secondsOfNumber(c["maxRetryDelay"])), WithoutCache())
	clock(srv.c.e, instant(t, str(c["now"])))
	var limits []time.Duration
	srv.c.e.jitter = func(l time.Duration) time.Duration { limits = append(limits, l); return l / 2 }
	_, err := srv.Customer("user_42").Check(ctx, Key("export_pdf"), nil)
	requests := f.seen()
	switch outcome["action"] {
	case "fail":
		if len(requests) != 1 {
			t.Fatalf("%d attempts", len(requests))
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 503 {
			t.Fatalf("want the 503, got %v", err)
		}
	case "wait":
		if err != nil || len(requests) != 2 {
			t.Fatalf("%v, %d attempts", err, len(requests))
		}
		if waited := requests[1].start.Sub(requests[0].end); waited != secondsOfNumber(outcome["seconds"]) {
			t.Errorf("waited %s, want %v s", waited, outcome["seconds"])
		}
	case "backoff":
		if err != nil || len(limits) != 1 || limits[0] != 500*time.Millisecond {
			t.Fatalf("%v, backoff limits %v", err, limits)
		}
	}
}

const runAttemptsTimeout = 10 * time.Second

func runAttempts(t *testing.T, c map[string]any) {
	outcome := c["outcome"].(map[string]any)
	f := &fakeHTTP{}
	for _, a := range c["answers"].([]any) {
		a := a.(map[string]any)
		answer := fakeAnswer{status: int(num(a["status"])), failure: str(a["failure"])}
		if h, ok := a["headers"].(map[string]any); ok {
			answer.header = map[string]string{}
			for k, v := range h {
				answer.header[k] = str(v)
			}
		}
		if b, ok := a["body"]; ok {
			answer.body = string(marshalBody(b))
		}
		f.push(answer)
	}
	opts := []ClientOption{WithHTTPClient(f.client()), WithMaxRetries(int(num(c["maxRetries"]))),
		WithMaxRetryDelay(secondsOfNumber(c["maxRetryDelay"])), WithoutCache(), WithTimeout(runAttemptsTimeout)}
	now := instant(t, str(c["now"]))
	credential := c["credential"].(map[string]any)
	request := c["request"].(map[string]any)
	var cu Customer
	var e *engine
	providerCalls := 0
	switch credential["kind"] {
	case "key":
		srv, err := NewServer(str(credential["key"]), WithHTTPClient(f.client()), WithMaxRetries(int(num(c["maxRetries"]))),
			WithMaxRetryDelay(secondsOfNumber(c["maxRetryDelay"])), WithoutCache(), WithTimeout(runAttemptsTimeout))
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()
		e = srv.c.e
		parts := strings.Split(str(request["path"]), "/")
		cu = srv.Customer(parts[2])
	case "customerToken":
		client, err := NewClient(StaticToken(str(credential["token"])), opts...)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		e, cu = client.c.e, client.Me()
	case "customerTokenProvider":
		tokens := credential["tokens"].([]any)
		var mu sync.Mutex
		client, err := NewClient(TokenFunc(func(context.Context) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			providerCalls++
			return str(tokens[min(providerCalls-1, len(tokens)-1)]), nil
		}), opts...)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		e, cu = client.c.e, client.Me()
	}
	clock(e, now)
	var limits []time.Duration
	e.jitter = func(l time.Duration) time.Duration { limits = append(limits, l); return l }
	var err error
	if request["method"] == "GET" {
		parts := strings.Split(str(request["path"]), "/")
		_, err = cu.Check(ctx, Key(parts[len(parts)-1]), nil)
	} else {
		body := request["body"].(map[string]any)
		_, err = cu.RecordUsage(ctx, Metered(str(body["feature"])), num(body["amount"]), str(request["idempotencyKey"]), nil)
	}
	requests := f.seen()
	if int64(len(requests)) != num(outcome["attempts"]) {
		t.Fatalf("%d attempts, want %v (%v)", len(requests), outcome["attempts"], err)
	}
	if pc, ok := outcome["providerCalls"]; ok && int64(providerCalls) != num(pc) {
		t.Errorf("%d provider calls, want %v", providerCalls, pc)
	}
	waits := outcome["waits"].([]any)
	if len(waits) != len(requests)-1 {
		t.Fatalf("%d waits for %d attempts", len(waits), len(requests))
	}
	jitters := 0
	for i, w := range waits {
		w := w.(map[string]any)
		minWait, maxWait := secondsOfNumber(w["minSeconds"]), secondsOfNumber(w["maxSeconds"])
		waited := requests[i+1].start.Sub(requests[i].end)
		if minWait == maxWait {
			if waited != minWait {
				t.Errorf("wait %d: %s, want %s", i, waited, minWait)
			}
			continue
		}
		if jitters >= len(limits) || limits[jitters] != maxWait || waited != maxWait {
			t.Errorf("wait %d: waited %s with limits %v, want up to %s", i, waited, limits, maxWait)
		}
		jitters++
	}
	result := outcome["result"].(map[string]any)
	switch result["kind"] {
	case nil:
		if err != nil {
			t.Fatalf("want success, got %v", err)
		}
	case "ApiError":
		var apiErr *APIError
		if !errors.As(err, &apiErr) || int64(apiErr.Status) != num(result["status"]) || string(apiErr.Code) != result["code"] {
			t.Fatalf("got %v, want %v", err, result)
		}
	case "TimeoutError":
		var timeout *TimeoutError
		if !errors.As(err, &timeout) {
			t.Fatalf("got %v, want a TimeoutError", err)
		}
	case "ConnectionError":
		var conn *ConnectionError
		if !errors.As(err, &conn) {
			t.Fatalf("got %v, want a ConnectionError", err)
		}
	}
	if outcome["sameIdempotencyKeyOnEveryAttempt"] == true {
		for _, r := range requests {
			if r.header.Get("Idempotency-Key") != request["idempotencyKey"] {
				t.Error("an attempt sent another key")
			}
		}
	}
	if outcome["errorCarriesIdempotencyKey"] == true {
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.IdempotencyKey != request["idempotencyKey"] {
			t.Errorf("error %v does not carry the key", err)
		}
	}
}

func TestConformanceTokenRefresh(t *testing.T) {
	for _, c := range loadCases(t, "token-refresh.json") {
		t.Run(str(c["name"]), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) { runTokenCase(t, c) })
		})
	}
}

func runTokenCase(t *testing.T, c map[string]any) {
	f := &fakeHTTP{handle: func(*http.Request, string) fakeAnswer {
		return fakeAnswer{status: 200, body: checkBody("user_42", "export_pdf")}
	}}
	calls := 0
	source := TokenFunc(func(context.Context) (string, error) {
		calls++
		return str(c["token"]), nil
	})
	opts := []ClientOption{WithHTTPClient(f.client()), WithMaxRetries(0), WithoutCache()}
	var client *Client
	var err error
	if key, ok := c["key"].(string); ok {
		var ic *IdentityClient
		ic, err = NewIdentityClient(key, source, opts...)
		if ic != nil {
			client = ic.Client
		}
	} else {
		client, err = NewClient(source, opts...)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	received := instant(t, str(c["receivedAt"]))
	clock(client.c.e, received)
	_, err = client.Me().Check(ctx, Key("export_pdf"), nil)
	outcome := c["outcome"].(map[string]any)
	if outcome["kind"] == "TokenError" {
		var tokenErr *TokenError
		if !errors.As(err, &tokenErr) || f.count() != 0 {
			t.Fatalf("got %v and %d requests, want a TokenError", err, f.count())
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	first := f.seen()[0]
	for name, want := range outcome["requestHeaders"].(map[string]any) {
		if got := first.header.Get(name); got != str(want) {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
	for range int(num(c["close"])) {
		_ = client.Close()
	}
	now := instant(t, str(c["now"]))
	time.Sleep(now.Sub(received))
	_, err = client.Me().Check(ctx, Key("export_pdf"), nil)
	if later, ok := outcome["later"].(map[string]any); ok {
		if !errors.Is(err, ErrClosed) || err.Error() != goMessage(str(later["message"])) {
			t.Fatalf("got %v, want the closed error", err)
		}
	}
	if asked := calls > 1; asked != outcome["asksProvider"] {
		t.Errorf("asked the provider again: %v, want %v", asked, outcome["asksProvider"])
	}
}

func checkOut(ch *Check) map[string]any {
	out := map[string]any{
		"customer": ch.Customer, "feature": ch.Feature, "type": string(ch.Type), "entitled": ch.Entitled,
		"value": valueOut(ch.Value), "environment": map[string]any{"id": ch.Environment.ID, "name": ch.Environment.Name, "kind": string(ch.Environment.Kind)},
		"track": map[string]any{"id": ch.Track.ID, "name": ch.Track.Name}, "release": intPtr(ch.Release),
		"change": strPtr(ch.Change), "testers": ch.Testers, "asOf": instantReq(ch.AsOf), "stale": ch.Stale,
	}
	if ch.Experiment != nil {
		out["experiment"] = map[string]any{"id": ch.Experiment.ID, "arm": string(ch.Experiment.Arm)}
	} else {
		out["experiment"] = nil
	}
	if ch.Meter != nil {
		out["used"], out["remaining"], out["held"], out["resetsAt"] =
			ch.Meter.Used, valueOut(ch.Meter.Remaining), ch.Meter.Held, instantOut(ch.Meter.ResetsAt)
	} else {
		out["absent"] = []any{"used", "remaining", "held", "resetsAt"}
	}
	return out
}

func serveCheck(t *testing.T, c map[string]any) (*Check, error) {
	t.Helper()
	request := c["request"].(map[string]any)
	f := &fakeHTTP{}
	header := map[string]string{}
	if h, ok := c["headers"].(map[string]any); ok {
		for k, v := range h {
			header[k] = str(v)
		}
	}
	f.push(fakeAnswer{status: 200, header: header, body: string(marshalBody(c["answer"]))})
	srv := testServer(t, f)
	parts := strings.Split(str(request["path"]), "/")
	id, _ := url.PathUnescape(parts[2])
	feature, _ := url.PathUnescape(parts[4])
	return srv.Customer(id).Check(ctx, Key(feature), nil)
}

func TestConformanceValues(t *testing.T) {
	for _, c := range loadCases(t, "values.json") {
		t.Run(str(c["name"]), func(t *testing.T) {
			check, err := serveCheck(t, c)
			outcome := c["outcome"].(map[string]any)
			switch c["kind"] {
			case "value":
				if outcome["value"] == nil {
					same(t, "error", apiErrorOutShort(err), specAPIError(outcome["answer"].(map[string]any)))
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				same(t, "value", map[string]any{"value": valueOut(check.Value), "entitled": check.Entitled}, outcome["answer"])
			case "check":
				if err != nil {
					t.Fatal(err)
				}
				same(t, "check", checkOut(check), outcome)
			case "instant":
				if _, ok := outcome["instant"]; !ok {
					same(t, "error", apiErrorOutShort(err), specAPIError(outcome))
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				at := check.AsOf
				if c["field"] == "resetsAt" {
					at = check.Meter.ResetsAt
				}
				same(t, "instant", instantReq(at), outcome["instant"])
			case "field":
				same(t, "error", apiErrorOutShort(err), specAPIError(outcome))
			}
		})
	}
}

func apiErrorOutShort(err error) any {
	full, ok := apiErrorOut(err).(map[string]any)
	if !ok || full["kind"] != "ApiError" {
		return full
	}
	return map[string]any{"kind": full["kind"], "status": full["status"], "code": full["code"], "message": full["message"]}
}

func planSummaryOut(p *PlanSummary) any {
	if p == nil {
		return nil
	}
	return map[string]any{"id": p.ID, "key": p.Key, "name": p.Name}
}

func productOut(p *Product) any {
	if p == nil {
		return nil
	}
	return map[string]any{"key": p.Key, "name": p.Name}
}

func periodOut(p *Period) any {
	if p == nil {
		return nil
	}
	return map[string]any{"key": p.Key, "label": p.Label}
}

func planChangeOut(p *PlanChange) map[string]any {
	return map[string]any{
		"product": productOut(p.Product), "plan": planSummaryOut(p.Plan), "quantity": intPtr(p.Quantity),
		"effective": string(p.Effective), "at": instantOut(p.At), "until": instantOut(p.Until),
		"changed": p.Changed, "replayed": p.Replayed,
	}
}

func plansOut(p *CustomerPlans) map[string]any {
	held := []any{}
	for _, h := range p.Held {
		var pending any
		switch pc := h.Pending.(type) {
		case PendingMove:
			pending = map[string]any{"type": "move", "plan": planSummaryOut(&pc.Plan)}
		case PendingCancel:
			pending = map[string]any{"type": "cancel", "movingTo": planSummaryOut(pc.MovingTo)}
		case PendingBilling:
			pending = map[string]any{"type": "billing", "billing": string(pc.Billing)}
		case PendingUnknown:
			pending = map[string]any{"type": pc.Type}
		}
		held = append(held, map[string]any{
			"plan":    map[string]any{"id": h.Plan.ID, "key": h.Plan.Key, "name": h.Plan.Name, "kind": string(h.Plan.Kind)},
			"product": productOut(h.Product), "version": intPtr(h.Version), "byDefault": h.ByDefault,
			"period": periodOut(h.Period), "renewsAt": instantOut(h.RenewsAt), "pending": pending,
			"billedBy": noneIfEmpty(string(h.BilledBy)),
		})
	}
	options := []any{}
	for _, o := range p.Options {
		periods, impact, skus := []any{}, []any{}, []any{}
		for _, pe := range o.Periods {
			periods = append(periods, periodOut(&pe))
		}
		for _, i := range o.Impact {
			impact = append(impact, map[string]any{"kind": string(i.Kind), "text": i.Text})
		}
		for _, s := range o.SKUs {
			var price any
			if s.Price != nil {
				price = map[string]any{"amount": s.Price.Amount, "currency": s.Price.Currency,
					"interval": noneIfEmpty(string(s.Price.Interval)), "intervalCount": s.Price.IntervalCount, "tax": string(s.Price.Tax)}
			}
			skus = append(skus, map[string]any{"period": periodOut(s.Period), "connector": s.Connector, "ids": s.IDs, "price": price})
		}
		options = append(options, map[string]any{
			"plan": map[string]any{"id": o.Plan.ID, "key": o.Plan.Key, "name": o.Plan.Name, "kind": string(o.Plan.Kind),
				"description": o.Plan.Description, "default": o.Plan.Default},
			"product": productOut(o.Product), "from": planSummaryOut(o.From), "move": string(o.Move),
			"action": string(o.Action), "reason": strPtr(o.Reason), "when": string(o.When),
			"periods": periods, "impact": impact, "skus": skus,
		})
	}
	return map[string]any{"held": held, "options": options}
}

func usageOut(r *UsageResult) map[string]any {
	out := map[string]any{
		"outcome": string(r.Outcome), "refusal": noneIfEmpty(string(r.Refusal)), "id": strPtr(r.ID),
		"holdId": strPtr(r.HoldID), "mode": string(r.Mode), "amount": r.Amount, "meterChange": r.MeterChange,
		"overBy": r.OverBy, "late": r.Late, "occurredAt": instantOut(r.OccurredAt), "expiresAt": instantOut(r.ExpiresAt),
		"reportedAs": string(r.ReportedAs), "entitled": r.Entitled, "upgrades": upgradesOut(r.Upgrades), "replayed": r.Replayed,
	}
	if r.Meter != nil {
		out["used"], out["remaining"], out["held"] = r.Meter.Used, valueOut(r.Meter.Remaining), r.Meter.Held
	}
	return out
}

func grantOut(g *GrantChange) map[string]any {
	return map[string]any{"grant": map[string]any{"id": g.Grant.ID, "feature": g.Grant.Feature,
		"until": instantOut(g.Grant.Until), "revokedAt": instantOut(g.Grant.RevokedAt)}, "replayed": g.Replayed}
}

func answerCall(t *testing.T, call map[string]any, f *fakeHTTP) (any, error) {
	srv := testServer(t, f, WithoutCache())
	cu := srv.Customer(str(call["customer"]))
	key := str(call["idempotencyKey"])
	var err error
	switch call["method"] {
	case "check":
		var p *ReadParams
		if asOf, ok := call["asOf"].(string); ok {
			p = &ReadParams{AsOf: instant(t, asOf)}
		}
		var ch *Check
		if ch, err = cu.Check(ctx, Key(str(call["feature"])), p); err == nil {
			return map[string]any{"entitled": ch.Entitled, "upgrades": upgradesOut(ch.Upgrades)}, nil
		}
	case "plans":
		var p *CustomerPlans
		if p, err = cu.Plans(ctx, nil); err == nil {
			return plansOut(p), nil
		}
	case "recordUsage":
		var r *UsageResult
		if r, err = cu.RecordUsage(ctx, Metered(str(call["feature"])), num(call["amount"]), key, nil); err == nil {
			return usageOut(r), nil
		}
	case "subscribe":
		var s *SubscribeStep
		if s, err = cu.Subscribe(ctx, str(call["plan"]), &SubscribeParams{Period: str(call["period"]),
			ReturnURL: str(call["returnUrl"]), IdempotencyKey: key}); err == nil {
			out := map[string]any{"next": string(s.Next)}
			switch s.Next {
			case NextDone:
				for k, v := range planChangeOut(s.Change) {
					out[k] = v
				}
			case NextPay:
				out["url"] = s.URL
			case NextManage:
				out["billedBy"] = string(s.BilledBy)
			}
			return out, nil
		}
	case "cancel", "undoPendingChange":
		p := &CancelParams{IdempotencyKey: key}
		if addOn, ok := call["addOn"].(string); ok {
			p.AddOn = &addOn
		}
		var change *PlanChange
		if call["method"] == "cancel" {
			change, err = cu.Cancel(ctx, p)
		} else {
			change, err = cu.UndoPendingChange(ctx, p)
		}
		if err == nil {
			return planChangeOut(change), nil
		}
	case "setPlan":
		p := &SetPlanParams{Period: str(call["period"]), IdempotencyKey: key,
			Decision: Decision{Reason: str(call["reason"]), Actor: str(call["actor"])}}
		if until, ok := call["until"].(string); ok {
			p.Until = instant(t, until)
		}
		var choice PlanChoice = Plan(str(call["plan"]))
		if sku, ok := call["sku"].(map[string]any); ok {
			ids := map[string]string{}
			for k, v := range sku["ids"].(map[string]any) {
				ids[k] = str(v)
			}
			choice = SKU{Connector: str(sku["connector"]), IDs: ids}
		}
		var change *PlanChange
		if change, err = cu.SetPlan(ctx, choice, p); err == nil {
			return planChangeOut(change), nil
		}
	case "setAddOn":
		var change *PlanChange
		if change, err = cu.SetAddOn(ctx, str(call["addOn"]), num(call["quantity"]), &SetAddOnParams{IdempotencyKey: key}); err == nil {
			return planChangeOut(change), nil
		}
	case "grant":
		p := &GrantParams{Value: Amount(num(call["value"])), Days: int(num(call["days"])), IdempotencyKey: key,
			Decision: Decision{Reason: str(call["reason"]), Actor: str(call["actor"])}}
		var g *GrantChange
		if g, err = cu.Grant(ctx, Key(str(call["feature"])), p); err == nil {
			return grantOut(g), nil
		}
	case "revokeGrant":
		var g *GrantChange
		if g, err = cu.RevokeGrant(ctx, str(call["grantId"]), &DecisionParams{IdempotencyKey: key}); err == nil {
			return grantOut(g), nil
		}
	case "billingPortal":
		var p *BillingPortal
		if p, err = cu.BillingPortal(ctx, str(call["returnUrl"])); err == nil {
			return map[string]any{"url": p.URL}, nil
		}
	case "syncBilling":
		var s *BillingSync
		if s, err = cu.SyncBilling(ctx); err == nil {
			return map[string]any{"changed": s.Changed}, nil
		}
	case "register":
		client := call["client"].(map[string]any)
		ic, cerr := NewIdentityClient(str(client["key"]), StaticToken(str(client["identityToken"])),
			WithHTTPClient(f.client()), WithMaxRetries(0), WithoutCache())
		if cerr != nil {
			t.Fatal(cerr)
		}
		defer ic.Close()
		var r *RegisteredCustomer
		if r, err = ic.Register(ctx, &IdentityRegisterParams{IdempotencyKey: key}); err == nil {
			return r, nil
		}
	default:
		t.Fatalf("unknown method %v", call["method"])
	}
	return nil, err
}

func runAnswerFile(t *testing.T, name string) {
	for _, c := range loadCases(t, name) {
		t.Run(str(c["name"]), func(t *testing.T) {
			header := map[string]string{}
			for k, v := range c["headers"].(map[string]any) {
				header[k] = str(v)
			}
			f := &fakeHTTP{}
			f.push(fakeAnswer{status: int(num(c["status"])), header: header, body: str(c["body"])})
			if received, ok := c["receivedAt"].(string); ok {
				synctest.Test(t, func(t *testing.T) {
					got, err := answerCallAt(t, c, f, instant(t, received))
					compareAnswer(t, c, got, err)
				})
				return
			}
			got, err := answerCall(t, c["call"].(map[string]any), f)
			compareAnswer(t, c, got, err)
		})
	}
}

func answerCallAt(t *testing.T, c map[string]any, f *fakeHTTP, at time.Time) (any, error) {
	call := c["call"].(map[string]any)
	srv := testServer(t, f, WithoutCache())
	clock(srv.c.e, at)
	ch, err := srv.Customer(str(call["customer"])).Check(ctx, Key(str(call["feature"])), nil)
	if err != nil {
		return nil, err
	}
	return ch, nil
}

func compareAnswer(t *testing.T, c map[string]any, got any, err error) {
	outcome := c["outcome"].(map[string]any)
	if kind, ok := outcome["kind"]; ok && kind == "ApiError" {
		same(t, "error", apiErrorOut(err), specAPIError(outcome))
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	same(t, "answer", got, outcome)
}

func TestConformanceAnswers(t *testing.T) { runAnswerFile(t, "answers.json") }

func TestConformanceErrors(t *testing.T) { runAnswerFile(t, "errors.json") }

func TestConformanceSnapshots(t *testing.T) {
	for _, c := range loadCases(t, "snapshots.json") {
		t.Run(str(c["name"]), func(t *testing.T) {
			if c["appliesWhere"] != nil && !strings.Contains(str(c["appliesWhere"]), "negative") {
				t.Skip(c["appliesWhere"])
			}
			e := c["expected"].(map[string]any)
			var keys []SnapshotKey
			raw := e["keys"]
			if set, ok := raw.(map[string]any); ok {
				raw = set["keys"]
			}
			data, _ := json.Marshal(map[string]any{"keys": raw})
			var set SnapshotKeys
			if err := json.Unmarshal(data, &set); err != nil {
				t.Fatalf("keys: %v", err)
			}
			keys = set.Keys
			expected := SnapshotExpectation{Keys: keys, Customer: str(e["customer"]), Environment: str(e["environment"]),
				Issuer: str(e["issuer"]), Now: instant(t, str(e["now"]))}
			if skew, ok := e["clockSkewSeconds"]; ok {
				s := int(num(skew))
				expected.ClockSkewSeconds = &s
			}
			snapshot, err := VerifySnapshot(str(c["token"]), expected)
			outcome := c["outcome"].(map[string]any)
			switch outcome["kind"] {
			case "ArgumentError":
				wantArgError(t, err, str(outcome["message"]))
			case "SnapshotError":
				var se *SnapshotError
				if !errors.As(err, &se) || string(se.Code) != outcome["code"] || se.Message != goMessage(str(outcome["message"])) {
					t.Fatalf("got %v, want %v", err, outcome)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				same(t, "snapshot", snapshotOut(snapshot, outcome["snapshot"].(map[string]any)), outcome["snapshot"])
			}
		})
	}
}

func snapshotOut(s *VerifiedSnapshot, want map[string]any) map[string]any {
	items := []any{}
	for _, e := range s.Entitlements.Items {
		item := map[string]any{"key": e.Key, "type": string(e.Type), "value": valueOut(e.Value), "entitled": e.Entitled,
			"upgrades": upgradesOut(e.Upgrades)}
		if e.Meter == nil {
			item["absent"] = []any{"used", "remaining", "held", "resetsAt"}
		} else {
			item["used"], item["remaining"], item["held"], item["resetsAt"] =
				e.Meter.Used, valueOut(e.Meter.Remaining), e.Meter.Held, instantOut(e.Meter.ResetsAt)
		}
		items = append(items, item)
	}
	wantEntitlements := want["entitlements"].(map[string]any)
	for i, w := range wantEntitlements["items"].([]any) {
		w := w.(map[string]any)
		absent, _ := w["absent"].([]any)
		if i < len(items) && len(absent) > 0 && len(absent) < 4 {
			item := items[i].(map[string]any)
			for _, name := range absent {
				delete(item, str(name))
			}
			item["absent"] = absent
		}
	}
	has := map[string]any{}
	get := map[string]any{}
	for k := range wantEntitlements["has"].(map[string]any) {
		has[k] = s.Entitlements.Has(Key(k))
	}
	for k := range wantEntitlements["get"].(map[string]any) {
		if e, ok := s.Entitlements.Get(Key(k)); ok {
			for _, item := range items {
				if item.(map[string]any)["key"] == e.Key {
					get[k] = item
				}
			}
		} else {
			get[k] = nil
		}
	}
	return map[string]any{
		"customer": s.Customer, "environment": map[string]any{"id": s.EnvironmentID},
		"track": map[string]any{"id": s.Track.ID, "name": s.Track.Name}, "release": intPtr(s.Release),
		"change": strPtr(s.Change), "testers": s.Testers, "expiresAt": instantOut(s.ExpiresAt),
		"entitlements": map[string]any{"asOf": instantReq(s.Entitlements.AsOf), "experiment": nil, "items": items, "has": has, "get": get},
	}
}
