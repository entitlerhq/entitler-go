# Reading at another instant

A `*entitler.ServerCustomer`'s reads that the API answers at an instant (`Check`, `IsEntitled`,
`Entitlements`, `Plans`, `Usage` and `Details`) take `AsOf`, sent as `Entitler-As-Of` on that
request only:

```go
nextMonth, err := customer.Plans(ctx, &entitler.ReadParams{AsOf: time.Now().AddDate(0, 1, 0)})
```

- Reads at another instant need the organisation's `as_of` capability
  (`409 capability_required` otherwise).
- Pricing is always computed now, and writes never take an instant, so a preview never breaks
  writes in a live environment. In-app reads are answered now: an in-app read given `AsOf` fails.
- As-of shows the effects of time on the plans, grants and meters already in place (renewals,
  booked moves, grant expiries, meter resets), never a release nobody has rolled out yet, since
  nothing is derived on a schedule. To preview an unreleased change, put a test customer on a track
  for testers.
