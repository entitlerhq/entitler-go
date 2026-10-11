package entitler

func decodeEnvironment(o object) Environment {
	return Environment{ID: o.at("id").str(), Name: o.at("name").str(), Kind: EnvironmentKind(o.at("kind").str())}
}

func decodeTrack(o object) Track {
	return Track{ID: o.at("id").str(), Name: o.at("name").str()}
}

func decodeExperiment(o object) Experiment {
	return Experiment{ID: o.at("id").str(), Arm: Arm(o.at("arm").str())}
}

func decodeUpgrade(o object) Upgrade {
	return Upgrade{
		Plan:   o.at("plan").str(),
		Name:   o.at("name").str(),
		Move:   Move(o.at("move").str()),
		Action: Action(o.at("action").str()),
		Reason: o.at("reason").strOrNull(),
	}
}

func decodePeriod(o object) Period {
	return Period{Key: o.at("key").str(), Label: o.at("label").str()}
}

func decodeSource(o object) Source {
	s := Source{Type: SourceType(o.at("type").str())}
	switch s.Type {
	case SourcePlan, SourceAddOn:
		s.Plan = o.at("plan").str()
		s.Name = o.at("name").str()
		s.Version = o.at("version").int()
		if s.Type == SourcePlan {
			s.ByDefault = o.at("byDefault").boolean()
		} else {
			s.Quantity = o.at("quantity").int()
		}
		s.Value = o.at("value").value()
	case SourceGrant:
		s.Grant = o.at("grant").str()
		s.Until = o.at("until").instantOrNull()
		s.Value = o.at("value").value()
	case SourceBanked:
		s.Banked = o.at("value").int()
	case SourceGroup:
		s.Features = o.at("features").strings()
	}
	return s
}

// decodeMeter reads the meter fields a check or entitlement carries for a metered feature.
func decodeMeter(o object) *Meter {
	if !o.has("used") && !o.has("held") && !o.has("remaining") && !o.has("resetsAt") {
		return nil
	}
	m := &Meter{}
	if n, ok := o.opt("used"); ok {
		m.Used = n.int()
	}
	if n, ok := o.opt("held"); ok {
		m.Held = n.int()
	}
	if n, ok := o.opt("remaining"); ok {
		m.Remaining = n.limit()
	}
	if n, ok := o.opt("resetsAt"); ok {
		m.ResetsAt = n.instantOrNull()
	}
	return m
}

type answerContext struct {
	environment Environment
	track       Track
	release     *int64
	change      *string
	testers     bool
	experiment  *Experiment
}

func decodeContext(o object, testers bool) answerContext {
	c := answerContext{
		environment: decodeEnvironment(o.at("environment").object()),
		track:       decodeTrack(o.at("track").object()),
		release:     o.at("release").intOrNull(),
		change:      o.at("change").strOrNull(),
	}
	if testers {
		c.testers = o.at("testers").boolean()
		c.experiment = objectOrNull(o.at("experiment"), decodeExperiment)
	}
	return c
}

func decodeCheck(o object) Check {
	c := Check{
		Customer: o.at("customer").str(),
		AsOf:     o.at("asOf").instant(),
		Feature:  o.at("feature").str(),
		Type:     FeatureType(o.at("type").str()),
		Entitled: o.at("entitled").boolean(),
		Value:    o.at("value").value(),
		Sources:  objects(o.at("sources"), decodeSource),
		Meter:    decodeMeter(o),
		Upgrades: objects(o.at("upgrades"), decodeUpgrade),
	}
	x := decodeContext(o, true)
	c.Environment, c.Track, c.Release, c.Change, c.Testers, c.Experiment =
		x.environment, x.track, x.release, x.change, x.testers, x.experiment
	return c
}

func decodeEntitlement(o object) Entitlement {
	return Entitlement{
		Key:      o.at("key").str(),
		Type:     FeatureType(o.at("type").str()),
		Value:    o.at("value").value(),
		Entitled: o.at("entitled").boolean(),
		Sources:  objects(o.at("sources"), decodeSource),
		Meter:    decodeMeter(o),
		Upgrades: objects(o.at("upgrades"), decodeUpgrade),
	}
}

func decodeEntitlements(o object) Entitlements {
	e := Entitlements{
		Customer: o.at("customer").str(),
		AsOf:     o.at("asOf").instant(),
		Items:    objects(o.at("entitlements"), decodeEntitlement),
	}
	x := decodeContext(o, true)
	e.Environment, e.Track, e.Release, e.Change, e.Testers, e.Experiment =
		x.environment, x.track, x.release, x.change, x.testers, x.experiment
	return e
}

func decodePlanSummary(o object) PlanSummary {
	return PlanSummary{ID: o.at("id").str(), Key: o.at("key").str(), Name: o.at("name").str()}
}

func decodeProduct(o object) Product {
	return Product{Key: o.at("key").str(), Name: o.at("name").str()}
}

func decodePending(n node) PendingChange {
	if n.null() {
		return nil
	}
	o := n.object()
	switch t := o.at("type").str(); t {
	case "move":
		return PendingMove{Plan: decodePlanSummary(o.at("plan").object())}
	case "cancel":
		return PendingCancel{MovingTo: objectOrNull(o.at("movingTo"), decodePlanSummary)}
	case "billing":
		return PendingBilling{Billing: BillingSource(o.at("billing").str())}
	default:
		return PendingUnknown{Type: t}
	}
}

func decodeHeldPlan(o object) HeldPlan {
	plan := o.at("plan").object()
	h := HeldPlan{
		Plan: PlanRef{
			ID:   plan.at("id").str(),
			Key:  plan.at("key").str(),
			Name: plan.at("name").str(),
			Kind: PlanKind(plan.at("kind").str()),
		},
		Product:   objectOrNull(o.at("product"), decodeProduct),
		Version:   o.at("version").intOrNull(),
		ByDefault: o.at("byDefault").boolean(),
		Period:    objectOrNull(o.at("period"), decodePeriod),
		RenewsAt:  o.at("renewsAt").instantOrNull(),
		Pending:   decodePending(o.at("pending")),
	}
	if billedBy := o.at("billedBy"); !billedBy.null() {
		h.BilledBy = Provider(billedBy.str())
	}
	return h
}

func decodePrice(o object) Price {
	p := Price{
		Amount:        o.at("amount").int(),
		Currency:      o.at("currency").str(),
		IntervalCount: o.at("intervalCount").int(),
		Tax:           Tax(o.at("tax").str()),
	}
	if interval := o.at("interval"); !interval.null() {
		p.Interval = Interval(interval.str())
	}
	return p
}

func optionalPrice(o object) *Price {
	if n, ok := o.opt("price"); ok {
		return objectOrNull(n, decodePrice)
	}
	return nil
}

func decodeOfferedSKU(o object) OfferedSKU {
	return OfferedSKU{
		Period:    objectOrNull(o.at("period"), decodePeriod),
		Connector: o.at("connector").str(),
		IDs:       o.at("ids").stringMap(),
		Price:     optionalPrice(o),
	}
}

func decodeMoveOption(o object) MoveOption {
	plan := o.at("plan").object()
	return MoveOption{
		Plan: OfferedPlan{
			ID:          plan.at("id").str(),
			Key:         plan.at("key").str(),
			Name:        plan.at("name").str(),
			Kind:        PlanKind(plan.at("kind").str()),
			Description: plan.at("description").str(),
			Default:     plan.at("default").boolean(),
		},
		Product: objectOrNull(o.at("product"), decodeProduct),
		From:    objectOrNull(o.at("from"), decodePlanSummary),
		Move:    Move(o.at("move").str()),
		Action:  Action(o.at("action").str()),
		Reason:  o.at("reason").strOrNull(),
		When:    When(o.at("when").str()),
		Periods: objects(o.at("periods"), decodePeriod),
		Impact: objects(o.at("impact"), func(o object) Impact {
			return Impact{Kind: ImpactKind(o.at("kind").str()), Text: o.at("text").str()}
		}),
		SKUs: objects(o.at("skus"), decodeOfferedSKU),
	}
}

func decodeCustomerPlans(o object) CustomerPlans {
	p := CustomerPlans{
		Customer: o.at("customer").str(),
		AsOf:     o.at("asOf").instant(),
		Held:     objects(o.at("held"), decodeHeldPlan),
		Options:  objects(o.at("options"), decodeMoveOption),
	}
	x := decodeContext(o, true)
	p.Environment, p.Track, p.Release, p.Change, p.Testers, p.Experiment =
		x.environment, x.track, x.release, x.change, x.testers, x.experiment
	return p
}

func decodeChannel(o object) Channel {
	return Channel{Provider: Provider(o.at("provider").str()), ConnectionID: o.at("connectionId").str()}
}

func decodeListingChannel(o object) ListingChannel {
	c := ListingChannel{
		Channel:     decodeChannel(o.at("channel").object()),
		Name:        o.at("name").str(),
		Purchasable: o.at("purchasable").boolean(),
		Price:       optionalPrice(o),
	}
	if mode := o.at("mode"); !mode.null() {
		c.Mode = EnvironmentKind(mode.str())
	}
	if ids := o.at("ids"); !ids.null() {
		c.IDs = ids.stringMap()
	}
	return c
}

func decodePricingPlan(o object) PricingPlan {
	p := PricingPlan{
		ID:          o.at("id").str(),
		Key:         o.at("key").str(),
		Name:        o.at("name").str(),
		Description: o.at("description").str(),
		Kind:        PlanKind(o.at("kind").str()),
		Product:     o.at("product").strOrNull(),
		SalesLed:    o.at("salesLed").boolean(),
		Status:      PlanStatus(o.at("status").str()),
		Version:     o.at("version").intOrNull(),
		Default:     o.at("default").boolean(),
		Periods: objects(o.at("periods"), func(o object) PricingPeriod {
			return PricingPeriod{
				Label: o.at("label").str(),
				Count: o.at("count").int(),
				Unit:  TimeUnit(o.at("unit").str()),
				Key:   o.at("key").str(),
			}
		}),
		AttachesTo: o.at("attachesTo").strings(),
		Listings: objects(o.at("listings"), func(o object) Listing {
			return Listing{
				Period:   objectOrNull(o.at("period"), decodePeriod),
				Channels: objects(o.at("channels"), decodeListingChannel),
			}
		}),
	}
	features := o.at("features").object()
	p.Features = make(map[string]Value, len(features.m))
	for key, v := range features.m {
		p.Features[key] = node{r: features.r, path: features.path + "[…]", v: v}.value()
	}
	return p
}

func decodePricing(o object) Pricing {
	p := Pricing{
		Customer:    o.at("customer").strOrNull(),
		DefaultPlan: o.at("defaultPlan").strOrNull(),
		Products: objects(o.at("products"), func(o object) PricingProduct {
			return PricingProduct{
				Key:         o.at("key").str(),
				Name:        o.at("name").str(),
				DefaultPlan: o.at("defaultPlan").strOrNull(),
			}
		}),
		Plans: objects(o.at("plans"), decodePricingPlan),
	}
	x := decodeContext(o, true)
	p.Environment, p.Track, p.Release, p.Change, p.Testers, p.Experiment =
		x.environment, x.track, x.release, x.change, x.testers, x.experiment
	return p
}

func decodeFeatureList(o object) FeatureList {
	l := FeatureList{
		Features: objects(o.at("features"), func(o object) FeatureListing {
			return FeatureListing{
				ID:          o.at("id").str(),
				Key:         o.at("key").str(),
				Name:        o.at("name").str(),
				Type:        FeatureType(o.at("type").str()),
				Description: o.at("description").str(),
				Unit:        o.at("unit").str(),
				ResetEvery: objectOrNull(o.at("resetEvery"), func(o object) MeterWindow {
					w := MeterWindow{Count: o.at("count").int(), Unit: TimeUnit(o.at("unit").str())}
					if anchor, ok := o.opt("anchor"); ok {
						w.Anchor = MeterAnchor(anchor.str())
					}
					return w
				}),
				Archived: o.at("archived").boolean(),
				Includes: o.at("includes").strings(),
			}
		}),
	}
	x := decodeContext(o, false)
	l.Environment, l.Track, l.Release, l.Change = x.environment, x.track, x.release, x.change
	return l
}

func decodeUsageResult(o object) UsageResult {
	r := UsageResult{
		Check:       decodeCheck(o),
		Outcome:     Outcome(o.at("outcome").str()),
		ID:          o.at("id").strOrNull(),
		HoldID:      o.at("holdId").strOrNull(),
		Mode:        UsageMode(o.at("mode").str()),
		Amount:      o.at("amount").int(),
		MeterChange: o.at("meterChange").int(),
		OverBy:      o.at("overBy").int(),
		Late:        o.at("late").boolean(),
		OccurredAt:  o.at("occurredAt").instantOrNull(),
		ExpiresAt:   o.at("expiresAt").instantOrNull(),
		ReportedAs:  UsageSource(o.at("reportedAs").str()),
	}
	if refusal := o.at("refusal"); !refusal.null() {
		r.Refusal = Refusal(refusal.str())
	}
	return r
}

func decodeUsageEvent(o object) UsageEvent {
	e := UsageEvent{
		ID:          o.at("id").str(),
		Feature:     o.at("feature").str(),
		Source:      UsageSource(o.at("source").str()),
		By:          o.at("by").strOrNull(),
		Actor:       o.at("actor").strOrNull(),
		Reason:      o.at("reason").strOrNull(),
		At:          o.at("at").instant(),
		CancelledAt: o.at("cancelledAt").instantOrNull(),
		Kind:        UsageEventKind(o.at("kind").str()),
		Amount:      o.at("amount").int(),
	}
	if e.Kind == UsageEventAdjust {
		e.SetTo = o.at("setTo").intOrNull()
	} else if setTo, ok := o.opt("setTo"); ok {
		e.SetTo = setTo.intOrNull()
	}
	return e
}

func decodeUsageLog(o object) UsageLog {
	l := UsageLog{Items: objects(o.at("items"), decodeUsageEvent)}
	if next := o.at("next"); !next.null() {
		l.Next = next.str()
	}
	return l
}

func decodeCustomerUsage(o object) CustomerUsage {
	u := CustomerUsage{
		Customer:           o.at("customer").str(),
		AsOf:               o.at("asOf").instant(),
		MetersStartAgainAt: o.at("metersStartAgainAt").instantOrNull(),
		Features: objects(o.at("features"), func(o object) FeatureUsage {
			f := FeatureUsage{
				Feature:  o.at("feature").str(),
				Type:     FeatureType(o.at("type").str()),
				Entitled: o.at("entitled").boolean(),
				Value:    o.at("value").value(),
				Sources:  objects(o.at("sources"), decodeSource),
				Used:     o.at("used").int(),
				Held:     o.at("held").int(),
				ResetsAt: o.at("resetsAt").instantOrNull(),
			}
			if remaining := o.at("remaining"); !remaining.null() {
				f.Remaining = remaining.limit()
			}
			return f
		}),
		Log: decodeUsageLog(o.at("log").object()),
	}
	x := decodeContext(o, true)
	u.Environment, u.Track, u.Release, u.Change, u.Testers, u.Experiment =
		x.environment, x.track, x.release, x.change, x.testers, x.experiment
	return u
}

func decodeCustomerSummary(o object) CustomerSummary {
	return CustomerSummary{
		ID:            o.at("id").str(),
		ExternalID:    o.at("externalId").str(),
		Name:          o.at("name").str(),
		Email:         o.at("email").str(),
		EnvironmentID: o.at("environmentId").str(),
		Sample:        o.at("sample").boolean(),
		CreatedAt:     o.at("createdAt").instant(),
		Plan: objectOrNull(o.at("plan"), func(o object) VersionedPlan {
			return VersionedPlan{
				ID: o.at("id").str(), Key: o.at("key").str(), Name: o.at("name").str(), Version: o.at("version").int(),
			}
		}),
		Plans: objects(o.at("plans"), func(o object) ProductPlan {
			return ProductPlan{
				ID: o.at("id").str(), Key: o.at("key").str(), Name: o.at("name").str(),
				Version: o.at("version").int(), Product: o.at("product").str(),
			}
		}),
		DefaultPlan:  objectOrNull(o.at("defaultPlan"), decodePlanSummary),
		Status:       o.at("status").str(),
		Kind:         CustomerKind(o.at("kind").str()),
		Metadata:     o.at("metadata").stringMap(),
		Track:        decodeTrack(o.at("track").object()),
		TestCustomer: o.at("testCustomer").boolean(),
	}
}

func decodeCustomerPage(o object) CustomerPage {
	p := CustomerPage{
		Items: objects(o.at("items"), decodeCustomerSummary),
		Used:  o.at("used").int(),
		Limit: o.at("limit").limit(),
	}
	if next := o.at("next"); !next.null() {
		p.Next = next.str()
	}
	return p
}

func decodePurchase(o object) Purchase {
	p := Purchase{
		Money:   Money(o.at("money").str()),
		Channel: objectOrNull(o.at("channel"), decodeChannel),
		Release: o.at("release").intOrNull(),
		Change:  o.at("change").strOrNull(),
	}
	if arm := o.at("arm"); !arm.null() {
		p.Arm = Arm(arm.str())
	}
	return p
}

func decodeHeldAddOn(o object) HeldAddOn {
	return HeldAddOn{
		Plan:      decodePlanSummary(o.at("plan").object()),
		Version:   o.at("version").int(),
		Quantity:  o.at("quantity").int(),
		Countable: o.at("countable").boolean(),
		AddedAt:   o.at("addedAt").instant(),
		MovingTo:  objectOrNull(o.at("movingTo"), decodePlanSummary),
		Purchase:  decodePurchase(o.at("purchase").object()),
	}
}

func decodeSubscription(o object) Subscription {
	return Subscription{
		Product:   decodeProduct(o.at("product").object()),
		Plan:      decodePlanSummary(o.at("plan").object()),
		Version:   o.at("version").int(),
		Cohort:    o.at("cohort").intOrNull(),
		Period:    objectOrNull(o.at("period"), decodePeriod),
		StartedAt: o.at("startedAt").instant(),
		RenewsAt:  o.at("renewsAt").instant(),
		Billing: objectOrNull(o.at("billing"), func(o object) ProviderPeriod {
			return ProviderPeriod{
				Provider:    o.at("provider").str(),
				StartsAt:    o.at("startsAt").instant(),
				EndsAt:      o.at("endsAt").instant(),
				TrialEndsAt: o.at("trialEndsAt").instantOrNull(),
				CancelsAt:   o.at("cancelsAt").instantOrNull(),
			}
		}),
		Until:    o.at("until").instantOrNull(),
		AddOns:   objects(o.at("addOns"), decodeHeldAddOn),
		Pending:  decodePending(o.at("pending")),
		Purchase: decodePurchase(o.at("purchase").object()),
		Override: objectOrNull(o.at("override"), func(o object) PlanOverride {
			return PlanOverride{
				From:     decodePlanSummary(o.at("from").object()),
				Period:   objectOrNull(o.at("period"), decodePeriod),
				BilledBy: objectOrNull(o.at("billedBy"), decodeChannel),
				Since:    o.at("since").instant(),
				By:       o.at("by").str(),
			}
		}),
	}
}

func decodeGrant(o object) Grant {
	g := Grant{
		ID:        o.at("id").str(),
		Feature:   o.at("feature").str(),
		From:      o.at("from").instant(),
		Until:     o.at("until").instantOrNull(),
		RevokedAt: o.at("revokedAt").instantOrNull(),
		Reason:    o.at("reason").str(),
		By:        o.at("by").str(),
		Actor:     o.at("actor").strOrNull(),
	}
	value := o.at("value")
	switch s := value.str(); {
	case s == "":
		g.Value = On{}
	case s == "unlimited":
		g.Value = Unlimited{}
	default:
		g.Value = grantAmount(value, s)
	}
	return g
}

func grantAmount(n node, s string) Value {
	var amount int64
	for _, c := range s {
		if c < '0' || c > '9' || amount > maxWhole/10 {
			n.r.fail(n.path, "expected \"\", \"unlimited\" or digits")
			return nil
		}
		amount = amount*10 + int64(c-'0')
	}
	return Amount(amount)
}

func decodeCustomerDetail(o object) CustomerDetail {
	d := CustomerDetail{
		Customer:     decodeCustomerSummary(o.at("customer").object()),
		AsOf:         o.at("asOf").instant(),
		Subscription: objectOrNull(o.at("subscription"), decodeSubscription),
		DefaultPlan:  objectOrNull(o.at("defaultPlan"), decodePlanSummary),
		Products: objects(o.at("products"), func(o object) ProductSubscription {
			return ProductSubscription{
				Product:      decodeProduct(o.at("product").object()),
				DefaultPlan:  objectOrNull(o.at("defaultPlan"), decodePlanSummary),
				Subscription: objectOrNull(o.at("subscription"), decodeSubscription),
			}
		}),
		AddOns:       objects(o.at("addOns"), decodeHeldAddOn),
		Entitlements: objects(o.at("entitlements"), decodeEntitlement),
		MoveOptions:  objects(o.at("moveOptions"), decodeMoveOption),
		Grants:       objects(o.at("grants"), decodeGrant),
		Usage:        decodeUsageLog(o.at("usage").object()),
		Activity: objects(o.at("activity"), func(o object) Activity {
			return Activity{
				Text:   o.at("text").str(),
				At:     o.at("at").instant(),
				By:     o.at("by").strOrNull(),
				Actor:  o.at("actor").strOrNull(),
				Reason: o.at("reason").strOrNull(),
			}
		}),
		Environment: decodeEnvironment(o.at("environment").object()),
	}
	banked := o.at("banked").object()
	d.Banked = make(map[string]int64, len(banked.m))
	for key, v := range banked.m {
		d.Banked[key] = node{r: banked.r, path: banked.path + "[…]", v: v}.int()
	}
	return d
}

func decodeRegisteredCustomer(o object) RegisteredCustomer {
	return RegisteredCustomer{
		ID:            o.at("id").str(),
		ExternalID:    o.at("externalId").str(),
		EnvironmentID: o.at("environmentId").str(),
		CreatedAt:     o.at("createdAt").instant(),
		Created:       o.at("created").boolean(),
	}
}

func decodeIssuedCustomerToken(o object) IssuedCustomerToken {
	return IssuedCustomerToken{
		Token:     o.at("token").str(),
		Customer:  o.at("customer").str(),
		Scopes:    listOf(o.at("scopes"), func(n node) Scope { return Scope(n.str()) }),
		ExpiresAt: o.at("expiresAt").instant(),
	}
}

func decodeIssuedSnapshot(o object) IssuedSnapshot {
	return IssuedSnapshot{
		Token:     o.at("token").str(),
		ExpiresAt: o.at("expiresAt").instant(),
		KeyID:     o.at("keyId").str(),
	}
}

func decodeCustomerTrack(o object) CustomerTrack {
	t := CustomerTrack{
		Customer:        o.at("customer").str(),
		EnvironmentID:   o.at("environmentId").str(),
		Track:           decodeTrack(o.at("track").object()),
		PreviousTrackID: o.at("previousTrackId").strOrNull(),
	}
	if source := o.at("source"); !source.null() {
		t.Source = TrackSource(source.str())
	}
	return t
}

func decodeDrift(o object) BillingDrift {
	return BillingDrift{
		BilledPlan: o.at("billedPlan").str(),
		HeldPlan:   o.at("heldPlan").strOrNull(),
		ObservedAt: o.at("observedAt").instant(),
	}
}

func statusOrNone(n node) SubscriptionStatus {
	if n.null() {
		return ""
	}
	return SubscriptionStatus(n.str())
}

func decodeCustomerBilling(o object) CustomerBilling {
	b := CustomerBilling{
		Status: statusOrNone(o.at("status")),
		SKU:    objectOrNull(o.at("sku"), decodeOfferedSKU),
		Items:  o.at("items").int(),
		Drift:  objectOrNull(o.at("drift"), decodeDrift),
		Products: objects(o.at("products"), func(o object) ProductBilling {
			return ProductBilling{
				Product: o.at("product").strOrNull(),
				Status:  statusOrNone(o.at("status")),
				SKU:     objectOrNull(o.at("sku"), decodeOfferedSKU),
				Items:   o.at("items").int(),
				Drift:   objectOrNull(o.at("drift"), decodeDrift),
			}
		}),
	}
	if provider := o.at("provider"); !provider.null() {
		b.Provider = provider.str()
	}
	return b
}

func decodePlanChange(o object) PlanChange {
	return PlanChange{
		Product:   objectOrNull(o.at("product"), decodeProduct),
		Plan:      objectOrNull(o.at("plan"), decodePlanSummary),
		Quantity:  o.at("quantity").intOrNull(),
		Effective: Effective(o.at("effective").str()),
		At:        o.at("at").instant(),
		Until:     o.at("until").instantOrNull(),
		Changed:   o.at("changed").boolean(),
	}
}

func decodeSubscribeStep(o object) SubscribeStep {
	s := SubscribeStep{Next: Next(o.at("next").str())}
	switch s.Next {
	case NextDone:
		change := decodePlanChange(o)
		s.Change = &change
	case NextPay:
		s.URL = o.at("url").str()
	case NextManage:
		s.BilledBy = Provider(o.at("billedBy").str())
	}
	return s
}

func decodeGrantChange(o object) GrantChange {
	return GrantChange{Grant: decodeGrant(o.at("grant").object())}
}

func decodeBillingPortal(o object) BillingPortal {
	return BillingPortal{Provider: o.at("provider").str(), URL: o.at("url").str()}
}

func decodeBillingSync(o object) BillingSync {
	return BillingSync{Changed: o.at("changed").boolean()}
}

// decodeScopes reads GET /keys/self, a key or a customer token, keeping only the scopes the SDK
// knows, in their fixed order.
func decodeScopes(o object) Scopes {
	if o.has("customer") {
		o.at("customer").str()
		o.at("expiresAt").str()
	} else {
		o.at("id").str()
		o.at("name").str()
		o.at("kind").str()
		o.at("orgId").str()
		o.at("projectId").strOrNull()
		o.at("environmentId").strOrNull()
		o.at("allowedOrigins").strings()
		o.at("createdAt").instant()
		o.at("revokedAt").instantOrNull()
	}
	held := map[Scope]bool{}
	o.at("scopes").list(func(n node) { held[Scope(n.str())] = true })
	s := Scopes{List: []Scope{}}
	for _, scope := range knownScopes {
		if held[scope] {
			s.List = append(s.List, scope)
		}
	}
	if registration, ok := o.opt("registration"); ok {
		s.Registration = registration.boolean()
	}
	return s
}

// decodeAnything accepts any JSON object, for answers the SDK passes over.
func decodeAnything(object) struct{} { return struct{}{} }
