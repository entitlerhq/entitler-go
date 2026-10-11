# Recording usage

Usage is recorded on metered features only, in whole numbers of the feature's unit from 1 to
2^53 − 1. Usage methods take an `entitler.MeteredFeature`: a generated constant, or
`entitler.Metered("ai_credits")` by hand.

## Idempotency keys are required

Every report and hold carries your own idempotency key: a key from your unit of work, such as a
message, job or request id. The SDK never makes one up for you, because a generated key protects
only its own retries; a retry from the browser, another process or a restart would charge twice.

The API keeps a key for good, so a key names an event (this message, this export request, this
webhook delivery), never an object whose state changes (a document, a deal). A key reused after
the object changed replays the first answer and records nothing, and a key reused for a different
request answers `422 idempotency_mismatch`.

```go
result, err := customer.RecordUsage(ctx, features.FeatureAiCredits, 25, requestID, nil)
if err != nil {
	return err
}
if result.Outcome == entitler.OutcomeRefused {
	return errOutOfCredits
}
if result.Replayed {
	log.Printf("request %s was recorded before", requestID)
}
```

## Modes

- `entitler.Gate` (the default) records only if the amount fits the allowance; otherwise it
  records nothing and answers `OutcomeRefused` with a `Refusal`. A refusal is an answer, not an
  error, and stores no key, so the same key can be retried once there is allowance. Use it before
  work that must not start without allowance.
- `entitler.Observe` always records the amount used, and `OverBy` says how far past the allowance
  the meter went. Use it for work that already happened: streamed tokens, bandwidth, minutes.

```go
_, err := customer.RecordUsage(ctx, features.FeatureAiCredits, tokens, streamID,
	&entitler.RecordUsageParams{Mode: entitler.Observe, OccurredAt: finishedAt})
```

`OccurredAt` counts the usage in the period it happened; the API refuses an instant more than the
project's offline days back, more than 5 minutes ahead, or before the customer registered.

## Holds for work whose cost is known afterwards

A hold reserves an amount before work starts. It counts against `Remaining`, and shows in
`Meter.Held`, until it is settled, released or expires (after 300 seconds unless `HoldParams.TTL`
says otherwise). `Remaining` drops by the whole hold while work runs and rises when it settles, so
a live meter shows `Held` apart.

`StartHold` answers the open hold before any work starts. It answers `*entitler.UsageRefusedError`
when there is no allowance, so a handler can answer `402` with an upgrade prompt before it streams,
and `*entitler.UsageReplayedError` when the key belongs to work that already finished, so the
handler answers that work's result rather than running it again. Never let either surface as a
`500`. Report the total used with `Use` as you go, and defer `Close` right after: however the
handler ends, the hold settles what was reported, or is released when nothing was.

```go
hold, err := customer.StartHold(ctx, aiCredits, 50, messageID, nil)
var refused *entitler.UsageRefusedError
var replayed *entitler.UsageReplayedError
switch {
case errors.As(err, &refused):
	return fmt.Errorf("%w: upgrade to %d plan(s)", errOutOfCredits, len(refused.Result.Upgrades))
case errors.As(err, &replayed):
	fmt.Printf("Message %s was answered before (%s).\n", messageID, replayed.Result.Outcome)
	return nil
case err != nil:
	return err
}
defer hold.Close(ctx)
if err := hold.Use(0); err != nil {
	return err
}
var used int64
for _, tokens := range chunks {
	used += tokens
	if err := hold.Use(used); err != nil {
		return err
	}
}
_, err = hold.Finish(ctx)
return err
```

- `Finish` settles the amount reported (the held amount when none was), up to the held amount, and
  records any excess in observe mode under the hold's key plus `:excess`, so the key is at most 193
  characters. When the hold expired first, the whole amount is recorded under that key, since the
  work happened.
- `Release` frees the hold, recording nothing, for work that never ran.
- Only the first of `Finish`, `Release` and `Close` acts. Each runs outside the caller's
  cancellation, bounded by the client's timeout, so work cut off halfway still settles.
- When `Finish` cannot settle after its retries, it answers `*entitler.UsageSettlementError` with
  the hold id, the amount to settle and the excess still to record: call `SettleUsage` before the
  hold expires, and record the excess under the same `:excess` key. `Close` passes that error to
  `onError` instead.
- A key that replays a hold already settled, released or expired answers
  `*entitler.UsageReplayedError`: the work it stands for already happened, so show its result,
  never an upgrade prompt. `Hold.Duplicate` is true when another caller holds the same key and may
  be doing the same work.

`WithHold` wraps work that runs inside one call, and answers the same two errors:

```go
s, err := entitler.WithHold(ctx, customer, aiCredits, 20, jobID,
	func(ctx context.Context, hold *entitler.Hold) (summary, error) {
		s := summary{text: "A short summary.", tokens: 7}
		return s, hold.Use(s.tokens)
	}, nil)
var refused *entitler.UsageRefusedError
var replayed *entitler.UsageReplayedError
switch {
case errors.As(err, &refused):
	return summary{}, errOutOfCredits
case errors.As(err, &replayed):
	return summaries[jobID], nil
case err != nil:
	return summary{}, err
}
summaries[jobID] = s
return s, nil
```

When `work` fails, or `ctx` ends while it runs, `WithHold` settles what `work` reported with `Use`
(else releases) and answers `work`'s error as it is, so a disconnect after 3,000 tokens charges
for 3,000 tokens. `WithHold` keeps the accounting exactly once, not `work`: apps that need the work
itself to run once read `hold.Duplicate` and coordinate it themselves.

`HoldUsage`, `SettleUsage` and `ReleaseUsage` are the raw calls, for a hold settled from another
process.

## Batches from a server

`srv.RecordUsageBatch` records events for many customers in observe mode, 500 to a request. It
answers one result per event, in input order, and fails only for a done context: an event the SDK
refuses (a blank customer, a bad amount or key) is answered `OutcomeError` without being sent, and
every event of a request that fails after its retries is answered `OutcomeError` with that
failure's code. Resend the events answered `OutcomeError`; their keys make any resend safe.

```go
result, err := srv.RecordUsageBatch(ctx, []entitler.BatchEvent{
	{Customer: "user_42", Feature: features.FeatureAiCredits, Amount: 120, IdempotencyKey: "invoice-77-line-1"},
	{Customer: "user_43", Feature: features.FeatureAiCredits, Amount: 80, IdempotencyKey: "invoice-77-line-2"},
}, nil)
if err != nil {
	return err
}
for i, r := range result.Results {
	if r.Outcome == entitler.OutcomeError {
		log.Printf("event %d: %s (%s)", i, r.Error.Message, r.Error.Code)
	}
}
```

## Reports from apps

Reports and holds sent with customer and identity tokens count towards 100 every 10 seconds per
customer, shared by all of that customer's devices (`429 rate_limited`, retried as any 429);
settlements and releases do not count. Counting on a device is advisory: a modified app can skip
it, so only work done on your server can be enforced.
