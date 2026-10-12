# Checking access

A customer is either the server's `srv.Customer(id)` or an app's `client.Me()`. Both are an
`entitler.Customer`, so gating code takes one and is written once.

## Is the customer entitled?

`IsEntitled` answers a bool and never fails because of Entitler. When the check cannot be made
(Entitler unreachable with nothing kept, a blank feature, an invalid timeout), it answers the
default you pass and hands the error to the function given with `WithOnError`. A done context
answers the default without calling it.

```go
if customer.IsEntitled(ctx, features.FeatureExportPdf, false, nil) {
	return renderPDF(document)
}
return upgradePrompt()
```

Choose the default per feature:

- `false` fails closed: for paid features, a customer is refused while Entitler cannot be asked.
- `true` fails open: only where losing a sale is worse than giving the feature away.

## The whole check

`Check` answers the server's decision with everything around it: the value (`entitler.On{}`, an
`entitler.Amount` or `entitler.Unlimited{}`), where it comes from, and the plans that would
entitle the customer.

```go
check, err := customer.Check(ctx, features.FeatureAiCredits, nil)
if err != nil {
	return err
}
if !check.Entitled {
	for _, upgrade := range check.Upgrades {
		fmt.Printf("%s %s (%s)\n", upgrade.Move, upgrade.Name, upgrade.Action)
	}
}
if check.Meter != nil {
	fmt.Printf("%s left, %d on hold\n", check.Meter.Remaining, check.Meter.Held)
}
```

- `Entitled` is the server's decision. Never derive it from the value: an on/off feature the
  customer lacks answers `Amount(0)`.
- For a metered feature, `Entitled` means allowance remains: the customer has the feature and
  `Meter.Remaining` is above 0.
- `Upgrades` lists the plans that would entitle the customer, filled only when they are not
  entitled. Its `Move` and `Action` follow the same rule as the paywall's options in `Plans`.
- `Stale` is true when the answer came from a kept copy because Entitler was unreachable.

`Entitlements` answers the whole list, groups included with the server's decision:

```go
list, err := customer.Entitlements(ctx, nil)
if err != nil {
	return err
}
if list.Has(features.FeatureCollaboration) {
	showCollaboration()
}
```

## Showing a change at once

Checks, entitlement lists, plans and pricing go through the answer cache, which honours the
answer's `max-age`. A page that knows the customer just changed (back from paying, after a
server-side upgrade) passes `Revalidate`, which skips a fresh kept answer and asks Entitler with
its `ETag`:

```go
check, err := customer.Check(ctx, features.FeatureExportPdf, &entitler.ReadParams{Revalidate: true})
```

Writes through the same client already revalidate that customer's kept answers.
