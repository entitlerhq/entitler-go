package entitler

import "time"

// Environment is the environment an answer comes from.
type Environment struct {
	// ID is the environment's id.
	ID string
	// Name is its name.
	Name string
	// Kind is test or live.
	Kind EnvironmentKind
}

// Track is a track, named by the same name in every environment.
type Track struct {
	// ID is the track's id.
	ID string
	// Name is its name.
	Name string
}

// Experiment is the experiment arm an answer was served from.
type Experiment struct {
	// ID is the experiment's id.
	ID string
	// Arm is the arm.
	Arm Arm
}

// Upgrade is a plan that would entitle the customer, listed only when they are not entitled. Its
// Move and Action follow the same rule as [MoveOption], so the upgrade button on a refused feature
// and the paywall agree.
type Upgrade struct {
	// Plan is the plan's key.
	Plan string
	// Name is the plan's name.
	Name string
	// Move is the kind of change.
	Move Move
	// Action says who can make it.
	Action Action
	// Reason says, for the developer, why the move is unavailable, or nil.
	Reason *string
}

// Period is a billing period: Key (monthly) is what writes take; Label (Monthly) is display copy.
type Period struct {
	// Key is the period's stable key.
	Key string
	// Label is its display label.
	Label string
}

// SourceType is the kind of an entitlement's source.
type SourceType string

// The source types.
const (
	// SourcePlan is a plan the customer holds.
	SourcePlan SourceType = "plan"
	// SourceAddOn is an add-on the customer holds.
	SourceAddOn SourceType = "addon"
	// SourceGrant is a grant.
	SourceGrant SourceType = "grant"
	// SourceBanked is banked credits.
	SourceBanked SourceType = "banked"
	// SourceGroup is a group that includes the feature.
	SourceGroup SourceType = "group"
)

// Source is one source of an entitlement's value. Which fields are set depends on Type.
type Source struct {
	// Type is the kind of source.
	Type SourceType
	// Plan is the plan or add-on's key (SourcePlan, SourceAddOn).
	Plan string
	// Name is the plan or add-on's name (SourcePlan, SourceAddOn).
	Name string
	// Version is the plan or add-on's version (SourcePlan, SourceAddOn).
	Version int64
	// ByDefault is true for the default plan (SourcePlan).
	ByDefault bool
	// Quantity is the add-on's quantity (SourceAddOn).
	Quantity int64
	// Grant is the grant's id (SourceGrant).
	Grant string
	// Until is when the grant ends, or the zero time for none (SourceGrant).
	Until time.Time
	// Value is the value this source gives (SourcePlan, SourceAddOn, SourceGrant).
	Value Value
	// Banked is the credits banked (SourceBanked).
	Banked int64
	// Features are the group's features (SourceGroup).
	Features []string
}

// Meter is a metered feature's meter.
type Meter struct {
	// Used is the amount used this period.
	Used int64
	// Remaining is what remains: an Amount, or Unlimited.
	Remaining Value
	// Held is the amount on hold. Remaining already excludes it, so a live meter shows it apart.
	Held int64
	// ResetsAt is when the meter resets, or the zero time for never.
	ResetsAt time.Time
}

// Check is one feature's entitlement for a customer. For a metered feature, Entitled means the
// customer has the feature and Meter.Remaining is above 0. Upgrades lists the plans that would
// entitle the customer, filled only when they are not entitled.
type Check struct {
	// Customer is the customer's external id.
	Customer string
	// Feature is the feature's key.
	Feature string
	// Type is the feature's type.
	Type FeatureType
	// Entitled is the server's decision.
	Entitled bool
	// Value is the feature's value.
	Value Value
	// Sources are where the value comes from.
	Sources []Source
	// Meter is set only for metered features.
	Meter *Meter
	// Upgrades lists the plans that would entitle the customer, when they are not entitled.
	Upgrades []Upgrade
	// AsOf is the instant the answer describes.
	AsOf time.Time
	// Environment is the environment.
	Environment Environment
	// Track is the customer's track.
	Track Track
	// Release is the release the track serves, or nil while it follows a change.
	Release *int64
	// Change is the change the track follows, or nil.
	Change *string
	// Testers is true on a track for testers.
	Testers bool
	// Experiment is the experiment arm, or nil.
	Experiment *Experiment
	// Stale is true only when the SDK answered from a kept copy because Entitler was unreachable.
	Stale bool
}

// Entitlement is one feature in an [Entitlements] list.
type Entitlement struct {
	// Key is the feature's key.
	Key string
	// Type is the feature's type.
	Type FeatureType
	// Value is the feature's value.
	Value Value
	// Entitled is the server's decision.
	Entitled bool
	// Sources are where the value comes from.
	Sources []Source
	// Meter is set only for metered features.
	Meter *Meter
	// Upgrades lists the plans that would entitle the customer, when they are not entitled.
	Upgrades []Upgrade
}

// Entitlements is a customer's entitlements, groups included with the server's decision.
type Entitlements struct {
	// Customer is the customer's external id.
	Customer string
	// Items are the entitlements.
	Items []Entitlement
	// AsOf is the instant the answer describes.
	AsOf time.Time
	// Environment is the environment.
	Environment Environment
	// Track is the customer's track.
	Track Track
	// Release is the release the track serves, or nil.
	Release *int64
	// Change is the change the track follows, or nil.
	Change *string
	// Testers is true on a track for testers.
	Testers bool
	// Experiment is the experiment arm, or nil.
	Experiment *Experiment
	// Stale is true only when the SDK answered from a kept copy because Entitler was unreachable.
	Stale bool
}

// Get answers the feature's entitlement, with its upgrades, and whether the list holds it.
func (e *Entitlements) Get(f Feature) (Entitlement, bool) {
	if f != nil {
		for _, item := range e.Items {
			if item.Key == f.Key() {
				return item, true
			}
		}
	}
	return Entitlement{}, false
}

// Has answers the feature's Entitled, and false when the list does not hold it. A group is in the
// list with the server's decision, so Has never expands one itself.
func (e *Entitlements) Has(f Feature) bool {
	item, ok := e.Get(f)
	return ok && item.Entitled
}

// PlanSummary names a plan or add-on.
type PlanSummary struct {
	// ID is the public id.
	ID string
	// Key is the key.
	Key string
	// Name is the name.
	Name string
}

// PlanRef names a held plan or add-on, with its kind.
type PlanRef struct {
	// ID is the public id.
	ID string
	// Key is the key.
	Key string
	// Name is the name.
	Name string
	// Kind is plan or add-on.
	Kind PlanKind
}

// Product is a product.
type Product struct {
	// Key is the product's key.
	Key string
	// Name is its name.
	Name string
}

// PendingChange is the change booked for a held plan's renewal: [PendingMove], [PendingCancel],
// [PendingBilling], or [PendingUnknown] for a kind this version does not know.
type PendingChange interface {
	// PendingType answers move, cancel, billing or the unknown kind's raw type.
	PendingType() string
	isPending()
}

// PendingMove is a move to another plan at renewal.
type PendingMove struct {
	// Plan is the plan the customer moves to.
	Plan PlanSummary
}

// PendingCancel is a cancel at renewal.
type PendingCancel struct {
	// MovingTo is the default plan the cancel leads to, or nil when the product has none.
	MovingTo *PlanSummary
}

// PendingBilling is a change of who bills the plan, which only the company books.
type PendingBilling struct {
	// Billing is who bills the plan from renewal.
	Billing BillingSource
}

// PendingUnknown is a booked change of a kind this version does not know.
type PendingUnknown struct {
	// Type is the raw type.
	Type string
}

// PendingType answers "move".
func (PendingMove) PendingType() string { return "move" }

// PendingType answers "cancel".
func (PendingCancel) PendingType() string { return "cancel" }

// PendingType answers "billing".
func (PendingBilling) PendingType() string { return "billing" }

// PendingType answers the raw type.
func (p PendingUnknown) PendingType() string { return p.Type }

func (PendingMove) isPending()    {}
func (PendingCancel) isPending()  {}
func (PendingBilling) isPending() {}
func (PendingUnknown) isPending() {}

// HeldPlan is a plan or add-on the customer holds.
type HeldPlan struct {
	// Plan is the plan held.
	Plan PlanRef
	// Product is its product, or nil.
	Product *Product
	// Version is the version held, or nil.
	Version *int64
	// ByDefault is true when the customer holds it as the default plan.
	ByDefault bool
	// Period is the billing period, or nil when Entitler sets none (a plan held by default).
	Period *Period
	// RenewsAt is when it renews, or the zero time.
	RenewsAt time.Time
	// Pending is the change booked for RenewsAt, or nil.
	Pending PendingChange
	// BilledBy is who bills the plan, or "" when nothing does. It decides between the store's own
	// management page and the billing portal.
	BilledBy Provider
}

// OfferedPlan is the plan a move option leads to.
type OfferedPlan struct {
	// ID is the public id.
	ID string
	// Key is the key.
	Key string
	// Name is the name.
	Name string
	// Kind is plan or add-on.
	Kind PlanKind
	// Description is its description.
	Description string
	// Default is true for the product's default plan.
	Default bool
}

// Impact is one line of what a move changes for the customer.
type Impact struct {
	// Kind is gains, loses, changes or same.
	Kind ImpactKind
	// Text is the line.
	Text string
}

// Price is a provider's price.
type Price struct {
	// Amount is a whole number in the currency's smallest unit.
	Amount int64
	// Currency is the ISO 4217 code in lower case.
	Currency string
	// Interval is how often it recurs, or "" for a one-time price.
	Interval Interval
	// IntervalCount is how many intervals each charge covers, 1 or more.
	IntervalCount int64
	// Tax is how the price treats tax.
	Tax Tax
}

// OfferedSKU is a product a provider sells for a plan: a store-billed app buys it through StoreKit
// or Play Billing.
type OfferedSKU struct {
	// Period is the period it sells, or nil.
	Period *Period
	// Connector names where it is bought, such as stripe, apple or google.
	Connector string
	// IDs are the connector's ids by field, such as productId.
	IDs map[string]string
	// Price is its price, or nil when the answer gives none.
	Price *Price
}

// MoveOption is a plan or add-on the customer can move to. Action decides the button.
type MoveOption struct {
	// Plan is the plan the move leads to.
	Plan OfferedPlan
	// Product is its product, or nil.
	Product *Product
	// From is the plan the move leaves, or nil.
	From *PlanSummary
	// Move is the kind of change.
	Move Move
	// Action says who can make it.
	Action Action
	// Reason says, for the developer, why it is unavailable, or nil.
	Reason *string
	// When says when it takes effect.
	When When
	// Periods are the periods it is sold in.
	Periods []Period
	// Impact lists what changes.
	Impact []Impact
	// SKUs are the store and provider products that sell it.
	SKUs []OfferedSKU
}

// CustomerPlans is the paywall's and the billing page's one read: the plans held and the moves
// open. Action decides each button, SKUs are the store products, and a held plan's BilledBy
// decides between the store's management page and the billing portal.
type CustomerPlans struct {
	// Customer is the customer's external id.
	Customer string
	// Held are the plans and add-ons held.
	Held []HeldPlan
	// Options are the moves open.
	Options []MoveOption
	// AsOf is the instant the answer describes.
	AsOf time.Time
	// Environment is the environment.
	Environment Environment
	// Track is the customer's track.
	Track Track
	// Release is the release the track serves, or nil.
	Release *int64
	// Change is the change the track follows, or nil.
	Change *string
	// Testers is true on a track for testers.
	Testers bool
	// Experiment is the experiment arm, or nil.
	Experiment *Experiment
	// Stale is true only when the SDK answered from a kept copy because Entitler was unreachable.
	Stale bool
}

// Channel is a provider connection that sells a plan.
type Channel struct {
	// Provider is stripe, apple or google.
	Provider Provider
	// ConnectionID is the connection's id.
	ConnectionID string
}

// ListingChannel is a plan's listing on one channel.
type ListingChannel struct {
	// Channel is the channel.
	Channel Channel
	// Name is the channel's name.
	Name string
	// Mode is the connection's mode, test or live, or "" for none.
	Mode EnvironmentKind
	// Purchasable is true when it can be bought there now.
	Purchasable bool
	// IDs are the provider's ids by field, or nil.
	IDs map[string]string
	// Price is the price, or nil.
	Price *Price
}

// Listing is a plan's listings for one period.
type Listing struct {
	// Period is the period, or nil.
	Period *Period
	// Channels are the channels that list it.
	Channels []ListingChannel
}

// PricingPeriod is a period a plan is sold in.
type PricingPeriod struct {
	// Key is the period's stable key.
	Key string
	// Label is its display label.
	Label string
	// Count is how many units it lasts.
	Count int64
	// Unit is days, weeks or months.
	Unit TimeUnit
}

// PricingProduct is a product on sale.
type PricingProduct struct {
	// Key is the product's key.
	Key string
	// Name is its name.
	Name string
	// DefaultPlan is its default plan's key, or nil.
	DefaultPlan *string
}

// PricingPlan is a plan on sale.
type PricingPlan struct {
	// ID is the public id.
	ID string
	// Key is the key.
	Key string
	// Name is the name.
	Name string
	// Description is its description.
	Description string
	// Kind is plan or add-on.
	Kind PlanKind
	// Product is its product's key, or nil.
	Product *string
	// SalesLed is true when only the company sells it.
	SalesLed bool
	// Status is active or legacy.
	Status PlanStatus
	// Version is the version on sale, or nil.
	Version *int64
	// Default is true for the default plan.
	Default bool
	// Periods are the periods it is sold in.
	Periods []PricingPeriod
	// AttachesTo are the plans an add-on goes on top of.
	AttachesTo []string
	// Features are its features' values by key.
	Features map[string]Value
	// Listings are its provider listings by period.
	Listings []Listing
}

// Pricing is the pricing on sale, signed out or to one customer. Pricing is always computed now.
type Pricing struct {
	// Customer is the customer's external id, or nil when signed out.
	Customer *string
	// DefaultPlan is the default plan's key, or nil.
	DefaultPlan *string
	// Products are the products on sale.
	Products []PricingProduct
	// Plans are the plans on sale.
	Plans []PricingPlan
	// Environment is the environment.
	Environment Environment
	// Track is the track served.
	Track Track
	// Release is the release the track serves, or nil.
	Release *int64
	// Change is the change the track follows, or nil.
	Change *string
	// Testers is true on a track for testers.
	Testers bool
	// Experiment is the experiment arm, or nil.
	Experiment *Experiment
	// Stale is true only when the SDK answered from a kept copy because Entitler was unreachable.
	Stale bool
}

// MeterWindow is how often a meter resets.
type MeterWindow struct {
	// Count is how many units.
	Count int64
	// Unit is hours, days, weeks or months.
	Unit TimeUnit
	// Anchor is what the window counts from, or "" when the answer leaves it out.
	Anchor MeterAnchor
}

// FeatureListing is a feature in the catalogue.
type FeatureListing struct {
	// ID is the feature's id.
	ID string
	// Key is its key.
	Key string
	// Name is its name.
	Name string
	// Type is its type.
	Type FeatureType
	// Description is its description.
	Description string
	// Unit is its unit, such as credits.
	Unit string
	// ResetEvery is how often its meter resets, or nil.
	ResetEvery *MeterWindow
	// Archived is true for an archived feature.
	Archived bool
	// Includes are a group's direct members.
	Includes []string
}

// FeatureList is the catalogue's features.
type FeatureList struct {
	// Features are the features.
	Features []FeatureListing
	// Environment is the environment.
	Environment Environment
	// Track is the track read.
	Track Track
	// Release is the release the track serves, or nil.
	Release *int64
	// Change is the change the track follows, or nil.
	Change *string
	// Stale is true only when the SDK answered from a kept copy because Entitler was unreachable.
	Stale bool
}

// UsageResult is the answer to a usage report, hold, settlement, release, cancel or meter
// adjustment: the feature's check, with its meter and upgrades, plus what the write did. A refusal
// is an answer (OutcomeRefused), not an error.
type UsageResult struct {
	Check
	// Outcome is what the write did.
	Outcome Outcome
	// Refusal says why it was refused, or "" when it was not.
	Refusal Refusal
	// ID is the usage report's id, or nil.
	ID *string
	// HoldID is the hold's id, or nil.
	HoldID *string
	// Mode is the report's mode.
	Mode UsageMode
	// Amount is the amount reported, held or settled.
	Amount int64
	// MeterChange is how far the meter moved.
	MeterChange int64
	// OverBy is how far past the allowance an observe report took the meter.
	OverBy int64
	// Late is true when the usage counted in an earlier period.
	Late bool
	// OccurredAt is when the usage happened, or the zero time.
	OccurredAt time.Time
	// ExpiresAt is when a hold expires, or the zero time.
	ExpiresAt time.Time
	// ReportedAs is where the report came from.
	ReportedAs UsageSource
	// Replayed is true when the API answered this idempotency key before, so this call changed nothing.
	Replayed bool
}

// FeatureUsage is one feature's meter in a [CustomerUsage].
type FeatureUsage struct {
	// Feature is the feature's key.
	Feature string
	// Type is the feature's type.
	Type FeatureType
	// Entitled is the server's decision.
	Entitled bool
	// Value is the feature's value.
	Value Value
	// Sources are where the value comes from.
	Sources []Source
	// Used is the amount used.
	Used int64
	// Held is the amount on hold.
	Held int64
	// Remaining is what remains, or nil.
	Remaining Value
	// ResetsAt is when the meter resets, or the zero time.
	ResetsAt time.Time
}

// UsageEvent is one entry of the usage log.
type UsageEvent struct {
	// ID is the report's id, for CancelUsage.
	ID string
	// Feature is the feature's key.
	Feature string
	// Kind is use or adjust.
	Kind UsageEventKind
	// Amount is the amount used, or how far an adjustment moved the meter.
	Amount int64
	// SetTo is the value an adjustment set the meter to, or nil.
	SetTo *int64
	// Source is where it came from.
	Source UsageSource
	// By names the key or person, or nil.
	By *string
	// Actor is the actor the write named, or nil.
	Actor *string
	// Reason is the reason the write gave, or nil.
	Reason *string
	// At is when it happened.
	At time.Time
	// CancelledAt is when it was cancelled, or the zero time.
	CancelledAt time.Time
}

// CustomerUsage is a customer's meters and the first page of their usage log.
type CustomerUsage struct {
	// Customer is the customer's external id.
	Customer string
	// MetersStartAgainAt is when the meters next reset, or the zero time.
	MetersStartAgainAt time.Time
	// Features are the meters.
	Features []FeatureUsage
	// Log is the usage log's first page; Log.All iterates every entry.
	Log UsageLog
	// AsOf is the instant the answer describes.
	AsOf time.Time
	// Environment is the environment.
	Environment Environment
	// Track is the customer's track.
	Track Track
	// Release is the release the track serves, or nil.
	Release *int64
	// Change is the change the track follows, or nil.
	Change *string
	// Testers is true on a track for testers.
	Testers bool
	// Experiment is the experiment arm, or nil.
	Experiment *Experiment
}

// VersionedPlan is a plan with its version.
type VersionedPlan struct {
	// ID is the public id.
	ID string
	// Key is the key.
	Key string
	// Name is the name.
	Name string
	// Version is the version held.
	Version int64
}

// ProductPlan is a plan held in a product.
type ProductPlan struct {
	// ID is the public id.
	ID string
	// Key is the key.
	Key string
	// Name is the name.
	Name string
	// Version is the version held.
	Version int64
	// Product is the product's key.
	Product string
}

// CustomerSummary is a customer as lists and updates answer them.
type CustomerSummary struct {
	// ID is Entitler's id.
	ID string
	// ExternalID is your app's id for the customer.
	ExternalID string
	// Name is the customer's name.
	Name string
	// Email is the customer's email.
	Email string
	// EnvironmentID is the environment's id.
	EnvironmentID string
	// Sample is true for a sample customer.
	Sample bool
	// CreatedAt is when they were registered.
	CreatedAt time.Time
	// Plan is their plan, or nil.
	Plan *VersionedPlan
	// Plans are their plans by product.
	Plans []ProductPlan
	// DefaultPlan is the default plan, or nil.
	DefaultPlan *PlanSummary
	// Status is their status.
	Status string
	// Kind summarises how they hold their plans.
	Kind CustomerKind
	// Metadata is your app's metadata, as sent.
	Metadata map[string]string
	// Track is their track.
	Track Track
	// TestCustomer is true for a test customer.
	TestCustomer bool
	// Replayed is true when Update's idempotency key was answered before; false on a list's items.
	Replayed bool
}

// CustomerPage is one page of the customer list.
type CustomerPage struct {
	// Items are the customers.
	Items []CustomerSummary
	// Next is the cursor of the next page, or "" on the last. A page may be empty while Next continues.
	Next string
	// Used is how many customers the environment holds.
	Used int64
	// Limit is how many it may hold: an Amount, or Unlimited.
	Limit Value
}

// Purchase describes how a plan was bought.
type Purchase struct {
	// Money is real or test.
	Money Money
	// Channel is the channel, or nil.
	Channel *Channel
	// Release is the release it was bought from, or nil.
	Release *int64
	// Change is the change it was bought from, or nil.
	Change *string
	// Arm is the experiment arm, or "".
	Arm Arm
}

// HeldAddOn is an add-on a subscription holds.
type HeldAddOn struct {
	// Plan is the add-on.
	Plan PlanSummary
	// Version is its version.
	Version int64
	// Quantity is its quantity.
	Quantity int64
	// Countable is true when its quantity counts.
	Countable bool
	// AddedAt is when it was added.
	AddedAt time.Time
	// MovingTo is the add-on it moves to at renewal, or nil.
	MovingTo *PlanSummary
	// Purchase is how it was bought.
	Purchase Purchase
}

// ProviderPeriod is the provider's billing period.
type ProviderPeriod struct {
	// Provider is the provider.
	Provider string
	// StartsAt is when the period started.
	StartsAt time.Time
	// EndsAt is when it ends.
	EndsAt time.Time
	// TrialEndsAt is when a trial ends, or the zero time.
	TrialEndsAt time.Time
	// CancelsAt is when it cancels, or the zero time.
	CancelsAt time.Time
}

// PlanOverride is a company decision that holds the customer on a plan the provider does not bill.
type PlanOverride struct {
	// From is the plan the provider bills.
	From PlanSummary
	// Period is the period, or nil.
	Period *Period
	// BilledBy is the channel that bills, or nil.
	BilledBy *Channel
	// Since is when the override began.
	Since time.Time
	// By names who made it.
	By string
}

// Subscription is a customer's plan in one product.
type Subscription struct {
	// Product is the product.
	Product Product
	// Plan is the plan.
	Plan PlanSummary
	// Version is its version.
	Version int64
	// Cohort is the cohort, or nil.
	Cohort *int64
	// Period is the period, or nil.
	Period *Period
	// StartedAt is when it started.
	StartedAt time.Time
	// RenewsAt is when it renews.
	RenewsAt time.Time
	// Billing is the provider's period, or nil.
	Billing *ProviderPeriod
	// Until is when it ends unless renewed, or the zero time.
	Until time.Time
	// AddOns are its add-ons.
	AddOns []HeldAddOn
	// Pending is the change booked for renewal, or nil.
	Pending PendingChange
	// Purchase is how it was bought.
	Purchase Purchase
	// Override is a company override, or nil.
	Override *PlanOverride
}

// ProductSubscription is a product and the customer's plan in it.
type ProductSubscription struct {
	// Product is the product.
	Product Product
	// DefaultPlan is its default plan, or nil.
	DefaultPlan *PlanSummary
	// Subscription is the customer's plan in it, or nil.
	Subscription *Subscription
}

// Grant is a feature, or a value of it, given beyond the customer's plan.
type Grant struct {
	// ID is the grant's id, for RevokeGrant.
	ID string
	// Feature is the feature's key.
	Feature string
	// Value is the value granted: On, an Amount or Unlimited.
	Value Value
	// From is when it began.
	From time.Time
	// Until is when it ends, or the zero time for no end.
	Until time.Time
	// RevokedAt is when it was revoked, or the zero time.
	RevokedAt time.Time
	// Reason is why.
	Reason string
	// By names the key or person that made it.
	By string
	// Actor is the actor the write named, or nil.
	Actor *string
}

// Activity is an entry in the customer's activity.
type Activity struct {
	// Text describes what happened.
	Text string
	// At is when.
	At time.Time
	// By names the key or person, or nil.
	By *string
	// Actor is the actor the write named, or nil.
	Actor *string
	// Reason is the reason given, or nil.
	Reason *string
}

// CustomerDetail is everything Entitler holds about a customer, for support tools.
type CustomerDetail struct {
	// Customer is the customer.
	Customer CustomerSummary
	// AsOf is the instant the answer describes.
	AsOf time.Time
	// Subscription is their plan, or nil.
	Subscription *Subscription
	// DefaultPlan is the default plan, or nil.
	DefaultPlan *PlanSummary
	// Products are the products and their plans.
	Products []ProductSubscription
	// AddOns are their add-ons.
	AddOns []HeldAddOn
	// Entitlements are their entitlements.
	Entitlements []Entitlement
	// Banked are banked credits by feature.
	Banked map[string]int64
	// MoveOptions are the moves open.
	MoveOptions []MoveOption
	// Grants are their grants.
	Grants []Grant
	// Usage is the usage log's first page; Usage.All iterates every entry.
	Usage UsageLog
	// Activity is their recent activity.
	Activity []Activity
	// Environment is the environment.
	Environment Environment
	// Replayed is true when Customers.Create's idempotency key was answered before; false on Details.
	Replayed bool
}

// RegisteredCustomer is the answer to registering a customer.
type RegisteredCustomer struct {
	// ID is Entitler's id.
	ID string
	// ExternalID is your app's id for the customer.
	ExternalID string
	// EnvironmentID is the environment's id.
	EnvironmentID string
	// CreatedAt is when they were registered.
	CreatedAt time.Time
	// Created is true when this call created them.
	Created bool
	// Replayed is true when the API answered this idempotency key before.
	Replayed bool
}

// IssuedCustomerToken is a customer token for an in-app client. Its String form leaves the token out.
type IssuedCustomerToken struct {
	// Token is the token. Hand it only to the signed-in customer it names.
	Token string
	// Customer is the customer's external id.
	Customer string
	// Scopes are the token's scopes.
	Scopes []Scope
	// ExpiresAt is when it expires.
	ExpiresAt time.Time
}

// String describes the token without the token itself.
func (t IssuedCustomerToken) String() string {
	return "customer token for " + t.Customer + " (expires " + formatInstant(t.ExpiresAt) + ")"
}

// IssuedSnapshot is a signed snapshot of the customer's entitlements for offline use. Verify it
// with [VerifySnapshot].
type IssuedSnapshot struct {
	// Token is the signed snapshot.
	Token string
	// ExpiresAt is when it expires.
	ExpiresAt time.Time
	// KeyID is the id of the key that signed it.
	KeyID string
}

// CustomerTrack is the answer to SetTrack.
type CustomerTrack struct {
	// Customer is the customer's external id.
	Customer string
	// EnvironmentID is the environment's id.
	EnvironmentID string
	// Track is the customer's track now.
	Track Track
	// Source says who put them there, or "".
	Source TrackSource
	// PreviousTrackID is their previous track's id, or nil.
	PreviousTrackID *string
	// Replayed is true when the API answered this idempotency key before.
	Replayed bool
}

// BillingDrift is a difference between the plan the provider bills and the plan held.
type BillingDrift struct {
	// BilledPlan is the plan billed.
	BilledPlan string
	// HeldPlan is the plan held, or nil.
	HeldPlan *string
	// ObservedAt is when it was seen.
	ObservedAt time.Time
}

// ProductBilling is the provider's view of one product.
type ProductBilling struct {
	// Product is the product's key, or nil.
	Product *string
	// Status is the provider's status, or "".
	Status SubscriptionStatus
	// SKU is the SKU billed, or nil.
	SKU *OfferedSKU
	// Items is how many items are billed.
	Items int64
	// Drift is a difference from the plan held, or nil.
	Drift *BillingDrift
}

// CustomerBilling is the payment provider's view of a customer, for support tools. Billing pages
// read Plans instead.
type CustomerBilling struct {
	// Provider is the provider, or "" when none bills the customer.
	Provider string
	// Status is the provider's status, or "".
	Status SubscriptionStatus
	// SKU is the SKU billed, or nil.
	SKU *OfferedSKU
	// Items is how many items are billed.
	Items int64
	// Drift is a difference from the plan held, or nil.
	Drift *BillingDrift
	// Products are the provider's view by product.
	Products []ProductBilling
}

// PlanChange is the answer to a plan or add-on change.
type PlanChange struct {
	// Product is the product, or nil.
	Product *Product
	// Plan is the plan or add-on the change leaves the customer on, or nil when they hold none in the product.
	Plan *PlanSummary
	// Quantity is an add-on's quantity after the change (0 when removed), or nil for a plan.
	Quantity *int64
	// Effective says when it takes effect.
	Effective Effective
	// At is when it takes effect.
	At time.Time
	// Until is when the plan ends unless renewed, or the zero time.
	Until time.Time
	// Changed is false when nothing changed.
	Changed bool
	// Replayed is true when the API answered this idempotency key before.
	Replayed bool
}

// SubscribeStep is the next step after Subscribe. Next is NextDone (Change holds the change),
// NextPay (send the customer to URL), NextConfirming (the provider is confirming a payment) or
// NextManage (a store bills the product: BilledBy), or a step this version does not know.
type SubscribeStep struct {
	// Next is the step.
	Next Next
	// Change is the change made, for NextDone, else nil. Its Replayed says whether the key was answered before.
	Change *PlanChange
	// URL is the provider's page, for NextPay, else "".
	URL string
	// BilledBy is the store, for NextManage, else "".
	BilledBy Provider
}

// GrantChange is the answer to Grant and RevokeGrant.
type GrantChange struct {
	// Grant is the grant made or revoked; keep its ID.
	Grant Grant
	// Replayed is true when the API answered this idempotency key before.
	Replayed bool
}

// BillingPortal is the provider's billing portal page.
type BillingPortal struct {
	// Provider is the provider.
	Provider string
	// URL is the page to open.
	URL string
}

// BillingSync is the answer to SyncBilling.
type BillingSync struct {
	// Changed is true when the provider's state moved the customer.
	Changed bool
}

// Scopes is what a credential may do, for deciding which controls to show. The server decides on
// every call regardless.
type Scopes struct {
	// List holds the scopes the SDK knows, in a fixed order.
	List []Scope
	// Registration reports, for an identity client, whether a new person may register.
	Registration bool
}

// Has reports whether the credential holds the scope.
func (s *Scopes) Has(scope Scope) bool {
	for _, held := range s.List {
		if held == scope {
			return true
		}
	}
	return false
}
