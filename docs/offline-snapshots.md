# Offline snapshots

A snapshot is the customer's entitlements signed by Entitler (ES256), for gating while offline or
after a restart. Mint one while online, keep it, and verify it with no request:

```go
verified, err := entitler.VerifySnapshot(snapshot, expected)
return err == nil && verified.Entitlements.Has(feature)
```

Meters in a snapshot are frozen when it is signed. `VerifySnapshot` answers a
`*entitler.SnapshotError` (`snapshot_invalid` or `snapshot_expired`) when a check fails.

## Where the expected values come from

A mistake here shows only offline, so take each from the right place:

- `Customer` is the signed-in person's external id from the app's own session, never from the
  stored snapshot. For an identity-token customer it is `<provider id>:<subject>`, with the sign-in
  provider's Entitler id from the app's configuration.
- `Environment` is the environment id the app's build ships, beside its snapshot keys.
- `Keys` are the snapshot keys the app ships.

## Key pinning

The keys decide which snapshots the app trusts. Ship them with the app, written at build time:

```sh
go tool entitler snapshot-keys -out snapshot_keys.json
```

and load them with `//go:embed` and `json.Unmarshal` into an `entitler.SnapshotKeys`. Replace them
only with keys fetched from Entitler over HTTPS (`SnapshotKeys`). Never store them beside the token
or load them from the same record, since anyone who can edit that record could replace both.

## Key rotation

- Entitler publishes a new signing key before signing with it.
- It keeps retired keys published for 30 days after their last use, longer than any snapshot lives.
- An emergency replacement withdraws the old keys at once, and snapshots they signed stop
  verifying once the app refetches.
- So refresh the keys from `SnapshotKeys` whenever the app is online, keep them in the app's own
  trusted storage, and verify offline against the keys it holds. A snapshot signed by a key the app
  has not fetched yet fails until the app is next online.

## Falling back

Ask Entitler first, and fall back to the snapshot when `entitler.IsUnreachable(err)`:

```go
func isEntitled(ctx context.Context, customer entitler.Customer, feature entitler.Feature,
	snapshot string, expected entitler.SnapshotExpectation) bool {
	check, err := customer.Check(ctx, feature, nil)
	if err == nil {
		return check.Entitled
	}
	if !entitler.IsUnreachable(err) {
		return false
	}
	verified, err := entitler.VerifySnapshot(snapshot, expected)
	return err == nil && verified.Entitlements.Has(feature)
}
```
