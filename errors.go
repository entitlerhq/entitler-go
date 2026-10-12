package entitler

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidArgument is wrapped by every error an argument causes before any request: a blank
// credential, id or feature, an out-of-range amount, an invalid idempotency key, timeout or
// instant. Match it with errors.Is. These errors are never an [Error].
var ErrInvalidArgument = errors.New("invalid argument")

// ErrClosed is the error every call of a closed client returns, and the calls in flight end with
// when it closes, as net.ErrClosed is for a closed connection. Match it with errors.Is.
var ErrClosed = errors.New("this Entitler client is closed. Create a new one")

type argumentError struct{ message string }

func (e *argumentError) Error() string { return e.message }

func (e *argumentError) Unwrap() error { return ErrInvalidArgument }

func argError(message string) error { return &argumentError{message: message} }

// Code is an error code: the API's error.code, or one of the SDK's own (CodeHTTPError,
// CodeInvalidResponse, CodeConnectionFailed and CodeTimedOut). It is an open set: a code this
// version does not know is kept as it is.
type Code string

// Error is implemented by every failure the SDK raises from Entitler: [*APIError],
// [*ConnectionError], [*TimeoutError], [*TokenError], [*SnapshotError], [*UsageRefusedError],
// [*UsageReplayedError] and [*UsageSettlementError]. Argument errors ([ErrInvalidArgument]), the
// closed client ([ErrClosed]) and the caller's own cancellation are not. Match a kind with
// errors.As or errors.AsType. No error holds a key, a token or a request or answer body.
type Error interface {
	error
	entitlerError()
}

// PaymentStatus is the state of a payment an [APIError] with status 402 describes. It is an open set.
type PaymentStatus string

// The payment states the API names.
const (
	// PaymentDeclined means the provider declined the payment.
	PaymentDeclined PaymentStatus = "declined"
	// PaymentRequiresAction means the customer must confirm the payment at the URL.
	PaymentRequiresAction PaymentStatus = "requires_action"
	// PaymentProcessing means the provider is still processing the payment.
	PaymentProcessing PaymentStatus = "processing"
	// PaymentPending means the payment waits on the customer, such as an invoice to pay.
	PaymentPending PaymentStatus = "pending"
)

// Payment describes the payment behind a 402 answer.
type Payment struct {
	// Status is the payment's state.
	Status PaymentStatus
	// URL is where the customer confirms or pays, or "" when the answer gives none.
	URL string
}

// APIError is an answer with a status other than 2xx (and 304 where a kept answer was
// revalidated), any redirect, or a 2xx answer the SDK cannot decode, whose Code is
// CodeInvalidResponse. API messages are written for the developer, not for end users.
type APIError struct {
	// Status is the HTTP status.
	Status int
	// Code is the API's error.code, or CodeHTTPError when the body names none and for every redirect.
	Code Code
	// Message is the API's error.message, or the SDK's own when the body gives none.
	Message string
	// RequestID is the x-request-id header, or "" when the answer has none. Quote it when asking about a request.
	RequestID string
	// RetryAfter is how long the answer asked the caller to wait (its Retry-After), or 0 when it
	// gave no readable value.
	RetryAfter time.Duration
	// IdempotencyKey is the key the request sent, so the call can be repeated with it; "" for a GET.
	IdempotencyKey string
	// Payment is the payment a 402 answer describes, or nil.
	Payment *Payment

	retryAfter *time.Duration
	cause      error
}

// Error answers the message: the API's own, or the SDK's when the body gives none.
func (e *APIError) Error() string { return e.Message }

// Unwrap answers the decoding failure behind CodeInvalidResponse, which names what failed and
// where but never holds the body, or nil.
func (e *APIError) Unwrap() error { return e.cause }

func (*APIError) entitlerError() {}

// ConnectionError means no answer arrived (DNS, TLS, a refused or reset connection) or the
// answer was cut off while it was read.
type ConnectionError struct {
	// IdempotencyKey is the key the request sent, or "" for a GET.
	IdempotencyKey string

	cause error
}

// Error says Entitler could not be reached, and why.
func (e *ConnectionError) Error() string {
	return "could not reach Entitler: " + e.cause.Error()
}

// Unwrap answers the transport's error.
func (e *ConnectionError) Unwrap() error { return e.cause }

func (*ConnectionError) entitlerError() {}

// TimeoutError means an attempt ran past its timeout. The caller's own context ending is never a
// TimeoutError: the call answers ctx.Err() instead.
type TimeoutError struct {
	// IdempotencyKey is the key the request sent, or "" for a GET.
	IdempotencyKey string

	timeout time.Duration
}

// Error says Entitler did not answer within the attempt's timeout.
func (e *TimeoutError) Error() string {
	return fmt.Sprintf("Entitler did not answer within %s", e.timeout)
}

// Timeout reports true, as net.Error's Timeout does.
func (*TimeoutError) Timeout() bool { return true }

func (*TimeoutError) entitlerError() {}

// TokenError means a token source failed, timed out, or answered a blank or unreadable token. Its
// cause is the source's own error, kept as it is; what that error holds is outside the SDK's
// control. It never holds the token.
type TokenError struct {
	message string
	cause   error
}

// Error says why the credential could not be used, with its cause when there is one.
func (e *TokenError) Error() string {
	if e.cause != nil {
		return e.message + ": " + e.cause.Error()
	}
	return e.message
}

// Unwrap answers the token source's error, or nil.
func (e *TokenError) Unwrap() error { return e.cause }

func (*TokenError) entitlerError() {}

// SnapshotError means an offline snapshot failed verification. Code is CodeSnapshotInvalid or
// CodeSnapshotExpired.
type SnapshotError struct {
	// Code is CodeSnapshotInvalid or CodeSnapshotExpired.
	Code Code
	// Message says which check failed.
	Message string
}

// The codes of a [SnapshotError].
const (
	// CodeSnapshotInvalid means the snapshot is malformed, unsigned by the keys given, or for
	// another issuer, customer or environment.
	CodeSnapshotInvalid Code = "snapshot_invalid"
	// CodeSnapshotExpired means the snapshot's expiry has passed.
	CodeSnapshotExpired Code = "snapshot_expired"
)

// Error answers the message, which says why the snapshot was refused.
func (e *SnapshotError) Error() string { return e.Message }

func (*SnapshotError) entitlerError() {}

// UsageRefusedError is returned only by StartHold and WithHold when the hold is refused: the
// customer has no allowance for it. Result holds the answer, with its Refusal and Upgrades.
type UsageRefusedError struct {
	// Result is the hold's answer.
	Result *UsageResult
}

// Error names the feature and the refusal.
func (e *UsageRefusedError) Error() string {
	return fmt.Sprintf("Entitler refused the hold on %s (%s)", e.Result.Feature, refusalText(e.Result.Refusal))
}

func (*UsageRefusedError) entitlerError() {}

func refusalText(r Refusal) string {
	if r == "" {
		return "refused"
	}
	return string(r)
}

// UsageReplayedError is returned only by StartHold and WithHold when the idempotency key replays a
// hold already settled, released or expired: the work the key stands for already happened, so show
// that work's result, never an upgrade prompt. Result holds the answer.
type UsageReplayedError struct {
	// Result is the replayed hold's answer.
	Result *UsageResult
}

// Error says the key replays a hold that was settled, released or expired.
func (e *UsageReplayedError) Error() string {
	return fmt.Sprintf("the idempotency key replays a hold that is %s", e.Result.Outcome)
}

func (*UsageReplayedError) entitlerError() {}

// UsageSettlementError is returned by [Hold.Finish], and so by [WithHold] after its work succeeded,
// when settling the hold or recording the excess fails after its retries. Keep the work's output,
// call SettleUsage(ctx, HoldID, Amount, nil) before the hold expires, and record Excess in observe
// mode under the hold's key plus ":excess". A disposal passes this error to onError instead.
type UsageSettlementError struct {
	// HoldID is the hold's id.
	HoldID string
	// Amount is the amount to settle.
	Amount int64
	// Excess is the amount still to record beyond the hold, or 0 for none.
	Excess int64
	// Result is the value WithHold's work returned, or nil for Hold.Finish.
	Result any

	cause error
}

// Error names the hold and the failure.
func (e *UsageSettlementError) Error() string {
	return fmt.Sprintf("could not settle hold %s: %v", e.HoldID, e.cause)
}

// Unwrap answers the error the settlement or the excess report failed with.
func (e *UsageSettlementError) Unwrap() error { return e.cause }

func (*UsageSettlementError) entitlerError() {}

// IsUnreachable reports whether err means Entitler could not be reached: a [*ConnectionError], a
// [*TimeoutError], a [*TokenError] (a token source failing, offline typically), or an [*APIError]
// with status 429, a 5xx status or CodeInvalidResponse. An offline-capable app falls back to its
// snapshot when it is true.
func IsUnreachable(err error) bool {
	var e Error
	if !errors.As(err, &e) {
		return false
	}
	switch e := e.(type) {
	case *APIError:
		return e.Status == 429 || e.Status >= 500 && e.Status <= 599 || e.Code == CodeInvalidResponse
	case *ConnectionError, *TimeoutError, *TokenError:
		return true
	}
	return false
}
