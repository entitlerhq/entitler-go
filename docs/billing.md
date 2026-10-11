# Billing pages

The customer's own billing choices are methods of `entitler.Customer`, on either client, under
self-serve rules: a change must be one `Plans` offers, along a self-serve path, or the API answers
`403 not_self_serve`. The project's policies decide when a change takes effect (a downgrade or a
cancel at renewal, an upgrade now). On a server these need `customers:write`; in an app the token
must hold `billing:self` (`403 scope_required` otherwise) and the organisation needs the customer
portal capability (`409 capability_required`). Mint `billing:self` only for people who may buy
for the customer, such as a workspace's owners, not every member. A customer token never charges a
saved card: a paid change from an app always answers `NextPay`.

## One read for the page

`Plans` is the paywall's and the billing page's one source: `Action` decides each button,
`SKUs` are the store products, and a held plan's `BilledBy` decides between the store's own
management page and the billing portal.

```go
plans, err := customer.Plans(ctx, nil)
if err != nil {
	return err
}
for _, held := range plans.Held {
	switch held.BilledBy {
	case entitler.ProviderApple, entitler.ProviderGoogle:
		fmt.Printf("%s: manage it in the %s store\n", held.Plan.Name, held.BilledBy)
	default:
		fmt.Printf("%s: manage it in the billing portal\n", held.Plan.Name)
	}
}
for _, option := range plans.Options {
	switch option.Action {
	case entitler.ActionBuy:
		fmt.Printf("[%s %s]\n", option.Move, option.Plan.Name)
	case entitler.ActionContact:
		fmt.Printf("[Talk to sales about %s]\n", option.Plan.Name)
	default:
		fmt.Printf("%s is unavailable\n", option.Plan.Name)
	}
}
return nil
```

A plan or add-on is named by its key or public id, a period by its key (`monthly`), which stays
the same when its label is renamed or translated.

## Subscribe and the next step

`Subscribe` answers a `*entitler.SubscribeStep`. Handle every `Next`, and treat one you do not
know as a reason to show the plans again:

```go
step, err := customer.Subscribe(ctx, plan, &entitler.SubscribeParams{Period: period, ReturnURL: returnURL})
var apiErr *entitler.APIError
if errors.As(err, &apiErr) && apiErr.Payment != nil && apiErr.Payment.Status == entitler.PaymentDeclined {
	fmt.Println("The card was declined.")
	return nil
}
if err != nil {
	return err
}
switch step.Next {
case entitler.NextDone:
	fmt.Printf("Now on %s (%s)\n", plan, step.Change.Effective)
case entitler.NextPay:
	fmt.Println("Send the customer to", step.URL)
case entitler.NextConfirming:
	fmt.Println("The bank is confirming the payment.")
case entitler.NextManage:
	fmt.Println("Change it in the store:", step.BilledBy)
default:
	fmt.Println("Show the plans again: next step", step.Next)
}
return nil
```

- `NextDone`: the change is made; `Change.Effective` is `EffectiveNow`, or `EffectiveRenewal` with
  `Change.At` the instant it takes effect. `Change.Replayed` says whether the key was answered before.
- `NextPay`: send the customer to `URL`, the provider's page (Stripe Checkout, or a confirmation
  such as 3-D Secure). The plan changes once they pay.
- `NextConfirming`: the provider is still confirming a payment (a bank debit); show it as pending.
- `NextManage`: a store bills the product (`BilledBy`), so the customer changes it there.
- A declined card stays an error: `402 payment_required` with `Payment.Status` `PaymentDeclined`.

## Return URLs

`ReturnURL` matters only when the next step is a web page: the provider sends the customer back
to it whether they paid or left. A change that needs a page and has none is refused with
`400 return_url_required` before anything changes, so pass one whenever Stripe may bill the
customer. A web app passes a page of its own; a desktop or mobile app billed through Stripe passes
a universal link or app link and opens the `NextPay` URL in the system browser. Apps whose plans a
store bills never call `Subscribe`: see [store purchases](store-purchases.md).

## The return page

Back from the provider, read its state now, then revalidate the plans, so the customer sees the
plan they paid for without waiting for the provider's notification or a kept answer's `max-age`:

```go
if _, err := customer.SyncBilling(ctx); err != nil {
	return err
}
plans, err := customer.Plans(ctx, &entitler.ReadParams{Revalidate: true})
if err != nil {
	return err
}
```

## Cancel and undo

`Cancel` cancels the plan, or the add-on `AddOn` names; `UndoPendingChange` undoes the change
booked for renewal. Each takes at most one of `AddOn` and `Product`, and answers a
`*entitler.PlanChange` whose `Changed` is false when there was nothing to do. A `billing` change,
which only the company books, cannot be undone by the customer (`403 not_self_serve`): undo it with
`SetPlan`.

```go
change, err := customer.Cancel(ctx, &entitler.CancelParams{AddOn: new("sso_addon")})
```

## The billing portal

`BillingPortal` opens the provider's page for payment details and invoices; it changes no plan.
Handle its two expected refusals: a customer the provider has never billed answers
`404 not_found`, and an environment with no payment provider connected answers `409 stale`.

```go
portal, err := customer.BillingPortal(ctx, returnURL)
var apiErr *entitler.APIError
switch {
case err == nil:
	fmt.Println("Open", portal.URL)
case errors.As(err, &apiErr) && apiErr.Code == entitler.CodeNotFound:
	fmt.Println("Nothing billed yet, so there is no portal.")
case errors.As(err, &apiErr) && apiErr.Code == entitler.CodeStale:
	fmt.Println("No payment provider is connected in this environment.")
default:
	fmt.Println("The portal is unavailable:", err)
}
```

## Codes each call can answer

| Call | Codes |
| --- | --- |
| `Subscribe` | `400 invalid_body`, `400 return_url_required`, `402 payment_required`, `403 not_self_serve`, `403 scope_required`, `404 customer_not_found`, `409 billed_elsewhere`, `409 capability_required` |
| `Cancel`, `UndoPendingChange` | `403 not_self_serve`, `403 scope_required`, `404 customer_not_found`, `409 capability_required` |
| `BillingPortal` | `403 scope_required`, `404 not_found`, `409 stale`, `409 capability_required` |
| `SyncBilling` | `403 scope_required`, `404 customer_not_found`, `409 capability_required` |
| `SetPlan`, `SetAddOn` | `400 invalid_body`, `402 payment_required`, `404 customer_not_found`, `409 billed_elsewhere` |

API messages are written for the developer, not for end users: map codes to your own copy.
