# Testing your app

The `entitlertest` package, like `net/http/httptest`, tests code that uses the SDK without the
network. Your binaries never include it unless your tests import it.

## A fake customer

`entitlertest.FakeCustomer` answers a `*entitler.ServerCustomer` from values held in memory. Every
method answers a complete body the SDK decodes as it would from the API: reads answer from the
values, usage writes move the meters as the API does (a gated report past the allowance is refused,
holds settle and release, a reused key replays), every write is recorded, and writes the fake does
not model answer a plain success that `Answer` replaces.

```go
func TestSummariseChargesWhatItUsed(t *testing.T) {
	ctx := context.Background()
	customer, _ := entitlertest.FakeCustomer(entitlertest.Values{"ai_credits": entitlertest.Metered(100, 0)}, nil)
	if _, err := Summarise(ctx, customer, "job-1", 250); err != nil {
		t.Fatal(err)
	}
	check, _ := customer.Check(ctx, entitler.Metered("ai_credits"), nil)
	if check.Meter.Used != 3 {
		t.Fatalf("used %d", check.Meter.Used)
	}
```

Replace one method's answer, and assert on the writes:

```go
customer, fake := entitlertest.FakeCustomer(nil, nil)
fake.Answer("Subscribe", func(entitlertest.Write) (int, []byte) {
	return 200, entitlertest.SubscribeStep(entitler.SubscribeStep{Next: entitler.NextPay, URL: "https://checkout.example.com/pay"})
})
next, err := Upgrade(ctx, customer, "pro")
if err != nil || next != "https://checkout.example.com/pay" {
	t.Fatalf("%q %v", next, err)
}
writes := fake.Writes()
```

A feature the values do not hold answers `404 feature_not_found`, so `IsEntitled` answers its
default. `Options` sets the customer's id and what `Plans` and `Pricing` answer.

## Faking the transport

Tests that fake the HTTP transport (an `http.RoundTripper` or an `httptest.Server`) answer with the
answer builders, one per answer the SDK decodes (`entitlertest.Check`, `entitlertest.CustomerPlans`,
`entitlertest.UsageResult`…). Each takes the SDK's own answer type and fills what you leave out, so
a fixture is never missing a field the decoder requires. `entitlertest.Error` writes an API error.

Give a client under test `entitler.WithMaxRetries(0)` and `entitler.WithoutCache()`, so a test sees
each answer it set up exactly once. Code that waits (retries, hold expiry, token refresh) is best
tested inside `testing/synctest`, whose fake clock the SDK's timers follow.
