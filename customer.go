package entitler

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// Customer is a customer as both clients answer one: [*ServerCustomer] from Server.Customer on a
// server, and Client.Me in an app. Code that gates features, records usage and offers the
// customer's own billing choices takes a Customer and is written once.
type Customer interface {
	// ID answers the customer's external id: the one given on the server; in an app, the one the
	// latest answer named ("" before the first answer).
	ID() string
	// Check checks one feature, through the answer cache.
	Check(ctx context.Context, f Feature, p *ReadParams) (*Check, error)
	// IsEntitled answers whether the customer is entitled to the feature, and never fails
	// because of Entitler: a blank feature or any failure answers def and goes to onError.
	IsEntitled(ctx context.Context, f Feature, def bool, p *ReadParams) bool
	// Entitlements answers the customer's entitlements, through the answer cache.
	Entitlements(ctx context.Context, p *ReadParams) (*Entitlements, error)
	// Plans answers the plans held and the moves open, through the answer cache: the paywall's
	// and the billing page's one read.
	Plans(ctx context.Context, p *ReadParams) (*CustomerPlans, error)
	// Pricing answers the pricing on sale to the customer, through the answer cache.
	Pricing(ctx context.Context, p *PricingParams) (*Pricing, error)
	// Usage answers the customer's meters and the first page of their usage log.
	Usage(ctx context.Context, p *UsageParams) (*CustomerUsage, error)
	// RecordUsage records usage of a metered feature under your own idempotency key.
	RecordUsage(ctx context.Context, f MeteredFeature, amount int64, idempotencyKey string, p *RecordUsageParams) (*UsageResult, error)
	// StartHold holds an amount before work starts and answers the open hold.
	StartHold(ctx context.Context, f MeteredFeature, amount int64, idempotencyKey string, p *HoldParams) (*Hold, error)
	// HoldUsage holds an amount and answers the hold's UsageResult, for a hold settled elsewhere.
	HoldUsage(ctx context.Context, f MeteredFeature, amount int64, idempotencyKey string, p *HoldParams) (*UsageResult, error)
	// SettleUsage settles a hold with the amount really used.
	SettleUsage(ctx context.Context, holdID string, amount int64, p *SettleUsageParams) (*UsageResult, error)
	// ReleaseUsage releases a hold, recording nothing.
	ReleaseUsage(ctx context.Context, holdID string, p *ReleaseUsageParams) (*UsageResult, error)
	// Snapshot signs the customer's entitlements for offline use.
	Snapshot(ctx context.Context, p *SnapshotParams) (*IssuedSnapshot, error)
	// Subscribe moves the customer to a plan or adds an add-on, as their own choice.
	Subscribe(ctx context.Context, plan string, p *SubscribeParams) (*SubscribeStep, error)
	// Cancel cancels the plan, or an add-on, as the customer's own choice.
	Cancel(ctx context.Context, p *CancelParams) (*PlanChange, error)
	// UndoPendingChange undoes the change booked for renewal.
	UndoPendingChange(ctx context.Context, p *CancelParams) (*PlanChange, error)
	// BillingPortal opens the provider's page for payment details and invoices.
	BillingPortal(ctx context.Context, returnURL string) (*BillingPortal, error)
	// SyncBilling reads the customer's state from the provider now.
	SyncBilling(ctx context.Context) (*BillingSync, error)

	base() *customer
}

// ReadParams are the options of a read through the answer cache; nil for none.
type ReadParams struct {
	// Revalidate skips a fresh kept answer and revalidates it with its ETag, so a page that knows
	// the customer just changed (back from paying, after a server-side upgrade) shows the change
	// at once. A 304 still answers the kept body.
	Revalidate bool
	// AsOf reads at another instant, on a ServerCustomer only: an in-app read given one fails. It
	// needs the organisation's as_of capability (409 capability_required otherwise), and shows the
	// effects of time on the plans, grants and meters in place (renewals, booked moves, grant
	// expiries, meter resets), never a release nobody has rolled out yet: to preview one, put a
	// test customer on a track for testers. The zero time reads now.
	AsOf time.Time
}

// PricingParams are the options of a pricing read; nil for none.
type PricingParams struct {
	// Visitor is a visitor id from NewVisitorID, to keep the visitor's experiment arm. An in-app
	// client sends its own when this is "".
	Visitor string
	// Revalidate skips a fresh kept answer and revalidates it.
	Revalidate bool
}

// UsageParams are the options of Usage; nil for none.
type UsageParams struct {
	// Cursor starts the usage log at the page a previous answer's Next names.
	Cursor string
	// AsOf reads at another instant, on a ServerCustomer only, as ReadParams.AsOf says.
	AsOf time.Time
}

// customer is what both clients' customers share.
type customer struct {
	c      caller
	id     string
	self   bool
	err    error
	path   string
	latest atomic.Pointer[string]
}

func newServerCustomer(c caller, id string) *customer {
	cu := &customer{c: c, id: id}
	seg, err := segment(id, "provide the id your app uses for the customer")
	cu.err = err
	cu.path = "/customers/" + seg
	return cu
}

func newSelf(c caller) *customer {
	return &customer{c: c, self: true, path: "/customers/me"}
}

func (cu *customer) base() *customer { return cu }

// ID answers the customer's external id: the one given on the server; in an app, the one the
// latest answer named ("" before the first answer).
func (cu *customer) ID() string {
	if !cu.self {
		return cu.id
	}
	if id := cu.latest.Load(); id != nil {
		return *id
	}
	return ""
}

func (cu *customer) saw(id string) {
	if cu.self && id != "" {
		cu.latest.Store(&id)
	}
}

// generationKey names the customer for write generations.
func (cu *customer) generationKey() string {
	if cu.self {
		return "me"
	}
	return cu.id
}

func (cu *customer) asOf(at time.Time) (string, error) {
	if at.IsZero() {
		return "", nil
	}
	if cu.self {
		return "", argError("pass asOf only to a server customer's reads: in-app reads are answered now")
	}
	return checkInstant(at, "pass asOf as a valid date")
}

// read prepares a GET of the customer's path.
func (cu *customer) read(suffix string, cached bool) (*request, error) {
	if cu.err != nil {
		return nil, cu.err
	}
	return &request{method: "GET", path: cu.path + suffix, cached: cached, customer: cu.generationKey()}, nil
}

// write prepares a write of the customer's path, under the idempotency key given.
func (cu *customer) write(method, suffix, key string, body any, bumps bool) (*request, error) {
	if cu.err != nil {
		return nil, cu.err
	}
	r := &request{method: method, path: cu.path + suffix, key: key, customer: cu.generationKey()}
	if body != nil {
		r.body = marshalBody(body)
	}
	if bumps {
		r.writes = []string{cu.generationKey()}
	}
	return r, nil
}

func readParams(p *ReadParams) ReadParams {
	if p == nil {
		return ReadParams{}
	}
	return *p
}

// Check checks one feature, through the answer cache. A metered check's Meter holds the meter.
func (cu *customer) Check(ctx context.Context, f Feature, p *ReadParams) (*Check, error) {
	params := readParams(p)
	key, err := featureKey(f)
	if err != nil {
		return nil, err
	}
	seg, err := segment(key, "name the feature by its key")
	if err != nil {
		return nil, err
	}
	r, err := cu.read("/entitlements/"+seg, true)
	if err != nil {
		return nil, err
	}
	if r.asOf, err = cu.asOf(params.AsOf); err != nil {
		return nil, err
	}
	r.revalidate = params.Revalidate
	check, m, err := fetch(ctx, cu.c, r, decodeCheck)
	if err != nil {
		return nil, err
	}
	check.Stale = m.stale
	cu.saw(check.Customer)
	return &check, nil
}

// IsEntitled answers Check's Entitled, a stale answer included, and never fails because of
// Entitler: when the check fails for any reason, a blank feature and an invalid timeout included,
// it answers def and passes the error to onError. A done ctx answers def without calling onError.
// Pass false for paid features (fail closed), and true only where losing a sale is worse than
// giving the feature away (fail open).
func (cu *customer) IsEntitled(ctx context.Context, f Feature, def bool, p *ReadParams) bool {
	check, err := cu.Check(ctx, f, p)
	if err != nil {
		if ctx.Err() == nil || !errors.Is(err, ctx.Err()) {
			cu.c.e.report(err)
		}
		return def
	}
	return check.Entitled
}

// Entitlements answers the customer's entitlements, groups included, through the answer cache.
func (cu *customer) Entitlements(ctx context.Context, p *ReadParams) (*Entitlements, error) {
	params := readParams(p)
	r, err := cu.read("/entitlements", true)
	if err != nil {
		return nil, err
	}
	if r.asOf, err = cu.asOf(params.AsOf); err != nil {
		return nil, err
	}
	r.revalidate = params.Revalidate
	list, m, err := fetch(ctx, cu.c, r, decodeEntitlements)
	if err != nil {
		return nil, err
	}
	list.Stale = m.stale
	cu.saw(list.Customer)
	return &list, nil
}

// Plans answers the paywall's and the billing page's one read, through the answer cache: the
// plans held (BilledBy decides between the store's own page and the billing portal) and the moves
// open (Action decides the button, SKUs are the store products). In an app it needs the
// organisation's customer portal capability (409 capability_required otherwise).
func (cu *customer) Plans(ctx context.Context, p *ReadParams) (*CustomerPlans, error) {
	params := readParams(p)
	r, err := cu.read("/plans", true)
	if err != nil {
		return nil, err
	}
	if r.asOf, err = cu.asOf(params.AsOf); err != nil {
		return nil, err
	}
	r.revalidate = params.Revalidate
	plans, m, err := fetch(ctx, cu.c, r, decodeCustomerPlans)
	if err != nil {
		return nil, err
	}
	plans.Stale = m.stale
	cu.saw(plans.Customer)
	return &plans, nil
}

// Pricing answers the pricing on sale to the customer, through the answer cache. Pricing is
// always computed now.
func (cu *customer) Pricing(ctx context.Context, p *PricingParams) (*Pricing, error) {
	r, err := cu.read("/pricing", true)
	if err != nil {
		return nil, err
	}
	if err := pricingParams(r, p); err != nil {
		return nil, err
	}
	pricing, m, err := fetch(ctx, cu.c, r, decodePricing)
	if err != nil {
		return nil, err
	}
	pricing.Stale = m.stale
	if pricing.Customer != nil {
		cu.saw(*pricing.Customer)
	}
	return &pricing, nil
}

func pricingParams(r *request, p *PricingParams) error {
	if p == nil {
		return nil
	}
	if p.Visitor != "" {
		if err := checkVisitor(p.Visitor); err != nil {
			return err
		}
		r.visitor = p.Visitor
	}
	r.revalidate = p.Revalidate
	return nil
}

// Usage answers the customer's meters and the usage log's first page, from Cursor when given;
// Log.All iterates every entry, fetching each later page when the loop reaches it.
func (cu *customer) Usage(ctx context.Context, p *UsageParams) (*CustomerUsage, error) {
	var params UsageParams
	if p != nil {
		params = *p
	}
	asOf, err := cu.asOf(params.AsOf)
	if err != nil {
		return nil, err
	}
	page := func(ctx context.Context, cursor string) (CustomerUsage, error) {
		r, err := cu.read("/usage", false)
		if err != nil {
			return CustomerUsage{}, err
		}
		r.asOf = asOf
		if cursor != "" {
			q, err := queryValue(cursor)
			if err != nil {
				return CustomerUsage{}, err
			}
			r.query = []string{"cursor=" + q}
		}
		usage, _, err := fetch(ctx, cu.c, r, decodeCustomerUsage)
		return usage, err
	}
	usage, err := page(ctx, params.Cursor)
	if err != nil {
		return nil, err
	}
	cu.saw(usage.Customer)
	usage.Log.more = func(ctx context.Context, cursor string) (UsageLog, error) {
		next, err := page(ctx, cursor)
		return next.Log, err
	}
	return &usage, nil
}

// Snapshot signs the customer's entitlements for offline use, for TTL when given (by default, and
// at most, the project's offline days). Verify it with [VerifySnapshot]. Meters in a snapshot are
// frozen when it is signed.
func (cu *customer) Snapshot(ctx context.Context, p *SnapshotParams) (*IssuedSnapshot, error) {
	body := map[string]any{}
	if p != nil && p.TTL != 0 {
		seconds, err := wholeSeconds(p.TTL, 60, maxWhole, "pass the snapshot's TTL as a whole number of seconds of 60 or more")
		if err != nil {
			return nil, err
		}
		body["ttlSeconds"] = seconds
	}
	r, err := cu.write("POST", "/snapshots", newUUID(), body, false)
	if err != nil {
		return nil, err
	}
	snapshot, _, err := fetch(ctx, cu.c, r, decodeIssuedSnapshot)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// SnapshotParams are the options of Snapshot; nil for none.
type SnapshotParams struct {
	// TTL is how long the snapshot lasts, in whole seconds; 0 for the project's offline days.
	TTL time.Duration
}

func wholeSeconds(d time.Duration, from, to int64, message string) (int64, error) {
	if d%time.Second != 0 || d < time.Duration(from)*time.Second || int64(d/time.Second) > to {
		return 0, argError(message)
	}
	return int64(d / time.Second), nil
}
