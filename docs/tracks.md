# Tracks

A track decides which release of the catalogue a customer is served. Put a customer on a track by
its name, which stays the same in every environment, and return them to All customers with `nil`:

```go
if _, err := customer.SetTrack(ctx, new("Beta"), nil); err != nil {
	return err
}
_, err := customer.SetTrack(ctx, nil, nil)
```

The server key preset lacks `tracks:assign`, so a key minted from it answers
`403 scope_required`: add the scope to the key in the dashboard.
