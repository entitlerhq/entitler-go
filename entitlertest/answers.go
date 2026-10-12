// Package entitlertest helps test apps that use the Entitler SDK without the network, as
// net/http/httptest does for net/http. [FakeCustomer] answers a *entitler.ServerCustomer from
// values held in memory, and the answer builders write complete JSON bodies for tests that fake
// the HTTP transport themselves, so a fixture missing one field never decodes as invalid_response
// and passes a test for the wrong reason.
//
// Each builder takes the SDK's own answer type and fills what a test leaves at its zero value with
// a plausible default (an environment, a track, now for AsOf), so a test names only the fields it
// asserts.
package entitlertest

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"github.com/entitlerhq/entitler-go"
)

type obj = map[string]any

func encode(v any) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

func value(v entitler.Value) any {
	switch v := v.(type) {
	case entitler.On:
		return true
	case entitler.Unlimited:
		return "unlimited"
	case entitler.Amount:
		return int64(v)
	}
	return 0
}

func limit(v entitler.Value) any {
	if _, on := v.(entitler.On); on || v == nil {
		return 0
	}
	return value(v)
}

func at(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func atOrNull(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return at(t)
}

func orNull[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func enumOrNull[T ~string](v T) any {
	if v == "" {
		return nil
	}
	return string(v)
}

func or[T ~string](v, fallback T) T {
	if v == "" {
		return fallback
	}
	return v
}

func environment(e entitler.Environment) obj {
	return obj{"id": or(e.ID, "env_test"), "name": or(e.Name, "Development"), "kind": or(e.Kind, entitler.EnvironmentTest)}
}

func track(t entitler.Track) obj {
	return obj{"id": or(t.ID, "trk_all_customers"), "name": or(t.Name, "All customers")}
}

func experiment(e *entitler.Experiment) any {
	if e == nil {
		return nil
	}
	return obj{"id": e.ID, "arm": string(e.Arm)}
}

func withContext(o obj, env entitler.Environment, tr entitler.Track, release *int64, change *string, testers bool, exp *entitler.Experiment) obj {
	o["environment"], o["track"], o["release"], o["change"], o["testers"], o["experiment"] =
		environment(env), track(tr), orNull(release), orNull(change), testers, experiment(exp)
	return o
}

func upgrades(us []entitler.Upgrade) []any {
	out := []any{}
	for _, u := range us {
		out = append(out, obj{"plan": u.Plan, "name": u.Name, "move": string(u.Move), "action": string(u.Action), "reason": orNull(u.Reason)})
	}
	return out
}

func sources(ss []entitler.Source) []any {
	out := []any{}
	for _, s := range ss {
		o := obj{"type": string(s.Type)}
		switch s.Type {
		case entitler.SourcePlan, entitler.SourceAddOn:
			o["plan"], o["name"], o["version"], o["value"] = s.Plan, s.Name, s.Version, value(s.Value)
			if s.Type == entitler.SourcePlan {
				o["byDefault"] = s.ByDefault
			} else {
				o["quantity"] = s.Quantity
			}
		case entitler.SourceGrant:
			o["grant"], o["until"], o["value"] = s.Grant, atOrNull(s.Until), value(s.Value)
		case entitler.SourceBanked:
			o["value"] = s.Banked
		case entitler.SourceGroup:
			o["features"] = stringList(s.Features)
		}
		out = append(out, o)
	}
	return out
}

func stringList(ss []string) []any {
	out := []any{}
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}

func meter(o obj, m *entitler.Meter) obj {
	if m != nil {
		o["used"], o["held"], o["remaining"], o["resetsAt"] = m.Used, m.Held, limit(m.Remaining), atOrNull(m.ResetsAt)
	}
	return o
}

func checkObj(c entitler.Check) obj {
	val := c.Value
	if val == nil {
		val = entitler.Amount(0)
		if c.Entitled {
			val = entitler.On{}
		}
	}
	o := obj{
		"customer": or(c.Customer, "user_1"), "asOf": at(c.AsOf), "feature": c.Feature,
		"type": string(or(c.Type, entitler.FeatureTypeBoolean)), "entitled": c.Entitled, "value": value(val),
		"sources": sources(c.Sources), "upgrades": upgrades(c.Upgrades),
	}
	return withContext(meter(o, c.Meter), c.Environment, c.Track, c.Release, c.Change, c.Testers, c.Experiment)
}

// Check writes a check answer (GET /customers/{id}/entitlements/{feature}).
func Check(c entitler.Check) []byte { return encode(checkObj(c)) }

func entitlement(e entitler.Entitlement) obj {
	val := e.Value
	if val == nil {
		val = entitler.Amount(0)
		if e.Entitled {
			val = entitler.On{}
		}
	}
	return meter(obj{
		"key": e.Key, "type": string(or(e.Type, entitler.FeatureTypeBoolean)), "value": value(val),
		"entitled": e.Entitled, "sources": sources(e.Sources), "upgrades": upgrades(e.Upgrades),
	}, e.Meter)
}

func entitlementList(es []entitler.Entitlement) []any {
	out := []any{}
	for _, e := range es {
		out = append(out, entitlement(e))
	}
	return out
}

// Entitlements writes an entitlement list (GET /customers/{id}/entitlements).
func Entitlements(e entitler.Entitlements) []byte {
	o := obj{"customer": or(e.Customer, "user_1"), "asOf": at(e.AsOf), "entitlements": entitlementList(e.Items)}
	return encode(withContext(o, e.Environment, e.Track, e.Release, e.Change, e.Testers, e.Experiment))
}

func planSummary(p entitler.PlanSummary) obj {
	return obj{"id": p.ID, "key": p.Key, "name": p.Name}
}

func planSummaryOrNull(p *entitler.PlanSummary) any {
	if p == nil {
		return nil
	}
	return planSummary(*p)
}

func product(p *entitler.Product) any {
	if p == nil {
		return nil
	}
	return obj{"key": p.Key, "name": p.Name}
}

func period(p *entitler.Period) any {
	if p == nil {
		return nil
	}
	return obj{"key": p.Key, "label": p.Label}
}

func pending(p entitler.PendingChange) any {
	switch p := p.(type) {
	case entitler.PendingMove:
		return obj{"type": "move", "plan": planSummary(p.Plan)}
	case entitler.PendingCancel:
		return obj{"type": "cancel", "movingTo": planSummaryOrNull(p.MovingTo)}
	case entitler.PendingBilling:
		return obj{"type": "billing", "billing": string(p.Billing)}
	case entitler.PendingUnknown:
		return obj{"type": p.Type}
	}
	return nil
}

func price(p *entitler.Price) any {
	if p == nil {
		return nil
	}
	return obj{"amount": p.Amount, "currency": p.Currency, "interval": enumOrNull(p.Interval),
		"intervalCount": max(p.IntervalCount, 1), "tax": string(or(p.Tax, entitler.TaxUnspecified))}
}

func ids(m map[string]string) obj {
	o := obj{}
	for k, v := range m {
		o[k] = v
	}
	return o
}

func sku(s entitler.OfferedSKU) obj {
	return obj{"period": period(s.Period), "connector": s.Connector, "ids": ids(s.IDs), "price": price(s.Price)}
}

func moveOptions(options []entitler.MoveOption) []any {
	out := []any{}
	for _, m := range options {
		periods, impact, skus := []any{}, []any{}, []any{}
		for _, p := range m.Periods {
			periods = append(periods, period(&p))
		}
		for _, i := range m.Impact {
			impact = append(impact, obj{"kind": string(i.Kind), "text": i.Text})
		}
		for _, s := range m.SKUs {
			skus = append(skus, sku(s))
		}
		out = append(out, obj{
			"plan": obj{"id": m.Plan.ID, "key": m.Plan.Key, "name": m.Plan.Name, "kind": string(or(m.Plan.Kind, entitler.PlanKindPlan)),
				"description": m.Plan.Description, "default": m.Plan.Default},
			"product": product(m.Product), "from": planSummaryOrNull(m.From), "move": string(or(m.Move, entitler.MoveUpgrade)),
			"action": string(or(m.Action, entitler.ActionBuy)), "reason": orNull(m.Reason), "when": string(or(m.When, entitler.WhenNow)),
			"periods": periods, "impact": impact, "skus": skus,
		})
	}
	return out
}

// CustomerPlans writes a customer's plans (GET /customers/{id}/plans).
func CustomerPlans(p entitler.CustomerPlans) []byte {
	held := []any{}
	for _, h := range p.Held {
		held = append(held, obj{
			"plan":    obj{"id": h.Plan.ID, "key": h.Plan.Key, "name": h.Plan.Name, "kind": string(or(h.Plan.Kind, entitler.PlanKindPlan))},
			"product": product(h.Product), "version": orNull(h.Version), "byDefault": h.ByDefault, "period": period(h.Period),
			"renewsAt": atOrNull(h.RenewsAt), "pending": pending(h.Pending), "billedBy": enumOrNull(h.BilledBy),
		})
	}
	o := obj{"customer": or(p.Customer, "user_1"), "asOf": at(p.AsOf), "held": held, "options": moveOptions(p.Options)}
	return encode(withContext(o, p.Environment, p.Track, p.Release, p.Change, p.Testers, p.Experiment))
}

// Pricing writes pricing (GET /pricing and GET /customers/{id}/pricing).
func Pricing(p entitler.Pricing) []byte {
	products, plans := []any{}, []any{}
	for _, pr := range p.Products {
		products = append(products, obj{"key": pr.Key, "name": pr.Name, "defaultPlan": orNull(pr.DefaultPlan)})
	}
	for _, pl := range p.Plans {
		periods, listings := []any{}, []any{}
		for _, pe := range pl.Periods {
			periods = append(periods, obj{"label": pe.Label, "count": max(pe.Count, 1), "unit": string(or(pe.Unit, entitler.UnitMonths)), "key": pe.Key})
		}
		for _, l := range pl.Listings {
			channels := []any{}
			for _, c := range l.Channels {
				var ch any
				if c.IDs != nil {
					ch = ids(c.IDs)
				}
				channels = append(channels, obj{"channel": obj{"provider": string(c.Channel.Provider), "connectionId": c.Channel.ConnectionID},
					"name": c.Name, "mode": enumOrNull(c.Mode), "purchasable": c.Purchasable, "ids": ch, "price": price(c.Price)})
			}
			listings = append(listings, obj{"period": period(l.Period), "channels": channels})
		}
		features := obj{}
		for k, v := range pl.Features {
			features[k] = value(v)
		}
		plans = append(plans, obj{
			"id": pl.ID, "key": pl.Key, "name": pl.Name, "description": pl.Description,
			"kind": string(or(pl.Kind, entitler.PlanKindPlan)), "product": orNull(pl.Product), "salesLed": pl.SalesLed,
			"status": string(or(pl.Status, entitler.PlanActive)), "version": orNull(pl.Version), "default": pl.Default,
			"periods": periods, "attachesTo": stringList(pl.AttachesTo), "features": features, "listings": listings,
		})
	}
	o := obj{"customer": orNull(p.Customer), "defaultPlan": orNull(p.DefaultPlan), "products": products, "plans": plans}
	return encode(withContext(o, p.Environment, p.Track, p.Release, p.Change, p.Testers, p.Experiment))
}

// FeatureList writes the feature list (GET /pricing/features).
func FeatureList(l entitler.FeatureList) []byte {
	features := []any{}
	for _, f := range l.Features {
		var window any
		if f.ResetEvery != nil {
			w := obj{"count": max(f.ResetEvery.Count, 1), "unit": string(or(f.ResetEvery.Unit, entitler.UnitMonths))}
			if f.ResetEvery.Anchor != "" {
				w["anchor"] = string(f.ResetEvery.Anchor)
			}
			window = w
		}
		features = append(features, obj{"id": f.ID, "key": f.Key, "name": f.Name, "type": string(or(f.Type, entitler.FeatureTypeBoolean)),
			"description": f.Description, "unit": f.Unit, "resetEvery": window, "archived": f.Archived, "includes": stringList(f.Includes)})
	}
	return encode(obj{"environment": environment(l.Environment), "track": track(l.Track), "release": orNull(l.Release),
		"change": orNull(l.Change), "features": features})
}

func usageResultObj(r entitler.UsageResult) obj {
	o := checkObj(r.Check)
	o["type"] = string(or(r.Type, entitler.FeatureTypeMetered))
	if r.Meter == nil {
		meter(o, &entitler.Meter{Remaining: entitler.Amount(0)})
	}
	o["outcome"], o["refusal"], o["id"], o["holdId"] = string(or(r.Outcome, entitler.OutcomeRecorded)), enumOrNull(r.Refusal), orNull(r.ID), orNull(r.HoldID)
	o["mode"], o["amount"], o["meterChange"], o["overBy"], o["late"] = string(or(r.Mode, entitler.Gate)), r.Amount, r.MeterChange, r.OverBy, r.Late
	o["occurredAt"], o["expiresAt"], o["reportedAs"] = atOrNull(r.OccurredAt), atOrNull(r.ExpiresAt), string(or(r.ReportedAs, entitler.UsageSourceAPI))
	return o
}

// UsageResult writes the answer to a usage report, hold, settlement, release, cancel or adjustment.
func UsageResult(r entitler.UsageResult) []byte { return encode(usageResultObj(r)) }

func usageLog(l entitler.UsageLog) obj {
	items := []any{}
	for _, e := range l.Items {
		kind := or(e.Kind, entitler.UsageEventUse)
		var setTo any
		switch {
		case kind == entitler.UsageEventUse:
		case e.SetTo != nil:
			setTo = *e.SetTo
		case kind == entitler.UsageEventAdjust:
			setTo = 0
		}
		items = append(items, obj{"id": e.ID, "feature": e.Feature, "source": string(or(e.Source, entitler.UsageSourceAPI)),
			"by": orNull(e.By), "actor": orNull(e.Actor), "reason": orNull(e.Reason), "at": at(e.At),
			"cancelledAt": atOrNull(e.CancelledAt), "kind": string(kind), "amount": e.Amount, "setTo": setTo})
	}
	var next any
	if l.Next != "" {
		next = l.Next
	}
	return obj{"items": items, "next": next}
}

// CustomerUsage writes a customer's usage (GET /customers/{id}/usage).
func CustomerUsage(u entitler.CustomerUsage) []byte {
	features := []any{}
	for _, f := range u.Features {
		var remaining any
		if f.Remaining != nil {
			remaining = limit(f.Remaining)
		}
		val := f.Value
		if val == nil {
			val = entitler.Amount(0)
		}
		features = append(features, obj{"feature": f.Feature, "type": string(or(f.Type, entitler.FeatureTypeMetered)),
			"entitled": f.Entitled, "value": value(val), "sources": sources(f.Sources), "used": f.Used, "held": f.Held,
			"remaining": remaining, "resetsAt": atOrNull(f.ResetsAt)})
	}
	o := obj{"customer": or(u.Customer, "user_1"), "asOf": at(u.AsOf), "metersStartAgainAt": atOrNull(u.MetersStartAgainAt),
		"features": features, "log": usageLog(u.Log)}
	return encode(withContext(o, u.Environment, u.Track, u.Release, u.Change, u.Testers, u.Experiment))
}

func customerSummary(c entitler.CustomerSummary) obj {
	var plan any
	if c.Plan != nil {
		plan = obj{"id": c.Plan.ID, "key": c.Plan.Key, "name": c.Plan.Name, "version": c.Plan.Version}
	}
	plans := []any{}
	for _, p := range c.Plans {
		plans = append(plans, obj{"id": p.ID, "key": p.Key, "name": p.Name, "version": p.Version, "product": p.Product})
	}
	metadata := ids(c.Metadata)
	return obj{"id": or(c.ID, "cus_1"), "externalId": or(c.ExternalID, "user_1"), "name": c.Name, "email": c.Email,
		"environmentId": or(c.EnvironmentID, "env_test"), "sample": c.Sample, "createdAt": at(c.CreatedAt), "plan": plan,
		"plans": plans, "defaultPlan": planSummaryOrNull(c.DefaultPlan), "status": or(c.Status, "active"),
		"kind": string(or(c.Kind, entitler.CustomerDefault)), "metadata": metadata, "track": track(c.Track), "testCustomer": c.TestCustomer}
}

// CustomerSummary writes a customer as Update answers one (PATCH /customers/{id}).
func CustomerSummary(c entitler.CustomerSummary) []byte { return encode(customerSummary(c)) }

// CustomerPage writes a page of the customer list (GET /customers).
func CustomerPage(p entitler.CustomerPage) []byte {
	items := []any{}
	for _, c := range p.Items {
		items = append(items, customerSummary(c))
	}
	var next any
	if p.Next != "" {
		next = p.Next
	}
	lim := p.Limit
	if lim == nil {
		lim = entitler.Unlimited{}
	}
	return encode(obj{"items": items, "next": next, "used": p.Used, "limit": limit(lim)})
}

func purchase(p entitler.Purchase) obj {
	var channel any
	if p.Channel != nil {
		channel = obj{"provider": string(p.Channel.Provider), "connectionId": p.Channel.ConnectionID}
	}
	return obj{"money": string(or(p.Money, entitler.MoneyTest)), "channel": channel, "release": orNull(p.Release),
		"change": orNull(p.Change), "arm": enumOrNull(p.Arm)}
}

func addOns(as []entitler.HeldAddOn) []any {
	out := []any{}
	for _, a := range as {
		out = append(out, obj{"plan": planSummary(a.Plan), "version": a.Version, "quantity": a.Quantity, "countable": a.Countable,
			"addedAt": at(a.AddedAt), "movingTo": planSummaryOrNull(a.MovingTo), "purchase": purchase(a.Purchase)})
	}
	return out
}

func subscription(s *entitler.Subscription) any {
	if s == nil {
		return nil
	}
	var billing any
	if s.Billing != nil {
		billing = obj{"provider": or(s.Billing.Provider, "stripe"), "startsAt": at(s.Billing.StartsAt), "endsAt": at(s.Billing.EndsAt),
			"trialEndsAt": atOrNull(s.Billing.TrialEndsAt), "cancelsAt": atOrNull(s.Billing.CancelsAt)}
	}
	var override any
	if s.Override != nil {
		var billedBy any
		if s.Override.BilledBy != nil {
			billedBy = obj{"provider": string(s.Override.BilledBy.Provider), "connectionId": s.Override.BilledBy.ConnectionID}
		}
		override = obj{"from": planSummary(s.Override.From), "period": period(s.Override.Period), "billedBy": billedBy,
			"since": at(s.Override.Since), "by": s.Override.By}
	}
	return obj{"product": product(&s.Product), "plan": planSummary(s.Plan), "version": s.Version, "cohort": orNull(s.Cohort),
		"period": period(s.Period), "startedAt": at(s.StartedAt), "renewsAt": at(s.RenewsAt), "billing": billing,
		"until": atOrNull(s.Until), "addOns": addOns(s.AddOns), "pending": pending(s.Pending), "purchase": purchase(s.Purchase),
		"override": override}
}

func grant(g entitler.Grant) obj {
	var v string
	switch val := g.Value.(type) {
	case entitler.Amount:
		v = strconv.FormatInt(int64(val), 10)
	case entitler.Unlimited:
		v = "unlimited"
	}
	return obj{"id": g.ID, "feature": g.Feature, "value": v, "from": at(g.From), "until": atOrNull(g.Until),
		"revokedAt": atOrNull(g.RevokedAt), "reason": g.Reason, "by": or(g.By, "SDK tests"), "actor": orNull(g.Actor)}
}

// CustomerDetail writes a customer's details (GET /customers/{id}, and POST /customers).
func CustomerDetail(d entitler.CustomerDetail) []byte {
	products, grants, activity := []any{}, []any{}, []any{}
	for _, p := range d.Products {
		products = append(products, obj{"product": product(&p.Product), "defaultPlan": planSummaryOrNull(p.DefaultPlan),
			"subscription": subscription(p.Subscription)})
	}
	for _, g := range d.Grants {
		grants = append(grants, grant(g))
	}
	for _, a := range d.Activity {
		activity = append(activity, obj{"text": a.Text, "at": at(a.At), "by": orNull(a.By), "actor": orNull(a.Actor), "reason": orNull(a.Reason)})
	}
	banked := obj{}
	for k, v := range d.Banked {
		banked[k] = v
	}
	return encode(obj{"customer": customerSummary(d.Customer), "asOf": at(d.AsOf), "subscription": subscription(d.Subscription),
		"defaultPlan": planSummaryOrNull(d.DefaultPlan), "products": products, "addOns": addOns(d.AddOns),
		"entitlements": entitlementList(d.Entitlements), "banked": banked, "moveOptions": moveOptions(d.MoveOptions),
		"grants": grants, "usage": usageLog(d.Usage), "activity": activity, "environment": environment(d.Environment)})
}

// RegisteredCustomer writes the answer to registering a customer (PUT /customers/{id}).
func RegisteredCustomer(r entitler.RegisteredCustomer) []byte {
	return encode(obj{"id": or(r.ID, "cus_1"), "externalId": or(r.ExternalID, "user_1"), "environmentId": or(r.EnvironmentID, "env_test"),
		"createdAt": at(r.CreatedAt), "created": r.Created})
}

// IssuedCustomerToken writes a minted customer token (POST /customers/{id}/tokens).
func IssuedCustomerToken(t entitler.IssuedCustomerToken) []byte {
	scopes := []any{}
	for _, s := range t.Scopes {
		scopes = append(scopes, string(s))
	}
	return encode(obj{"token": or(t.Token, "token"), "customer": or(t.Customer, "user_1"), "scopes": scopes, "expiresAt": at(t.ExpiresAt)})
}

// IssuedSnapshot writes a minted snapshot (POST /customers/{id}/snapshots).
func IssuedSnapshot(s entitler.IssuedSnapshot) []byte {
	return encode(obj{"token": or(s.Token, "snapshot"), "expiresAt": at(s.ExpiresAt), "keyId": or(s.KeyID, "key_1")})
}

// CustomerTrack writes the answer to SetTrack (PUT /customers/{id}/track).
func CustomerTrack(t entitler.CustomerTrack) []byte {
	return encode(obj{"customer": or(t.Customer, "user_1"), "environmentId": or(t.EnvironmentID, "env_test"), "track": track(t.Track),
		"source": enumOrNull(t.Source), "previousTrackId": orNull(t.PreviousTrackID)})
}

func drift(d *entitler.BillingDrift) any {
	if d == nil {
		return nil
	}
	return obj{"billedPlan": d.BilledPlan, "heldPlan": orNull(d.HeldPlan), "observedAt": at(d.ObservedAt)}
}

func skuOrNull(s *entitler.OfferedSKU) any {
	if s == nil {
		return nil
	}
	return sku(*s)
}

// CustomerBilling writes the provider's view of a customer (GET /customers/{id}/billing).
func CustomerBilling(b entitler.CustomerBilling) []byte {
	products := []any{}
	for _, p := range b.Products {
		products = append(products, obj{"product": orNull(p.Product), "status": enumOrNull(p.Status), "sku": skuOrNull(p.SKU),
			"items": p.Items, "drift": drift(p.Drift)})
	}
	return encode(obj{"provider": enumOrNull(b.Provider), "status": enumOrNull(b.Status), "sku": skuOrNull(b.SKU),
		"items": b.Items, "drift": drift(b.Drift), "products": products})
}

func planChange(p entitler.PlanChange) obj {
	return obj{"product": product(p.Product), "plan": planSummaryOrNull(p.Plan), "quantity": orNull(p.Quantity),
		"effective": string(or(p.Effective, entitler.EffectiveNow)), "at": at(p.At), "until": atOrNull(p.Until), "changed": p.Changed}
}

// PlanChange writes the answer to a plan or add-on change.
func PlanChange(p entitler.PlanChange) []byte { return encode(planChange(p)) }

// SubscribeStep writes the answer to Subscribe (POST /customers/{id}/subscription).
func SubscribeStep(s entitler.SubscribeStep) []byte {
	switch s.Next {
	case entitler.NextDone, "":
		change := entitler.PlanChange{Changed: true}
		if s.Change != nil {
			change = *s.Change
		}
		o := planChange(change)
		o["next"] = "done"
		return encode(o)
	case entitler.NextPay:
		return encode(obj{"next": "pay", "url": s.URL})
	case entitler.NextManage:
		return encode(obj{"next": "manage", "billedBy": string(or(s.BilledBy, entitler.ProviderApple))})
	}
	return encode(obj{"next": string(s.Next)})
}

// GrantChange writes the answer to Grant and RevokeGrant.
func GrantChange(g entitler.GrantChange) []byte { return encode(obj{"grant": grant(g.Grant)}) }

// BillingPortal writes the billing portal page (POST /customers/{id}/billing-portal).
func BillingPortal(p entitler.BillingPortal) []byte {
	return encode(obj{"provider": or(p.Provider, "stripe"), "url": or(p.URL, "https://billing.example.com/session")})
}

// BillingSync writes the answer to SyncBilling (POST /customers/{id}/billing/sync).
func BillingSync(s entitler.BillingSync) []byte { return encode(obj{"changed": s.Changed}) }

// Scopes writes what GET /keys/self answers for a key.
func Scopes(s entitler.Scopes) []byte {
	scopes := []any{}
	for _, sc := range s.List {
		scopes = append(scopes, string(sc))
	}
	return encode(obj{"id": "key_1", "name": "Test key", "kind": "secret", "orgId": "org_1", "projectId": "prj_1",
		"environmentId": "env_test", "scopes": scopes, "allowedOrigins": []any{}, "createdAt": at(time.Time{}),
		"revokedAt": nil, "registration": s.Registration})
}

// SnapshotKeys writes the snapshot key set (GET /customers/snapshot-keys).
func SnapshotKeys(k entitler.SnapshotKeys) []byte {
	data, _ := k.MarshalJSON()
	return data
}

// BatchResult writes the answer to POST /usage/events for the events of one request, in order.
func BatchResult(r entitler.BatchResult) []byte {
	results := []any{}
	for i, e := range r.Results {
		var errObj any
		if e.Error != nil {
			errObj = obj{"code": string(e.Error.Code), "message": e.Error.Message}
		}
		results = append(results, obj{"index": i, "outcome": string(or(e.Outcome, entitler.OutcomeRecorded)), "id": orNull(e.ID),
			"late": e.Late, "error": errObj})
	}
	return encode(obj{"results": results, "recorded": r.Recorded, "duplicates": r.Duplicates, "errors": r.Errors})
}

// Error writes an API error body: {"error": {"code", "message"}}.
func Error(code entitler.Code, message string) []byte {
	return encode(obj{"error": obj{"code": string(code), "message": message}})
}
