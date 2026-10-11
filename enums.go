package entitler

// EnvironmentKind is an environment's kind.
type EnvironmentKind string

// The environment kinds.
const (
	// EnvironmentTest is a test environment.
	EnvironmentTest EnvironmentKind = "test"
	// EnvironmentLive is the live environment.
	EnvironmentLive EnvironmentKind = "live"
)

// Arm is an experiment arm.
type Arm string

// The experiment arms.
const (
	// ArmControl is the control arm.
	ArmControl Arm = "control"
	// ArmVariant is the variant arm.
	ArmVariant Arm = "variant"
)

// Move is the kind of plan change an option or upgrade offers.
type Move string

// The moves.
const (
	// MoveSubscribe starts a plan in a product the customer holds no plan in.
	MoveSubscribe Move = "subscribe"
	// MoveUpgrade moves to a plan that gives more.
	MoveUpgrade Move = "upgrade"
	// MoveDowngrade moves to a plan that gives less.
	MoveDowngrade Move = "downgrade"
	// MoveSwitch moves sideways, to another plan or period.
	MoveSwitch Move = "switch"
	// MoveAdd adds an add-on.
	MoveAdd Move = "add"
	// MoveReplace replaces one add-on with another.
	MoveReplace Move = "replace"
)

// Action says who can make a move.
type Action string

// The actions.
const (
	// ActionBuy means the customer can make the move alone.
	ActionBuy Action = "buy"
	// ActionContact means a sales-led move, which only the company can make.
	ActionContact Action = "contact"
	// ActionUnavailable means the move cannot be made now; the reason says why, for the developer.
	ActionUnavailable Action = "unavailable"
)

// PlanKind says whether a plan is a plan or an add-on.
type PlanKind string

// The plan kinds.
const (
	// PlanKindPlan is a plan.
	PlanKindPlan PlanKind = "plan"
	// PlanKindAddOn is an add-on.
	PlanKindAddOn PlanKind = "addon"
)

// Provider names who sells or bills: Stripe, the App Store or Google Play.
type Provider string

// The providers.
const (
	// ProviderStripe is Stripe.
	ProviderStripe Provider = "stripe"
	// ProviderApple is Apple's App Store.
	ProviderApple Provider = "apple"
	// ProviderGoogle is Google Play.
	ProviderGoogle Provider = "google"
)

// BillingSource says who bills a plan after a booked billing change.
type BillingSource string

// The billing sources.
const (
	// BillingSourceProvider means the payment provider bills the plan.
	BillingSourceProvider BillingSource = "provider"
	// BillingSourceOutside means no payment provider bills it, as with an invoiced contract.
	BillingSourceOutside BillingSource = "outside"
	// BillingSourceStore means an app store bills it.
	BillingSourceStore BillingSource = "store"
)

// When says when a change takes effect.
type When string

// The times a change takes effect.
const (
	// WhenNow takes effect now.
	WhenNow When = "now"
	// WhenEnd takes effect at renewal, the end of the period.
	WhenEnd When = "end"
)

// Effective says when a made change takes effect.
type Effective string

// The times a made change takes effect.
const (
	// EffectiveNow means the change took effect now.
	EffectiveNow Effective = "now"
	// EffectiveRenewal means the change takes effect at renewal, at PlanChange.At.
	EffectiveRenewal Effective = "renewal"
)

// ImpactKind is the kind of one line of a move's impact.
type ImpactKind string

// The impact kinds.
const (
	// ImpactGains is something the customer gains.
	ImpactGains ImpactKind = "gains"
	// ImpactLoses is something the customer loses.
	ImpactLoses ImpactKind = "loses"
	// ImpactChanges is something that changes.
	ImpactChanges ImpactKind = "changes"
	// ImpactSame is something that stays the same.
	ImpactSame ImpactKind = "same"
)

// Interval is how often a price recurs.
type Interval string

// The price intervals.
const (
	// IntervalDay recurs daily.
	IntervalDay Interval = "day"
	// IntervalWeek recurs weekly.
	IntervalWeek Interval = "week"
	// IntervalMonth recurs monthly.
	IntervalMonth Interval = "month"
	// IntervalYear recurs yearly.
	IntervalYear Interval = "year"
)

// Tax says how a price treats tax.
type Tax string

// The tax behaviours.
const (
	// TaxInclusive means the price includes tax.
	TaxInclusive Tax = "inclusive"
	// TaxExclusive means tax is added to the price.
	TaxExclusive Tax = "exclusive"
	// TaxUnspecified means the provider does not say.
	TaxUnspecified Tax = "unspecified"
)

// Outcome is what a usage write, or one batch event, did.
type Outcome string

// The outcomes.
const (
	// OutcomeRecorded means the usage was recorded.
	OutcomeRecorded Outcome = "recorded"
	// OutcomeDuplicate means the key was seen before, so nothing new was recorded (or, for a hold, it is still open).
	OutcomeDuplicate Outcome = "duplicate"
	// OutcomeRefused means the amount did not fit the allowance, so nothing was recorded.
	OutcomeRefused Outcome = "refused"
	// OutcomeHeld means the amount is on hold.
	OutcomeHeld Outcome = "held"
	// OutcomeSettled means the hold was settled.
	OutcomeSettled Outcome = "settled"
	// OutcomeReleased means the hold was released.
	OutcomeReleased Outcome = "released"
	// OutcomeCancelled means the usage report was cancelled.
	OutcomeCancelled Outcome = "cancelled"
	// OutcomeAdjusted means the meter was adjusted.
	OutcomeAdjusted Outcome = "adjusted"
	// OutcomeError means a batch event was not recorded; its Error says why.
	OutcomeError Outcome = "error"
)

// Refusal says why a usage write was refused.
type Refusal string

// The refusals.
const (
	// RefusalNotEntitled means the customer does not have the feature.
	RefusalNotEntitled Refusal = "not_entitled"
	// RefusalOverAllowance means the amount is more than what remains.
	RefusalOverAllowance Refusal = "over_allowance"
)

// UsageMode decides whether a report may pass the allowance.
type UsageMode string

// The usage modes.
const (
	// Gate (the default) records only if the amount fits the allowance; otherwise it records
	// nothing and answers OutcomeRefused. Use it before work that must not start without allowance.
	Gate UsageMode = "gate"
	// Observe always records the amount used, and OverBy says how far past the allowance the meter
	// went. Use it for work that already happened.
	Observe UsageMode = "observe"
)

// UsageSource says where a usage report came from.
type UsageSource string

// The usage sources.
const (
	// UsageSourceAPI is a server key.
	UsageSourceAPI UsageSource = "api"
	// UsageSourceClient is a customer or identity token.
	UsageSourceClient UsageSource = "client"
	// UsageSourceDashboard is the dashboard.
	UsageSourceDashboard UsageSource = "dashboard"
)

// UsageEventKind is the kind of a usage log entry.
type UsageEventKind string

// The usage log entry kinds.
const (
	// UsageEventUse is usage recorded.
	UsageEventUse UsageEventKind = "use"
	// UsageEventAdjust is a meter adjustment.
	UsageEventAdjust UsageEventKind = "adjust"
)

// CustomerKind summarises how a customer holds their plans.
type CustomerKind string

// The customer kinds.
const (
	// CustomerRecurring holds a recurring plan.
	CustomerRecurring CustomerKind = "recurring"
	// CustomerOneTime holds a one-time plan.
	CustomerOneTime CustomerKind = "one_time"
	// CustomerChanging has a change booked.
	CustomerChanging CustomerKind = "changing"
	// CustomerDefault holds the default plan.
	CustomerDefault CustomerKind = "default"
	// CustomerNone holds no plan.
	CustomerNone CustomerKind = "none"
)

// TrackSource says who put a customer on their track.
type TrackSource string

// The track sources.
const (
	// TrackSourceServer is a server key.
	TrackSourceServer TrackSource = "server"
	// TrackSourceDashboard is the dashboard.
	TrackSourceDashboard TrackSource = "dashboard"
	// TrackSourceStoreSandbox is a store's sandbox purchase.
	TrackSourceStoreSandbox TrackSource = "store_sandbox"
)

// SubscriptionStatus is the payment provider's subscription status.
type SubscriptionStatus string

// The subscription statuses Stripe names.
const (
	// StatusIncomplete is incomplete.
	StatusIncomplete SubscriptionStatus = "incomplete"
	// StatusIncompleteExpired is incomplete and expired.
	StatusIncompleteExpired SubscriptionStatus = "incomplete_expired"
	// StatusTrialing is in a trial.
	StatusTrialing SubscriptionStatus = "trialing" //nolint:misspell // Stripe spells its status this way
	// StatusActive is active.
	StatusActive SubscriptionStatus = "active"
	// StatusPastDue is past due.
	StatusPastDue SubscriptionStatus = "past_due"
	// StatusCanceled is cancelled.
	StatusCanceled SubscriptionStatus = "canceled"
	// StatusUnpaid is unpaid.
	StatusUnpaid SubscriptionStatus = "unpaid"
	// StatusPaused is paused.
	StatusPaused SubscriptionStatus = "paused"
)

// Money says whether a purchase was paid with real or test money.
type Money string

// The kinds of money.
const (
	// MoneyReal is real money.
	MoneyReal Money = "real"
	// MoneyTest is test money.
	MoneyTest Money = "test"
)

// PlanStatus is a plan's sale status.
type PlanStatus string

// The plan statuses.
const (
	// PlanActive is on sale.
	PlanActive PlanStatus = "active"
	// PlanLegacy is no longer sold, though customers may hold it.
	PlanLegacy PlanStatus = "legacy"
)

// TimeUnit is the unit of a period or a meter's reset window.
type TimeUnit string

// The time units.
const (
	// UnitHours is hours.
	UnitHours TimeUnit = "hours"
	// UnitDays is days.
	UnitDays TimeUnit = "days"
	// UnitWeeks is weeks.
	UnitWeeks TimeUnit = "weeks"
	// UnitMonths is months.
	UnitMonths TimeUnit = "months"
)

// MeterAnchor says what a meter's reset window counts from.
type MeterAnchor string

// The meter anchors.
const (
	// AnchorPeriod counts from the customer's billing period.
	AnchorPeriod MeterAnchor = "period"
	// AnchorCalendar counts from the calendar.
	AnchorCalendar MeterAnchor = "calendar"
)

// Next is the next step after Subscribe.
type Next string

// The next steps.
const (
	// NextDone means the change is made; SubscribeStep.Change holds it.
	NextDone Next = "done"
	// NextPay means send the customer to SubscribeStep.URL, the provider's page.
	NextPay Next = "pay"
	// NextConfirming means the provider is still confirming a payment; show it as pending.
	NextConfirming Next = "confirming"
	// NextManage means a store bills the product (SubscribeStep.BilledBy), so the customer changes it there.
	NextManage Next = "manage"
)

// Scope is a credential's permission.
type Scope string

// The scopes the SDK knows, in the order Scopes answers them.
const (
	// ScopePlansRead reads pricing and the feature list.
	ScopePlansRead Scope = "plans:read"
	// ScopeEntitlementsRead reads checks, entitlements, plans and snapshots.
	ScopeEntitlementsRead Scope = "entitlements:read"
	// ScopeUsageRead reads usage.
	ScopeUsageRead Scope = "usage:read"
	// ScopeUsageWrite records usage.
	ScopeUsageWrite Scope = "usage:write"
	// ScopeBillingSelf lets a customer credential buy for the customer.
	ScopeBillingSelf Scope = "billing:self"
	// ScopeCustomersRegister registers customers.
	ScopeCustomersRegister Scope = "customers:register"
	// ScopeCustomersRead reads customer details.
	ScopeCustomersRead Scope = "customers:read"
	// ScopeCustomersWrite changes customers and their plans.
	ScopeCustomersWrite Scope = "customers:write"
	// ScopeCustomersProfile changes customers' details.
	ScopeCustomersProfile Scope = "customers:profile"
	// ScopeCustomersSample manages sample customers.
	ScopeCustomersSample Scope = "customers:sample"
	// ScopeTokensMint mints customer tokens.
	ScopeTokensMint Scope = "tokens:mint"
	// ScopePlansWrite edits the catalogue.
	ScopePlansWrite Scope = "plans:write"
	// ScopePlansRelease releases the catalogue.
	ScopePlansRelease Scope = "plans:release"
	// ScopeTracksManage manages tracks.
	ScopeTracksManage Scope = "tracks:manage"
	// ScopeTracksPromote promotes tracks.
	ScopeTracksPromote Scope = "tracks:promote"
	// ScopeTracksAssign puts customers on tracks.
	ScopeTracksAssign Scope = "tracks:assign"
	// ScopeKeysManage manages keys.
	ScopeKeysManage Scope = "keys:manage"
	// ScopeMembersManage manages members.
	ScopeMembersManage Scope = "members:manage"
	// ScopeProjectsManage manages projects.
	ScopeProjectsManage Scope = "projects:manage"
	// ScopeOrgManage manages the organisation.
	ScopeOrgManage Scope = "org:manage"
)

var knownScopes = []Scope{
	ScopePlansRead, ScopeEntitlementsRead, ScopeUsageRead, ScopeUsageWrite, ScopeBillingSelf,
	ScopeCustomersRegister, ScopeCustomersRead, ScopeCustomersWrite, ScopeCustomersProfile,
	ScopeCustomersSample, ScopeTokensMint, ScopePlansWrite, ScopePlansRelease, ScopeTracksManage,
	ScopeTracksPromote, ScopeTracksAssign, ScopeKeysManage, ScopeMembersManage, ScopeProjectsManage,
	ScopeOrgManage,
}

// PlanVersion names the version SetPlan moves a customer to.
type PlanVersion string

// VersionLatest moves the customer now to the newest version of the plan they hold, ending the
// grandfathered version.
const VersionLatest PlanVersion = "latest"

// Billing says how SetPlan treats the payment provider.
type Billing string

// The billing choices of SetPlan.
const (
	// BillingProvider (the default) charges the change through the provider that bills the product.
	BillingProvider Billing = "provider"
	// BillingKeep moves the customer in Entitler while the provider keeps billing the plan it
	// bills, as a deliberate exception, until a later SetPlan.
	BillingKeep Billing = "keep"
	// BillingEnd ends the provider's subscription when the change takes effect, and the customer
	// holds the plan outside any provider, as with an invoiced contract.
	BillingEnd Billing = "end"
)
