# Pricing pages and visitors

## Signed out, in an app

An app with no backend route for its pricing page reads it with a publishable key alone
(`ent_pk_…`), which holds product scopes only:

```go
client, err := entitler.NewPublishableClient(key)
if err != nil {
	log.Fatal(err)
}
defer client.Close()
pricing, err := client.Pricing(ctx, nil)
if err != nil {
	log.Fatal(err)
}
```

## Signed out, on a server

A server never generates or keeps a visitor, because it serves many. Mint one with
`entitler.NewVisitorID()` for a signed-out person, keep it in a first-party cookie, and pass it to
every pricing read, so the person sees the same experiment arm on every page:

```go
visitor := entitler.NewVisitorID()
pricing, err := srv.Pricing(ctx, &entitler.PricingParams{Visitor: visitor})
if err != nil {
	log.Fatal(err)
}
```

## The visitor through sign-up

An in-app client sends its visitor on every request: the one given with `WithVisitor`, else one
generated for the client's lifetime. Go has no device store, and a library writes no files
unasked, so keep `client.Visitor()` where the app keeps its own data (for example under
`os.UserConfigDir()`) and pass it back at the next launch.

The signed-out screen's visitor goes into the app's own sign-up request, and its server passes it
to `Register`, so the arm the person saw before signing up is the one they keep:

```go
_, err := srv.Customer(userID).Register(ctx, &entitler.RegisterParams{
	Name:    new(name),
	Email:   new(email),
	Visitor: visitorFromSignUp,
})
```

Reset the stored visitor at sign-out and at account deletion, so the next person on a shared
device is never linked to the last.

## Printing prices

Each plan lists its periods (`Key` such as `monthly`, which writes take, and `Label`, display
copy) and its listings per period, with the provider's price where one is set:

```go
func priceFor(plan entitler.PricingPlan, period string) string {
	for _, listing := range plan.Listings {
		if listing.Period == nil || listing.Period.Key != period {
			continue
		}
		for _, channel := range listing.Channels {
			if channel.Price != nil && channel.Purchasable {
				p := channel.Price
				return fmt.Sprintf("%d.%02d %s", p.Amount/100, p.Amount%100, strings.ToUpper(p.Currency))
			}
		}
	}
	return "Coming soon"
}
```

A signed-in customer's pricing is `customer.Pricing(ctx, nil)`. Pricing is always computed now.
