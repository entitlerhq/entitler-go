package entitler

import (
	"context"
	"errors"
	"iter"
	"sync"
	"time"
)

const (
	amountMessage  = "pass amount as a whole number from 1 to 9007199254740991"
	settledMessage = "pass amount as a whole number from 0 to the held amount"
	usedMessage    = "pass the amount used as a whole number of 0 or more"
)

// RecordUsageParams are the options of RecordUsage; nil for none.
type RecordUsageParams struct {
	// Mode is Gate (the default) or Observe.
	Mode UsageMode
	// OccurredAt is when the usage happened, so it counts in that period; the zero time for now.
	// The API refuses an instant more than the project's offline days back, more than 5 minutes
	// ahead, or before the customer registered (400 invalid_occurred_at).
	OccurredAt time.Time
	// Register registers a customer not registered yet, if the credential may register customers.
	Register bool
}

// HoldParams are the options of a hold; nil for none.
type HoldParams struct {
	// TTL is how long the hold lasts before it expires on its own, in whole seconds from 1 to
	// 3,600; 0 for 300 seconds.
	TTL time.Duration
}

// SettleUsageParams are the options of SettleUsage; nil for none.
type SettleUsageParams struct {
	// IdempotencyKey names this settlement; a new UUID when "".
	IdempotencyKey string
}

// ReleaseUsageParams are the options of ReleaseUsage; nil for none.
type ReleaseUsageParams struct {
	// IdempotencyKey names this release; a new UUID when "".
	IdempotencyKey string
}

func usageAmount(amount int64) error {
	if amount < 1 || amount > maxWhole {
		return argError(amountMessage)
	}
	return nil
}

func meteredKey(f MeteredFeature) (string, error) {
	if blank(f.key) {
		return "", argError("name the feature by its key")
	}
	return f.key, nil
}

// RecordUsage records usage of a metered feature: a whole number from 1 in the feature's unit,
// under your own idempotency key. Name the key after the event (this message, this export request,
// this webhook delivery), never an object whose state changes: the API keeps the key for good, so
// reusing it replays the first answer and records nothing. A refusal (Gate mode past the allowance)
// is an answer, OutcomeRefused, not an error, and stores no key, so the same key can be retried
// once there is allowance. Reports from customer and identity tokens count towards 100 every 10
// seconds per customer (429 rate_limited); counting on a device is advisory.
func (cu *customer) RecordUsage(ctx context.Context, f MeteredFeature, amount int64, idempotencyKey string, p *RecordUsageParams) (*UsageResult, error) {
	key, err := requiredKey(idempotencyKey, 200)
	if err != nil {
		return nil, err
	}
	feature, err := meteredKey(f)
	if err != nil {
		return nil, err
	}
	if err := usageAmount(amount); err != nil {
		return nil, err
	}
	body := map[string]any{"feature": feature, "amount": amount}
	if p != nil {
		if p.Mode != "" {
			body["mode"] = p.Mode
		}
		if !p.OccurredAt.IsZero() {
			at, err := checkInstant(p.OccurredAt, "pass occurredAt as a valid date")
			if err != nil {
				return nil, err
			}
			body["occurredAt"] = at
		}
		if p.Register {
			body["register"] = true
		}
	}
	return cu.usageWrite(ctx, "POST", "/usage", key, body)
}

func (cu *customer) usageWrite(ctx context.Context, method, suffix, key string, body any) (*UsageResult, error) {
	r, err := cu.write(method, suffix, key, body, true)
	if err != nil {
		return nil, err
	}
	result, m, err := fetch(ctx, cu.c, r, decodeUsageResult)
	if err != nil {
		return nil, err
	}
	result.Replayed = m.replayed
	cu.saw(result.Customer)
	return &result, nil
}

func holdBody(feature string, amount int64, p *HoldParams) (map[string]any, error) {
	body := map[string]any{"feature": feature, "amount": amount}
	if p != nil && p.TTL != 0 {
		seconds, err := wholeSeconds(p.TTL, 1, 3600, "pass the hold's TTL as a whole number of seconds from 1 to 3600")
		if err != nil {
			return nil, err
		}
		body["ttlSeconds"] = seconds
	}
	return body, nil
}

// HoldUsage holds an amount and answers the hold's UsageResult, for a hold settled from another
// process with SettleUsage. A hold counts against Remaining, and shows in Held, until it is
// settled, released or expires. Most apps use StartHold or WithHold instead.
func (cu *customer) HoldUsage(ctx context.Context, f MeteredFeature, amount int64, idempotencyKey string, p *HoldParams) (*UsageResult, error) {
	key, err := requiredKey(idempotencyKey, 200)
	if err != nil {
		return nil, err
	}
	return cu.hold(ctx, f, amount, key, p)
}

func (cu *customer) hold(ctx context.Context, f MeteredFeature, amount int64, key string, p *HoldParams) (*UsageResult, error) {
	feature, err := meteredKey(f)
	if err != nil {
		return nil, err
	}
	if err := usageAmount(amount); err != nil {
		return nil, err
	}
	body, err := holdBody(feature, amount, p)
	if err != nil {
		return nil, err
	}
	return cu.usageWrite(ctx, "POST", "/usage/holds", key, body)
}

// SettleUsage settles a hold with the amount really used, from 0 to the amount held. Settling the
// same amount again answers OutcomeDuplicate; a hold settled, released or expired answers 409
// hold_settled, hold_released or hold_expired.
func (cu *customer) SettleUsage(ctx context.Context, holdID string, amount int64, p *SettleUsageParams) (*UsageResult, error) {
	seg, err := segment(holdID, "provide the id of the hold")
	if err != nil {
		return nil, err
	}
	if amount < 0 || amount > maxWhole {
		return nil, argError(settledMessage)
	}
	var given string
	if p != nil {
		given = p.IdempotencyKey
	}
	key, err := optionalKey(given)
	if err != nil {
		return nil, err
	}
	return cu.usageWrite(ctx, "POST", "/usage/holds/"+seg+"/settle", key, map[string]any{"amount": amount})
}

// ReleaseUsage releases a hold, recording nothing. Releasing twice is safe.
func (cu *customer) ReleaseUsage(ctx context.Context, holdID string, p *ReleaseUsageParams) (*UsageResult, error) {
	seg, err := segment(holdID, "provide the id of the hold")
	if err != nil {
		return nil, err
	}
	var given string
	if p != nil {
		given = p.IdempotencyKey
	}
	key, err := optionalKey(given)
	if err != nil {
		return nil, err
	}
	return cu.usageWrite(ctx, "DELETE", "/usage/holds/"+seg, key, nil)
}

// StartHold holds amount before any work starts and answers the open [Hold], so a handler can
// answer a refusal before it streams. It returns *UsageRefusedError when there is no allowance,
// and *UsageReplayedError when the key replays a hold already settled, released or expired. A hold
// whose key another caller holds answers with Duplicate true. The idempotency key is 1 to 193
// characters, leaving room for ":excess". Defer the hold's Close right after:
//
//	hold, err := customer.StartHold(ctx, features.AICredits, 2000, messageID, nil)
//	if err != nil {
//		return err
//	}
//	defer hold.Close(ctx)
func (cu *customer) StartHold(ctx context.Context, f MeteredFeature, amount int64, idempotencyKey string, p *HoldParams) (*Hold, error) {
	key, err := requiredKey(idempotencyKey, 193)
	if err != nil {
		return nil, err
	}
	result, err := cu.hold(ctx, f, amount, key, p)
	if err != nil {
		return nil, err
	}
	if result.Outcome == OutcomeRefused {
		return nil, &UsageRefusedError{Result: result}
	}
	open := result.HoldID != nil && !result.ExpiresAt.IsZero() &&
		(result.Outcome == OutcomeHeld || result.Outcome == OutcomeDuplicate && result.ExpiresAt.After(result.AsOf))
	if !open {
		return nil, &UsageReplayedError{Result: result}
	}
	return &Hold{
		ID:        *result.HoldID,
		Amount:    result.Amount,
		ExpiresAt: result.ExpiresAt,
		Result:    result,
		Duplicate: result.Outcome == OutcomeDuplicate,
		cu:        cu,
		feature:   f,
		key:       key,
	}, nil
}

// Hold is an open hold from StartHold. Report the amount used with Use, then call Finish however
// the work ended, or Release for work that never ran; defer Close right after StartHold so a hold
// is never left open. Only the first of Finish, Release and Close acts; a later call answers the
// first one's result without a request. Each runs outside the caller's cancellation, bounded by
// the client's timeout, so work cancelled halfway still settles or releases. Remaining drops by
// the whole hold while work runs and rises when it settles. Methods are safe for concurrent use.
type Hold struct {
	// ID is the hold's id.
	ID string
	// Amount is the amount held.
	Amount int64
	// ExpiresAt is when the hold expires unless settled or released.
	ExpiresAt time.Time
	// Result is the hold's answer, with the meter.
	Result *UsageResult
	// Duplicate is true when another caller holds the same key and may be doing the same work, so
	// the app can wait for that caller's result instead of paying for the work twice.
	Duplicate bool

	cu      *customer
	feature MeteredFeature
	key     string

	mu       sync.Mutex
	used     int64
	reported bool
	once     sync.Once
	result   *UsageResult
	err      error
}

// Use reports the total the work really used so far, with no request; a later call replaces an
// earlier one. Work that reports as it goes calls Use(0) before it starts, so finishing after a
// failure that used nothing frees the whole hold.
func (h *Hold) Use(n int64) error {
	if n < 0 || n > maxWhole {
		return argError(usedMessage)
	}
	h.mu.Lock()
	h.used, h.reported = n, true
	h.mu.Unlock()
	return nil
}

func (h *Hold) usage() (int64, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.used, h.reported
}

// Finish ends the work, whether it succeeded, failed or was cut off, and charges for what happened:
// it settles the amount reported with Use (the held amount when none was), up to the held amount,
// and records any excess in observe mode under the hold's key plus ":excess". When the hold expired
// first, the whole amount is recorded under that key. It answers the settlement's UsageResult, or
// the excess report's when the hold had expired, and *UsageSettlementError when that fails.
func (h *Hold) Finish(ctx context.Context) (*UsageResult, error) {
	h.once.Do(func() { h.result, h.err = h.settle(ctx) })
	return h.result, h.err
}

// Release frees the hold, recording nothing, for work that never ran.
func (h *Hold) Release(ctx context.Context) (*UsageResult, error) {
	h.once.Do(func() { h.result, h.err = h.release(ctx) })
	return h.result, h.err
}

// Close disposes of the hold when neither Finish nor Release ran: it settles the amount reported
// with Use, else releases. It returns nothing, so it suits defer; a failure goes to onError, and an
// unreleased hold expires on its own.
func (h *Hold) Close(ctx context.Context) {
	h.once.Do(func() {
		if _, reported := h.usage(); reported {
			h.result, h.err = h.settle(ctx)
		} else {
			h.result, h.err = h.release(ctx)
		}
		h.cu.c.e.report(h.err)
	})
}

// detached runs a hold's last request outside the caller's cancellation, bounded by the client's timeout.
func (h *Hold) detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), clampDuration(h.cu.c.timeout))
}

func (h *Hold) release(ctx context.Context) (*UsageResult, error) {
	ctx, cancel := h.detached(ctx)
	defer cancel()
	return h.cu.ReleaseUsage(ctx, h.ID, nil)
}

func (h *Hold) settle(ctx context.Context) (*UsageResult, error) {
	total, reported := h.usage()
	if !reported {
		total = h.Amount
	}
	settled := min(total, h.Amount)
	excess := total - settled
	observe := func(amount int64) (*UsageResult, error) {
		ctx, cancel := h.detached(ctx)
		defer cancel()
		return h.cu.RecordUsage(ctx, h.feature, amount, h.key+":excess", &RecordUsageParams{Mode: Observe})
	}
	detached, cancel := h.detached(ctx)
	result, err := h.cu.SettleUsage(detached, h.ID, settled, nil)
	cancel()
	if err != nil {
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Code != CodeHoldExpired || total == 0 {
			return nil, &UsageSettlementError{HoldID: h.ID, Amount: settled, Excess: excess, cause: err}
		}
		recorded, err := observe(total)
		if err != nil {
			return nil, &UsageSettlementError{HoldID: h.ID, Amount: settled, Excess: total, cause: err}
		}
		return recorded, nil
	}
	if excess > 0 {
		if _, err := observe(excess); err != nil {
			return nil, &UsageSettlementError{HoldID: h.ID, Amount: settled, Excess: excess, cause: err}
		}
	}
	return result, nil
}

// WithHold starts a hold, runs work, and finishes the hold, answering work's value. work calls
// h.Use with the amount it used. When work fails, or ctx ends while it runs, WithHold disposes of
// the hold (settling the amount reported with Use, else releasing) and returns work's error as it
// is, so a disconnect after 3,000 tokens charges for 3,000 tokens. A failed settlement returns
// *UsageSettlementError, with work's value both returned and in its Result.
//
// WithHold keeps the accounting exactly once, not work: work reads h.Duplicate to learn that
// another caller holds the same key and may be doing the same work; apps that need work itself
// to run once coordinate it themselves.
func WithHold[T any](ctx context.Context, c Customer, f MeteredFeature, amount int64, idempotencyKey string,
	work func(ctx context.Context, h *Hold) (T, error), p *HoldParams) (T, error) {
	var zero T
	if c == nil || work == nil {
		return zero, argError("pass a customer and work")
	}
	h, err := c.StartHold(ctx, f, amount, idempotencyKey, p)
	if err != nil {
		return zero, err
	}
	finished := false
	defer func() {
		if !finished {
			h.Close(ctx)
		}
	}()
	value, err := work(ctx, h)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return value, err
	}
	finished = true
	if _, err := h.Finish(ctx); err != nil {
		var settlement *UsageSettlementError
		if errors.As(err, &settlement) {
			withValue := *settlement
			withValue.Result = value
			return value, &withValue
		}
		return value, err
	}
	return value, nil
}

// UsageLog is a page of the usage log. All iterates every entry from this page on.
type UsageLog struct {
	// Items are this page's entries.
	Items []UsageEvent
	// Next is the cursor of the next page, or "" on the last.
	Next string

	more func(ctx context.Context, cursor string) (UsageLog, error)
}

// All iterates every entry from this page on, fetching each later page when the loop reaches it.
// A failed page ends the loop with its error.
func (l UsageLog) All(ctx context.Context) iter.Seq2[UsageEvent, error] {
	return func(yield func(UsageEvent, error) bool) {
		page := l
		for {
			for _, item := range page.Items {
				if !yield(item, nil) {
					return
				}
			}
			if page.Next == "" || page.more == nil {
				return
			}
			next, err := page.more(ctx, page.Next)
			if err != nil {
				yield(UsageEvent{}, err)
				return
			}
			next.more = page.more
			page = next
		}
	}
}
