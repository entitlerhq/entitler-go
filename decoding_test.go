package entitler_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/entitlerhq/entitler-go"
	"github.com/entitlerhq/entitler-go/entitlertest"
)

// paths lists every object member of a JSON value, as dotted paths with [i] for array items.
func paths(v any, prefix string) []string {
	var out []string
	switch v := v.(type) {
	case map[string]any:
		for k, item := range v {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			out = append(out, p)
			out = append(out, paths(item, p)...)
		}
	case []any:
		for i, item := range v {
			out = append(out, paths(item, prefix+"["+string(rune('0'+i))+"]")...)
		}
	}
	return out
}

func without(v any, path string) any {
	data, _ := json.Marshal(v)
	var copied any
	_ = json.Unmarshal(data, &copied)
	parts := regexp.MustCompile(`\.|\[`).Split(path, -1)
	node := copied
	for i, part := range parts {
		last := i == len(parts)-1
		if strings.HasSuffix(part, "]") {
			idx := int(part[0] - '0')
			node = node.([]any)[idx]
			continue
		}
		m := node.(map[string]any)
		if last {
			delete(m, part)
		} else {
			node = m[part]
		}
	}
	return copied
}

func TestEveryRequiredMemberMissingInTurn(t *testing.T) {
	now := time.Now()
	held := "hold_1"
	ids := map[string]string{"k": "v"}
	price := &entitler.Price{Amount: 1, Currency: "aud", IntervalCount: 1, Tax: entitler.TaxExclusive}
	sku := entitler.OfferedSKU{Connector: "apple", IDs: ids, Price: price}
	sum := entitler.PlanSummary{ID: "p", Key: "p", Name: "P"}
	sub := &entitler.Subscription{Pending: entitler.PendingMove{Plan: sum}, Billing: &entitler.ProviderPeriod{},
		AddOns: []entitler.HeldAddOn{{Plan: sum}}, Override: &entitler.PlanOverride{From: sum}}
	meter := &entitler.Meter{Remaining: entitler.Amount(1)}
	option := entitler.MoveOption{SKUs: []entitler.OfferedSKU{sku}, Periods: []entitler.Period{{Key: "m", Label: "M"}},
		Impact: []entitler.Impact{{Kind: entitler.ImpactGains, Text: "t"}}, From: &sum}
	sources := []entitler.Source{{Type: entitler.SourcePlan, Value: entitler.On{}}, {Type: entitler.SourceAddOn, Value: entitler.On{}},
		{Type: entitler.SourceGrant, Value: entitler.On{}}, {Type: entitler.SourceBanked}, {Type: entitler.SourceGroup, Features: []string{"a"}}}
	answers := []struct {
		name string
		body []byte
		read func(*entitler.Server) error
	}{
		{"check", entitlertest.Check(entitler.Check{Meter: meter, Sources: sources, Upgrades: []entitler.Upgrade{{}}, Experiment: &entitler.Experiment{}}),
			func(s *entitler.Server) error {
				_, err := s.Customer("u").Check(bg, entitler.Key("f"), nil)
				return err
			}},
		{"entitlements", entitlertest.Entitlements(entitler.Entitlements{Items: []entitler.Entitlement{{Sources: sources, Upgrades: []entitler.Upgrade{{}}}}}),
			func(s *entitler.Server) error { _, err := s.Customer("u").Entitlements(bg, nil); return err }},
		{"plans", entitlertest.CustomerPlans(entitler.CustomerPlans{Held: []entitler.HeldPlan{{Product: &entitler.Product{}, Period: &entitler.Period{},
			Pending: entitler.PendingCancel{MovingTo: &sum}}, {Pending: entitler.PendingBilling{}}}, Options: []entitler.MoveOption{option}}),
			func(s *entitler.Server) error { _, err := s.Customer("u").Plans(bg, nil); return err }},
		{"pricing", entitlertest.Pricing(entitler.Pricing{Products: []entitler.PricingProduct{{}}, Plans: []entitler.PricingPlan{{
			Periods: []entitler.PricingPeriod{{}}, Features: map[string]entitler.Value{"a": entitler.On{}},
			Listings: []entitler.Listing{{Channels: []entitler.ListingChannel{{IDs: ids, Price: price}}}}}}}),
			func(s *entitler.Server) error { _, err := s.Pricing(bg, nil); return err }},
		{"features", entitlertest.FeatureList(entitler.FeatureList{Features: []entitler.FeatureListing{{ResetEvery: &entitler.MeterWindow{Anchor: entitler.AnchorPeriod}}}}),
			func(s *entitler.Server) error { _, err := s.Features(bg); return err }},
		{"usage result", entitlertest.UsageResult(entitler.UsageResult{HoldID: &held, Check: entitler.Check{Meter: meter}}),
			func(s *entitler.Server) error { _, err := s.Customer("u").SettleUsage(bg, "h", 1, nil); return err }},
		{"usage", entitlertest.CustomerUsage(entitler.CustomerUsage{Features: []entitler.FeatureUsage{{Remaining: entitler.Amount(1)}},
			Log: entitler.UsageLog{Items: []entitler.UsageEvent{{Kind: entitler.UsageEventAdjust}}}}),
			func(s *entitler.Server) error { _, err := s.Customer("u").Usage(bg, nil); return err }},
		{"details", entitlertest.CustomerDetail(entitler.CustomerDetail{Subscription: sub, DefaultPlan: &sum,
			Products: []entitler.ProductSubscription{{Subscription: sub}}, Grants: []entitler.Grant{{}}, Activity: []entitler.Activity{{}},
			Customer:    entitler.CustomerSummary{Plan: &entitler.VersionedPlan{}, Plans: []entitler.ProductPlan{{}}},
			MoveOptions: []entitler.MoveOption{option}, Usage: entitler.UsageLog{Items: []entitler.UsageEvent{{}}}}),
			func(s *entitler.Server) error { _, err := s.Customer("u").Details(bg, nil); return err }},
		{"page", entitlertest.CustomerPage(entitler.CustomerPage{Items: []entitler.CustomerSummary{{}}}),
			func(s *entitler.Server) error { _, err := s.Customers.Page(bg, nil); return err }},
		{"registered", entitlertest.RegisteredCustomer(entitler.RegisteredCustomer{}),
			func(s *entitler.Server) error { _, err := s.Customer("u").Register(bg, nil); return err }},
		{"summary", entitlertest.CustomerSummary(entitler.CustomerSummary{}),
			func(s *entitler.Server) error { _, err := s.Customer("u").Update(bg, nil); return err }},
		{"token", entitlertest.IssuedCustomerToken(entitler.IssuedCustomerToken{ExpiresAt: now}),
			func(s *entitler.Server) error { _, err := s.Customer("u").Token(bg, nil); return err }},
		{"snapshot", entitlertest.IssuedSnapshot(entitler.IssuedSnapshot{}),
			func(s *entitler.Server) error { _, err := s.Customer("u").Snapshot(bg, nil); return err }},
		{"track", entitlertest.CustomerTrack(entitler.CustomerTrack{}),
			func(s *entitler.Server) error { _, err := s.Customer("u").SetTrack(bg, nil, nil); return err }},
		{"billing", entitlertest.CustomerBilling(entitler.CustomerBilling{SKU: &sku, Drift: &entitler.BillingDrift{},
			Products: []entitler.ProductBilling{{SKU: &sku, Drift: &entitler.BillingDrift{}}}}),
			func(s *entitler.Server) error { _, err := s.Customer("u").Billing(bg); return err }},
		{"plan change", entitlertest.PlanChange(entitler.PlanChange{Product: &entitler.Product{}, Plan: &sum}),
			func(s *entitler.Server) error { _, err := s.Customer("u").Cancel(bg, nil); return err }},
		{"step done", entitlertest.SubscribeStep(entitler.SubscribeStep{}),
			func(s *entitler.Server) error { _, err := s.Customer("u").Subscribe(bg, "p", nil); return err }},
		{"step pay", entitlertest.SubscribeStep(entitler.SubscribeStep{Next: entitler.NextPay, URL: "u"}),
			func(s *entitler.Server) error { _, err := s.Customer("u").Subscribe(bg, "p", nil); return err }},
		{"step manage", entitlertest.SubscribeStep(entitler.SubscribeStep{Next: entitler.NextManage}),
			func(s *entitler.Server) error { _, err := s.Customer("u").Subscribe(bg, "p", nil); return err }},
		{"grant", entitlertest.GrantChange(entitler.GrantChange{}),
			func(s *entitler.Server) error { _, err := s.Customer("u").RevokeGrant(bg, "g", nil); return err }},
		{"portal", entitlertest.BillingPortal(entitler.BillingPortal{}),
			func(s *entitler.Server) error { _, err := s.Customer("u").BillingPortal(bg, "https://x"); return err }},
		{"sync", entitlertest.BillingSync(entitler.BillingSync{}),
			func(s *entitler.Server) error { _, err := s.Customer("u").SyncBilling(bg); return err }},
		{"scopes", entitlertest.Scopes(entitler.Scopes{}),
			func(s *entitler.Server) error { _, err := s.Scopes(bg); return err }},
		{"keys", entitlertest.SnapshotKeys(entitler.SnapshotKeys{Keys: []entitler.SnapshotKey{{}}}),
			func(s *entitler.Server) error { _, err := s.SnapshotKeys(bg); return err }},
	}
	optional := regexp.MustCompile(`^(used|held|remaining|resetsAt|registration)$|(^|\.)(price|anchor)$|^(entitlements|items|features)\[\d\]\.(used|held|remaining|resetsAt)$`)
	meterAtTop := regexp.MustCompile(`^(used|held|remaining|resetsAt)$`)
	usageRequired := regexp.MustCompile(`^features\[\d\]\.(used|held|remaining|resetsAt)$`)
	for _, a := range answers {
		var whole any
		if err := json.Unmarshal(a.body, &whole); err != nil {
			t.Fatal(err)
		}
		serve := func(body []byte) error {
			rt := roundTripper(func(*http.Request) (int, []byte) { return 200, body })
			srv, _ := entitler.NewServer("ent_test_k", entitler.WithHTTPClient(&http.Client{Transport: rt}), entitler.WithoutCache(), entitler.WithMaxRetries(0))
			defer srv.Close()
			return a.read(srv)
		}
		if err := serve(a.body); err != nil {
			t.Fatalf("%s: the whole body fails: %v", a.name, err)
		}
		if err := serve([]byte(`{}`)); !invalid(err) {
			t.Errorf("%s: {} decodes", a.name)
		}
		for _, p := range paths(whole, "") {
			if strings.HasPrefix(p, "plans[0].features.") || strings.Contains(p, ".ids.") || strings.HasPrefix(p, "banked.") ||
				strings.Contains(p, "metadata.") {
				continue
			}
			body, _ := json.Marshal(without(whole, p))
			err := serve(body)
			required := a.name == "usage" && usageRequired.MatchString(p) ||
				a.name != "check" && a.name != "usage result" && meterAtTop.MatchString(p)
			isOptional := optional.MatchString(p) && !required
			if isOptional && err != nil {
				t.Errorf("%s: leaving out the optional %s fails: %v", a.name, p, err)
			}
			if !isOptional && !invalid(err) {
				t.Errorf("%s: leaving out %s still decodes", a.name, p)
			}
		}
	}
}

func invalid(err error) bool {
	var apiErr *entitler.APIError
	return errors.As(err, &apiErr) && apiErr.Code == entitler.CodeInvalidResponse
}

func TestFreeFormMapsKeepTheirStrings(t *testing.T) {
	body := entitlertest.CustomerSummary(entitler.CustomerSummary{Metadata: map[string]string{"at": "2026-02-31T00:00:00Z", "until": "soon"}})
	rt := roundTripper(func(*http.Request) (int, []byte) { return 200, body })
	srv, _ := entitler.NewServer("ent_test_k", entitler.WithHTTPClient(&http.Client{Transport: rt}))
	defer srv.Close()
	s, err := srv.Customer("u").Update(bg, nil)
	if err != nil || s.Metadata["at"] != "2026-02-31T00:00:00Z" || s.Metadata["until"] != "soon" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestImpossibleInstantsAreRefused(t *testing.T) {
	for _, at := range []string{"2026-02-31T00:00:00Z", "2025-02-29T00:00:00Z", "2026-01-01T24:00:00Z", "2026-01-01T00:00:00+25:00",
		"2026-01-01T00:60:00Z", "2026-01-01T00:00:60Z", "2026-13-01T00:00:00Z", "0000-01-01T00:00:00Z", "2026-01-01 00:00:00Z",
		"2026-01-01T00:00:00", "2026-01-01T00:00:00.Z", "2026-01-01T00:00:00+05:60"} {
		var body map[string]any
		_ = json.Unmarshal(entitlertest.Check(entitler.Check{}), &body)
		body["asOf"] = at
		data, _ := json.Marshal(body)
		rt := roundTripper(func(*http.Request) (int, []byte) { return 200, data })
		srv, _ := entitler.NewServer("ent_test_k", entitler.WithHTTPClient(&http.Client{Transport: rt}), entitler.WithMaxRetries(0))
		if _, err := srv.Customer("u").Check(bg, entitler.Key("f"), nil); !invalid(err) {
			t.Errorf("%s decodes", at)
		}
		_ = srv.Close()
	}
	for at, want := range map[string]string{
		"2024-02-29T23:30:00-01:30":      "2024-03-01T01:00:00.000Z",
		"2026-07-01t09:30:00.123456789z": "2026-07-01T09:30:00.123Z",
	} {
		var body map[string]any
		_ = json.Unmarshal(entitlertest.Check(entitler.Check{}), &body)
		body["asOf"] = at
		data, _ := json.Marshal(body)
		rt := roundTripper(func(*http.Request) (int, []byte) { return 200, data })
		srv, _ := entitler.NewServer("ent_test_k", entitler.WithHTTPClient(&http.Client{Transport: rt}))
		c, err := srv.Customer("u").Check(bg, entitler.Key("f"), nil)
		if err != nil || c.AsOf.UTC().Format("2006-01-02T15:04:05.000Z") != want {
			t.Errorf("%s: %v %v", at, c, err)
		}
		_ = srv.Close()
	}
}

func TestSetToFollowsTheEventKind(t *testing.T) {
	five := int64(5)
	for _, c := range []struct {
		kind  entitler.UsageEventKind
		setTo any
		ok    bool
	}{
		{entitler.UsageEventAdjust, 5, true},
		{entitler.UsageEventAdjust, nil, false},
		{entitler.UsageEventUse, nil, true},
		{entitler.UsageEventUse, 5, false},
		{"refund", nil, true},
		{"refund", 5, true},
	} {
		var body map[string]any
		_ = json.Unmarshal(entitlertest.CustomerUsage(entitler.CustomerUsage{
			Log: entitler.UsageLog{Items: []entitler.UsageEvent{{Kind: entitler.UsageEventAdjust, SetTo: &five}}}}), &body)
		item := body["log"].(map[string]any)["items"].([]any)[0].(map[string]any)
		item["kind"], item["setTo"] = string(c.kind), c.setTo
		data, _ := json.Marshal(body)
		rt := roundTripper(func(*http.Request) (int, []byte) { return 200, data })
		srv, _ := entitler.NewServer("ent_test_k", entitler.WithHTTPClient(&http.Client{Transport: rt}), entitler.WithoutCache(), entitler.WithMaxRetries(0))
		usage, err := srv.Customer("u").Usage(bg, nil)
		if c.ok != (err == nil) {
			t.Errorf("%s with setTo %v: %v", c.kind, c.setTo, err)
		}
		if err == nil && (c.setTo == nil) != (usage.Log.Items[0].SetTo == nil) {
			t.Errorf("%s with setTo %v decodes as %v", c.kind, c.setTo, usage.Log.Items[0].SetTo)
		}
		_ = srv.Close()
	}
	for _, e := range []entitler.UsageEvent{{Kind: entitler.UsageEventAdjust}, {Kind: entitler.UsageEventUse, SetTo: &five}} {
		rt := roundTripper(func(*http.Request) (int, []byte) {
			return 200, entitlertest.CustomerUsage(entitler.CustomerUsage{Log: entitler.UsageLog{Items: []entitler.UsageEvent{e}}})
		})
		srv, _ := entitler.NewServer("ent_test_k", entitler.WithHTTPClient(&http.Client{Transport: rt}), entitler.WithoutCache())
		if _, err := srv.Customer("u").Usage(bg, nil); err != nil {
			t.Errorf("the builder's %s event does not decode: %v", e.Kind, err)
		}
		_ = srv.Close()
	}
}
