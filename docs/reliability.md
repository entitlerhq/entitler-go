# Reliability

## Timeouts and cancellation

Every method takes a `context.Context` first and blocks; add goroutines where you need
concurrency. Each attempt has its own deadline, the client's timeout (10 seconds by default),
covering the whole attempt from connecting to the last byte of the answer. For one call's
deadline, derive a client that shares the transport, cache and tokens:

```go
fast := srv.With(entitler.WithTimeout(2 * time.Second))
check, err := fast.Customer(userID).Check(ctx, features.FeatureExportPdf, nil)
```

A timeout must be above 0; any other is refused before a token source runs or anything is sent.
Your own context ending answers `ctx.Err()`, never an `entitler.Error`; only an attempt's own
deadline is a `*entitler.TimeoutError`. Cancelling the context ends retry waits at once.

## Retries

Connection failures, timeouts, answers cut off while they are read, and `408`, `429`, `500`,
`502`, `503` and `504` are retried, at most `WithMaxRetries` times (2 by default), for every
request: reads are safe, and every write carries an idempotency key the API honours. A
`Retry-After` is waited exactly; one asking for longer than `WithMaxRetryDelay` (10 seconds by
default) fails at once, and the error's `RetryAfter` tells you when to try again. Otherwise the
wait before retry n is random between 0 and min(8, 0.5 × 2^n) seconds. Retries are invisible
except through time: the answer or error is the last attempt's.

Every write sends an `Idempotency-Key`: yours where you pass one, else a new UUID for that call,
the same on every attempt. A failed call's error carries the key it sent, so you can repeat the
call later with it. `Replayed` on the answer says the API had answered that key before, so this
call changed nothing.

## The answer cache

Checks, entitlement lists, customer plans and pricing, the server's pricing and feature list, and
the signed-out pricing go through an answer cache: an in-memory store of 1,000 answers, least
recently used first out, safe for concurrent use. A fresh answer (younger than its `max-age`) is
answered without a request; an older one is revalidated with its `ETag`, and a `304` answers the
kept body. Answers holding a meter are revalidated every time and still kept for stale answers.
Writes through the client revalidate that customer's kept answers.

- `WithMemoryCache(n)` sets the store's size, and `WithoutCache()` turns caching off.
- `WithCache(store)` gives the server client a custom store, such as Redis, so servers share
  answers. Implement `entitler.Cache`; a failing `Get` counts as a miss and a failing `Set` is
  skipped, both reported to `onError`. `Set` receives a time to live, `staleFor` plus the answer's
  `max-age`: expire entries after it. Entries are private to this SDK.
- `net/http` keeps no HTTP cache, so the SDK's cache is the only one. A client given with
  `WithHTTPClient` must not use a caching `Transport`, or revalidation after a write could miss
  Entitler.

## When Entitler is unreachable

When a read through the cache fails because Entitler is unreachable (a connection failure, a
timeout, `429`, a `5xx`, or an answer that does not decode, usually a proxy or captive portal) and
the cache holds an answer received less than `WithStaleFor` ago (24 hours by default), the read
answers it with `Stale` true and passes the error to `onError`. For the next 30 seconds (or the
answer's `Retry-After`, if longer, never longer than `staleFor`) reads with a kept answer answer
it without a request, then one read tests the API; a success ends the window. So an outage costs
one slow read, not one per call. A kept answer never stands in for another credential.

`entitler.IsUnreachable(err)` answers whether an error means Entitler could not be reached, for
apps that fall back to an [offline snapshot](offline-snapshots.md).

## onError

`WithOnError` receives each error a fallback absorbed: a stale answer, an `IsEntitled` default, a
hold's failed release or disposal, and a custom store's own failures. It never changes a call's
answer; a panic inside it is recovered and ignored.

```go
srv, err := entitler.NewServer(key, entitler.WithOnError(func(err error) {
	slog.Warn("entitler fallback", "error", err)
}))
```
