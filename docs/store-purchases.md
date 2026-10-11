# Store purchases

Apps whose plans the App Store or Google Play bill never call `Subscribe` or use a return URL:
most storefronts forbid card charges and web checkout for digital goods. The app's paywall buys the
`SKUs` that `Plans` lists through StoreKit or Play Billing, and your server records each verified
purchase with `SetPlan`. Clients never record purchases. A store SKU never touches Stripe: when
Stripe already bills that product, the API answers `409 billed_elsewhere` and changes nothing.

## Apple

1. The app buys the SKU and sends the transaction to your server.
2. Your server verifies it with the App Store Server API.
3. It records the purchase, ending with the transaction's expiry:

```go
_, err := customer.SetPlan(ctx, entitler.SKU{
	Connector: "apple",
	IDs:       map[string]string{"productId": transaction.ProductID},
}, &entitler.SetPlanParams{
	Until:          transaction.ExpiresAt,
	Decision:       entitler.Decision{Reason: "App Store purchase", Actor: "app-store"},
	IdempotencyKey: "apple-" + transaction.ID,
})
```

4. On each renewal notification, verify it and send the same call with the new expiry and the new
   transaction's id. A missed notification ends the plan at `Until` instead of keeping it for ever.
5. On a refund or revocation, end the plan: `SetPlan(ctx, nil, &entitler.SetPlanParams{Product: "app", When: entitler.WhenNow})`.

## Google Play

The same steps with the Play Developer API: verify the purchase token, then record
`entitler.SKU{Connector: "google", IDs: map[string]string{"productId": ..., "basePlanId": ...}}`
with `Until` set to the subscription's expiry, on each renewal too.

## Deleting an account

Store subscriptions are invisible to Entitler. Before `Erase`, cancel the customer's store
subscription through Apple or Google (or ask them to), then erase:

```go
err := customer.Erase(ctx, nil)
```

`Erase` ends any Stripe subscription now and erases the customer's details, usage and events.
Erasing twice is safe.
