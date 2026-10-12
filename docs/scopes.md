# Scopes

Entitler decides on every call which calls a credential may make: the SDK never refuses a call
itself, and a missing scope answers `403 scope_required`. `Scopes` tells an app which scopes its
client holds, for deciding which controls to show; each call asks again, so a newly granted scope
shows without a restart.

```go
scopes, err := client.Scopes(ctx)
if err != nil {
	return err
}
showBuyButton := scopes.Has(entitler.ScopeBillingSelf)
```

For an identity client, `Registration` says whether the sign-in provider lets a new person
register.

| Call | Scope |
| --- | --- |
| `Check`, `IsEntitled`, `Entitlements`, `Plans`, `Pricing` (a customer's), `Snapshot` | `entitlements:read` |
| `Server.Pricing`, `Features`, `PublishableClient.Pricing` | `plans:read` |
| `Usage` | `usage:read` |
| `RecordUsage`, holds, `RecordUsageBatch` | `usage:write` |
| `Subscribe`, `Cancel`, `UndoPendingChange`, `BillingPortal`, `SyncBilling` | `customers:write`; in an app, `billing:self` |
| `Register` | `customers:register`; changing an existing customer's details also `customers:write` or `customers:profile` |
| `Details`, `Billing`, `Customers.Page`, `Customers.List` | `customers:read` |
| `Update` | `customers:write` or `customers:profile` |
| `Erase`, `SetPlan`, `SetAddOn`, `Grant`, `RevokeGrant`, `AdjustMeter`, `CancelUsage`, `Customers.Create` | `customers:write` |
| `Token` | `tokens:mint` |
| `SetTrack` | `tracks:assign` |
| `Scopes`, `SnapshotKeys` | none |

Customer tokens hold at most `entitlements:read`, `usage:read`, `usage:write` and `billing:self`.
