//go:build integration

package entitler_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/entitlerhq/entitler-go"
)

var ctxLive = context.Background()

// statuses records the HTTP status of every answer, so the suite can see 304s.
type statuses struct {
	mu   sync.Mutex
	seen []int
}

func (s *statuses) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err == nil {
		s.mu.Lock()
		s.seen = append(s.seen, resp.StatusCode)
		s.mu.Unlock()
	}
	return resp, err
}

func (s *statuses) last() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[len(s.seen)-1]
}

func liveKey(t *testing.T) string {
	key := os.Getenv("ENTITLER_TEST_KEY")
	if key == "" {
		t.Skip("Live API tests skipped: set ENTITLER_TEST_KEY to the SDK test project's development key.")
	}
	return key
}

func liveServer(t *testing.T, opts ...entitler.ServerOption) *entitler.Server {
	srv, err := entitler.NewServer(liveKey(t), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// newCustomer registers a customer with a unique id and erases it when the test ends.
func newCustomer(t *testing.T, srv *entitler.Server) *entitler.ServerCustomer {
	t.Helper()
	id := "sdk-go-" + entitler.NewVisitorID()[:16]
	c := srv.Customer(id)
	r, err := c.Register(ctxLive, &entitler.RegisterParams{Name: new("Go SDK test"), Email: new(id + "@example.com")})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Created || r.ExternalID != id {
		t.Fatalf("register: %+v", r)
	}
	t.Cleanup(func() {
		if err := c.Erase(context.WithoutCancel(ctxLive), nil); err != nil {
			t.Errorf("erase %s: %v", id, err)
		}
	})
	return c
}

func wantCode(t *testing.T, err error, status int, code entitler.Code) *entitler.APIError {
	t.Helper()
	var apiErr *entitler.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != status || apiErr.Code != code {
		t.Fatalf("want %d %s, got %v", status, code, err)
	}
	return apiErr
}

func TestLiveKeyPricingAndFeatures(t *testing.T) {
	srv := liveServer(t)
	scopes, err := srv.Scopes(ctxLive)
	if err != nil || !scopes.Has(entitler.ScopePlansRead) || !scopes.Has(entitler.ScopeTracksAssign) {
		t.Fatalf("%+v %v", scopes, err)
	}
	pricing, err := srv.Pricing(ctxLive, &entitler.PricingParams{Visitor: entitler.NewVisitorID()})
	if err != nil || len(pricing.Plans) == 0 || pricing.Customer != nil {
		t.Fatalf("%+v %v", pricing, err)
	}
	list, err := srv.Features(ctxLive)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]entitler.FeatureType{}
	for _, f := range list.Features {
		keys[f.Key] = f.Type
	}
	for key, typ := range map[string]entitler.FeatureType{"ai_credits": entitler.FeatureTypeMetered, "collaboration": entitler.FeatureTypeGroup,
		"export_pdf": entitler.FeatureTypeBoolean, "support_contacts": entitler.FeatureTypeConfig} {
		if keys[key] != typ {
			t.Errorf("%s: %s", key, keys[key])
		}
	}
}

func TestLiveRegistrationDetailsAndList(t *testing.T) {
	srv := liveServer(t)
	c := newCustomer(t, srv)
	again, err := c.Register(ctxLive, &entitler.RegisterParams{Name: new("Renamed"), Metadata: map[string]*string{"team": new("go")}})
	if err != nil || again.Created {
		t.Fatalf("second register: %+v %v", again, err)
	}
	detail, err := c.Details(ctxLive, nil)
	if err != nil || detail.Customer.Name != "Renamed" || detail.Customer.Metadata["team"] != "go" {
		t.Fatalf("details: %+v %v", detail, err)
	}
	updated, err := c.Update(ctxLive, &entitler.UpdateParams{Metadata: map[string]*string{"team": nil}})
	if err != nil || len(updated.Metadata) != 0 {
		t.Fatalf("update: %+v %v", updated, err)
	}
	page, err := srv.Customers.Page(ctxLive, &entitler.ListCustomersParams{Q: c.ID(), IncludeTest: true})
	if err != nil || len(page.Items) != 1 || page.Items[0].ExternalID != c.ID() {
		t.Fatalf("search: %+v %v", page, err)
	}
	count := 0
	for s, err := range srv.Customers.List(ctxLive, &entitler.ListCustomersParams{Q: "test_10"}) {
		if err != nil {
			t.Fatal(err)
		}
		_ = s
		count++
	}
	if count < 14 {
		t.Errorf("listed %d sample customers", count)
	}
	if page.Next != "" {
		if _, err := srv.Customers.Page(ctxLive, &entitler.ListCustomersParams{Cursor: page.Next}); err != nil {
			t.Error(err)
		}
	}
}

func TestLiveChecksAndCache(t *testing.T) {
	codes := &statuses{}
	srv := liveServer(t, entitler.WithHTTPClient(&http.Client{Transport: codes}))
	c := newCustomer(t, srv)
	check, err := c.Check(ctxLive, entitler.OnOff("export_pdf"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if check.Customer != c.ID() || check.Stale || check.Meter != nil {
		t.Errorf("%+v", check)
	}
	if _, err := c.Check(ctxLive, entitler.OnOff("export_pdf"), &entitler.ReadParams{Revalidate: true}); err != nil || codes.last() != 304 {
		t.Errorf("revalidating the check answered %d: %v", codes.last(), err)
	}
	list, err := c.Entitlements(ctxLive, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := list.Get(entitler.Group("collaboration")); !ok {
		t.Error("the group is in the list")
	}
	_ = list.Has(entitler.Group("team_essentials"))
	if _, err := c.Entitlements(ctxLive, &entitler.ReadParams{Revalidate: true}); err != nil || codes.last() != 304 {
		t.Errorf("revalidating the list answered %d: %v", codes.last(), err)
	}
	credits, err := c.Check(ctxLive, entitler.Metered("ai_credits"), nil)
	if err != nil || credits.Meter == nil || credits.Type != entitler.FeatureTypeMetered {
		t.Fatalf("metered: %+v %v", credits, err)
	}
	if c.IsEntitled(ctxLive, entitler.OnOff("sso"), false, nil) {
		t.Fatal("a new customer has sso")
	}
	writer := liveServer(t)
	if _, err := writer.Customer(c.ID()).Grant(ctxLive, entitler.OnOff("sso"), &entitler.GrantParams{Days: 1,
		Decision: entitler.Decision{Reason: "Integration test", Actor: "sdk-go"}}); err != nil {
		t.Fatal(err)
	}
	sso, err := c.Check(ctxLive, entitler.OnOff("sso"), &entitler.ReadParams{Revalidate: true})
	if err != nil || !sso.Entitled {
		t.Errorf("revalidate after a server-side change: %+v %v", sso, err)
	}
	_, err = c.Check(ctxLive, entitler.Key("no_such_feature"), nil)
	if e := wantCode(t, err, 404, entitler.CodeFeatureNotFound); e.RequestID == "" {
		t.Error("no request id")
	}
	at, err := c.Plans(ctxLive, &entitler.ReadParams{AsOf: time.Now().Add(24 * time.Hour)})
	if err != nil {
		wantCode(t, err, 409, entitler.CodeCapabilityRequired)
	} else if at.AsOf.Before(time.Now()) {
		t.Errorf("as of %s", at.AsOf)
	}
	_, err = srv.Customer("sdk-go-unregistered-"+entitler.NewVisitorID()[:8]).RecordUsage(ctxLive, entitler.Metered("ai_credits"), 1, "k", nil)
	if e := wantCode(t, err, 404, entitler.CodeCustomerNotFound); e.RequestID == "" || e.IdempotencyKey != "k" {
		t.Errorf("%+v", e)
	}
}

func TestLiveUsage(t *testing.T) {
	srv := liveServer(t)
	c := newCustomer(t, srv)
	credits := entitler.Metered("ai_credits")
	first, err := c.RecordUsage(ctxLive, credits, 5, "rec-1", nil)
	if err != nil || first.Outcome != entitler.OutcomeRecorded || first.Replayed || first.ID == nil {
		t.Fatalf("%+v %v", first, err)
	}
	again, err := c.RecordUsage(ctxLive, credits, 5, "rec-1", nil)
	if err != nil || again.Outcome != entitler.OutcomeDuplicate || !again.Replayed {
		t.Fatalf("replay: %+v %v", again, err)
	}
	remaining := first.Meter.Remaining.(entitler.Amount)
	refused, err := c.RecordUsage(ctxLive, credits, int64(remaining)+1, "rec-2", nil)
	if err != nil || refused.Outcome != entitler.OutcomeRefused || refused.Refusal != entitler.RefusalOverAllowance {
		t.Fatalf("refusal: %+v %v", refused, err)
	}
	over, err := c.RecordUsage(ctxLive, credits, int64(remaining)+3, "rec-3", &entitler.RecordUsageParams{Mode: entitler.Observe})
	if err != nil || over.Outcome != entitler.OutcomeRecorded || over.OverBy != 3 {
		t.Fatalf("observe: %+v %v", over, err)
	}
	occurred := first.OccurredAt
	late, err := c.RecordUsage(ctxLive, credits, 1, "rec-4", &entitler.RecordUsageParams{Mode: entitler.Observe, OccurredAt: occurred})
	if err != nil || !late.OccurredAt.Equal(occurred) {
		t.Fatalf("occurredAt: %+v %v", late, err)
	}
	cancelled, err := c.CancelUsage(ctxLive, *over.ID, &entitler.DecisionParams{Decision: entitler.Decision{Reason: "Test", Actor: "sdk-go"}})
	if err != nil || cancelled.Outcome != entitler.OutcomeCancelled || cancelled.MeterChange != -over.Amount {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	by, err := c.AdjustMeter(ctxLive, credits, &entitler.AdjustMeterParams{By: new(int64(-1)), IdempotencyKey: "adj-1"})
	if err != nil || by.Outcome != entitler.OutcomeAdjusted || by.MeterChange != -1 {
		t.Fatalf("adjust by: %+v %v", by, err)
	}
	to, err := c.AdjustMeter(ctxLive, credits, &entitler.AdjustMeterParams{To: new(int64(0)), IdempotencyKey: "adj-2"})
	if err != nil || to.Meter.Used != 0 {
		t.Fatalf("adjust to: %+v %v", to, err)
	}
	usage, err := c.Usage(ctxLive, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := 0
	for _, err := range usage.Log.All(ctxLive) {
		if err != nil {
			t.Fatal(err)
		}
		entries++
	}
	if entries < 4 {
		t.Errorf("%d log entries", entries)
	}
}

func TestLiveHolds(t *testing.T) {
	srv := liveServer(t)
	c := newCustomer(t, srv)
	credits := entitler.Metered("ai_credits")
	h, err := c.StartHold(ctxLive, credits, 5, "hold-1", nil)
	if err != nil || h.Duplicate {
		t.Fatalf("%+v %v", h, err)
	}
	_ = h.Use(3)
	settled, err := h.Finish(ctxLive)
	if err != nil || settled.Outcome != entitler.OutcomeSettled {
		t.Fatalf("finish: %+v %v", settled, err)
	}
	h2, err := c.StartHold(ctxLive, credits, 5, "hold-2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if released, err := h2.Release(ctxLive); err != nil || released.Outcome != entitler.OutcomeReleased {
		t.Fatalf("release: %+v %v", released, err)
	}
	value, err := entitler.WithHold(ctxLive, c, credits, 5, "hold-3", func(_ context.Context, h *entitler.Hold) (string, error) {
		return "done", h.Use(2)
	}, nil)
	if err != nil || value != "done" {
		t.Fatalf("WithHold: %v %v", value, err)
	}
	boom := errors.New("work failed")
	if _, err := entitler.WithHold(ctxLive, c, credits, 5, "hold-4", func(_ context.Context, h *entitler.Hold) (int, error) {
		_ = h.Use(1)
		return 0, boom
	}, nil); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := entitler.WithHold(ctxLive, c, credits, 5, "hold-5", func(context.Context, *entitler.Hold) (int, error) {
		return 0, boom
	}, nil); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	check, _ := c.Check(ctxLive, credits, nil)
	if check.Meter.Used != 6 || check.Meter.Held != 0 {
		t.Errorf("settled 3 + 2 + 1, released the rest: %+v", check.Meter)
	}
	_, err = c.StartHold(ctxLive, credits, 5, "hold-1", nil)
	var replayed *entitler.UsageReplayedError
	if !errors.As(err, &replayed) {
		t.Fatalf("a finished hold's key: %v", err)
	}
	other := newCustomer(t, srv)
	events := []entitler.BatchEvent{
		{Customer: c.ID(), Feature: credits, Amount: 1, IdempotencyKey: "batch-1"},
		{Customer: other.ID(), Feature: credits, Amount: 2, IdempotencyKey: "batch-2"},
	}
	if r, err := srv.RecordUsageBatch(ctxLive, events, nil); err != nil || r.Recorded != 2 {
		t.Fatalf("batch: %+v %v", r, err)
	}
	events = append(events, entitler.BatchEvent{Customer: c.ID(), Feature: credits, Amount: 3, IdempotencyKey: "batch-3"})
	r, err := srv.RecordUsageBatch(ctxLive, events, nil)
	if err != nil || r.Results[0].Outcome != entitler.OutcomeDuplicate || r.Results[2].Outcome != entitler.OutcomeRecorded || r.Results[0].Replayed {
		t.Fatalf("a batch with replayed events: %+v %v", r, err)
	}
	resent, err := srv.RecordUsageBatch(ctxLive, events, nil)
	if err != nil || !resent.Results[2].Replayed {
		t.Fatalf("the same batch resent: %+v %v", resent, err)
	}
}

func TestLiveTokensAndInAppClients(t *testing.T) {
	srv := liveServer(t)
	c := newCustomer(t, srv)
	plain, err := c.Token(ctxLive, nil)
	if err != nil || len(plain.Scopes) != 1 || plain.Scopes[0] != entitler.ScopeEntitlementsRead {
		t.Fatalf("%+v %v", plain, err)
	}
	var calls int
	client, err := entitler.NewClient(entitler.TokenFunc(func(ctx context.Context) (string, error) {
		calls++
		token, err := c.Token(ctx, &entitler.TokenParams{Scopes: []entitler.Scope{entitler.ScopeEntitlementsRead, entitler.ScopeUsageWrite, entitler.ScopeBillingSelf}})
		if err != nil {
			return "", err
		}
		return token.Token, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	scopes, err := client.Scopes(ctxLive)
	if err != nil || !scopes.Has(entitler.ScopeUsageWrite) || !scopes.Has(entitler.ScopeBillingSelf) {
		t.Fatalf("scopes: %+v %v", scopes, err)
	}
	me := client.Me()
	if _, err := me.Check(ctxLive, entitler.OnOff("export_pdf"), nil); err != nil || me.ID() != c.ID() {
		t.Fatalf("check: %v %q", err, me.ID())
	}
	if r, err := me.RecordUsage(ctxLive, entitler.Metered("ai_credits"), 1, "app-1", nil); err != nil || r.ReportedAs != entitler.UsageSourceClient {
		t.Fatalf("usage: %+v %v", r, err)
	}
	if step, err := me.Subscribe(ctxLive, "pro", &entitler.SubscribeParams{Period: "monthly"}); err != nil {
		wantCode(t, err, 409, entitler.CodeCapabilityRequired)
	} else if step.Next != entitler.NextDone {
		t.Errorf("step %+v", step)
	}
	if calls != 1 {
		t.Errorf("asked the token source %d times", calls)
	}
	readOnly, _ := entitler.NewClient(entitler.StaticToken(plain.Token))
	defer readOnly.Close()
	_, err = readOnly.Me().Subscribe(ctxLive, "pro", nil)
	wantCode(t, err, 403, entitler.CodeScopeRequired)
}

func TestLiveSignedOutPricing(t *testing.T) {
	key := os.Getenv("ENTITLER_TEST_PUBLISHABLE_KEY")
	if key == "" {
		t.Skip("Not run: ENTITLER_TEST_PUBLISHABLE_KEY is not set here; CI sets it.")
	}
	client, err := entitler.NewPublishableClient(key)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	pricing, err := client.Pricing(ctxLive, nil)
	if err != nil || len(pricing.Plans) == 0 {
		t.Fatalf("%+v %v", pricing, err)
	}
}

func TestLiveSnapshotAndTrack(t *testing.T) {
	srv := liveServer(t)
	c := newCustomer(t, srv)
	check, err := c.Check(ctxLive, entitler.OnOff("export_pdf"), nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.Snapshot(ctxLive, nil)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := srv.SnapshotKeys(ctxLive)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := entitler.VerifySnapshot(snapshot.Token, entitler.SnapshotExpectation{Keys: keys.Keys, Customer: c.ID(), Environment: check.Environment.ID})
	if err != nil || verified.Entitlements.Has(entitler.OnOff("export_pdf")) != check.Entitled {
		t.Fatalf("%+v %v", verified, err)
	}
	beta, err := c.SetTrack(ctxLive, new("Beta"), nil)
	if err != nil || beta.Track.Name != "Beta" {
		t.Fatalf("%+v %v", beta, err)
	}
	all, err := c.SetTrack(ctxLive, nil, nil)
	if err != nil || all.Track.Name != "All customers" {
		t.Fatalf("%+v %v", all, err)
	}
}

func TestLiveBilling(t *testing.T) {
	srv := liveServer(t)
	c := newCustomer(t, srv)
	step, err := c.Subscribe(ctxLive, "pro", &entitler.SubscribeParams{Period: "monthly", IdempotencyKey: "upgrade-" + c.ID()})
	if err != nil || step.Next != entitler.NextDone || step.Change.Plan.Key != "pro" {
		t.Fatalf("subscribe: %+v %v", step, err)
	}
	plans, err := c.Plans(ctxLive, nil)
	if err != nil || plans.Held[0].Period == nil || plans.Held[0].Period.Key != "monthly" || plans.Held[0].BilledBy != "" {
		t.Fatalf("plans: %+v %v", plans, err)
	}
	if len(plans.Options) == 0 || plans.Options[0].Action == "" || len(plans.Options[0].Periods) == 0 {
		t.Errorf("options: %+v", plans.Options)
	}
	if _, err := c.Pricing(ctxLive, nil); err != nil {
		t.Error(err)
	}
	addOn, err := c.Subscribe(ctxLive, "sso_addon", &entitler.SubscribeParams{Period: "monthly"})
	if err != nil || addOn.Next != entitler.NextDone {
		t.Fatalf("add-on: %+v %v", addOn, err)
	}
	removed, err := c.Cancel(ctxLive, &entitler.CancelParams{AddOn: new("sso_addon")})
	if err != nil || !removed.Changed {
		t.Fatalf("cancel add-on: %+v %v", removed, err)
	}
	cancel, err := c.Cancel(ctxLive, nil)
	if err != nil || !cancel.Changed {
		t.Fatalf("cancel: %+v %v", cancel, err)
	}
	undo, err := c.UndoPendingChange(ctxLive, nil)
	if err != nil || !undo.Changed {
		t.Fatalf("undo: %+v %v", undo, err)
	}
	if sync, err := c.SyncBilling(ctxLive); err != nil || sync.Changed {
		t.Errorf("sync: %+v %v", sync, err)
	}
	_, err = c.BillingPortal(ctxLive, "https://example.com/billing")
	wantCode(t, err, 409, entitler.CodeStale)

	free := newCustomer(t, srv)
	if nothing, err := free.Cancel(ctxLive, nil); err != nil || nothing.Changed {
		t.Errorf("nothing to cancel: %+v %v", nothing, err)
	}
	freePlans, _ := free.Plans(ctxLive, nil)
	period := "monthly"
	for _, o := range freePlans.Options {
		if o.Plan.Key == "pro_basic" && len(o.Periods) > 0 {
			period = o.Periods[0].Key
		}
	}
	who := entitler.Decision{Reason: "Integration test", Actor: "sdk-go"}
	set, err := free.SetPlan(ctxLive, entitler.Plan("pro_basic"), &entitler.SetPlanParams{Period: period, When: entitler.WhenNow, Decision: who})
	if err != nil || set.Plan.Key != "pro_basic" {
		t.Fatalf("set plan: %+v %v", set, err)
	}
	until := time.Now().Add(60 * 24 * time.Hour).Truncate(time.Second)
	withUntil, err := free.SetPlan(ctxLive, entitler.Plan("pro_basic"), &entitler.SetPlanParams{Period: period, Until: until, Decision: who})
	if err != nil || !withUntil.Until.Equal(until) {
		t.Fatalf("until: %+v %v", withUntil, err)
	}
	same, err := free.SetPlan(ctxLive, entitler.Plan("pro_basic"), &entitler.SetPlanParams{Period: period, Until: until, Decision: who})
	if err != nil || same.Changed {
		t.Fatalf("already held: %+v %v", same, err)
	}
	one, err := free.SetAddOn(ctxLive, "support_standard", 1, &entitler.SetAddOnParams{Decision: who})
	if err != nil || one.Quantity == nil || *one.Quantity != 1 {
		t.Fatalf("add-on 1: %+v %v", one, err)
	}
	zero, err := free.SetAddOn(ctxLive, "support_standard", 0, &entitler.SetAddOnParams{Decision: who})
	if err != nil || zero.Quantity == nil || *zero.Quantity != 0 {
		t.Fatalf("add-on 0: %+v %v", zero, err)
	}
	grant, err := free.Grant(ctxLive, entitler.Config("support_contacts"), &entitler.GrantParams{Value: entitler.Amount(10), Days: 30, Decision: who})
	if err != nil || grant.Grant.ID == "" || grant.Grant.Value != entitler.Amount(10) {
		t.Fatalf("grant: %+v %v", grant, err)
	}
	revoked, err := free.RevokeGrant(ctxLive, grant.Grant.ID, &entitler.DecisionParams{Decision: who})
	if err != nil || revoked.Grant.RevokedAt.IsZero() {
		t.Fatalf("revoke: %+v %v", revoked, err)
	}
	if billing, err := free.Billing(ctxLive); err != nil || billing.Provider != "" {
		t.Errorf("billing: %+v %v", billing, err)
	}
}
