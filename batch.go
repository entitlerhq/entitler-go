package entitler

import (
	"context"
	"errors"
	"time"
)

const batchSize = 500

// BatchEvent is one usage report in a batch, always recorded in observe mode.
type BatchEvent struct {
	// Customer is your app's id for the customer.
	Customer string
	// Feature is the metered feature.
	Feature MeteredFeature
	// Amount is a whole number from 1 in the feature's unit.
	Amount int64
	// OccurredAt is when the usage happened; the zero time for now.
	OccurredAt time.Time
	// IdempotencyKey is required: a key from your own unit of work, such as a message or job id.
	IdempotencyKey string
}

// RecordUsageBatchParams are the options of RecordUsageBatch; nil for none.
type RecordUsageBatchParams struct {
	// Register registers customers not registered yet.
	Register bool
}

// BatchError says why a batch event was not recorded.
type BatchError struct {
	// Code is the API's code, invalid_body, invalid_amount or invalid_idempotency_key for an event
	// the SDK refused itself, or connection_failed or timed_out when no answer arrived.
	Code Code
	// Message says what went wrong, for the developer.
	Message string
}

// BatchEventResult is one event's result, in input order.
type BatchEventResult struct {
	// Outcome is OutcomeRecorded, OutcomeDuplicate or OutcomeError.
	Outcome Outcome
	// ID is the usage report's id, or nil.
	ID *string
	// Late is true when the usage counted in an earlier period.
	Late bool
	// Error says why the event was not recorded, for OutcomeError.
	Error *BatchError
	// IdempotencyKey is the event's key.
	IdempotencyKey string
	// Replayed is true when the API answered the event's request before.
	Replayed bool
}

// BatchResult is the answer to RecordUsageBatch.
type BatchResult struct {
	// Results holds one result per event, in input order.
	Results []BatchEventResult
	// Recorded counts the events recorded.
	Recorded int64
	// Duplicates counts the events whose key was seen before.
	Duplicates int64
	// Errors counts the events not recorded.
	Errors int64
}

type sentEvent struct {
	index int
	body  map[string]any
	key   []any
}

// RecordUsageBatch records usage for many customers in observe mode, in requests of at most 500
// events. It returns an error only for a done context, a closed client or a client derived with an
// invalid option: an event the SDK refuses itself is not sent and answers OutcomeError with a code
// and message, as does every event of a request that fails after its retries, while the call
// carries on. Resend the events answered OutcomeError: their keys make any resend safe. Each
// request's Idempotency-Key derives from its events, so the same events resent replay the first
// answer.
func (s *Server) RecordUsageBatch(ctx context.Context, events []BatchEvent, p *RecordUsageBatchParams) (*BatchResult, error) {
	register := p != nil && p.Register
	out := &BatchResult{Results: make([]BatchEventResult, len(events))}
	var sent []sentEvent
	for i, ev := range events {
		out.Results[i].IdempotencyKey = ev.IdempotencyKey
		code, err := checkBatchEvent(ev)
		if err != nil {
			out.Results[i].Outcome = OutcomeError
			out.Results[i].Error = &BatchError{Code: code, Message: err.Error()}
			continue
		}
		body := map[string]any{"customer": ev.Customer, "feature": ev.Feature.key, "amount": ev.Amount,
			"idempotencyKey": ev.IdempotencyKey}
		var at any
		if !ev.OccurredAt.IsZero() {
			body["occurredAt"] = formatInstant(ev.OccurredAt)
			at = body["occurredAt"]
		}
		sent = append(sent, sentEvent{index: i, body: body,
			key: []any{ev.Customer, ev.Feature.key, ev.Amount, at, ev.IdempotencyKey}})
	}
	for start := 0; start < len(sent); start += batchSize {
		chunk := sent[start:min(start+batchSize, len(sent))]
		if err := s.sendBatch(ctx, chunk, register, out); err != nil {
			return nil, err
		}
	}
	for _, r := range out.Results {
		switch r.Outcome {
		case OutcomeRecorded:
			out.Recorded++
		case OutcomeDuplicate:
			out.Duplicates++
		case OutcomeError:
			out.Errors++
		}
	}
	return out, nil
}

func checkBatchEvent(ev BatchEvent) (Code, error) {
	if _, err := segment(ev.Customer, "provide the id your app uses for the customer"); err != nil {
		return CodeInvalidBody, err
	}
	if blank(ev.Feature.key) {
		return CodeInvalidBody, argError("name the feature by its key")
	}
	if err := usageAmount(ev.Amount); err != nil {
		return CodeInvalidAmount, err
	}
	if _, err := requiredKey(ev.IdempotencyKey, 200); err != nil {
		return CodeInvalidIdempotencyKey, err
	}
	if y := ev.OccurredAt.UTC().Year(); !ev.OccurredAt.IsZero() && (y < 1 || y > 9999) {
		return CodeInvalidBody, argError("pass occurredAt as a valid date")
	}
	return "", nil
}

// batchKey derives a request's Idempotency-Key from its events.
func batchKey(register bool, chunk []sentEvent) string {
	rows := make([]any, len(chunk))
	for i, ev := range chunk {
		rows[i] = ev.key
	}
	return "batch:" + sha256Hex(serialise([]any{"entitler-batch-v1", register, rows}))
}

type batchAnswer struct {
	results []batchItem
}

type batchItem struct {
	index   int64
	outcome Outcome
	id      *string
	late    bool
	err     *BatchError
}

func decodeBatchAnswer(o object) batchAnswer {
	a := batchAnswer{results: objects(o.at("results"), func(o object) batchItem {
		item := batchItem{
			index:   o.at("index").int(),
			outcome: Outcome(o.at("outcome").str()),
			id:      o.at("id").strOrNull(),
			late:    o.at("late").boolean(),
			err: objectOrNull(o.at("error"), func(o object) BatchError {
				return BatchError{Code: Code(o.at("code").str()), Message: o.at("message").str()}
			}),
		}
		return item
	})}
	o.at("recorded").int()
	o.at("duplicates").int()
	o.at("errors").int()
	return a
}

func (s *Server) sendBatch(ctx context.Context, chunk []sentEvent, register bool, out *BatchResult) error {
	bodies := make([]any, len(chunk))
	customers := make([]string, len(chunk))
	for i, ev := range chunk {
		bodies[i] = ev.body
		customers[i] = ev.body["customer"].(string)
	}
	body := map[string]any{"events": bodies}
	if register {
		body["register"] = true
	}
	key := batchKey(register, chunk)
	r := &request{method: "POST", path: "/usage/events", key: key, body: marshalBody(body), writes: customers}
	answer, m, err := fetch(ctx, s.c, r, func(o object) batchAnswer {
		a := decodeBatchAnswer(o)
		if !coversEach(a, len(chunk)) {
			o.r.fail("results", "expected one result for each event")
		}
		return a
	})
	if err != nil {
		failure, ok := batchFailure(err)
		if !ok || ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return err
		}
		for _, ev := range chunk {
			out.Results[ev.index].Outcome = OutcomeError
			out.Results[ev.index].Error = &failure
		}
		return nil
	}
	for _, item := range answer.results {
		res := &out.Results[chunk[item.index].index]
		res.Outcome, res.ID, res.Late, res.Error, res.Replayed = item.outcome, item.id, item.late, item.err, m.replayed
	}
	return nil
}

// coversEach reports whether the answer holds exactly one result for each event sent.
func coversEach(a batchAnswer, sent int) bool {
	seen := make([]bool, sent)
	for _, item := range a.results {
		if item.index < 0 || item.index >= int64(sent) || seen[item.index] {
			return false
		}
		seen[item.index] = true
	}
	return len(a.results) == sent
}

// batchFailure answers the result every event of a failed request carries, and false for a
// failure that is no request's answer (an argument error, a closed client), which the call returns.
func batchFailure(err error) (BatchError, bool) {
	var apiErr *APIError
	var timeout *TimeoutError
	var conn *ConnectionError
	switch {
	case errors.As(err, &apiErr):
		return BatchError{Code: apiErr.Code, Message: apiErr.Message}, true
	case errors.As(err, &timeout):
		return BatchError{Code: CodeTimedOut, Message: err.Error()}, true
	case errors.As(err, &conn):
		return BatchError{Code: CodeConnectionFailed, Message: err.Error()}, true
	}
	return BatchError{}, false
}
