package entitler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/entitlerhq/entitler-go"
	"github.com/entitlerhq/entitler-go/entitlertest"
)

var bg = context.Background()

type recorded struct {
	method, path, query string
	header              http.Header
	body                map[string]any
	rawBody             string
}

// api answers every route with a complete body from the answer builders and records requests.
type api struct {
	mu       sync.Mutex
	requests []recorded
}

func (a *api) RoundTrip(req *http.Request) (*http.Response, error) {
	var raw []byte
	if req.Body != nil {
		raw, _ = io.ReadAll(req.Body)
	}
	r := recorded{method: req.Method, path: req.URL.EscapedPath(), query: req.URL.RawQuery, header: req.Header.Clone(), rawBody: string(raw)}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &r.body)
	}
	a.mu.Lock()
	a.requests = append(a.requests, r)
	a.mu.Unlock()
	status, body := route(req.Method, req.URL.EscapedPath())
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
}

func (a *api) last() recorded {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.requests[len(a.requests)-1]
}

func route(method, path string) (int, []byte) {
	now := time.Now()
	held := "hold_1"
	switch {
	case path == "/keys/self":
		return 200, entitlertest.Scopes(entitler.Scopes{List: []entitler.Scope{entitler.ScopePlansRead}})
	case path == "/customers/snapshot-keys":
		return 200, entitlertest.SnapshotKeys(entitler.SnapshotKeys{})
	case path == "/pricing":
		return 200, entitlertest.Pricing(entitler.Pricing{})
	case path == "/pricing/features":
		return 200, entitlertest.FeatureList(entitler.FeatureList{})
	case path == "/usage/events":
		return 200, entitlertest.BatchResult(entitler.BatchResult{Results: []entitler.BatchEventResult{{}}})
	case path == "/customers" && method == "GET":
		return 200, entitlertest.CustomerPage(entitler.CustomerPage{})
	case path == "/customers" && method == "POST":
		return 201, entitlertest.CustomerDetail(entitler.CustomerDetail{})
	}
	parts := strings.Split(strings.TrimPrefix(path, "/customers/"), "/")
	rest := strings.Join(parts[1:], "/")
	switch {
	case rest == "" && method == "GET":
		return 200, entitlertest.CustomerDetail(entitler.CustomerDetail{})
	case rest == "" && method == "PUT":
		return 200, entitlertest.RegisteredCustomer(entitler.RegisteredCustomer{})
	case rest == "" && method == "PATCH":
		return 200, entitlertest.CustomerSummary(entitler.CustomerSummary{})
	case rest == "" && method == "DELETE":
		return 204, nil
	case strings.HasPrefix(rest, "entitlements/"):
		return 200, entitlertest.Check(entitler.Check{Feature: parts[len(parts)-1]})
	case rest == "entitlements":
		return 200, entitlertest.Entitlements(entitler.Entitlements{})
	case rest == "plans":
		return 200, entitlertest.CustomerPlans(entitler.CustomerPlans{})
	case rest == "pricing":
		return 200, entitlertest.Pricing(entitler.Pricing{})
	case rest == "usage" && method == "GET":
		return 200, entitlertest.CustomerUsage(entitler.CustomerUsage{})
	case rest == "billing":
		return 200, entitlertest.CustomerBilling(entitler.CustomerBilling{})
	case rest == "usage/holds":
		return 200, entitlertest.UsageResult(entitler.UsageResult{Outcome: entitler.OutcomeHeld, HoldID: &held, Amount: 5, ExpiresAt: now.Add(time.Hour), Check: entitler.Check{AsOf: now}})
	case strings.HasPrefix(rest, "usage") || strings.HasPrefix(rest, "meters"):
		return 200, entitlertest.UsageResult(entitler.UsageResult{})
	case rest == "subscription" && method == "POST":
		return 200, entitlertest.SubscribeStep(entitler.SubscribeStep{})
	case strings.HasPrefix(rest, "subscription") || rest == "plan" || strings.HasPrefix(rest, "add-ons"):
		return 200, entitlertest.PlanChange(entitler.PlanChange{})
	case strings.HasPrefix(rest, "grants"):
		return 200, entitlertest.GrantChange(entitler.GrantChange{})
	case rest == "tokens":
		return 201, entitlertest.IssuedCustomerToken(entitler.IssuedCustomerToken{})
	case rest == "snapshots":
		return 201, entitlertest.IssuedSnapshot(entitler.IssuedSnapshot{})
	case rest == "track":
		return 200, entitlertest.CustomerTrack(entitler.CustomerTrack{})
	case rest == "billing-portal":
		return 200, entitlertest.BillingPortal(entitler.BillingPortal{})
	case rest == "billing/sync":
		return 200, entitlertest.BillingSync(entitler.BillingSync{})
	}
	return 404, entitlertest.Error(entitler.CodeNotFound, "No route.")
}

func newAPI(t *testing.T) (*api, *entitler.Server) {
	a := &api{}
	srv, err := entitler.NewServer("ent_test_key", entitler.WithHTTPClient(&http.Client{Transport: a}), entitler.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return a, srv
}

func TestEveryMethodsRequest(t *testing.T) {
	at := time.Date(2026, 7, 1, 19, 30, 0, 0, time.FixedZone("AEST", 10*3600))
	sku := &entitler.SKU{Connector: "apple", IDs: map[string]string{"productId": "pro"}}
	cases := []struct {
		name   string
		call   func(c *entitler.ServerCustomer, s *entitler.Server) error
		method string
		path   string
		query  string
		body   string
		key    string
	}{
		{"register with nothing", func(c *entitler.ServerCustomer, _ *entitler.Server) error { _, err := c.Register(bg, nil); return err },
			"PUT", "/customers/user%2F42", "", "", "uuid"},
		{"register with details", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.Register(bg, &entitler.RegisterParams{Name: new("Ada"), Email: new(""), Metadata: map[string]*string{"plan": new("x"), "old": nil},
				Visitor: "visitor_0123456789abcdef", IdempotencyKey: "signup-1"})
			return err
		}, "PUT", "/customers/user%2F42", "", `{"email":"","metadata":{"old":null,"plan":"x"},"name":"Ada"}`, "signup-1"},
		{"update", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.Update(bg, &entitler.UpdateParams{Metadata: map[string]*string{"gone": nil}})
			return err
		}, "PATCH", "/customers/user%2F42", "", `{"metadata":{"gone":null}}`, "uuid"},
		{"erase", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			return c.Erase(bg, &entitler.EraseParams{IdempotencyKey: "gdpr-9"})
		},
			"DELETE", "/customers/user%2F42", "erase=true", "", "gdpr-9"},
		{"details as of", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.Details(bg, &entitler.DetailsParams{Cursor: "c/2", AsOf: at})
			return err
		}, "GET", "/customers/user%2F42", "cursor=c%2F2", "", ""},
		{"token", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.Token(bg, &entitler.TokenParams{Scopes: []entitler.Scope{entitler.ScopeUsageWrite, entitler.ScopeBillingSelf}, TTL: time.Hour})
			return err
		}, "POST", "/customers/user%2F42/tokens", "", `{"scopes":["usage:write","billing:self"],"ttlSeconds":3600}`, "uuid"},
		{"token default", func(c *entitler.ServerCustomer, _ *entitler.Server) error { _, err := c.Token(bg, nil); return err },
			"POST", "/customers/user%2F42/tokens", "", `{}`, "uuid"},
		{"set track", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.SetTrack(bg, new("Beta"), nil)
			return err
		},
			"PUT", "/customers/user%2F42/track", "", `{"track":"Beta"}`, "uuid"},
		{"set track none", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.SetTrack(bg, nil, nil)
			return err
		},
			"PUT", "/customers/user%2F42/track", "", `{"track":null}`, "uuid"},
		{"billing", func(c *entitler.ServerCustomer, _ *entitler.Server) error { _, err := c.Billing(bg); return err },
			"GET", "/customers/user%2F42/billing", "", "", ""},
		{"record usage", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.RecordUsage(bg, entitler.Metered("ai_credits"), 5, "msg-1", &entitler.RecordUsageParams{Mode: entitler.Observe, OccurredAt: at, Register: true})
			return err
		}, "POST", "/customers/user%2F42/usage", "", `{"amount":5,"feature":"ai_credits","mode":"observe","occurredAt":"2026-07-01T09:30:00.000Z","register":true}`, "msg-1"},
		{"hold usage", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.HoldUsage(bg, entitler.Metered("ai_credits"), 5, "msg-2", &entitler.HoldParams{TTL: time.Minute})
			return err
		}, "POST", "/customers/user%2F42/usage/holds", "", `{"amount":5,"feature":"ai_credits","ttlSeconds":60}`, "msg-2"},
		{"settle", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.SettleUsage(bg, "h/1", 0, nil)
			return err
		},
			"POST", "/customers/user%2F42/usage/holds/h%2F1/settle", "", `{"amount":0}`, "uuid"},
		{"release", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.ReleaseUsage(bg, "h1", &entitler.ReleaseUsageParams{IdempotencyKey: "r1"})
			return err
		}, "DELETE", "/customers/user%2F42/usage/holds/h1", "", "", "r1"},
		{"usage page", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.Usage(bg, &entitler.UsageParams{Cursor: "n 2"})
			return err
		}, "GET", "/customers/user%2F42/usage", "cursor=n%202", "", ""},
		{"snapshot", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.Snapshot(bg, &entitler.SnapshotParams{TTL: 24 * time.Hour})
			return err
		}, "POST", "/customers/user%2F42/snapshots", "", `{"ttlSeconds":86400}`, "uuid"},
		{"snapshot default", func(c *entitler.ServerCustomer, _ *entitler.Server) error { _, err := c.Snapshot(bg, nil); return err },
			"POST", "/customers/user%2F42/snapshots", "", `{}`, "uuid"},
		{"subscribe", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.Subscribe(bg, "sso_addon", &entitler.SubscribeParams{Period: "monthly", Quantity: 3, ReturnURL: "https://app/x", Register: true, IdempotencyKey: "up-1"})
			return err
		}, "POST", "/customers/user%2F42/subscription", "", `{"period":"monthly","plan":"sso_addon","quantity":3,"register":true,"returnUrl":"https://app/x"}`, "up-1"},
		{"cancel", func(c *entitler.ServerCustomer, _ *entitler.Server) error { _, err := c.Cancel(bg, nil); return err },
			"DELETE", "/customers/user%2F42/subscription", "", "", "uuid"},
		{"cancel product", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.Cancel(bg, &entitler.CancelParams{Product: new("app")})
			return err
		}, "DELETE", "/customers/user%2F42/subscription", "product=app", "", "uuid"},
		{"cancel add-on", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.Cancel(bg, &entitler.CancelParams{AddOn: new("sso_addon")})
			return err
		}, "DELETE", "/customers/user%2F42/subscription/add-ons/sso_addon", "", "", "uuid"},
		{"undo", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.UndoPendingChange(bg, nil)
			return err
		},
			"DELETE", "/customers/user%2F42/subscription/pending", "", "", "uuid"},
		{"undo add-on", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.UndoPendingChange(bg, &entitler.CancelParams{AddOn: new("sso_addon")})
			return err
		}, "DELETE", "/customers/user%2F42/subscription/add-ons/sso_addon/pending", "", "", "uuid"},
		{"billing portal", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.BillingPortal(bg, "https://app/b")
			return err
		},
			"POST", "/customers/user%2F42/billing-portal", "", `{"returnUrl":"https://app/b"}`, "uuid"},
		{"sync billing", func(c *entitler.ServerCustomer, _ *entitler.Server) error { _, err := c.SyncBilling(bg); return err },
			"POST", "/customers/user%2F42/billing/sync", "", "", "uuid"},
		{"set plan", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.SetPlan(bg, entitler.Plan("enterprise"), &entitler.SetPlanParams{Period: "yearly", When: entitler.WhenEnd, Billing: entitler.BillingEnd,
				Until: at, Version: entitler.VersionLatest, Register: true, Decision: entitler.Decision{Reason: "Deal", Actor: "hubspot"}, IdempotencyKey: "deal-1"})
			return err
		}, "PUT", "/customers/user%2F42/plan", "", `{"actor":"hubspot","billing":"end","period":"yearly","plan":"enterprise","reason":"Deal","register":true,"until":"2026-07-01T09:30:00.000Z","version":"latest","when":"end"}`, "deal-1"},
		{"set plan sku", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.SetPlan(bg, *sku, &entitler.SetPlanParams{Until: at})
			return err
		}, "PUT", "/customers/user%2F42/plan", "", `{"sku":{"connector":"apple","ids":{"productId":"pro"}},"until":"2026-07-01T09:30:00.000Z"}`, "uuid"},
		{"end plan", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.SetPlan(bg, nil, &entitler.SetPlanParams{Product: "app", When: entitler.WhenNow})
			return err
		}, "PUT", "/customers/user%2F42/plan", "", `{"plan":null,"product":"app","when":"now"}`, "uuid"},
		{"set add-on", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.SetAddOn(bg, "support_premium", 1, &entitler.SetAddOnParams{Replaces: "support_standard", SKU: sku})
			return err
		}, "PUT", "/customers/user%2F42/add-ons/support_premium", "", `{"quantity":1,"replaces":"support_standard","sku":{"connector":"apple","ids":{"productId":"pro"}}}`, "uuid"},
		{"remove add-on", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.SetAddOn(bg, "sso", 0, nil)
			return err
		},
			"PUT", "/customers/user%2F42/add-ons/sso", "", `{"quantity":0}`, "uuid"},
		{"grant on", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.Grant(bg, entitler.OnOff("sso"), nil)
			return err
		},
			"POST", "/customers/user%2F42/grants", "", `{"feature":"sso"}`, "uuid"},
		{"grant amount", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.Grant(bg, entitler.Config("seats"), &entitler.GrantParams{Value: entitler.Amount(0), Days: 3650, Decision: entitler.Decision{Reason: "r"}})
			return err
		}, "POST", "/customers/user%2F42/grants", "", `{"days":3650,"feature":"seats","reason":"r","value":"0"}`, "uuid"},
		{"grant unlimited", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.Grant(bg, entitler.Key("seats"), &entitler.GrantParams{Value: entitler.Unlimited{}})
			return err
		}, "POST", "/customers/user%2F42/grants", "", `{"feature":"seats","value":"unlimited"}`, "uuid"},
		{"revoke grant", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.RevokeGrant(bg, "grt_1", &entitler.DecisionParams{Decision: entitler.Decision{Actor: "agent"}})
			return err
		}, "DELETE", "/customers/user%2F42/grants/grt_1", "", `{"actor":"agent"}`, "uuid"},
		{"adjust by", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.AdjustMeter(bg, entitler.Metered("ai_credits"), &entitler.AdjustMeterParams{By: new(int64(-500)), IdempotencyKey: "t-1"})
			return err
		}, "POST", "/customers/user%2F42/meters/ai_credits/adjustments", "", `{"by":-500}`, "t-1"},
		{"adjust to", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.AdjustMeter(bg, entitler.Metered("ai_credits"), &entitler.AdjustMeterParams{To: new(int64(0)), IdempotencyKey: "t-2", Decision: entitler.Decision{Reason: "r"}})
			return err
		}, "POST", "/customers/user%2F42/meters/ai_credits/adjustments", "", `{"reason":"r","to":0}`, "t-2"},
		{"cancel usage", func(c *entitler.ServerCustomer, _ *entitler.Server) error {
			_, err := c.CancelUsage(bg, "use_1", nil)
			return err
		},
			"DELETE", "/customers/user%2F42/usage/use_1", "", "", "uuid"},
		{"list", func(_ *entitler.ServerCustomer, s *entitler.Server) error {
			_, err := s.Customers.Page(bg, &entitler.ListCustomersParams{Q: "ada lovelace", Cohort: "3", Track: "trk", IncludeTest: true, Cursor: "c"})
			return err
		}, "GET", "/customers", "q=ada%20lovelace&cohort=3&track=trk&cursor=c&includeTest=true", "", ""},
		{"list plain", func(_ *entitler.ServerCustomer, s *entitler.Server) error {
			_, err := s.Customers.Page(bg, &entitler.ListCustomersParams{IncludeTest: false})
			return err
		}, "GET", "/customers", "", "", ""},
		{"create", func(_ *entitler.ServerCustomer, s *entitler.Server) error {
			_, err := s.Customers.Create(bg, entitler.CreateCustomerParams{ExternalID: "x", Name: "X", Plan: "pro", Metadata: map[string]string{"a": "b"}})
			return err
		}, "POST", "/customers", "", `{"externalId":"x","metadata":{"a":"b"},"name":"X","plan":"pro"}`, "uuid"},
		{"batch", func(_ *entitler.ServerCustomer, s *entitler.Server) error {
			_, err := s.RecordUsageBatch(bg, []entitler.BatchEvent{{Customer: "u", Feature: entitler.Metered("ai"), Amount: 2, IdempotencyKey: "e1", OccurredAt: at}}, &entitler.RecordUsageBatchParams{Register: true})
			return err
		}, "POST", "/usage/events", "", `{"events":[{"amount":2,"customer":"u","feature":"ai","idempotencyKey":"e1","occurredAt":"2026-07-01T09:30:00.000Z"}],"register":true}`, "batch"},
		{"features", func(_ *entitler.ServerCustomer, s *entitler.Server) error { _, err := s.Features(bg); return err },
			"GET", "/pricing/features", "", "", ""},
		{"scopes", func(_ *entitler.ServerCustomer, s *entitler.Server) error { _, err := s.Scopes(bg); return err },
			"GET", "/keys/self", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, srv := newAPI(t)
			if err := c.call(srv.Customer("user/42"), srv); err != nil {
				t.Fatal(err)
			}
			r := a.last()
			if r.method != c.method || r.path != c.path || r.query != c.query {
				t.Errorf("%s %s?%s, want %s %s?%s", r.method, r.path, r.query, c.method, c.path, c.query)
			}
			if c.body == "" {
				if r.rawBody != "" || r.header.Get("Content-Type") != "" {
					t.Errorf("sent a body %q", r.rawBody)
				}
			} else {
				var want map[string]any
				_ = json.Unmarshal([]byte(c.body), &want)
				if !reflect.DeepEqual(r.body, want) || r.header.Get("Content-Type") != "application/json" {
					t.Errorf("body %s, want %s", r.rawBody, c.body)
				}
			}
			key := r.header.Get("Idempotency-Key")
			switch c.key {
			case "":
				if key != "" {
					t.Errorf("a GET sent a key")
				}
			case "uuid":
				if len(key) != 36 {
					t.Errorf("key %q", key)
				}
			case "batch":
				if !strings.HasPrefix(key, "batch:") {
					t.Errorf("key %q", key)
				}
			default:
				if key != c.key {
					t.Errorf("key %q, want %q", key, c.key)
				}
			}
			if strings.Contains(c.name, "as of") && r.header.Get("Entitler-As-Of") != "2026-07-01T09:30:00.000Z" {
				t.Error("as-of header")
			}
			if c.name == "register with details" && r.header.Get("Entitler-Visitor") != "visitor_0123456789abcdef" {
				t.Error("visitor header")
			}
			if r.method != "GET" && r.header.Get("Entitler-As-Of") != "" {
				t.Error("a write sent Entitler-As-Of")
			}
		})
	}
}

func TestArgumentsRefusedBeforeAnyRequest(t *testing.T) {
	a, srv := newAPI(t)
	c := srv.Customer("u")
	blank := srv.Customer(" ")
	credits := entitler.Metered("ai_credits")
	cases := map[string]struct {
		err  error
		want string
	}{}
	add := func(name string, err error, want string) {
		cases[name] = struct {
			err  error
			want string
		}{err, want}
	}
	_, err := blank.Check(bg, entitler.Key("f"), nil)
	add("blank customer", err, "provide the id your app uses for the customer")
	_, err = c.RecordUsage(bg, credits, 0, "k", nil)
	add("amount 0", err, "pass amount as a whole number from 1 to 9007199254740991")
	_, err = c.RecordUsage(bg, credits, 1<<53, "k", nil)
	add("amount too big", err, "pass amount as a whole number from 1 to 9007199254740991")
	_, err = c.RecordUsage(bg, entitler.Metered(""), 1, "k", nil)
	add("blank metered", err, "name the feature by its key")
	_, err = c.RecordUsage(bg, credits, 1, "k", &entitler.RecordUsageParams{OccurredAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)})
	add("occurredAt", err, "pass occurredAt as a valid date")
	_, err = c.HoldUsage(bg, credits, 1, "k", &entitler.HoldParams{TTL: 3601 * time.Second})
	add("ttl", err, "pass the hold's TTL as a whole number of seconds from 1 to 3600")
	_, err = c.HoldUsage(bg, credits, 1, "k", &entitler.HoldParams{TTL: 1500 * time.Millisecond})
	add("ttl fraction", err, "pass the hold's TTL as a whole number of seconds from 1 to 3600")
	_, err = c.SettleUsage(bg, "h", -1, nil)
	add("settle", err, "pass amount as a whole number from 0 to the held amount")
	_, err = c.SettleUsage(bg, "", 1, nil)
	add("hold id", err, "provide the id of the hold")
	_, err = c.SettleUsage(bg, "h", 1, &entitler.SettleUsageParams{IdempotencyKey: " x"})
	add("key with a space", err, "pass idempotencyKey as 1 to 200 printable ASCII characters")
	_, err = c.Subscribe(bg, " ", nil)
	add("plan", err, "name the plan by its id or its key")
	_, err = c.Subscribe(bg, "pro", &entitler.SubscribeParams{Quantity: -1})
	add("quantity", err, "pass quantity as a whole number from 1 to 10000")
	_, err = c.Cancel(bg, &entitler.CancelParams{AddOn: new("")})
	add("blank add-on", err, "name the plan by its id or its key")
	_, err = c.Cancel(bg, &entitler.CancelParams{Product: new(" ")})
	add("blank product", err, "name the product by its key")
	_, err = c.BillingPortal(bg, "")
	add("return url", err, "pass returnURL as the page to send the customer back to")
	_, err = c.SetPlan(bg, nil, nil)
	add("end without product", err, "pass product: the product whose plan ends")
	_, err = c.SetPlan(bg, nil, &entitler.SetPlanParams{Product: "app", Period: "monthly"})
	add("end with period", err, "pass no period, until or version when ending a plan")
	_, err = c.SetPlan(bg, nil, &entitler.SetPlanParams{Product: " "})
	add("end blank product", err, "name the product by its key")
	_, err = c.SetPlan(bg, entitler.Plan("pro"), &entitler.SetPlanParams{Product: "app"})
	add("product with plan", err, "pass product only when ending a plan")
	_, err = c.SetPlan(bg, entitler.SKU{Connector: "apple"}, &entitler.SetPlanParams{Product: "app"})
	add("product with sku", err, "pass product only when ending a plan")
	_, err = c.SetPlan(bg, entitler.Plan(""), nil)
	add("blank set plan", err, "name the plan by its id or its key")
	_, err = c.SetPlan(bg, entitler.Plan("pro"), &entitler.SetPlanParams{Until: time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)})
	add("until", err, "pass until as a valid date")
	_, err = c.SetAddOn(bg, "a", 10001, nil)
	add("add-on quantity", err, "pass quantity as a whole number from 0 to 10000")
	_, err = c.SetAddOn(bg, "a", 1, &entitler.SetAddOnParams{Replaces: " "})
	add("replaces", err, "name the plan by its id or its key")
	_, err = c.Grant(bg, entitler.Key(""), nil)
	add("grant feature", err, "name the feature by its key")
	_, err = c.Grant(bg, entitler.Key("f"), &entitler.GrantParams{Value: entitler.Amount(1_000_000_000)})
	add("grant value", err, "pass value as a whole number from 0 to 999999999, or Unlimited")
	_, err = c.Grant(bg, entitler.Key("f"), &entitler.GrantParams{Days: 3651})
	add("grant days", err, "pass days as a whole number from 0 to 3650")
	_, err = c.RevokeGrant(bg, "", nil)
	add("grant id", err, "provide the id of the grant")
	_, err = c.CancelUsage(bg, "..", nil)
	add("usage id", err, "pass an id that is not made only of dots")
	_, err = c.CancelUsage(bg, "", nil)
	add("blank usage id", err, "provide the id of the usage report")
	adjust := "pass either by, a whole number other than 0, or to, a whole number of 0 or more"
	for name, p := range map[string]*entitler.AdjustMeterParams{
		"neither":  {IdempotencyKey: "k"},
		"both":     {By: new(int64(1)), To: new(int64(1)), IdempotencyKey: "k"},
		"by zero":  {By: new(int64(0)), IdempotencyKey: "k"},
		"to below": {To: new(int64(-1)), IdempotencyKey: "k"},
		"by big":   {By: new(int64(1 << 53)), IdempotencyKey: "k"},
	} {
		_, err = c.AdjustMeter(bg, credits, p)
		add("adjust "+name, err, adjust)
	}
	_, err = c.AdjustMeter(bg, credits, nil)
	add("adjust key", err, "pass idempotencyKey as 1 to 200 printable ASCII characters")
	_, err = c.SetTrack(bg, new(""), nil)
	add("track", err, "name the track by its name")
	_, err = c.Token(bg, &entitler.TokenParams{TTL: 30 * time.Second})
	add("token ttl", err, "pass the token's TTL as a whole number of seconds from 60 to 3600")
	_, err = c.Snapshot(bg, &entitler.SnapshotParams{TTL: time.Second})
	add("snapshot ttl", err, "pass the snapshot's TTL as a whole number of seconds of 60 or more")
	_, err = c.Pricing(bg, &entitler.PricingParams{Visitor: "bad visitor"})
	add("visitor", err, "pass visitor as an id of 16 to 64 letters, numbers, hyphens or underscores")
	_, err = c.Register(bg, &entitler.RegisterParams{Visitor: "x"})
	add("register visitor", err, "pass visitor as an id of 16 to 64 letters, numbers, hyphens or underscores")
	_, err = c.Check(bg, entitler.Key("f"), &entitler.ReadParams{AsOf: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)})
	add("as of", err, "pass asOf as a valid date")
	_, err = srv.Customers.Create(bg, entitler.CreateCustomerParams{})
	add("create", err, "provide the id your app uses for the customer")
	_, err = entitler.WithHold[int](bg, nil, credits, 1, "k", nil, nil)
	add("with hold", err, "pass a customer and work")
	for name, c := range cases {
		if !errors.Is(c.err, entitler.ErrInvalidArgument) || c.err.Error() != c.want {
			t.Errorf("%s: got %v, want %q", name, c.err, c.want)
		}
	}
	if len(a.requests) != 0 {
		t.Errorf("%d requests were sent", len(a.requests))
	}
}

func TestPagedReadsFetchLazily(t *testing.T) {
	pages := map[string][]byte{
		"":   entitlertest.CustomerPage(entitler.CustomerPage{Items: []entitler.CustomerSummary{{ExternalID: "a"}}, Next: "p2"}),
		"p2": entitlertest.CustomerPage(entitler.CustomerPage{Items: []entitler.CustomerSummary{}, Next: "p3"}),
		"p3": entitlertest.CustomerPage(entitler.CustomerPage{Items: []entitler.CustomerSummary{{ExternalID: "b"}, {ExternalID: "c"}}}),
	}
	var asked []string
	rt := roundTripper(func(r *http.Request) (int, []byte) {
		asked = append(asked, r.URL.Query().Get("cursor"))
		return 200, pages[r.URL.Query().Get("cursor")]
	})
	srv, _ := entitler.NewServer("ent_test_k", entitler.WithHTTPClient(&http.Client{Transport: rt}))
	defer srv.Close()
	var ids []string
	for c, err := range srv.Customers.List(bg, nil) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ExternalID)
		if len(ids) == 1 && len(asked) != 1 {
			t.Fatal("a later page was fetched before the loop reached it")
		}
	}
	if !reflect.DeepEqual(ids, []string{"a", "b", "c"}) || !reflect.DeepEqual(asked, []string{"", "p2", "p3"}) {
		t.Fatalf("%v %v", ids, asked)
	}
	asked = nil
	for c := range srv.Customers.List(bg, &entitler.ListCustomersParams{Cursor: "p3"}) {
		_ = c
		break
	}
	if !reflect.DeepEqual(asked, []string{"p3"}) {
		t.Fatalf("cursor starts the iteration: %v", asked)
	}
	failing := roundTripper(func(*http.Request) (int, []byte) { return 404, entitlertest.Error(entitler.CodeNotFound, "x") })
	srv2, _ := entitler.NewServer("ent_test_k", entitler.WithHTTPClient(&http.Client{Transport: failing}), entitler.WithMaxRetries(0))
	defer srv2.Close()
	for _, err := range srv2.Customers.List(bg, nil) {
		if err == nil {
			t.Fatal("want the page's error")
		}
	}
}

func TestUsageLogIteratesEveryPage(t *testing.T) {
	first := entitlertest.CustomerUsage(entitler.CustomerUsage{Log: entitler.UsageLog{Items: []entitler.UsageEvent{{ID: "1"}}, Next: "n2"}})
	second := entitlertest.CustomerUsage(entitler.CustomerUsage{Log: entitler.UsageLog{Items: []entitler.UsageEvent{{ID: "2"}}}})
	detail := entitlertest.CustomerDetail(entitler.CustomerDetail{Usage: entitler.UsageLog{Items: []entitler.UsageEvent{{ID: "d1"}}, Next: "d2"}})
	detail2 := entitlertest.CustomerDetail(entitler.CustomerDetail{Usage: entitler.UsageLog{Items: []entitler.UsageEvent{{ID: "d2"}}}})
	var asOf []string
	rt := roundTripper(func(r *http.Request) (int, []byte) {
		asOf = append(asOf, r.Header.Get("Entitler-As-Of"))
		cursor := r.URL.Query().Get("cursor")
		switch {
		case strings.HasSuffix(r.URL.Path, "/usage") && cursor == "":
			return 200, first
		case strings.HasSuffix(r.URL.Path, "/usage"):
			return 200, second
		case cursor == "":
			return 200, detail
		}
		return 200, detail2
	})
	srv, _ := entitler.NewServer("ent_test_k", entitler.WithHTTPClient(&http.Client{Transport: rt}))
	defer srv.Close()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	usage, err := srv.Customer("u").Usage(bg, &entitler.UsageParams{AsOf: at})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for e, err := range usage.Log.All(bg) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
	}
	d, _ := srv.Customer("u").Details(bg, nil)
	for e := range d.Usage.All(bg) {
		ids = append(ids, e.ID)
	}
	if !reflect.DeepEqual(ids, []string{"1", "2", "d1", "d2"}) || asOf[1] != "2026-01-01T00:00:00.000Z" {
		t.Fatalf("%v %v", ids, asOf)
	}
}

type roundTripper func(*http.Request) (int, []byte)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	status, body := f(r)
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
}

type replayingTripper []byte

func (b replayingTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	header := http.Header{"Idempotent-Replayed": {"true"}}
	return &http.Response{StatusCode: 201, Header: header, Body: io.NopCloser(bytes.NewReader(b)), Request: r}, nil
}

func TestCreateCustomerKeepsReplayed(t *testing.T) {
	body := entitlertest.CustomerDetail(entitler.CustomerDetail{})
	srv, _ := entitler.NewServer("ent_test_k", entitler.WithHTTPClient(&http.Client{Transport: replayingTripper(body)}))
	defer srv.Close()
	detail, err := srv.Customers.Create(bg, entitler.CreateCustomerParams{ExternalID: "x", Name: "X", IdempotencyKey: "signup-1"})
	if err != nil || !detail.Replayed {
		t.Fatalf("%+v %v", detail, err)
	}
}
