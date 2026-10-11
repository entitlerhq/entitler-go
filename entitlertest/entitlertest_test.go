package entitlertest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/entitlerhq/entitler-go"
	"github.com/entitlerhq/entitler-go/entitlertest"
)

var ctx = context.Background()

type serve []byte

func (s serve) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(s)), Request: req}, nil
}

func server(t *testing.T, body []byte) *entitler.Server {
	t.Helper()
	srv, err := entitler.NewServer("ent_test_key", entitler.WithHTTPClient(&http.Client{Transport: serve(body)}),
		entitler.WithMaxRetries(0), entitler.WithoutCache())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

var (
	when     = time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC)
	later    = when.Add(30 * 24 * time.Hour)
	release  = int64(2)
	change   = "chg_1"
	reason   = "reason"
	env      = entitler.Environment{ID: "env_1", Name: "Development", Kind: entitler.EnvironmentTest}
	trk      = entitler.Track{ID: "trk_1", Name: "All customers"}
	exp      = &entitler.Experiment{ID: "exp_1", Arm: entitler.ArmVariant}
	plan     = entitler.PlanSummary{ID: "pln_pro", Key: "pro", Name: "Pro"}
	product  = &entitler.Product{Key: "app", Name: "App"}
	monthly  = &entitler.Period{Key: "monthly", Label: "Monthly"}
	price    = &entitler.Price{Amount: 1200, Currency: "aud", Interval: entitler.IntervalMonth, IntervalCount: 1, Tax: entitler.TaxInclusive}
	sources  = []entitler.Source{{Type: entitler.SourcePlan, Plan: "pro", Name: "Pro", Version: 1, Value: entitler.On{}}, {Type: entitler.SourceGrant, Grant: "grt_1", Until: later, Value: entitler.Amount(5)}, {Type: entitler.SourceBanked, Banked: -3}, {Type: entitler.SourceGroup, Features: []string{"a"}}, {Type: entitler.SourceAddOn, Plan: "sso", Name: "SSO", Version: 2, Quantity: 3, Value: entitler.Unlimited{}}}
	upgrades = []entitler.Upgrade{{Plan: "pro", Name: "Pro", Move: entitler.MoveUpgrade, Action: entitler.ActionBuy, Reason: &reason}}
	meter    = &entitler.Meter{Used: 3, Remaining: entitler.Amount(7), Held: 1, ResetsAt: later}
	sku      = entitler.OfferedSKU{Period: monthly, Connector: "apple", IDs: map[string]string{"productId": "pro.monthly"}, Price: price}
	option   = entitler.MoveOption{Plan: entitler.OfferedPlan{ID: "pln_pro", Key: "pro", Name: "Pro", Kind: entitler.PlanKindPlan, Description: "More", Default: false},
		Product: product, From: &plan, Move: entitler.MoveUpgrade, Action: entitler.ActionBuy, When: entitler.WhenNow,
		Periods: []entitler.Period{*monthly}, Impact: []entitler.Impact{{Kind: entitler.ImpactGains, Text: "SSO"}}, SKUs: []entitler.OfferedSKU{sku}}
	purchase = entitler.Purchase{Money: entitler.MoneyTest, Channel: &entitler.Channel{Provider: entitler.ProviderStripe, ConnectionID: "con_1"}, Release: &release, Arm: entitler.ArmControl}
	sub      = &entitler.Subscription{Product: *product, Plan: plan, Version: 1, Period: monthly, StartedAt: when, RenewsAt: later,
		Billing: &entitler.ProviderPeriod{Provider: "stripe", StartsAt: when, EndsAt: later}, AddOns: []entitler.HeldAddOn{{Plan: plan, Version: 1, Quantity: 2, Countable: true, AddedAt: when, Purchase: purchase}},
		Pending: entitler.PendingBilling{Billing: entitler.BillingSourceOutside}, Purchase: purchase,
		Override: &entitler.PlanOverride{From: plan, Since: when, By: "agent"}}
	logPage = entitler.UsageLog{Items: []entitler.UsageEvent{{ID: "use_1", Feature: "ai_credits", Kind: entitler.UsageEventUse, Amount: 5, Source: entitler.UsageSourceAPI, At: when}}}
)

func TestBuildersDecodeWithTheRealDecoder(t *testing.T) {
	check := entitler.Check{Customer: "user_1", Feature: "ai_credits", Type: entitler.FeatureTypeMetered, Entitled: true, Value: entitler.Amount(10),
		Sources: sources, Meter: meter, Upgrades: upgrades, AsOf: when, Environment: env, Track: trk, Release: &release, Change: &change, Testers: true, Experiment: exp}
	cases := []struct {
		name string
		body []byte
		call func(*entitler.Server) (any, error)
		want any
	}{
		{"check", entitlertest.Check(check), func(s *entitler.Server) (any, error) {
			return s.Customer("user_1").Check(ctx, entitler.Key("ai_credits"), nil)
		}, &check},
		{"entitlements", entitlertest.Entitlements(entitler.Entitlements{Customer: "user_1", AsOf: when, Environment: env, Track: trk,
			Items: []entitler.Entitlement{{Key: "sso", Type: entitler.FeatureTypeBoolean, Value: entitler.On{}, Entitled: true, Sources: sources, Upgrades: []entitler.Upgrade{}}}}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").Entitlements(ctx, nil) },
			&entitler.Entitlements{Customer: "user_1", AsOf: when, Environment: env, Track: trk,
				Items: []entitler.Entitlement{{Key: "sso", Type: entitler.FeatureTypeBoolean, Value: entitler.On{}, Entitled: true, Sources: sources, Upgrades: []entitler.Upgrade{}}}}},
		{"plans", entitlertest.CustomerPlans(entitler.CustomerPlans{Customer: "user_1", AsOf: when, Environment: env, Track: trk,
			Held:    []entitler.HeldPlan{{Plan: entitler.PlanRef{ID: "pln_pro", Key: "pro", Name: "Pro", Kind: entitler.PlanKindPlan}, Product: product, Version: &release, Period: monthly, RenewsAt: later, Pending: entitler.PendingCancel{MovingTo: &plan}, BilledBy: entitler.ProviderApple}},
			Options: []entitler.MoveOption{option}}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").Plans(ctx, nil) },
			&entitler.CustomerPlans{Customer: "user_1", AsOf: when, Environment: env, Track: trk,
				Held:    []entitler.HeldPlan{{Plan: entitler.PlanRef{ID: "pln_pro", Key: "pro", Name: "Pro", Kind: entitler.PlanKindPlan}, Product: product, Version: &release, Period: monthly, RenewsAt: later, Pending: entitler.PendingCancel{MovingTo: &plan}, BilledBy: entitler.ProviderApple}},
				Options: []entitler.MoveOption{option}}},
		{"plan change", entitlertest.PlanChange(entitler.PlanChange{Product: product, Plan: &plan, Quantity: &release, Effective: entitler.EffectiveRenewal, At: later, Until: later, Changed: true}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").Cancel(ctx, nil) },
			&entitler.PlanChange{Product: product, Plan: &plan, Quantity: &release, Effective: entitler.EffectiveRenewal, At: later, Until: later, Changed: true}},
		{"subscribe pay", entitlertest.SubscribeStep(entitler.SubscribeStep{Next: entitler.NextPay, URL: "https://pay"}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").Subscribe(ctx, "pro", nil) },
			&entitler.SubscribeStep{Next: entitler.NextPay, URL: "https://pay"}},
		{"subscribe manage", entitlertest.SubscribeStep(entitler.SubscribeStep{Next: entitler.NextManage, BilledBy: entitler.ProviderGoogle}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").Subscribe(ctx, "pro", nil) },
			&entitler.SubscribeStep{Next: entitler.NextManage, BilledBy: entitler.ProviderGoogle}},
		{"subscribe confirming", entitlertest.SubscribeStep(entitler.SubscribeStep{Next: entitler.NextConfirming}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").Subscribe(ctx, "pro", nil) },
			&entitler.SubscribeStep{Next: entitler.NextConfirming}},
		{"subscribe done", entitlertest.SubscribeStep(entitler.SubscribeStep{Next: entitler.NextDone, Change: &entitler.PlanChange{Plan: &plan, Effective: entitler.EffectiveNow, At: when, Changed: true}}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").Subscribe(ctx, "pro", nil) },
			&entitler.SubscribeStep{Next: entitler.NextDone, Change: &entitler.PlanChange{Plan: &plan, Effective: entitler.EffectiveNow, At: when, Changed: true}}},
		{"grant change", entitlertest.GrantChange(entitler.GrantChange{Grant: entitler.Grant{ID: "grt_1", Feature: "seats", Value: entitler.Amount(5), From: when, Until: later, Reason: "r", By: "key", Actor: &reason}}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").RevokeGrant(ctx, "grt_1", nil) },
			&entitler.GrantChange{Grant: entitler.Grant{ID: "grt_1", Feature: "seats", Value: entitler.Amount(5), From: when, Until: later, Reason: "r", By: "key", Actor: &reason}}},
		{"billing portal", entitlertest.BillingPortal(entitler.BillingPortal{Provider: "stripe", URL: "https://b"}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").BillingPortal(ctx, "https://app") },
			&entitler.BillingPortal{Provider: "stripe", URL: "https://b"}},
		{"billing sync", entitlertest.BillingSync(entitler.BillingSync{Changed: true}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").SyncBilling(ctx) }, &entitler.BillingSync{Changed: true}},
		{"registered", entitlertest.RegisteredCustomer(entitler.RegisteredCustomer{ID: "cus_1", ExternalID: "user_1", EnvironmentID: "env_1", CreatedAt: when, Created: true}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").Register(ctx, nil) },
			&entitler.RegisteredCustomer{ID: "cus_1", ExternalID: "user_1", EnvironmentID: "env_1", CreatedAt: when, Created: true}},
		{"token", entitlertest.IssuedCustomerToken(entitler.IssuedCustomerToken{Token: "tok", Customer: "user_1", Scopes: []entitler.Scope{entitler.ScopeUsageWrite}, ExpiresAt: later}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").Token(ctx, nil) },
			&entitler.IssuedCustomerToken{Token: "tok", Customer: "user_1", Scopes: []entitler.Scope{entitler.ScopeUsageWrite}, ExpiresAt: later}},
		{"snapshot", entitlertest.IssuedSnapshot(entitler.IssuedSnapshot{Token: "s", ExpiresAt: later, KeyID: "k"}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").Snapshot(ctx, nil) },
			&entitler.IssuedSnapshot{Token: "s", ExpiresAt: later, KeyID: "k"}},
		{"track", entitlertest.CustomerTrack(entitler.CustomerTrack{Customer: "user_1", EnvironmentID: "env_1", Track: trk, Source: entitler.TrackSourceServer, PreviousTrackID: &change}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").SetTrack(ctx, nil, nil) },
			&entitler.CustomerTrack{Customer: "user_1", EnvironmentID: "env_1", Track: trk, Source: entitler.TrackSourceServer, PreviousTrackID: &change}},
		{"billing", entitlertest.CustomerBilling(entitler.CustomerBilling{Provider: "stripe", Status: entitler.StatusActive, SKU: &sku, Items: 1,
			Drift: &entitler.BillingDrift{BilledPlan: "pro", ObservedAt: when}, Products: []entitler.ProductBilling{{Product: &change, Items: 1}}}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").Billing(ctx) },
			&entitler.CustomerBilling{Provider: "stripe", Status: entitler.StatusActive, SKU: &sku, Items: 1,
				Drift: &entitler.BillingDrift{BilledPlan: "pro", ObservedAt: when}, Products: []entitler.ProductBilling{{Product: &change, Items: 1}}}},
		{"summary", entitlertest.CustomerSummary(entitler.CustomerSummary{ID: "cus_1", ExternalID: "user_1", Name: "Ada", Email: "a@x", EnvironmentID: "env_1", CreatedAt: when,
			Plan: &entitler.VersionedPlan{ID: "p", Key: "pro", Name: "Pro", Version: 1}, Plans: []entitler.ProductPlan{{ID: "p", Key: "pro", Name: "Pro", Version: 1, Product: "app"}},
			DefaultPlan: &plan, Status: "active", Kind: entitler.CustomerRecurring, Metadata: map[string]string{"a": "b"}, Track: trk}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").Update(ctx, nil) },
			&entitler.CustomerSummary{ID: "cus_1", ExternalID: "user_1", Name: "Ada", Email: "a@x", EnvironmentID: "env_1", CreatedAt: when,
				Plan: &entitler.VersionedPlan{ID: "p", Key: "pro", Name: "Pro", Version: 1}, Plans: []entitler.ProductPlan{{ID: "p", Key: "pro", Name: "Pro", Version: 1, Product: "app"}},
				DefaultPlan: &plan, Status: "active", Kind: entitler.CustomerRecurring, Metadata: map[string]string{"a": "b"}, Track: trk}},
		{"usage result", entitlertest.UsageResult(entitler.UsageResult{Check: check, Outcome: entitler.OutcomeHeld, HoldID: &change, Mode: entitler.Observe, Amount: 3, MeterChange: 1, OverBy: 2, Late: true, OccurredAt: when, ExpiresAt: later, ReportedAs: entitler.UsageSourceClient}),
			func(s *entitler.Server) (any, error) { return s.Customer("user_1").SettleUsage(ctx, "h", 1, nil) },
			&entitler.UsageResult{Check: check, Outcome: entitler.OutcomeHeld, HoldID: &change, Mode: entitler.Observe, Amount: 3, MeterChange: 1, OverBy: 2, Late: true, OccurredAt: when, ExpiresAt: later, ReportedAs: entitler.UsageSourceClient}},
		{"scopes", entitlertest.Scopes(entitler.Scopes{List: []entitler.Scope{entitler.ScopePlansRead, entitler.ScopeUsageWrite}, Registration: true}),
			func(s *entitler.Server) (any, error) { return s.Scopes(ctx) },
			&entitler.Scopes{List: []entitler.Scope{entitler.ScopePlansRead, entitler.ScopeUsageWrite}, Registration: true}},
		{"features", entitlertest.FeatureList(entitler.FeatureList{Environment: env, Track: trk, Release: &release, Features: []entitler.FeatureListing{{ID: "f", Key: "ai", Name: "AI", Type: entitler.FeatureTypeMetered, Unit: "credits",
			ResetEvery: &entitler.MeterWindow{Count: 1, Unit: entitler.UnitMonths, Anchor: entitler.AnchorCalendar}, Includes: []string{}}}}),
			func(s *entitler.Server) (any, error) { return s.Features(ctx) },
			&entitler.FeatureList{Environment: env, Track: trk, Release: &release, Features: []entitler.FeatureListing{{ID: "f", Key: "ai", Name: "AI", Type: entitler.FeatureTypeMetered, Unit: "credits",
				ResetEvery: &entitler.MeterWindow{Count: 1, Unit: entitler.UnitMonths, Anchor: entitler.AnchorCalendar}, Includes: []string{}}}}},
		{"pricing", entitlertest.Pricing(entitler.Pricing{DefaultPlan: &change, Environment: env, Track: trk, Products: []entitler.PricingProduct{{Key: "app", Name: "App"}},
			Plans: []entitler.PricingPlan{{ID: "p", Key: "pro", Name: "Pro", Kind: entitler.PlanKindPlan, Status: entitler.PlanActive, Periods: []entitler.PricingPeriod{{Key: "monthly", Label: "Monthly", Count: 1, Unit: entitler.UnitMonths}},
				AttachesTo: []string{}, Features: map[string]entitler.Value{"sso": entitler.On{}, "seats": entitler.Amount(5)},
				Listings: []entitler.Listing{{Period: monthly, Channels: []entitler.ListingChannel{{Channel: entitler.Channel{Provider: entitler.ProviderStripe, ConnectionID: "c"}, Name: "Stripe", Mode: entitler.EnvironmentTest, Purchasable: true, IDs: map[string]string{"priceId": "x"}, Price: price}}}}}}}),
			func(s *entitler.Server) (any, error) { return s.Pricing(ctx, nil) },
			&entitler.Pricing{DefaultPlan: &change, Environment: env, Track: trk, Products: []entitler.PricingProduct{{Key: "app", Name: "App"}},
				Plans: []entitler.PricingPlan{{ID: "p", Key: "pro", Name: "Pro", Kind: entitler.PlanKindPlan, Status: entitler.PlanActive, Periods: []entitler.PricingPeriod{{Key: "monthly", Label: "Monthly", Count: 1, Unit: entitler.UnitMonths}},
					AttachesTo: []string{}, Features: map[string]entitler.Value{"sso": entitler.On{}, "seats": entitler.Amount(5)},
					Listings: []entitler.Listing{{Period: monthly, Channels: []entitler.ListingChannel{{Channel: entitler.Channel{Provider: entitler.ProviderStripe, ConnectionID: "c"}, Name: "Stripe", Mode: entitler.EnvironmentTest, Purchasable: true, IDs: map[string]string{"priceId": "x"}, Price: price}}}}}}}},
		{"customer page", entitlertest.CustomerPage(entitler.CustomerPage{Items: []entitler.CustomerSummary{}, Next: "c2", Used: 3, Limit: entitler.Amount(10)}),
			func(s *entitler.Server) (any, error) { return s.Customers.Page(ctx, nil) },
			&entitler.CustomerPage{Items: []entitler.CustomerSummary{}, Next: "c2", Used: 3, Limit: entitler.Amount(10)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.call(server(t, c.body))
			if err != nil {
				t.Fatalf("%v (%s)", err, c.body)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("round trip:\n got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestBuildersForPagedAnswers(t *testing.T) {
	usage := entitler.CustomerUsage{Customer: "user_1", AsOf: when, Environment: env, Track: trk, Log: logPage,
		Features: []entitler.FeatureUsage{{Feature: "ai", Type: entitler.FeatureTypeMetered, Entitled: true, Value: entitler.Amount(5), Sources: sources, Used: 1, Held: 0, Remaining: entitler.Amount(4)}}}
	got, err := server(t, entitlertest.CustomerUsage(usage)).Customer("user_1").Usage(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Features, usage.Features) || !reflect.DeepEqual(got.Log.Items, usage.Log.Items) || got.AsOf != when {
		t.Errorf("usage: %+v", got)
	}
	detail := entitler.CustomerDetail{Customer: entitler.CustomerSummary{ExternalID: "user_1", CreatedAt: when, Metadata: map[string]string{}, Track: trk},
		AsOf: when, Subscription: sub, DefaultPlan: &plan, Products: []entitler.ProductSubscription{{Product: *product, Subscription: sub}},
		AddOns: []entitler.HeldAddOn{}, Entitlements: []entitler.Entitlement{}, Banked: map[string]int64{"ai": 3}, MoveOptions: []entitler.MoveOption{option},
		Grants: []entitler.Grant{{ID: "g", Feature: "sso", Value: entitler.On{}, From: when, By: "k"}}, Usage: logPage,
		Activity: []entitler.Activity{{Text: "t", At: when}}, Environment: env}
	d, err := server(t, entitlertest.CustomerDetail(detail)).Customer("user_1").Details(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.Subscription, detail.Subscription) || !reflect.DeepEqual(d.Grants, detail.Grants) ||
		!reflect.DeepEqual(d.MoveOptions, detail.MoveOptions) || !reflect.DeepEqual(d.Banked, detail.Banked) || d.Customer.ExternalID != "user_1" {
		t.Errorf("details: %+v", d)
	}
	keys := entitler.SnapshotKeys{Keys: []entitler.SnapshotKey{{Kty: "EC", Crv: "P-256", X: "x", Y: "y", Kid: "k", Alg: "ES256", Use: "sig"}}}
	k, err := server(t, entitlertest.SnapshotKeys(keys)).SnapshotKeys(ctx)
	if err != nil || len(k.Keys) != 1 || k.Keys[0].Kid != "k" {
		t.Errorf("keys: %+v %v", k, err)
	}
	batch := entitlertest.BatchResult(entitler.BatchResult{Results: []entitler.BatchEventResult{{Outcome: entitler.OutcomeRecorded}}, Recorded: 1})
	b, err := server(t, batch).RecordUsageBatch(ctx, []entitler.BatchEvent{{Customer: "u", Feature: entitler.Metered("ai"), Amount: 1, IdempotencyKey: "k"}}, nil)
	if err != nil || b.Results[0].Outcome != entitler.OutcomeRecorded {
		t.Errorf("batch: %+v %v", b, err)
	}
}

func TestFakeCustomerReads(t *testing.T) {
	customer, _ := entitlertest.FakeCustomer(entitlertest.Values{
		"export_pdf": true, "sso": false, "seats": 5, "contacts": entitler.Unlimited{}, "ai_credits": entitlertest.Metered(100, 97),
	}, &entitlertest.Options{ID: "user_42"})
	if customer.ID() != "user_42" {
		t.Fatal(customer.ID())
	}
	if !customer.IsEntitled(ctx, entitler.Key("export_pdf"), false, nil) || customer.IsEntitled(ctx, entitler.Key("sso"), true, nil) {
		t.Error("on/off features")
	}
	if !customer.IsEntitled(ctx, entitler.Key("missing"), true, nil) {
		t.Error("an unknown feature answers the default")
	}
	_, err := customer.Check(ctx, entitler.Key("missing"), nil)
	var apiErr *entitler.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 404 || apiErr.Code != entitler.CodeFeatureNotFound {
		t.Errorf("missing feature: %v", err)
	}
	check, err := customer.Check(ctx, entitler.Metered("ai_credits"), nil)
	if err != nil || check.Meter.Remaining != entitler.Amount(3) || !check.Entitled {
		t.Errorf("meter: %+v %v", check, err)
	}
	list, err := customer.Entitlements(ctx, nil)
	if err != nil || !list.Has(entitler.Key("seats")) || list.Has(entitler.Key("sso")) || len(list.Items) != 5 {
		t.Errorf("entitlements: %+v %v", list, err)
	}
	if _, err := customer.Plans(ctx, nil); err != nil {
		t.Error(err)
	}
	if _, err := customer.Pricing(ctx, nil); err != nil {
		t.Error(err)
	}
	if _, err := customer.Details(ctx, nil); err != nil {
		t.Error(err)
	}
	if _, err := customer.Billing(ctx); err != nil {
		t.Error(err)
	}
}

func TestFakeCustomerMeters(t *testing.T) {
	credits := entitler.Metered("ai_credits")
	customer, fake := entitlertest.FakeCustomer(entitlertest.Values{"ai_credits": entitlertest.Metered(100, 90), "sso": true}, nil)
	r, err := customer.RecordUsage(ctx, credits, 20, "job-1", nil)
	if err != nil || r.Outcome != entitler.OutcomeRefused || r.Refusal != entitler.RefusalOverAllowance {
		t.Fatalf("gated past the allowance: %+v %v", r, err)
	}
	r, _ = customer.RecordUsage(ctx, credits, 5, "job-1", nil)
	if r.Outcome != entitler.OutcomeRecorded || r.Meter.Used != 95 || r.Replayed {
		t.Fatalf("recorded: %+v", r)
	}
	again, _ := customer.RecordUsage(ctx, credits, 5, "job-1", nil)
	if again.Outcome != entitler.OutcomeDuplicate || again.MeterChange != 0 || !again.Replayed || again.Meter.Used != 95 {
		t.Fatalf("replayed: %+v", again)
	}
	over, _ := customer.RecordUsage(ctx, credits, 10, "job-2", &entitler.RecordUsageParams{Mode: entitler.Observe})
	if over.Outcome != entitler.OutcomeRecorded || over.OverBy != 5 {
		t.Fatalf("observe: %+v", over)
	}
	cancelled, _ := customer.CancelUsage(ctx, *r.ID, nil)
	if cancelled.Outcome != entitler.OutcomeCancelled || cancelled.MeterChange != -5 || cancelled.Meter.Used != 100 {
		t.Fatalf("cancelled: %+v", cancelled)
	}
	replayCancelled, _ := customer.RecordUsage(ctx, credits, 5, "job-1", nil)
	if replayCancelled.Outcome != entitler.OutcomeCancelled {
		t.Fatalf("a cancelled report replays as cancelled: %+v", replayCancelled)
	}
	adjusted, _ := customer.AdjustMeter(ctx, credits, &entitler.AdjustMeterParams{By: new(int64(-50)), IdempotencyKey: "ticket-1"})
	if adjusted.Outcome != entitler.OutcomeAdjusted || adjusted.Meter.Used != 50 || adjusted.ID == nil {
		t.Fatalf("adjusted: %+v", adjusted)
	}
	to, _ := customer.AdjustMeter(ctx, credits, &entitler.AdjustMeterParams{To: new(int64(50)), IdempotencyKey: "ticket-2"})
	if to.ID != nil || to.MeterChange != 0 {
		t.Fatalf("no change: %+v", to)
	}
	usage, _ := customer.Usage(ctx, nil)
	if len(usage.Log.Items) != 4 || len(usage.Features) != 1 {
		t.Fatalf("log: %+v", usage.Log.Items)
	}
	if _, err := customer.RecordUsage(ctx, entitler.Metered("sso"), 1, "x", nil); !isCode(err, entitler.CodeNotMetered) {
		t.Errorf("not metered: %v", err)
	}
	if len(fake.Writes()) != 9 || fake.Writes()[0].Method != "RecordUsage" || fake.Writes()[0].IdempotencyKey != "job-1" {
		t.Errorf("writes: %+v", fake.Writes())
	}
}

func isCode(err error, code entitler.Code) bool {
	var apiErr *entitler.APIError
	return errors.As(err, &apiErr) && apiErr.Code == code
}

func TestFakeCustomerHolds(t *testing.T) {
	credits := entitler.Metered("ai_credits")
	customer, _ := entitlertest.FakeCustomer(entitlertest.Values{"ai_credits": entitlertest.Metered(100, 0)}, nil)
	h, err := customer.StartHold(ctx, credits, 30, "msg-1", nil)
	if err != nil || h.Duplicate || h.Amount != 30 {
		t.Fatalf("hold: %+v %v", h, err)
	}
	dup, err := customer.StartHold(ctx, credits, 30, "msg-1", nil)
	if err != nil || !dup.Duplicate || dup.ID != h.ID {
		t.Fatalf("duplicate: %+v %v", dup, err)
	}
	_ = h.Use(40)
	res, err := h.Finish(ctx)
	if err != nil || res.Outcome != entitler.OutcomeSettled {
		t.Fatalf("finish: %+v %v", res, err)
	}
	check, _ := customer.Check(ctx, credits, nil)
	if check.Meter.Used != 40 || check.Meter.Held != 0 {
		t.Fatalf("settled with the excess: %+v", check.Meter)
	}
	if _, err := customer.StartHold(ctx, credits, 30, "msg-1", nil); !errors.As(err, new(*entitler.UsageReplayedError)) {
		t.Fatalf("replay of a settled hold: %v", err)
	}
	if _, err := customer.SettleUsage(ctx, h.ID, 20, nil); !isCode(err, entitler.CodeHoldSettled) {
		t.Errorf("settle again with another amount: %v", err)
	}
	if r, err := customer.SettleUsage(ctx, h.ID, 30, nil); err != nil || r.Outcome != entitler.OutcomeDuplicate {
		t.Errorf("settle again with the same amount: %v %v", r, err)
	}
	if _, err := customer.ReleaseUsage(ctx, h.ID, nil); !isCode(err, entitler.CodeHoldSettled) {
		t.Errorf("release a settled hold: %v", err)
	}
	h2, _ := customer.StartHold(ctx, credits, 10, "msg-2", nil)
	if _, err := customer.SettleUsage(ctx, h2.ID, 11, nil); !isCode(err, entitler.CodeInvalidBody) {
		t.Errorf("settle more than held: %v", err)
	}
	r1, _ := h2.Release(ctx)
	r2, _ := customer.ReleaseUsage(ctx, h2.ID, nil)
	if r1.Outcome != entitler.OutcomeReleased || r2.Outcome != entitler.OutcomeReleased {
		t.Errorf("release twice: %v %v", r1.Outcome, r2.Outcome)
	}
	if _, err := customer.SettleUsage(ctx, h2.ID, 1, nil); !isCode(err, entitler.CodeHoldReleased) {
		t.Errorf("settle a released hold: %v", err)
	}
	if _, err := customer.StartHold(ctx, credits, 1000, "msg-3", nil); !errors.As(err, new(*entitler.UsageRefusedError)) {
		t.Errorf("refused: %v", err)
	}
	value, err := entitler.WithHold(ctx, customer, credits, 5, "msg-4", func(_ context.Context, h *entitler.Hold) (string, error) {
		return "done", h.Use(3)
	}, nil)
	if err != nil || value != "done" {
		t.Errorf("WithHold: %v %v", value, err)
	}
}

func TestFakeCustomerSettlesAfterTheHoldMatches(t *testing.T) {
	credits := entitler.Metered("ai_credits")
	customer, _ := entitlertest.FakeCustomer(entitlertest.Values{"ai_credits": entitlertest.Metered(100, 0)}, nil)
	h, _ := customer.StartHold(ctx, credits, 10, "m", nil)
	first, err := customer.SettleUsage(ctx, h.ID, 4, nil)
	if err != nil || first.Outcome != entitler.OutcomeSettled {
		t.Fatal(first, err)
	}
	again, err := customer.SettleUsage(ctx, h.ID, 4, nil)
	if err != nil || again.Outcome != entitler.OutcomeDuplicate {
		t.Fatal(again, err)
	}
}

func TestFakeCustomerWritesAndAnswers(t *testing.T) {
	customer, fake := entitlertest.FakeCustomer(nil, nil)
	step, err := customer.Subscribe(ctx, "pro", &entitler.SubscribeParams{Period: "monthly"})
	if err != nil || step.Next != entitler.NextDone || step.Change.Plan.Key != "pro" {
		t.Fatalf("subscribe: %+v %v", step, err)
	}
	fake.Answer("Subscribe", func(entitlertest.Write) (int, []byte) {
		return 200, entitlertest.SubscribeStep(entitler.SubscribeStep{Next: entitler.NextPay, URL: "https://checkout"})
	})
	step, _ = customer.Subscribe(ctx, "pro", nil)
	if step.Next != entitler.NextPay {
		t.Fatalf("replaced answer: %+v", step)
	}
	fake.Answer("BillingPortal", func(entitlertest.Write) (int, []byte) {
		return 409, entitlertest.Error(entitler.CodeStale, "No payment provider is connected.")
	})
	if _, err := customer.BillingPortal(ctx, "https://app"); !isCode(err, entitler.CodeStale) {
		t.Errorf("replaced error: %v", err)
	}
	if _, err := customer.SetPlan(ctx, entitler.Plan("enterprise"), &entitler.SetPlanParams{Billing: entitler.BillingEnd}); err != nil {
		t.Error(err)
	}
	g, err := customer.Grant(ctx, entitler.Key("seats"), &entitler.GrantParams{Value: entitler.Amount(5), Days: 30, Decision: entitler.Decision{Reason: "r", Actor: "a"}})
	if err != nil || g.Grant.Value != entitler.Amount(5) {
		t.Fatalf("grant: %+v %v", g, err)
	}
	revoked, err := customer.RevokeGrant(ctx, g.Grant.ID, nil)
	if err != nil || revoked.Grant.RevokedAt.IsZero() {
		t.Errorf("revoke: %+v %v", revoked, err)
	}
	for _, call := range []func() error{
		func() error { _, err := customer.Register(ctx, nil); return err },
		func() error { _, err := customer.Update(ctx, nil); return err },
		func() error { return customer.Erase(ctx, nil) },
		func() error { _, err := customer.Cancel(ctx, nil); return err },
		func() error { _, err := customer.UndoPendingChange(ctx, nil); return err },
		func() error { _, err := customer.SetAddOn(ctx, "sso", 1, nil); return err },
		func() error { _, err := customer.Token(ctx, nil); return err },
		func() error { _, err := customer.Snapshot(ctx, nil); return err },
		func() error { _, err := customer.SetTrack(ctx, new("Beta"), nil); return err },
		func() error { _, err := customer.SyncBilling(ctx); return err },
	} {
		if err := call(); err != nil {
			t.Error(err)
		}
	}
	methods := []string{}
	for _, w := range fake.Writes() {
		methods = append(methods, w.Method)
	}
	want := []string{"Subscribe", "Subscribe", "BillingPortal", "SetPlan", "Grant", "RevokeGrant", "Register", "Update", "Erase",
		"Cancel", "UndoPendingChange", "SetAddOn", "Token", "Snapshot", "SetTrack", "SyncBilling"}
	if !reflect.DeepEqual(methods, want) {
		t.Errorf("writes:\n got %v\nwant %v", methods, want)
	}
	if fake.Writes()[3].Body["billing"] != "end" {
		t.Errorf("SetPlan body: %v", fake.Writes()[3].Body)
	}
}

func TestFakeCustomerOtherCustomer(t *testing.T) {
	customer, _ := entitlertest.FakeCustomer(entitlertest.Values{"a": true}, nil)
	_ = customer
	defer func() {
		if recover() == nil {
			t.Error("an unsupported value should panic")
		}
	}()
	entitlertest.FakeCustomer(entitlertest.Values{"a": "text"}, nil)
}
