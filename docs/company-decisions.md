# Company decisions

`*entitler.ServerCustomer` adds the changes the vendor makes for a deal, a support ticket or a
verified store purchase, under no self-serve rules, sales-led plans included. Each takes a
`Decision`: `Reason` (at most 200 characters) and `Actor` (1 to 200 characters naming the person
or system that decided, such as a support agent's id or `hubspot`), which the customer's activity
shows beside the key's name. Name each idempotency key after the event (this deal won, this
webhook delivery), never after the deal itself: a deal won a second time after churning needs a
new key, or its write replays the first one and moves nobody.

## SetPlan

```go
_, err := customer.SetPlan(ctx, entitler.Plan("pro"), &entitler.SetPlanParams{
	Period:         "yearly",
	Billing:        entitler.BillingEnd,
	Decision:       entitler.Decision{Reason: "Annual contract signed", Actor: "hubspot"},
	IdempotencyKey: "deal-" + run,
})
```

- The choice is `entitler.Plan("key or id")`, an `entitler.SKU` a customer bought (which names its
  own period), or `nil` with `Product` to end the customer's plan in that product (they move to its
  default plan, or hold none).
- `When` is `WhenNow` or `WhenEnd` (at renewal); the project's policy when left out.
- `Billing`: `BillingProvider` (the default) charges through the provider that bills the product;
  `BillingKeep` moves the customer in Entitler while the provider keeps billing its plan, as a
  deliberate exception such as a goodwill upgrade; `BillingEnd` ends the provider's subscription
  when the change takes effect, and the customer holds the plan outside any provider, as with an
  invoiced contract. Moving a Stripe payer onto invoiced Enterprise is one write.
- `Until` is an instant in the future when the customer returns to the default plan unless a later
  `SetPlan` moved them; while Stripe bills the product it needs `BillingEnd`.
- `Version: entitler.VersionLatest` moves the customer now to the newest version of the plan
  they hold, ending a grandfathered version.
- The plan they already hold, with the same period, billing and until, answers `Changed` false, so
  a redelivered webhook is harmless even with a fresh key.

## SetAddOn

`SetAddOn(ctx, addOn, quantity, p)` adds an add-on, sets its quantity, or removes it at 0, taking
effect now. `Replaces` swaps another add-on in one write; `SKU` records a store purchase of it.

## Grants

```go
change, err := customer.Grant(ctx, entitler.Config("support_contacts"), &entitler.GrantParams{
	Value:          entitler.Amount(10),
	Days:           30,
	Decision:       entitler.Decision{Reason: "Goodwill for the outage", Actor: "agent_417"},
	IdempotencyKey: "ticket-" + run,
})
if err == nil {
	fmt.Println("Keep the grant's id to revoke it:", change.Grant.ID)
}
return err
```

`Days` is 0 to 3,650, 0 for no end; leave `Value` out for an on/off feature. `RevokeGrant` takes
the grant's id.

## Meters and corrections

`AdjustMeter` takes exactly one of `By` (a whole number other than 0: `new(int64(-500))` gives
back 500 credits without racing new usage) and `To`; its idempotency key is required, so a support
tool's double submit never applies twice. `CancelUsage` cancels one report by its id, moving the
meter back.
