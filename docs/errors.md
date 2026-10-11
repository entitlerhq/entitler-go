# Errors

Failures from Entitler are an `entitler.Error`, one of:

| Kind | When |
| --- | --- |
| `*entitler.APIError` | the API answered with a status other than 2xx, or any redirect, or a 2xx the SDK cannot decode (`CodeInvalidResponse`) |
| `*entitler.ConnectionError` | no answer arrived, or it was cut off while it was read |
| `*entitler.TimeoutError` | an attempt ran past its timeout |
| `*entitler.TokenError` | a token source failed or answered an unusable token |
| `*entitler.SnapshotError` | a snapshot failed verification |
| `*entitler.UsageRefusedError` | `StartHold` or `WithHold` was refused |
| `*entitler.UsageReplayedError` | `StartHold` or `WithHold`'s key replays a finished hold |
| `*entitler.UsageSettlementError` | `Finish` could not settle a hold |

Match them with `errors.As`:

```go
var apiErr *entitler.APIError
if errors.As(err, &apiErr) && apiErr.Code == entitler.CodeScopeRequired {
	log.Printf("the key lacks a scope (request %s)", apiErr.RequestID)
}
```

Arguments the SDK refuses before any request wrap `entitler.ErrInvalidArgument`; a closed client
answers `entitler.ErrClosed`; your own cancellation answers `ctx.Err()`. None of these is an
`entitler.Error`.

`APIError` holds the `Status`, the `Code`, the `Message`, the `RequestID` (quote it when asking
about a request), `RetryAfter`, the `IdempotencyKey` the request sent, and on a `402` the
`Payment` with its status and URL.

API messages are written for the developer, not for end users: map codes to your own copy. `Code`
is an open set: every code the API documents is a constant (`entitler.CodeCustomerNotFound`…), and
a code this version does not know is kept as it is. The SDK adds `CodeHTTPError` (a body naming no
code, and every redirect), `CodeInvalidResponse`, and, on batch results, `CodeConnectionFailed` and
`CodeTimedOut`. No error ever holds a key, a token or a request or answer body.

The codes each billing call can answer are listed in [billing pages](billing.md).
