package entitlertest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/entitlerhq/entitler-go"
)

// Values maps feature keys to what the fake customer holds: true (on), false (an on/off feature
// they lack), an int or int64 amount, an [entitler.Value], or a [Meter] for a metered feature.
type Values map[string]any

// Meter is a metered feature's allowance and how much of it is used.
type Meter struct {
	// Value is the allowance: an entitler.Amount, or entitler.Unlimited.
	Value entitler.Value
	// Used is the amount used.
	Used int64
}

// Metered is a metered feature with an allowance of limit, used up to used: Metered(100, 97).
func Metered(limit, used int64) Meter { return Meter{Value: entitler.Amount(limit), Used: used} }

// Options are the fake customer's options; nil for none.
type Options struct {
	// ID is the customer's external id; "user_1" when "".
	ID string
	// Plans is what Plans answers; one default plan held and no options when nil.
	Plans *entitler.CustomerPlans
	// Pricing is what Pricing answers; no products or plans when nil.
	Pricing *entitler.Pricing
}

// Write is a write the fake customer received.
type Write struct {
	// Method is the ServerCustomer method that sent it, such as RecordUsage or SetPlan.
	Method string
	// Path is the request path.
	Path string
	// Query is the request's query string, or "".
	Query string
	// Body is the request body decoded as JSON, or nil when there is none.
	Body map[string]any
	// IdempotencyKey is the key the write sent.
	IdempotencyKey string
}

// Responder answers a request in place of the fake's own answer: an HTTP status and a body, such
// as one an answer builder wrote. Its Write describes the request; for a read, Body is nil.
type Responder func(w Write) (status int, body []byte)

// Fake is the control panel of a fake customer.
type Fake struct {
	mu        sync.Mutex
	id        string
	opts      Options
	features  map[string]*feature
	order     []string
	holds     map[string]*hold
	reports   map[string]*report
	keys      map[string]keyed
	log       []entitler.UsageEvent
	grants    []entitler.Grant
	writes    []Write
	responses map[string]Responder
	next      int
}

type feature struct {
	key   string
	typ   entitler.FeatureType
	value entitler.Value
	used  int64
	held  int64
}

type hold struct {
	id      string
	feature string
	amount  int64
	key     string
	state   entitler.Outcome
	settled int64
	expires time.Time
}

type report struct {
	id        string
	feature   string
	amount    int64
	mode      entitler.UsageMode
	cancelled bool
	at        time.Time
}

type keyed struct {
	hold   string
	report string
}

// FakeCustomer answers a *entitler.ServerCustomer that makes no request: every method is answered
// in memory, with a complete body the SDK decodes as it would an answer from the API. Reads answer
// from values; a feature values does not hold answers 404 feature_not_found, so IsEntitled answers
// its default. Usage writes move the meters as the API does: a gated report past the allowance is
// refused, holds settle and release, a reused key replays, and every write that moves a meter adds
// to the usage log. Every write is recorded for assertions, and writes the fake does not model
// answer a plain success that Answer can replace.
//
//	customer, fake := entitlertest.FakeCustomer(entitlertest.Values{
//		"export_pdf": true,
//		"ai_credits": entitlertest.Metered(100, 97),
//	}, nil)
func FakeCustomer(values Values, opts *Options) (*entitler.ServerCustomer, *Fake) {
	f := &Fake{features: map[string]*feature{}, holds: map[string]*hold{}, reports: map[string]*report{},
		keys: map[string]keyed{}, responses: map[string]Responder{}}
	if opts != nil {
		f.opts = *opts
	}
	f.id = f.opts.ID
	if f.id == "" {
		f.id = "user_1"
	}
	for key, v := range values {
		f.features[key] = newFeature(key, v)
		f.order = append(f.order, key)
	}
	slices.Sort(f.order)
	srv, err := entitler.NewServer("ent_test_entitlertest_fake",
		entitler.WithHTTPClient(&http.Client{Transport: f}), entitler.WithMaxRetries(0), entitler.WithoutCache())
	if err != nil {
		panic(err)
	}
	return srv.Customer(f.id), f
}

func newFeature(key string, v any) *feature {
	f := &feature{key: key, typ: entitler.FeatureTypeConfig}
	switch v := v.(type) {
	case bool:
		f.typ, f.value = entitler.FeatureTypeBoolean, entitler.Amount(0)
		if v {
			f.value = entitler.On{}
		}
	case int:
		f.value = entitler.Amount(v)
	case int64:
		f.value = entitler.Amount(v)
	case entitler.On:
		f.typ, f.value = entitler.FeatureTypeBoolean, v
	case entitler.Value:
		f.value = v
	case Meter:
		f.typ, f.value, f.used = entitler.FeatureTypeMetered, v.Value, v.Used
	case *Meter:
		f.typ, f.value, f.used = entitler.FeatureTypeMetered, v.Value, v.Used
	default:
		panic(fmt.Sprintf("entitlertest: %s holds a %T; use a bool, an int, an entitler.Value or a Meter", key, v))
	}
	return f
}

// Writes answers every write the fake received, in order.
func (f *Fake) Writes() []Write {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.writes)
}

// Answer replaces the answer of one ServerCustomer method, such as "Subscribe" or "Check", with
// what fn answers. Writes are still recorded.
func (f *Fake) Answer(method string, fn Responder) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[method] = fn
}

func (f *Fake) remaining(ft *feature) entitler.Value {
	switch v := ft.value.(type) {
	case entitler.Unlimited:
		return v
	case entitler.Amount:
		return entitler.Amount(max(int64(v)-ft.used-ft.held, 0))
	}
	return entitler.Amount(0)
}

func (f *Fake) check(ft *feature) entitler.Check {
	c := entitler.Check{Customer: f.id, Feature: ft.key, Type: ft.typ, Value: ft.value, Sources: []entitler.Source{}, Upgrades: []entitler.Upgrade{}}
	switch v := ft.value.(type) {
	case entitler.On, entitler.Unlimited:
		c.Entitled = true
	case entitler.Amount:
		c.Entitled = v > 0
	}
	if ft.typ == entitler.FeatureTypeMetered {
		remaining := f.remaining(ft)
		c.Meter = &entitler.Meter{Used: ft.used, Held: ft.held, Remaining: remaining}
		_, unlimited := remaining.(entitler.Unlimited)
		c.Entitled = unlimited || remaining.(entitler.Amount) > 0
	}
	return c
}

// RoundTrip answers a request in memory. It is the fake's transport, and is not meant to be
// called directly.
func (f *Fake) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]any
	if req.Body != nil {
		data, _ := io.ReadAll(req.Body)
		if len(data) > 0 {
			d := json.NewDecoder(bytes.NewReader(data))
			d.UseNumber()
			_ = d.Decode(&body)
		}
	}
	f.mu.Lock()
	status, answer, replayed := f.serve(req, body)
	f.mu.Unlock()
	h := http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"req_entitlertest"}}
	if replayed {
		h.Set("Idempotent-Replayed", "true")
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(bytes.NewReader(answer)), Request: req}, nil
}

func notFound(code entitler.Code, message string) (int, []byte, bool) {
	return 404, Error(code, message), false
}

func (f *Fake) serve(req *http.Request, body map[string]any) (int, []byte, bool) {
	parts := strings.Split(strings.TrimPrefix(req.URL.EscapedPath(), "/"), "/")
	for i, p := range parts {
		parts[i], _ = url.PathUnescape(p)
	}
	if len(parts) < 2 || parts[0] != "customers" || parts[1] != f.id {
		return notFound(entitler.CodeCustomerNotFound, "The fake customer answers only for "+f.id+".")
	}
	rest := parts[2:]
	method, route := methodOf(req.Method, rest)
	if req.Method != http.MethodGet {
		f.writes = append(f.writes, Write{Method: method, Path: req.URL.Path, Query: req.URL.RawQuery, Body: body,
			IdempotencyKey: req.Header.Get("Idempotency-Key")})
	}
	if respond, ok := f.responses[method]; ok {
		w := Write{Method: method, Path: req.URL.Path, Query: req.URL.RawQuery, Body: body, IdempotencyKey: req.Header.Get("Idempotency-Key")}
		f.mu.Unlock()
		status, answer := respond(w)
		f.mu.Lock()
		return status, answer, false
	}
	key := req.Header.Get("Idempotency-Key")
	switch route {
	case "check":
		ft, ok := f.features[rest[1]]
		if !ok {
			return notFound(entitler.CodeFeatureNotFound, "No feature has the key "+rest[1]+" in this environment.")
		}
		return 200, Check(f.check(ft)), false
	case "entitlements":
		e := entitler.Entitlements{Customer: f.id, Items: []entitler.Entitlement{}}
		for _, k := range f.order {
			c := f.check(f.features[k])
			e.Items = append(e.Items, entitler.Entitlement{Key: c.Feature, Type: c.Type, Value: c.Value, Entitled: c.Entitled,
				Sources: c.Sources, Meter: c.Meter, Upgrades: c.Upgrades})
		}
		return 200, Entitlements(e), false
	case "plans":
		plans := entitler.CustomerPlans{Customer: f.id, Held: []entitler.HeldPlan{{
			Plan: entitler.PlanRef{ID: "pln_free", Key: "free", Name: "Free", Kind: entitler.PlanKindPlan}, ByDefault: true}}}
		if f.opts.Plans != nil {
			plans = *f.opts.Plans
			plans.Customer = f.id
		}
		return 200, CustomerPlans(plans), false
	case "pricing":
		pricing := entitler.Pricing{}
		if f.opts.Pricing != nil {
			pricing = *f.opts.Pricing
		}
		pricing.Customer = &f.id
		return 200, Pricing(pricing), false
	case "usage":
		return 200, f.usage(), false
	case "details":
		return 200, f.details(), false
	case "billing":
		return 200, CustomerBilling(entitler.CustomerBilling{}), false
	case "record":
		return f.record(body, key)
	case "hold":
		return f.hold(body, key)
	case "settle":
		return f.settle(rest[2], body)
	case "release":
		return f.release(rest[2])
	case "cancelUsage":
		return f.cancelUsage(rest[1], body)
	case "adjust":
		return f.adjust(rest[1], body)
	case "grant":
		return f.grant(body)
	case "revokeGrant":
		return f.revoke(rest[1])
	}
	return f.plainSuccess(method, body)
}

// methodOf names the ServerCustomer method behind a request, and the fake's route for it.
func methodOf(verb string, rest []string) (method, route string) {
	path := strings.Join(rest, "/")
	switch {
	case path == "":
		return map[string]string{"GET": "Details", "PUT": "Register", "PATCH": "Update", "DELETE": "Erase"}[verb], map[string]string{"GET": "details"}[verb]
	case verb == "GET" && path == "entitlements":
		return "Entitlements", "entitlements"
	case verb == "GET" && len(rest) == 2 && rest[0] == "entitlements":
		return "Check", "check"
	case verb == "GET" && path == "plans":
		return "Plans", "plans"
	case verb == "GET" && path == "pricing":
		return "Pricing", "pricing"
	case verb == "GET" && path == "usage":
		return "Usage", "usage"
	case verb == "GET" && path == "billing":
		return "Billing", "billing"
	case verb == "POST" && path == "usage":
		return "RecordUsage", "record"
	case verb == "POST" && path == "usage/holds":
		return "HoldUsage", "hold"
	case verb == "POST" && len(rest) == 4 && rest[3] == "settle":
		return "SettleUsage", "settle"
	case verb == "DELETE" && len(rest) == 3 && rest[1] == "holds":
		return "ReleaseUsage", "release"
	case verb == "DELETE" && len(rest) == 2 && rest[0] == "usage":
		return "CancelUsage", "cancelUsage"
	case verb == "POST" && len(rest) == 3 && rest[0] == "meters":
		return "AdjustMeter", "adjust"
	case verb == "POST" && path == "grants":
		return "Grant", "grant"
	case verb == "DELETE" && len(rest) == 2 && rest[0] == "grants":
		return "RevokeGrant", "revokeGrant"
	case verb == "POST" && path == "subscription":
		return "Subscribe", ""
	case verb == "DELETE" && strings.HasPrefix(path, "subscription") && strings.HasSuffix(path, "pending"):
		return "UndoPendingChange", ""
	case verb == "DELETE" && strings.HasPrefix(path, "subscription"):
		return "Cancel", ""
	case verb == "PUT" && path == "plan":
		return "SetPlan", ""
	case verb == "PUT" && rest[0] == "add-ons":
		return "SetAddOn", ""
	case verb == "POST" && path == "snapshots":
		return "Snapshot", ""
	case verb == "POST" && path == "tokens":
		return "Token", ""
	case verb == "PUT" && path == "track":
		return "SetTrack", ""
	case verb == "POST" && path == "billing-portal":
		return "BillingPortal", ""
	case verb == "POST" && path == "billing/sync":
		return "SyncBilling", ""
	}
	return verb + " " + path, ""
}

func (f *Fake) plainSuccess(method string, body map[string]any) (int, []byte, bool) {
	now := time.Now()
	switch method {
	case "Register":
		return 200, RegisteredCustomer(entitler.RegisteredCustomer{ExternalID: f.id, CreatedAt: now}), false
	case "Update":
		return 200, CustomerSummary(entitler.CustomerSummary{ExternalID: f.id, CreatedAt: now}), false
	case "Erase":
		return 204, nil, false
	case "Subscribe":
		plan := fmt.Sprint(body["plan"])
		return 200, SubscribeStep(entitler.SubscribeStep{Next: entitler.NextDone, Change: &entitler.PlanChange{
			Plan: &entitler.PlanSummary{ID: "pln_" + plan, Key: plan, Name: plan}, Effective: entitler.EffectiveNow, At: now, Changed: true}}), false
	case "SetPlan", "SetAddOn", "Cancel", "UndoPendingChange":
		return 200, PlanChange(entitler.PlanChange{Effective: entitler.EffectiveNow, At: now, Changed: true}), false
	case "Snapshot":
		return 201, IssuedSnapshot(entitler.IssuedSnapshot{ExpiresAt: now.Add(7 * 24 * time.Hour)}), false
	case "Token":
		return 201, IssuedCustomerToken(entitler.IssuedCustomerToken{Customer: f.id, Scopes: []entitler.Scope{entitler.ScopeEntitlementsRead},
			ExpiresAt: now.Add(time.Hour)}), false
	case "SetTrack":
		return 200, CustomerTrack(entitler.CustomerTrack{Customer: f.id}), false
	case "BillingPortal":
		return 200, BillingPortal(entitler.BillingPortal{}), false
	case "SyncBilling":
		return 200, BillingSync(entitler.BillingSync{}), false
	}
	return 404, Error(entitler.CodeNotFound, "The fake customer has no route for "+method+"."), false
}

func (f *Fake) newID(prefix string) string {
	f.next++
	return prefix + "_" + strconv.Itoa(f.next)
}

func number(v any) int64 {
	n, _ := v.(json.Number)
	i, _ := n.Int64()
	return i
}

func (f *Fake) metered(key string) (*feature, int, []byte) {
	ft, ok := f.features[key]
	if !ok {
		return nil, 404, Error(entitler.CodeFeatureNotFound, "No feature has the key "+key+" in this environment.")
	}
	if ft.typ != entitler.FeatureTypeMetered {
		return nil, 400, Error(entitler.CodeNotMetered, key+" is not metered.")
	}
	return ft, 0, nil
}

func (f *Fake) result(ft *feature, outcome entitler.Outcome, amount, change int64) entitler.UsageResult {
	return entitler.UsageResult{Check: f.check(ft), Outcome: outcome, Mode: entitler.Gate, Amount: amount, MeterChange: change,
		ReportedAs: entitler.UsageSourceAPI}
}

func (f *Fake) logUse(id, featureKey string, amount int64, setTo *int64, decision map[string]any) {
	kind := entitler.UsageEventUse
	if setTo != nil {
		kind = entitler.UsageEventAdjust
	}
	actor, reason := decided(decision)
	f.log = append(f.log, entitler.UsageEvent{ID: id, Feature: featureKey, Kind: kind, Amount: amount, SetTo: setTo,
		Source: entitler.UsageSourceAPI, At: time.Now(), Actor: actor, Reason: reason})
}

func decided(body map[string]any) (actor, reason *string) {
	if s, ok := body["actor"].(string); ok {
		actor = &s
	}
	if s, ok := body["reason"].(string); ok {
		reason = &s
	}
	return actor, reason
}

func (f *Fake) record(body map[string]any, key string) (int, []byte, bool) {
	ft, status, answer := f.metered(fmt.Sprint(body["feature"]))
	if ft == nil {
		return status, answer, false
	}
	amount := number(body["amount"])
	mode := entitler.UsageMode(fmt.Sprint(body["mode"]))
	if body["mode"] == nil {
		mode = entitler.Gate
	}
	if k, ok := f.keys[key]; ok && k.report != "" {
		r := f.reports[k.report]
		res := f.result(ft, entitler.OutcomeDuplicate, r.amount, 0)
		if r.cancelled {
			res.Outcome = entitler.OutcomeCancelled
		}
		res.ID, res.Mode, res.OccurredAt = &r.id, r.mode, r.at
		return 200, UsageResult(res), true
	}
	if mode == entitler.Gate {
		if remaining, ok := f.remaining(ft).(entitler.Amount); ok && int64(remaining) < amount {
			res := f.result(ft, entitler.OutcomeRefused, amount, 0)
			res.Refusal = entitler.RefusalOverAllowance
			if v, isAmount := ft.value.(entitler.Amount); isAmount && v == 0 {
				res.Refusal = entitler.RefusalNotEntitled
			}
			return 200, UsageResult(res), false
		}
	}
	r := &report{id: f.newID("use"), feature: ft.key, amount: amount, mode: mode, at: time.Now()}
	f.reports[r.id] = r
	f.keys[key] = keyed{report: r.id}
	ft.used += amount
	f.logUse(r.id, ft.key, amount, nil, nil)
	res := f.result(ft, entitler.OutcomeRecorded, amount, amount)
	res.ID, res.Mode, res.OccurredAt = &r.id, mode, r.at
	if v, ok := ft.value.(entitler.Amount); ok && ft.used > int64(v) {
		res.OverBy = ft.used - int64(v)
	}
	return 201, UsageResult(res), false
}

func (f *Fake) holdResult(ft *feature, h *hold, outcome entitler.Outcome, change int64) []byte {
	res := f.result(ft, outcome, h.amount, change)
	res.HoldID, res.ExpiresAt = &h.id, h.expires
	return UsageResult(res)
}

func (f *Fake) expire(h *hold) {
	if h.state == entitler.OutcomeHeld && !time.Now().Before(h.expires) {
		h.state = "expired"
		f.features[h.feature].held -= h.amount
	}
}

func (f *Fake) hold(body map[string]any, key string) (int, []byte, bool) {
	ft, status, answer := f.metered(fmt.Sprint(body["feature"]))
	if ft == nil {
		return status, answer, false
	}
	amount := number(body["amount"])
	if k, ok := f.keys[key]; ok && k.hold != "" {
		h := f.holds[k.hold]
		f.expire(h)
		outcome := h.state
		if outcome == entitler.OutcomeHeld || outcome == "expired" {
			outcome = entitler.OutcomeDuplicate
		}
		return 200, f.holdResult(ft, h, outcome, 0), true
	}
	if remaining, ok := f.remaining(ft).(entitler.Amount); ok && int64(remaining) < amount {
		res := f.result(ft, entitler.OutcomeRefused, amount, 0)
		res.Refusal = entitler.RefusalOverAllowance
		return 200, UsageResult(res), false
	}
	ttl := 300 * time.Second
	if s := number(body["ttlSeconds"]); s > 0 {
		ttl = time.Duration(s) * time.Second
	}
	h := &hold{id: f.newID("hold"), feature: ft.key, amount: amount, key: key, state: entitler.OutcomeHeld, expires: time.Now().Add(ttl)}
	f.holds[h.id] = h
	f.keys[key] = keyed{hold: h.id}
	ft.held += amount
	return 201, f.holdResult(ft, h, entitler.OutcomeHeld, 0), false
}

func (f *Fake) findHold(id string) (*hold, *feature, int, []byte) {
	h, ok := f.holds[id]
	if !ok {
		return nil, nil, 404, Error(entitler.CodeNotFound, "No hold has the id "+id+".")
	}
	f.expire(h)
	return h, f.features[h.feature], 0, nil
}

func (f *Fake) settle(id string, body map[string]any) (int, []byte, bool) {
	h, ft, status, answer := f.findHold(id)
	if h == nil {
		return status, answer, false
	}
	amount := number(body["amount"])
	switch h.state {
	case "expired":
		return 409, Error(entitler.CodeHoldExpired, "The hold expired before it was settled."), false
	case entitler.OutcomeReleased:
		return 409, Error(entitler.CodeHoldReleased, "The hold was released."), false
	case entitler.OutcomeSettled:
		if amount == h.settled {
			return 200, f.holdResult(ft, h, entitler.OutcomeDuplicate, 0), false
		}
		return 409, Error(entitler.CodeHoldSettled, "The hold was settled."), false
	}
	if amount > h.amount {
		return 400, Error(entitler.CodeInvalidBody, "Settle at most the amount held."), false
	}
	h.state, h.settled = entitler.OutcomeSettled, amount
	ft.held -= h.amount
	ft.used += amount
	if amount > 0 {
		f.logUse(h.id, ft.key, amount, nil, nil)
	}
	res := f.result(ft, entitler.OutcomeSettled, amount, amount)
	res.HoldID, res.ExpiresAt = &h.id, h.expires
	return 200, UsageResult(res), false
}

func (f *Fake) release(id string) (int, []byte, bool) {
	h, ft, status, answer := f.findHold(id)
	if h == nil {
		return status, answer, false
	}
	switch h.state {
	case entitler.OutcomeSettled:
		return 409, Error(entitler.CodeHoldSettled, "The hold was settled."), false
	case entitler.OutcomeHeld:
		ft.held -= h.amount
	}
	h.state = entitler.OutcomeReleased
	return 200, f.holdResult(ft, h, entitler.OutcomeReleased, 0), false
}

func (f *Fake) cancelUsage(id string, body map[string]any) (int, []byte, bool) {
	r, ok := f.reports[id]
	if !ok {
		return 404, Error(entitler.CodeNotFound, "No usage report has the id "+id+"."), false
	}
	ft := f.features[r.feature]
	change := int64(0)
	if !r.cancelled {
		r.cancelled = true
		change = -r.amount
		ft.used = max(ft.used-r.amount, 0)
		for i := range f.log {
			if f.log[i].ID == r.id {
				f.log[i].CancelledAt = time.Now()
				f.log[i].Actor, f.log[i].Reason = decided(body)
			}
		}
	}
	res := f.result(ft, entitler.OutcomeCancelled, r.amount, change)
	res.ID = &r.id
	return 200, UsageResult(res), false
}

func (f *Fake) adjust(key string, body map[string]any) (int, []byte, bool) {
	ft, status, answer := f.metered(key)
	if ft == nil {
		return status, answer, false
	}
	before := ft.used
	if by, ok := body["by"]; ok {
		ft.used = max(ft.used+number(by), 0)
	} else {
		ft.used = number(body["to"])
	}
	res := f.result(ft, entitler.OutcomeAdjusted, ft.used-before, ft.used-before)
	if ft.used != before {
		id := f.newID("use")
		setTo := ft.used
		f.logUse(id, ft.key, ft.used-before, &setTo, body)
		res.ID = &id
	}
	return 200, UsageResult(res), false
}

func (f *Fake) grant(body map[string]any) (int, []byte, bool) {
	g := entitler.Grant{ID: f.newID("grt"), Feature: fmt.Sprint(body["feature"]), From: time.Now(), Value: entitler.On{},
		Reason: fmt.Sprint(body["reason"]), By: "SDK tests"}
	if body["reason"] == nil {
		g.Reason = ""
	}
	switch v := body["value"].(type) {
	case string:
		if v == "unlimited" {
			g.Value = entitler.Unlimited{}
		} else if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			g.Value = entitler.Amount(n)
		}
	}
	if days := number(body["days"]); days > 0 {
		g.Until = g.From.Add(time.Duration(days) * 24 * time.Hour)
	}
	if actor, ok := body["actor"].(string); ok {
		g.Actor = &actor
	}
	f.grants = append(f.grants, g)
	return 201, GrantChange(entitler.GrantChange{Grant: g}), false
}

func (f *Fake) revoke(id string) (int, []byte, bool) {
	for i := range f.grants {
		if f.grants[i].ID == id {
			if f.grants[i].RevokedAt.IsZero() {
				f.grants[i].RevokedAt = time.Now()
			}
			return 200, GrantChange(entitler.GrantChange{Grant: f.grants[i]}), false
		}
	}
	return 404, Error(entitler.CodeNotFound, "No grant has the id "+id+"."), false
}

func (f *Fake) usage() []byte {
	u := entitler.CustomerUsage{Customer: f.id, Features: []entitler.FeatureUsage{}, Log: entitler.UsageLog{Items: f.logItems()}}
	for _, k := range f.order {
		ft := f.features[k]
		if ft.typ != entitler.FeatureTypeMetered {
			continue
		}
		c := f.check(ft)
		u.Features = append(u.Features, entitler.FeatureUsage{Feature: k, Type: ft.typ, Entitled: c.Entitled, Value: ft.value,
			Sources: []entitler.Source{}, Used: ft.used, Held: ft.held, Remaining: c.Meter.Remaining})
	}
	return CustomerUsage(u)
}

func (f *Fake) logItems() []entitler.UsageEvent {
	items := slices.Clone(f.log)
	slices.Reverse(items)
	return items
}

func (f *Fake) details() []byte {
	d := entitler.CustomerDetail{Customer: entitler.CustomerSummary{ExternalID: f.id}, Grants: slices.Clone(f.grants),
		Usage: entitler.UsageLog{Items: f.logItems()}}
	for _, k := range f.order {
		c := f.check(f.features[k])
		d.Entitlements = append(d.Entitlements, entitler.Entitlement{Key: c.Feature, Type: c.Type, Value: c.Value,
			Entitled: c.Entitled, Sources: c.Sources, Meter: c.Meter, Upgrades: c.Upgrades})
	}
	return CustomerDetail(d)
}
