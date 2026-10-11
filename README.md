# Entitler SDK for Go

The official Go SDK for [Entitler](https://entitler.co), which lets SaaS teams manage plans,
feature access, usage limits and customer grants. Check what a customer is entitled to, record
metered usage with idempotency keys, show pricing, and manage customers' plans, with retries, an
answer cache, answers that stand in while Entitler is unreachable, and offline snapshots. It uses
the standard library alone.

Requires Go 1.26 or later.

```sh
go get github.com/entitlerhq/entitler-go
```

## Quick start on a server

Create one server client from a secret key and share it. Register customers at sign-up and sign-in,
check features, and record usage under a key from your own unit of work:

```go
srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"))
if err != nil {
	log.Fatal(err)
}
defer srv.Close()

customer := srv.Customer(signedInUserID())
if _, err := customer.Register(ctx, &entitler.RegisterParams{
	Name:  new("Ada Lovelace"),
	Email: new("ada@example.com"),
}); err != nil {
	log.Fatal(err)
}

if customer.IsEntitled(ctx, entitler.OnOff("export_pdf"), false, nil) {
	fmt.Println("Export to PDF is on.")
} else {
	fmt.Println("Export to PDF needs an upgrade.")
}

requestID := "quickstart-" + time.Now().UTC().Format("20060102T150405.000")
result, err := customer.RecordUsage(ctx, entitler.Metered("ai_credits"), 1, requestID, nil)
```

Take the customer id from your authenticated session, never from anything the caller sends.
`IsEntitled` never fails because of Entitler: it answers the default you pass (`false` for paid
features) and reports the error to `WithOnError`. [`examples/quickstart`](examples/quickstart/main.go)
runs the same steps as a script, for the customer its configuration names.

## Two clients

- **On your servers**, `entitler.NewServer(secretKey)` acts on any customer: checks, usage,
  registration, customer tokens and the company's decisions (`SetPlan`, grants, meter adjustments).
- **In an app** that runs on a person's device, an in-app client acts for the one signed-in
  customer: `entitler.NewClient(tokenSource)` with customer tokens your server mints,
  `entitler.NewIdentityClient(publishableKey, identityToken)` with a sign-in provider's identity
  token, or `entitler.NewPublishableClient(publishableKey)` for a signed-out pricing page.

Both answer an `entitler.Customer` (`srv.Customer(id)`, `client.Me()`), so gating, usage and the
customer's own billing choices are written once.

A secret key never goes into an app: anyone with the app can read it, and it acts on every
customer. In-app clients take customer tokens and publishable keys (`ent_pk_…`), which hold product
scopes only, and each client refuses the other kind of key when it is created, so a mistake fails
on the developer's machine, never in a shipped app.

## Feature constants

Generate typed constants for your catalogue with the SDK's own command:

```sh
go get -tool github.com/entitlerhq/entitler-go/cmd/entitler
go tool entitler generate -out entitler_features.go -package features
```

Then `customer.Check(ctx, features.FeatureAiCredits, nil)`; usage methods accept only metered
features. See [feature constants](docs/feature-constants.md).

## Guides

- [Checking access](docs/checking-access.md)
- [Recording usage](docs/recording-usage.md): modes, idempotency keys, holds, batches
- [Pricing pages and visitors](docs/pricing-and-visitors.md)
- [Billing pages](docs/billing.md)
- [Company decisions](docs/company-decisions.md): `SetPlan`, add-ons, grants, meters
- [Store purchases](docs/store-purchases.md)
- [The in-app client](docs/in-app-client.md)
- [Offline snapshots](docs/offline-snapshots.md)
- [Reliability](docs/reliability.md): timeouts, retries, the cache, stale answers
- [Errors](docs/errors.md)
- [Reading at another instant](docs/as-of.md)
- [Tracks](docs/tracks.md)
- [Scopes](docs/scopes.md)
- [Configuration](docs/configuration.md)
- [Testing your app](docs/testing.md)
- [net/http](docs/net-http.md)
- [Feature constants and the command-line tool](docs/feature-constants.md)
- [Versioning and support](docs/versioning.md)

The API reference is on [pkg.go.dev](https://pkg.go.dev/github.com/entitlerhq/entitler-go).

## Examples

Each runs with `ENTITLER_KEY` set to a test environment's secret key, from the `examples` folder.
`quickstart`, `metered-work`, `billing` and `generated-features` act for the customer
`ENTITLER_CUSTOMER` names, or a sample customer; `in-app`, `offline` and `net-http` stand in an
app's session for the sample customer `test_1001`, and `pricing-page` acts for a visitor:

- [`quickstart`](examples/quickstart/main.go): register, check and record usage.
- [`pricing-page`](examples/pricing-page/main.go): pricing signed out with `ENTITLER_PUBLISHABLE_KEY`, and from a server with a visitor.
- [`in-app`](examples/in-app/main.go): a token route and an in-app client with a token source.
- [`metered-work`](examples/metered-work/main.go): holds around streamed work, `WithHold`, observe mode.
- [`offline`](examples/offline/main.go): snapshots verified offline as a fallback.
- [`billing`](examples/billing/main.go): a billing page, every next step, the portal and the company's decisions.
- [`generated-features`](examples/generated-features/main.go): generated constants in use.
- [`net-http`](examples/net-http/main.go): middleware for a `net/http` server.
- [`testing`](examples/testing/app_test.go): an app's tests with `entitlertest.FakeCustomer`.

```sh
cd examples && ENTITLER_KEY=ent_test_… go run ./quickstart
```

## Licence

MIT. See [LICENSE](LICENSE).
