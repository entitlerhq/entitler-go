package entitler

import (
	"context"
	"strconv"
	"time"
)

// ServerCustomer is a customer on the server: every [Customer] method, plus registration,
// details, tokens, tracks and the company's decisions. Server.Customer returns one without a
// request or an error; a blank or invalid id fails each of its methods instead, before any
// request. Its reads the API answers at an instant take ReadParams.AsOf.
type ServerCustomer struct {
	*customer
}

// DetailsParams are the options of Details; nil for none.
type DetailsParams struct {
	// Cursor starts the usage log at the page a previous answer's Next names.
	Cursor string
	// AsOf reads at another instant, as ReadParams.AsOf says.
	AsOf time.Time
}

// RegisterParams are the options of Register; nil for none.
type RegisterParams struct {
	// Name is the customer's name; nil leaves it alone.
	Name *string
	// Email is the customer's email; nil leaves it alone.
	Email *string
	// Metadata merges into the vendor's metadata: a nil value removes that key, and a key left out
	// is left alone.
	Metadata map[string]*string
	// Visitor is the visitor id the person had signed out, so they keep their experiment arm.
	Visitor string
	// IdempotencyKey names this registration; a new UUID when "".
	IdempotencyKey string
}

// UpdateParams are the options of Update.
type UpdateParams struct {
	// Name is the customer's name; nil leaves it alone.
	Name *string
	// Email is the customer's email; nil leaves it alone.
	Email *string
	// Metadata merges into the vendor's metadata: a nil value removes that key.
	Metadata map[string]*string
	// IdempotencyKey names this update; a new UUID when "".
	IdempotencyKey string
}

// EraseParams are the options of Erase; nil for none.
type EraseParams struct {
	// IdempotencyKey names this erasure; a new UUID when "".
	IdempotencyKey string
}

// TokenParams are the options of Token; nil for none.
type TokenParams struct {
	// Scopes are the token's scopes; ScopeEntitlementsRead alone when empty. Ask for
	// ScopeUsageWrite only when the app records usage itself, and for ScopeBillingSelf only for
	// people who may buy for the customer (a workspace's owners, not every member).
	Scopes []Scope
	// TTL is how long the token lasts, in whole seconds from 60 to 3,600; 0 for the API's default.
	TTL time.Duration
}

// SetTrackParams are the options of SetTrack; nil for none.
type SetTrackParams struct {
	// IdempotencyKey names this change; a new UUID when "".
	IdempotencyKey string
}

func details(name, email *string, metadata map[string]*string) map[string]any {
	body := map[string]any{}
	if name != nil {
		body["name"] = *name
	}
	if email != nil {
		body["email"] = *email
	}
	if metadata != nil {
		m := make(map[string]any, len(metadata))
		for k, v := range metadata {
			if v == nil {
				m[k] = nil
			} else {
				m[k] = *v
			}
		}
		body["metadata"] = m
	}
	return body
}

// Register keeps Entitler's copy of the customer current: call it at sign-up and at sign-in with
// the latest details. It sends only the details given (no body when none), and Created says
// whether this call created the customer. Changing an existing customer's details needs
// customers:write or customers:profile.
func (c *ServerCustomer) Register(ctx context.Context, p *RegisterParams) (*RegisteredCustomer, error) {
	var params RegisterParams
	if p != nil {
		params = *p
	}
	key, err := optionalKey(params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	var body any
	if b := details(params.Name, params.Email, params.Metadata); len(b) > 0 {
		body = b
	}
	r, err := c.write("PUT", "", key, body, true)
	if err != nil {
		return nil, err
	}
	if params.Visitor != "" {
		if err := checkVisitor(params.Visitor); err != nil {
			return nil, err
		}
		r.visitor = params.Visitor
	}
	registered, m, err := fetch(ctx, c.c, r, decodeRegisteredCustomer)
	if err != nil {
		return nil, err
	}
	registered.Replayed = m.replayed
	return &registered, nil
}

// Details answers everything Entitler holds about the customer, for support tools, with the usage
// log's first page from Cursor when given; Usage.All iterates every entry.
func (c *ServerCustomer) Details(ctx context.Context, p *DetailsParams) (*CustomerDetail, error) {
	var params DetailsParams
	if p != nil {
		params = *p
	}
	asOf, err := c.asOf(params.AsOf)
	if err != nil {
		return nil, err
	}
	page := func(ctx context.Context, cursor string) (CustomerDetail, error) {
		r, err := c.read("", false)
		if err != nil {
			return CustomerDetail{}, err
		}
		r.asOf = asOf
		if cursor != "" {
			q, err := queryValue(cursor)
			if err != nil {
				return CustomerDetail{}, err
			}
			r.query = []string{"cursor=" + q}
		}
		detail, _, err := fetch(ctx, c.c, r, decodeCustomerDetail)
		return detail, err
	}
	detail, err := page(ctx, params.Cursor)
	if err != nil {
		return nil, err
	}
	detail.Usage.more = func(ctx context.Context, cursor string) (UsageLog, error) {
		next, err := page(ctx, cursor)
		return next.Usage, err
	}
	return &detail, nil
}

// Update changes the customer's details. A nil metadata value removes that key.
func (c *ServerCustomer) Update(ctx context.Context, p *UpdateParams) (*CustomerSummary, error) {
	var params UpdateParams
	if p != nil {
		params = *p
	}
	key, err := optionalKey(params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	r, err := c.write("PATCH", "", key, details(params.Name, params.Email, params.Metadata), true)
	if err != nil {
		return nil, err
	}
	summary, m, err := fetch(ctx, c.c, r, decodeCustomerSummary)
	if err != nil {
		return nil, err
	}
	summary.Replayed = m.replayed
	return &summary, nil
}

// Erase is the account deletion app stores require: it ends any Stripe subscription now, then
// erases the customer's details, usage and events. Store subscriptions are invisible to Entitler,
// so cancel them through Apple or Google first. Erasing twice is safe.
func (c *ServerCustomer) Erase(ctx context.Context, p *EraseParams) error {
	var given string
	if p != nil {
		given = p.IdempotencyKey
	}
	key, err := optionalKey(given)
	if err != nil {
		return err
	}
	r, err := c.write("DELETE", "", key, nil, true)
	if err != nil {
		return err
	}
	r.query = []string{"erase=true"}
	r.empty = true
	_, _, err = fetch(ctx, c.c, r, decodeCustomerSummary)
	return err
}

// Token mints a customer token for an in-app client: entitlements:read alone unless Scopes says
// otherwise. Mint one only for the signed-in customer your server authenticated: a route that
// mints for any id it is sent hands every customer's access to anyone.
func (c *ServerCustomer) Token(ctx context.Context, p *TokenParams) (*IssuedCustomerToken, error) {
	body := map[string]any{}
	if p != nil {
		if len(p.Scopes) > 0 {
			body["scopes"] = p.Scopes
		}
		if p.TTL != 0 {
			seconds, err := wholeSeconds(p.TTL, 60, 3600, "pass the token's TTL as a whole number of seconds from 60 to 3600")
			if err != nil {
				return nil, err
			}
			body["ttlSeconds"] = seconds
		}
	}
	r, err := c.write("POST", "/tokens", newUUID(), body, false)
	if err != nil {
		return nil, err
	}
	token, _, err := fetch(ctx, c.c, r, decodeIssuedCustomerToken)
	if err != nil {
		return nil, err
	}
	return &token, nil
}

// SetTrack puts the customer on the track named, by its name, which stays the same in every
// environment, or returns them to All customers when track is nil. The server key preset lacks
// tracks:assign, so a key minted from it answers 403 scope_required: add the scope to the key.
func (c *ServerCustomer) SetTrack(ctx context.Context, track *string, p *SetTrackParams) (*CustomerTrack, error) {
	var name any
	if track != nil {
		if blank(*track) {
			return nil, argError("name the track by its name")
		}
		name = *track
	}
	var given string
	if p != nil {
		given = p.IdempotencyKey
	}
	key, err := optionalKey(given)
	if err != nil {
		return nil, err
	}
	r, err := c.write("PUT", "/track", key, map[string]any{"track": name}, true)
	if err != nil {
		return nil, err
	}
	answer, m, err := fetch(ctx, c.c, r, decodeCustomerTrack)
	if err != nil {
		return nil, err
	}
	answer.Replayed = m.replayed
	return &answer, nil
}

// Billing answers the payment provider's view of the customer (status, the SKU it bills, drift),
// for support tools. Billing pages read Plans instead.
func (c *ServerCustomer) Billing(ctx context.Context) (*CustomerBilling, error) {
	r, err := c.read("/billing", false)
	if err != nil {
		return nil, err
	}
	billing, _, err := fetch(ctx, c.c, r, decodeCustomerBilling)
	if err != nil {
		return nil, err
	}
	return &billing, nil
}

// PlanChoice is what SetPlan moves a customer to: a [Plan] by key or public id, or a [SKU] the
// customer bought, which names its own period. Pass nil, with SetPlanParams.Product, to end the
// customer's plan in that product.
type PlanChoice interface{ planChoice() map[string]any }

// Plan names a plan by its key or public id.
type Plan string

// SKU names a product a provider sells, such as an App Store or Google Play purchase the vendor's
// server verified.
type SKU struct {
	// Connector names the provider: apple, google, stripe….
	Connector string
	// IDs are the provider's ids by field, such as productId.
	IDs map[string]string
}

func (p Plan) planChoice() map[string]any { return map[string]any{"plan": string(p)} }

func (s SKU) planChoice() map[string]any {
	ids := s.IDs
	if ids == nil {
		ids = map[string]string{}
	}
	return map[string]any{"sku": map[string]any{"connector": s.Connector, "ids": ids}}
}

// Decision is who decided a company decision, and why, for the customer's activity.
type Decision struct {
	// Reason is why, at most 200 characters.
	Reason string
	// Actor names the person or system that decided, 1 to 200 characters, such as a support
	// agent's id or hubspot.
	Actor string
}

func (d Decision) into(body map[string]any) map[string]any {
	if d.Reason != "" {
		body["reason"] = d.Reason
	}
	if d.Actor != "" {
		body["actor"] = d.Actor
	}
	return body
}

// SetPlanParams are the options of SetPlan.
type SetPlanParams struct {
	Decision
	// Product is the product whose plan ends, by key; only with a nil choice.
	Product string
	// Period is the billing period's key, such as yearly; never with a SKU's own period or when ending a plan.
	Period string
	// When is WhenNow or WhenEnd (at renewal); "" for the project's policy.
	When When
	// Billing is BillingProvider (the default), BillingKeep or BillingEnd.
	Billing Billing
	// Until is an instant in the future, after any change booked for renewal, when the customer
	// returns to the product's default plan unless a later SetPlan moved them or set a later Until.
	// While Stripe bills the product it needs BillingEnd. The zero time for none.
	Until time.Time
	// Version is VersionLatest to move the customer now to the newest version of the plan they hold.
	Version PlanVersion
	// Register registers a customer not registered yet.
	Register bool
	// IdempotencyKey names this decision, after the event (this deal won, this webhook delivery);
	// a new UUID when "".
	IdempotencyKey string
}

// SetPlan makes the company's decision on the customer's plan, under no self-serve rules: a plan
// by key or public id (Plan("enterprise")), a SKU the customer bought, or nil with Product to end
// their plan in that product. The plan they already hold, with the same period, billing and
// until, answers Changed false. A store SKU never touches Stripe: when Stripe already bills the
// product the API answers 409 billed_elsewhere and changes nothing. Charging through the provider
// can answer 402 payment_required with any Payment status.
func (c *ServerCustomer) SetPlan(ctx context.Context, choice PlanChoice, p *SetPlanParams) (*PlanChange, error) {
	var params SetPlanParams
	if p != nil {
		params = *p
	}
	var body map[string]any
	switch choice := choice.(type) {
	case nil:
		if params.Product == "" {
			return nil, argError("pass product: the product whose plan ends")
		}
		if params.Period != "" || !params.Until.IsZero() || params.Version != "" {
			return nil, argError("pass no period, until or version when ending a plan")
		}
		if blank(params.Product) {
			return nil, argError("name the product by its key")
		}
		body = map[string]any{"plan": nil, "product": params.Product}
	case Plan:
		if params.Product != "" {
			return nil, argError("pass product only when ending a plan")
		}
		if err := planName(string(choice)); err != nil {
			return nil, err
		}
		body = choice.planChoice()
	default:
		if params.Product != "" {
			return nil, argError("pass product only when ending a plan")
		}
		body = choice.planChoice()
	}
	if params.Period != "" {
		body["period"] = params.Period
	}
	if params.When != "" {
		body["when"] = params.When
	}
	if params.Billing != "" {
		body["billing"] = params.Billing
	}
	if !params.Until.IsZero() {
		until, err := checkInstant(params.Until, "pass until as a valid date")
		if err != nil {
			return nil, err
		}
		body["until"] = until
	}
	if params.Version != "" {
		body["version"] = params.Version
	}
	if params.Register {
		body["register"] = true
	}
	key, err := optionalKey(params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	r, err := c.write("PUT", "/plan", key, params.Decision.into(body), true)
	if err != nil {
		return nil, err
	}
	return c.planChange(ctx, r)
}

// SetAddOnParams are the options of SetAddOn; nil for none.
type SetAddOnParams struct {
	Decision
	// SKU is an App Store or Google Play SKU that sells this add-on: the store is recorded as its
	// biller and Stripe is never charged (409 billed_elsewhere when Stripe bills the product).
	SKU *SKU
	// Replaces is the add-on this one takes the place of, by key or public id, swapped in one write
	// as Plans lists a replace move (quantity at least 1).
	Replaces string
	// IdempotencyKey names this decision; a new UUID when "".
	IdempotencyKey string
}

// SetAddOn adds an add-on, sets its quantity, or removes it at 0 (quantity 0 to 10,000), taking
// effect now. Naming an add-on's held quantity undoes an add-on move booked for renewal.
func (c *ServerCustomer) SetAddOn(ctx context.Context, addOn string, quantity int64, p *SetAddOnParams) (*PlanChange, error) {
	seg, err := segment(addOn, "name the plan by its id or its key")
	if err != nil {
		return nil, err
	}
	if quantity < 0 || quantity > 10000 {
		return nil, argError("pass quantity as a whole number from 0 to 10000")
	}
	var params SetAddOnParams
	if p != nil {
		params = *p
	}
	body := map[string]any{"quantity": quantity}
	if params.SKU != nil {
		body["sku"] = params.SKU.planChoice()["sku"]
	}
	if params.Replaces != "" {
		if err := planName(params.Replaces); err != nil {
			return nil, err
		}
		body["replaces"] = params.Replaces
	}
	key, err := optionalKey(params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	r, err := c.write("PUT", "/add-ons/"+seg, key, params.Decision.into(body), true)
	if err != nil {
		return nil, err
	}
	return c.planChange(ctx, r)
}

// GrantParams are the options of Grant; nil for none.
type GrantParams struct {
	Decision
	// Value is the value granted: an Amount from 0 to 999,999,999 or Unlimited; nil (or On) for an
	// on/off feature.
	Value Value
	// Days is how long the grant lasts, 0 to 3,650; 0 for no end.
	Days int
	// IdempotencyKey names this grant, after the event (this support ticket); a new UUID when "".
	IdempotencyKey string
}

// Grant gives the customer a feature, or a value of it, beyond their plan, and answers the grant
// made: keep its ID to revoke it.
func (c *ServerCustomer) Grant(ctx context.Context, f Feature, p *GrantParams) (*GrantChange, error) {
	feature, err := featureKey(f)
	if err != nil {
		return nil, err
	}
	var params GrantParams
	if p != nil {
		params = *p
	}
	body := map[string]any{"feature": feature}
	switch v := params.Value.(type) {
	case Amount:
		if v < 0 || v > 999999999 {
			return nil, argError("pass value as a whole number from 0 to 999999999, or Unlimited")
		}
		body["value"] = strconv.FormatInt(int64(v), 10)
	case Unlimited:
		body["value"] = "unlimited"
	}
	if params.Days != 0 {
		if params.Days < 0 || params.Days > 3650 {
			return nil, argError("pass days as a whole number from 0 to 3650")
		}
		body["days"] = params.Days
	}
	key, err := optionalKey(params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	r, err := c.write("POST", "/grants", key, params.Decision.into(body), true)
	if err != nil {
		return nil, err
	}
	return c.grantChange(ctx, r)
}

func (c *ServerCustomer) grantChange(ctx context.Context, r *request) (*GrantChange, error) {
	change, m, err := fetch(ctx, c.c, r, decodeGrantChange)
	if err != nil {
		return nil, err
	}
	change.Replayed = m.replayed
	return &change, nil
}

// DecisionParams are the options of RevokeGrant and CancelUsage; nil for none.
type DecisionParams struct {
	Decision
	// IdempotencyKey names this decision; a new UUID when "".
	IdempotencyKey string
}

func decisionBody(p *DecisionParams) (any, string, error) {
	var params DecisionParams
	if p != nil {
		params = *p
	}
	key, err := optionalKey(params.IdempotencyKey)
	if err != nil {
		return nil, "", err
	}
	if body := params.Decision.into(map[string]any{}); len(body) > 0 {
		return body, key, nil
	}
	return nil, key, nil
}

// RevokeGrant revokes a grant and answers the revoked grant.
func (c *ServerCustomer) RevokeGrant(ctx context.Context, grantID string, p *DecisionParams) (*GrantChange, error) {
	seg, err := segment(grantID, "provide the id of the grant")
	if err != nil {
		return nil, err
	}
	body, key, err := decisionBody(p)
	if err != nil {
		return nil, err
	}
	r, err := c.write("DELETE", "/grants/"+seg, key, body, true)
	if err != nil {
		return nil, err
	}
	return c.grantChange(ctx, r)
}

// AdjustMeterParams are the options of AdjustMeter: exactly one of By and To, and the idempotency key.
type AdjustMeterParams struct {
	Decision
	// By moves the meter up or down, a whole number other than 0: new(int64(-500)) gives back
	// 500 credits without racing new usage. The meter never goes below 0.
	By *int64
	// To sets the meter, a whole number of 0 or more.
	To *int64
	// IdempotencyKey is required, after the event (this support ticket), so a double submit never
	// applies twice.
	IdempotencyKey string
}

// AdjustMeter moves a meter by an amount or sets it, answering a UsageResult with OutcomeAdjusted,
// MeterChange how far it moved, and ID nil when nothing changed.
func (c *ServerCustomer) AdjustMeter(ctx context.Context, f MeteredFeature, p *AdjustMeterParams) (*UsageResult, error) {
	var params AdjustMeterParams
	if p != nil {
		params = *p
	}
	key, err := requiredKey(params.IdempotencyKey, 200)
	if err != nil {
		return nil, err
	}
	feature, err := meteredKey(f)
	if err != nil {
		return nil, err
	}
	seg, err := segment(feature, "name the feature by its key")
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	switch {
	case params.By != nil && params.To == nil && *params.By != 0 && *params.By >= -maxWhole && *params.By <= maxWhole:
		body["by"] = *params.By
	case params.To != nil && params.By == nil && *params.To >= 0 && *params.To <= maxWhole:
		body["to"] = *params.To
	default:
		return nil, argError("pass either by, a whole number other than 0, or to, a whole number of 0 or more")
	}
	return c.usageWrite(ctx, "POST", "/meters/"+seg+"/adjustments", key, params.Decision.into(body))
}

// CancelUsage cancels a usage report, a correction: it answers a UsageResult with
// OutcomeCancelled and MeterChange minus the amount that came off the meter.
func (c *ServerCustomer) CancelUsage(ctx context.Context, usageID string, p *DecisionParams) (*UsageResult, error) {
	seg, err := segment(usageID, "provide the id of the usage report")
	if err != nil {
		return nil, err
	}
	body, key, err := decisionBody(p)
	if err != nil {
		return nil, err
	}
	return c.usageWrite(ctx, "DELETE", "/usage/"+seg, key, body)
}
