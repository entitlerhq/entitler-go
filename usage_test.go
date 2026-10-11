package entitler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// holdAPI answers a hold of 10, then settlements, releases and excess reports as told.
type holdAPI struct {
	holdOutcome string
	expiresIn   time.Duration
	settle      fakeAnswer
	record      fakeAnswer
	release     fakeAnswer
}

func usageBody(outcome string, holdID any, expires time.Time, amount int64) string {
	body := map[string]any{}
	_ = json.Unmarshal([]byte(checkBody("u", "ai")), &body)
	body["type"], body["value"], body["used"], body["held"], body["remaining"], body["resetsAt"] = "metered", 100, 0, 10, 90, nil
	body["outcome"], body["refusal"], body["id"], body["holdId"] = outcome, nil, "use_1", holdID
	body["mode"], body["amount"], body["meterChange"], body["overBy"], body["late"] = "gate", amount, 0, 0, false
	body["occurredAt"], body["expiresAt"], body["reportedAs"] = nil, nil, "api"
	if outcome == "refused" {
		body["refusal"] = "over_allowance"
	}
	if !expires.IsZero() {
		body["expiresAt"] = formatInstant(expires)
	}
	return string(marshalBody(body))
}

func (h holdAPI) fake() *fakeHTTP {
	asOf := time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC)
	return &fakeHTTP{handle: func(r *http.Request, body string) fakeAnswer {
		switch {
		case strings.HasSuffix(r.URL.Path, "/usage/holds"):
			outcome := h.holdOutcome
			if outcome == "" {
				outcome = "held"
			}
			expires := h.expiresIn
			if expires == 0 {
				expires = 5 * time.Minute
			}
			return fakeAnswer{status: 200, body: usageBody(outcome, "hold_1", asOf.Add(expires), 10)}
		case strings.HasSuffix(r.URL.Path, "/settle"):
			if h.settle.status != 0 || h.settle.failure != "" {
				return h.settle
			}
			return fakeAnswer{status: 200, body: usageBody("settled", "hold_1", time.Time{}, 0)}
		case strings.HasSuffix(r.URL.Path, "/usage"):
			if h.record.status != 0 || h.record.failure != "" {
				return h.record
			}
			return fakeAnswer{status: 200, body: usageBody("recorded", nil, time.Time{}, 0)}
		case r.Method == "DELETE":
			if h.release.status != 0 {
				return h.release
			}
			return fakeAnswer{status: 200, body: usageBody("released", "hold_1", time.Time{}, 0)}
		}
		return fakeAnswer{status: 500}
	}}
}

func lastBody(f *fakeHTTP) map[string]any {
	seen := f.seen()
	var body map[string]any
	_ = json.Unmarshal([]byte(seen[len(seen)-1].body), &body)
	return body
}

var credits = Metered("ai_credits")

func TestStartHoldOutcomes(t *testing.T) {
	cases := map[string]struct {
		api  holdAPI
		want string
	}{
		"held":              {holdAPI{}, "open"},
		"open duplicate":    {holdAPI{holdOutcome: "duplicate"}, "duplicate"},
		"expired duplicate": {holdAPI{holdOutcome: "duplicate", expiresIn: -time.Minute}, "replayed"},
		"settled":           {holdAPI{holdOutcome: "settled"}, "replayed"},
		"released":          {holdAPI{holdOutcome: "released"}, "replayed"},
		"refused":           {holdAPI{holdOutcome: "refused"}, "refused"},
	}
	for name, c := range cases {
		h, err := testServer(t, c.api.fake()).Customer("u").StartHold(ctx, credits, 10, "k", nil)
		var refused *UsageRefusedError
		var replayed *UsageReplayedError
		switch c.want {
		case "open", "duplicate":
			if err != nil || h.ID != "hold_1" || h.Amount != 10 || h.Duplicate != (c.want == "duplicate") || h.Result == nil {
				t.Errorf("%s: %+v %v", name, h, err)
			}
		case "replayed":
			if !errors.As(err, &replayed) || replayed.Result == nil {
				t.Errorf("%s: %v", name, err)
			}
		case "refused":
			if !errors.As(err, &refused) || refused.Result.Refusal != RefusalOverAllowance {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
	if _, err := testServer(t, &fakeHTTP{}).Customer("u").StartHold(ctx, credits, 10, strings.Repeat("k", 194), nil); err == nil ||
		err.Error() != "pass idempotencyKey as 1 to 193 printable ASCII characters" {
		t.Error(err)
	}
}

func TestFinishSettlesWhatWasReported(t *testing.T) {
	cases := []struct {
		use        *int64
		settled    float64
		excess     float64
		recordsAny bool
	}{
		{nil, 10, 0, false},
		{new(int64(0)), 0, 0, false},
		{new(int64(4)), 4, 0, false},
		{new(int64(13)), 10, 3, true},
	}
	for _, c := range cases {
		f := holdAPI{}.fake()
		cu := testServer(t, f).Customer("u")
		h, _ := cu.StartHold(ctx, credits, 10, "msg-1", nil)
		if c.use != nil {
			if err := h.Use(*c.use); err != nil {
				t.Fatal(err)
			}
		}
		res, err := h.Finish(ctx)
		if err != nil || res.Outcome != OutcomeSettled {
			t.Fatalf("%v %v", res, err)
		}
		seen := f.seen()
		var settle map[string]any
		_ = json.Unmarshal([]byte(seen[1].body), &settle)
		if settle["amount"] != c.settled {
			t.Errorf("settled %v, want %v", settle["amount"], c.settled)
		}
		if c.recordsAny {
			excess := lastBody(f)
			if len(seen) != 3 || excess["amount"] != c.excess || excess["mode"] != "observe" || seen[2].header.Get("Idempotency-Key") != "msg-1:excess" {
				t.Errorf("excess %v", excess)
			}
		} else if len(seen) != 2 {
			t.Errorf("%d requests", len(seen))
		}
		again, err := h.Finish(ctx)
		_, _ = h.Release(ctx)
		h.Close(ctx)
		if again != res || err != nil || len(f.seen()) != len(seen) {
			t.Error("only the first of Finish, Release and Close acts")
		}
	}
	if err := (&Hold{}).Use(-1); err == nil || err.Error() != "pass the amount used as a whole number of 0 or more" {
		t.Error(err)
	}
}

func TestFinishAfterTheHoldExpiredRecordsTheWholeAmount(t *testing.T) {
	f := holdAPI{settle: fakeAnswer{status: 409, body: `{"error":{"code":"hold_expired","message":"Expired."}}`}}.fake()
	h, _ := testServer(t, f).Customer("u").StartHold(ctx, credits, 10, "msg-2", nil)
	_ = h.Use(12)
	res, err := h.Finish(ctx)
	if err != nil || res.Outcome != OutcomeRecorded {
		t.Fatalf("%v %v", res, err)
	}
	if body := lastBody(f); body["amount"] != float64(12) || f.seen()[2].header.Get("Idempotency-Key") != "msg-2:excess" {
		t.Fatalf("recorded %v", body)
	}
}

func TestSettlementFailures(t *testing.T) {
	f := holdAPI{settle: fakeAnswer{status: 400, body: `{"error":{"code":"invalid_body","message":"No."}}`}}.fake()
	h, _ := testServer(t, f).Customer("u").StartHold(ctx, credits, 10, "m", nil)
	_ = h.Use(12)
	_, err := h.Finish(ctx)
	var settlement *UsageSettlementError
	if !errors.As(err, &settlement) || settlement.HoldID != "hold_1" || settlement.Amount != 10 || settlement.Excess != 2 || settlement.Result != nil {
		t.Fatalf("%+v", err)
	}
	var apiErr *APIError
	if !errors.As(settlement, &apiErr) || apiErr.Code != CodeInvalidBody {
		t.Error("the cause is kept")
	}
	f = holdAPI{record: fakeAnswer{status: 400}}.fake()
	h, _ = testServer(t, f).Customer("u").StartHold(ctx, credits, 10, "m", nil)
	_ = h.Use(15)
	if _, err := h.Finish(ctx); !errors.As(err, &settlement) || settlement.Excess != 5 {
		t.Fatalf("excess failure: %v", err)
	}
	f = holdAPI{settle: fakeAnswer{status: 409, body: `{"error":{"code":"hold_expired","message":"x"}}`}, record: fakeAnswer{status: 400}}.fake()
	h, _ = testServer(t, f).Customer("u").StartHold(ctx, credits, 10, "m", nil)
	if _, err := h.Finish(ctx); !errors.As(err, &settlement) || settlement.Excess != 10 {
		t.Fatalf("expired and failing: %v", err)
	}
}

func TestCloseDisposesOfTheHold(t *testing.T) {
	var reported []error
	f := holdAPI{}.fake()
	cu := testServer(t, f, WithOnError(func(err error) { reported = append(reported, err) })).Customer("u")
	h, _ := cu.StartHold(ctx, credits, 10, "m", nil)
	h.Close(ctx)
	if f.seen()[1].method != "DELETE" {
		t.Error("closing a hold with nothing reported releases it")
	}
	h, _ = cu.StartHold(ctx, credits, 10, "m2", nil)
	_ = h.Use(3)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	h.Close(cancelled)
	if !strings.HasSuffix(f.seen()[3].url, "/settle") || lastBody(f)["amount"] != float64(3) {
		t.Error("closing after Use settles the amount reported, even with a done context")
	}
	failing := holdAPI{settle: fakeAnswer{status: 400}, release: fakeAnswer{status: 400}}.fake()
	cu = testServer(t, failing, WithOnError(func(err error) { reported = append(reported, err) })).Customer("u")
	h, _ = cu.StartHold(ctx, credits, 10, "m3", nil)
	_ = h.Use(1)
	h.Close(ctx)
	h2, _ := cu.StartHold(ctx, credits, 10, "m4", nil)
	h2.Close(ctx)
	var settlement *UsageSettlementError
	var apiErr *APIError
	if len(reported) != 2 || !errors.As(reported[0], &settlement) || !errors.As(reported[1], &apiErr) {
		t.Fatalf("reported %v", reported)
	}
}

func TestWithHold(t *testing.T) {
	f := holdAPI{}.fake()
	cu := testServer(t, f).Customer("u")
	value, err := WithHold(ctx, cu, credits, 10, "w1", func(_ context.Context, h *Hold) (string, error) {
		return "reply", h.Use(7)
	}, nil)
	if err != nil || value != "reply" || lastBody(f)["amount"] != float64(7) {
		t.Fatalf("%v %v", value, err)
	}
	boom := errors.New("boom")
	_, err = WithHold(ctx, cu, credits, 10, "w2", func(_ context.Context, h *Hold) (int, error) {
		_ = h.Use(3)
		return 0, boom
	}, nil)
	if !errors.Is(err, boom) || lastBody(f)["amount"] != float64(3) {
		t.Fatalf("a failure after Use settles what was reported: %v", err)
	}
	n := len(f.seen())
	_, err = WithHold(ctx, cu, credits, 10, "w3", func(context.Context, *Hold) (int, error) { return 0, boom }, nil)
	if !errors.Is(err, boom) || f.seen()[n+1].method != "DELETE" {
		t.Fatal("a failure before Use releases")
	}
	cancelled, cancel := context.WithCancel(ctx)
	_, err = WithHold(cancelled, cu, credits, 10, "w4", func(_ context.Context, h *Hold) (int, error) {
		_ = h.Use(2)
		cancel()
		return 1, nil
	}, nil)
	if !errors.Is(err, context.Canceled) || lastBody(f)["amount"] != float64(2) {
		t.Fatalf("a cancelled call settles what was reported: %v", err)
	}
	func() {
		defer func() { _ = recover() }()
		_, _ = WithHold(ctx, cu, credits, 10, "w5", func(_ context.Context, h *Hold) (int, error) {
			_ = h.Use(4)
			panic("work panicked")
		}, nil)
	}()
	if lastBody(f)["amount"] != float64(4) {
		t.Error("a panicking work still settles what it reported")
	}
	failing := holdAPI{settle: fakeAnswer{status: 400}}.fake()
	value, err = WithHold(ctx, testServer(t, failing).Customer("u"), credits, 10, "w6", func(context.Context, *Hold) (string, error) {
		return "kept", nil
	}, nil)
	var settlement *UsageSettlementError
	if !errors.As(err, &settlement) || settlement.Result != "kept" || value != "kept" {
		t.Fatalf("%v %v", value, err)
	}
	if _, err := WithHold(ctx, cu, credits, 10, "", func(context.Context, *Hold) (int, error) { return 0, nil }, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Error(err)
	}
}

func TestHoldRunsOutsideTheCallersCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := holdAPI{}.fake()
		cu := testServer(t, f, WithTimeout(time.Second)).Customer("u")
		h, _ := cu.StartHold(ctx, credits, 10, "m", nil)
		done, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := h.Finish(done); err != nil {
			t.Fatal(err)
		}
	})
}

func batchOK(n int, replayed bool) fakeAnswer {
	results := []any{}
	for i := range n {
		results = append(results, map[string]any{"index": i, "outcome": "recorded", "id": fmt.Sprint("u", i), "late": false, "error": nil})
	}
	a := fakeAnswer{status: 200, body: string(marshalBody(map[string]any{"results": results, "recorded": n, "duplicates": 0, "errors": 0}))}
	if replayed {
		a.header = map[string]string{"Idempotent-Replayed": "true"}
	}
	return a
}

func events(n int) []BatchEvent {
	out := make([]BatchEvent, n)
	for i := range out {
		out[i] = BatchEvent{Customer: fmt.Sprint("c", i%3), Feature: credits, Amount: int64(i + 1), IdempotencyKey: fmt.Sprint("e", i)}
	}
	return out
}

func TestBatchSplitsAndAnswersEachEvent(t *testing.T) {
	f := &fakeHTTP{}
	f.push(batchOK(500, false), fakeAnswer{status: 400, body: `{"error":{"code":"invalid_body","message":"Bad."}}`}, batchOK(1, true))
	srv := testServer(t, f)
	evs := events(1002)
	evs[3].Amount = 0
	result, err := srv.RecordUsageBatch(ctx, evs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.seen()) != 3 || result.Recorded != 501 || result.Errors != 501 || result.Duplicates != 0 {
		t.Fatalf("%d requests, totals %d/%d", len(f.seen()), result.Recorded, result.Errors)
	}
	if r := result.Results[3]; r.Outcome != OutcomeError || r.Error.Code != CodeInvalidAmount {
		t.Errorf("refused event %+v", r)
	}
	if r := result.Results[600]; r.Outcome != OutcomeError || r.Error.Code != CodeInvalidBody || r.Error.Message != "Bad." || r.IdempotencyKey != "e600" {
		t.Errorf("failed request %+v", r)
	}
	if r := result.Results[1001]; r.Outcome != OutcomeRecorded || !r.Replayed {
		t.Errorf("last %+v", r)
	}
	f.push(batchOK(500, false), batchOK(500, false), batchOK(1, false))
	_, _ = srv.RecordUsageBatch(ctx, evs, nil)
	first, again := f.seen()[0].header.Get("Idempotency-Key"), f.seen()[3].header.Get("Idempotency-Key")
	if first != again {
		t.Error("the same events send the same key")
	}
	var failed []BatchEvent
	for i, r := range result.Results {
		if r.Outcome == OutcomeError && i != 3 {
			failed = append(failed, evs[i])
		}
	}
	f.push(batchOK(100, false))
	_, _ = srv.RecordUsageBatch(ctx, failed[:100], nil)
	rerun := f.seen()[6].header.Get("Idempotency-Key")
	for _, s := range f.seen()[:3] {
		if s.header.Get("Idempotency-Key") == rerun {
			t.Error("a rerun of the failed events collides with a first request's key")
		}
	}
}

func TestBatchAnswersThatDoNotCoverTheEvents(t *testing.T) {
	for name, results := range map[string][]any{
		"missing":      {map[string]any{"index": 0, "outcome": "recorded", "id": "u", "late": false, "error": nil}},
		"repeated":     {map[string]any{"index": 0, "outcome": "recorded", "id": "u", "late": false, "error": nil}, map[string]any{"index": 0, "outcome": "recorded", "id": "u", "late": false, "error": nil}},
		"out of range": {map[string]any{"index": 0, "outcome": "recorded", "id": "u", "late": false, "error": nil}, map[string]any{"index": 5, "outcome": "recorded", "id": "u", "late": false, "error": nil}},
	} {
		f := &fakeHTTP{}
		f.push(fakeAnswer{status: 200, body: string(marshalBody(map[string]any{"results": results, "recorded": 1, "duplicates": 0, "errors": 0}))})
		result, err := testServer(t, f).RecordUsageBatch(ctx, events(2), nil)
		if err != nil || result.Results[0].Error == nil || result.Results[0].Error.Code != CodeInvalidResponse || result.Results[1].Error.Code != CodeInvalidResponse {
			t.Errorf("%s: %+v %v", name, result, err)
		}
	}
}

func TestBatchFailuresAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeHTTP{}
		f.push(fakeAnswer{failure: "connection"}, fakeAnswer{failure: "timeout"})
		srv := testServer(t, f, WithTimeout(time.Second))
		r, _ := srv.RecordUsageBatch(ctx, events(1), nil)
		if r.Results[0].Error.Code != CodeConnectionFailed {
			t.Error(r.Results[0].Error)
		}
		r, _ = srv.RecordUsageBatch(ctx, events(1), nil)
		if r.Results[0].Error.Code != CodeTimedOut {
			t.Error(r.Results[0].Error)
		}
		done, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := srv.RecordUsageBatch(done, events(1), nil); !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
		before := f.count()
		r, err := srv.RecordUsageBatch(ctx, []BatchEvent{{Customer: "", Feature: credits, Amount: 1, IdempotencyKey: "k"}}, nil)
		if err != nil || f.count() != before || r.Results[0].Error.Code != CodeInvalidBody {
			t.Error("an input with nothing to send sends nothing")
		}
		r, _ = srv.RecordUsageBatch(ctx, []BatchEvent{{Customer: "c", Feature: credits, Amount: 1, IdempotencyKey: "k", OccurredAt: time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)}}, nil)
		if r.Results[0].Error == nil {
			t.Error("an impossible instant is refused")
		}
		_ = srv.Close()
		if _, err := srv.RecordUsageBatch(ctx, events(1), nil); !errors.Is(err, ErrClosed) {
			t.Error(err)
		}
	})
}
