package entitler

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestRequestHeaders(t *testing.T) {
	f := &fakeHTTP{handle: func(r *http.Request, body string) fakeAnswer {
		if r.Method == "GET" {
			return fakeAnswer{status: 200, body: checkBody("user_42", "export_pdf")}
		}
		return fakeAnswer{status: 200, body: `{"changed":true}`}
	}}
	srv := testServer(t, f)
	if _, err := srv.Customer("user_42").Check(ctx, Key("export_pdf"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Customer("user_42").SyncBilling(ctx); err != nil {
		t.Fatal(err)
	}
	get, post := f.seen()[0], f.seen()[1]
	wantUA := "entitler-go/" + Version + " go/" + strings.TrimPrefix(runtime.Version(), "go")
	for _, r := range []*seen{get, post} {
		if r.header.Get("User-Agent") != wantUA || r.header.Get("Accept") != "application/json" ||
			r.header.Get("Authorization") != "Bearer "+testKey {
			t.Errorf("headers %v", r.header)
		}
	}
	if get.header.Get("Content-Type") != "" || get.header.Get("Idempotency-Key") != "" || get.header.Get("Entitler-Visitor") != "" {
		t.Errorf("a GET sent %v", get.header)
	}
	if post.header.Get("Content-Type") != "" || !isUUIDv4(post.header.Get("Idempotency-Key")) || post.body != "" {
		t.Errorf("a POST with no body sent %v %q", post.header, post.body)
	}
}

func TestReplayedHeaderMustBeExactlyTrue(t *testing.T) {
	for value, want := range map[string]bool{"true": true, "True": false, "false": false, "1": false, "": false} {
		f := &fakeHTTP{}
		f.push(fakeAnswer{status: 200, header: map[string]string{"Idempotent-Replayed": value},
			body: `{"product":null,"plan":null,"quantity":null,"effective":"now","at":"2026-07-01T09:30:00.000Z","until":null,"changed":false}`})
		change, err := testServer(t, f).Customer("u").Cancel(ctx, nil)
		if err != nil || change.Replayed != want {
			t.Errorf("%q: replayed %v, %v", value, change.Replayed, err)
		}
	}
}

func TestRedirectsReachNoOtherOrigin(t *testing.T) {
	var reached atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) }))
	defer other.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+r.URL.Path, status)
		}))
		token := StaticToken(tokenFor(time.Now().Add(time.Hour)))
		clients := map[string]Customer{}
		srv, _ := NewServer(testKey, WithBaseURL(api.URL), WithMaxRetries(0))
		clients["server"] = srv.Customer("u")
		c, _ := NewClient(token, WithBaseURL(api.URL), WithMaxRetries(0))
		clients["token"] = c.Me()
		ic, _ := NewIdentityClient("ent_pk_test_x", token, WithBaseURL(api.URL), WithMaxRetries(0))
		clients["identity"] = ic.Me()
		for name, cu := range clients {
			_, readErr := cu.Check(ctx, Key("export_pdf"), nil)
			_, writeErr := cu.RecordUsage(ctx, Metered("ai"), 1, "k", nil)
			for _, err := range []error{readErr, writeErr} {
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.Status != status || apiErr.Code != CodeHTTPError {
					t.Errorf("%s %d: %v", name, status, err)
				}
			}
		}
		_ = srv.Close()
		_ = c.Close()
		_ = ic.Close()
		api.Close()
	}
	if reached.Load() != 0 {
		t.Errorf("another origin was reached %d times", reached.Load())
	}
}

func TestInjectedClientFollowsNoRedirectAndStaysOpen(t *testing.T) {
	injected := &http.Client{}
	srv, _ := NewServer(testKey, WithHTTPClient(injected))
	if injected.CheckRedirect != nil {
		t.Error("the injected client was changed")
	}
	if srv.c.e.http == injected || srv.c.e.http.CheckRedirect == nil {
		t.Error("the SDK must use its own copy that follows no redirect")
	}
	_ = srv.Close()
	_ = srv.Close()
}

func TestTimeoutsAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeHTTP{}
		f.push(fakeAnswer{failure: "hang"})
		srv := testServer(t, f, WithTimeout(2*time.Second))
		started := time.Now()
		_, err := srv.Customer("u").Check(ctx, Key("f"), nil)
		var timeout *TimeoutError
		if !errors.As(err, &timeout) || !timeout.Timeout() || time.Since(started) != 2*time.Second {
			t.Fatalf("got %v after %s", err, time.Since(started))
		}
		if !strings.Contains(err.Error(), "2s") || !IsUnreachable(err) {
			t.Error(err)
		}

		// The caller's own deadline is never a TimeoutError.
		f.push(fakeAnswer{failure: "hang"})
		short, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		_, err = srv.Customer("u").Check(short, Key("f"), nil)
		if !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) {
			t.Fatalf("got %v", err)
		}

		// A cancelled call ends a retry wait at once.
		f.push(fakeAnswer{status: 503, header: map[string]string{"Retry-After": "9"}})
		retrying := srv.With(WithTimeout(time.Second))
		retrying.c.e.maxRetries = 2
		waiting, stop := context.WithCancel(ctx)
		go func() { time.Sleep(time.Second); stop() }()
		started = time.Now()
		_, err = retrying.Customer("u").Check(waiting, Key("f"), nil)
		if !errors.Is(err, context.Canceled) || time.Since(started) != time.Second {
			t.Fatalf("got %v after %s", err, time.Since(started))
		}
	})
}

func TestTimeoutValidation(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		if _, err := NewServer(testKey, WithTimeout(d)); !errors.Is(err, ErrInvalidArgument) || err.Error() != "pass timeout as a positive duration" {
			t.Errorf("%s: %v", d, err)
		}
	}
	f := &fakeHTTP{}
	var reported []error
	srv := testServer(t, f, WithOnError(func(err error) { reported = append(reported, err) }))
	bad := srv.With(WithTimeout(-1))
	if _, err := bad.Customer("u").Check(ctx, Key("f"), nil); !errors.Is(err, ErrInvalidArgument) {
		t.Error(err)
	}
	if bad.Customer("u").IsEntitled(ctx, Key("f"), true, nil) != true || len(reported) != 1 || !errors.Is(reported[0], ErrInvalidArgument) {
		t.Errorf("IsEntitled with a bad timeout: %v", reported)
	}
	if f.count() != 0 {
		t.Error("a request was sent")
	}
	if _, err := srv.With(WithBaseURL("https://x")).Features(ctx); !errors.Is(err, ErrInvalidArgument) {
		t.Error("With refuses options that belong to the client")
	}
	huge := srv.With(WithTimeout(time.Duration(math.MaxInt64)))
	f.push(fakeAnswer{status: 200, body: checkBody("u", "f")})
	if _, err := huge.Customer("u").Check(ctx, Key("f"), nil); err != nil {
		t.Errorf("a huge timeout is clamped: %v", err)
	}
	if clampDuration(time.Duration(math.MaxInt64)) != maxDuration || clampDuration(-5) != 0 {
		t.Error("clamp")
	}
}

func TestOptionsAreValidated(t *testing.T) {
	for name, opt := range map[string]ServerOption{
		"base url":        WithBaseURL("ftp://x"),
		"relative":        WithBaseURL("/v1"),
		"retries":         WithMaxRetries(-1),
		"retry delay":     WithMaxRetryDelay(-1),
		"cache size":      WithMemoryCache(0),
		"stale":           WithStaleFor(-1),
		"http client":     WithHTTPClient(nil),
		"cache":           WithCache(nil),
		"base url query":  WithBaseURL("https://x?y=1"),
		"base url broken": WithBaseURL("https://[::1"),
	} {
		if _, err := NewServer(testKey, opt); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := NewClient(StaticToken("t"), WithVisitor("short")); !errors.Is(err, ErrInvalidArgument) {
		t.Error("visitor")
	}
	srv, _ := NewServer(testKey, WithBaseURL("https://example.com/api//"), WithMemoryCache(5), WithStaleFor(time.Hour), nil)
	if srv.c.e.baseURL != "https://example.com/api" || srv.c.e.memory.size != 5 || srv.c.e.staleFor != time.Hour {
		t.Error(srv.c.e.baseURL)
	}
	if srv.String() != "entitler.Server(https://example.com/api)" {
		t.Error(srv.String())
	}
}

func TestClosedClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeHTTP{}
		f.push(fakeAnswer{failure: "hang"})
		srv := testServer(t, f, WithTimeout(time.Minute))
		derived := srv.With(WithTimeout(time.Hour))
		done := make(chan error)
		go func() {
			_, err := srv.Customer("u").Check(ctx, Key("f"), nil)
			done <- err
		}()
		time.Sleep(time.Second)
		_ = derived.Close()
		if err := <-done; !errors.Is(err, ErrClosed) || err.Error() != "this Entitler client is closed. Create a new one" {
			t.Fatalf("in flight: %v", err)
		}
		if _, err := srv.Features(ctx); err != ErrClosed {
			t.Errorf("later call: %v", err)
		}
		if _, err := derived.Customer("u").Check(ctx, Key("f"), nil); err != ErrClosed {
			t.Errorf("derived: %v", err)
		}
		if err := srv.Close(); err != nil {
			t.Error(err)
		}
	})
}

func TestCutOffAnswerIsAConnectionError(t *testing.T) {
	f := &fakeHTTP{}
	f.push(fakeAnswer{failure: "cut-off"})
	_, err := testServer(t, f).Customer("u").Check(ctx, Key("f"), nil)
	var conn *ConnectionError
	if !errors.As(err, &conn) || conn.Unwrap() == nil || !strings.Contains(conn.Error(), "could not reach Entitler") {
		t.Fatal(err)
	}
	f.push(fakeAnswer{failure: "connection"})
	_, err = testServer(t, f).Customer("u").RecordUsage(ctx, Metered("ai"), 1, "key-1", nil)
	if !errors.As(err, &conn) || conn.IdempotencyKey != "key-1" {
		t.Fatal(err)
	}
}

func TestInvalidResponseNeverHoldsTheBody(t *testing.T) {
	secret := "ent_live_secret_in_the_body"
	for _, body := range []string{
		`{"token":"` + secret + `"}`,
		`{"customer":1234567,"secret":"` + secret + `"}`,
		`not json ` + secret,
		`{"customer":"u","asOf":"` + secret + `"}`,
		`[1, "` + secret + `"]`,
		``,
	} {
		f := &fakeHTTP{}
		f.push(fakeAnswer{status: 200, body: body, header: map[string]string{"X-Request-Id": "req_1"}})
		_, err := testServer(t, f).Customer("u").Check(ctx, Key("f"), nil)
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Code != CodeInvalidResponse || apiErr.RequestID != "req_1" || !IsUnreachable(err) {
			t.Fatalf("%q: %v", body, err)
		}
		chain := fmt.Sprintf("%v %+v %v", err, apiErr, apiErr.Unwrap())
		if strings.Contains(chain, secret) || strings.Contains(chain, "1234567") || apiErr.Unwrap() == nil {
			t.Errorf("the cause holds body text: %s", chain)
		}
	}
}

func TestEmptyObjectIsInvalidForEveryRead(t *testing.T) {
	f := &fakeHTTP{handle: func(*http.Request, string) fakeAnswer { return fakeAnswer{status: 200, body: `{}`} }}
	srv := testServer(t, f, WithoutCache())
	cu := srv.Customer("u")
	calls := map[string]func() error{
		"check":        func() error { _, err := cu.Check(ctx, Key("f"), nil); return err },
		"entitlements": func() error { _, err := cu.Entitlements(ctx, nil); return err },
		"plans":        func() error { _, err := cu.Plans(ctx, nil); return err },
		"pricing":      func() error { _, err := cu.Pricing(ctx, nil); return err },
		"usage":        func() error { _, err := cu.Usage(ctx, nil); return err },
		"details":      func() error { _, err := cu.Details(ctx, nil); return err },
		"billing":      func() error { _, err := cu.Billing(ctx); return err },
		"features":     func() error { _, err := srv.Features(ctx); return err },
		"srv pricing":  func() error { _, err := srv.Pricing(ctx, nil); return err },
		"scopes":       func() error { _, err := srv.Scopes(ctx); return err },
		"keys":         func() error { _, err := srv.SnapshotKeys(ctx); return err },
		"page":         func() error { _, err := srv.Customers.Page(ctx, nil); return err },
		"record":       func() error { _, err := cu.RecordUsage(ctx, Metered("a"), 1, "k", nil); return err },
		"subscribe":    func() error { _, err := cu.Subscribe(ctx, "pro", nil); return err },
		"cancel":       func() error { _, err := cu.Cancel(ctx, nil); return err },
		"portal":       func() error { _, err := cu.BillingPortal(ctx, "https://x"); return err },
		"sync":         func() error { _, err := cu.SyncBilling(ctx); return err },
		"snapshot":     func() error { _, err := cu.Snapshot(ctx, nil); return err },
		"register":     func() error { _, err := cu.Register(ctx, nil); return err },
		"update":       func() error { _, err := cu.Update(ctx, nil); return err },
		"erase":        func() error { return cu.Erase(ctx, nil) },
		"token":        func() error { _, err := cu.Token(ctx, nil); return err },
		"track":        func() error { _, err := cu.SetTrack(ctx, nil, nil); return err },
		"plan":         func() error { _, err := cu.SetPlan(ctx, Plan("pro"), nil); return err },
		"add-on":       func() error { _, err := cu.SetAddOn(ctx, "a", 1, nil); return err },
		"grant":        func() error { _, err := cu.Grant(ctx, Key("f"), nil); return err },
		"revoke":       func() error { _, err := cu.RevokeGrant(ctx, "g", nil); return err },
		"adjust": func() error {
			_, err := cu.AdjustMeter(ctx, Metered("a"), &AdjustMeterParams{To: new(int64(1)), IdempotencyKey: "k"})
			return err
		},
		"cancel usage": func() error { _, err := cu.CancelUsage(ctx, "u", nil); return err },
		"create": func() error {
			_, err := srv.Customers.Create(ctx, CreateCustomerParams{ExternalID: "x", Name: "X"})
			return err
		},
	}
	for name, call := range calls {
		var apiErr *APIError
		if err := call(); !errors.As(err, &apiErr) || apiErr.Code != CodeInvalidResponse {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC)
	cases := map[string]*time.Duration{
		"":                                  nil,
		"5":                                 new(5 * time.Second),
		"Wed, 01 Jul 2026 09:30:30 GMT":     new(30 * time.Second),
		"Wednesday, 01-Jul-26 09:30:30 GMT": new(30 * time.Second),
		"Wed Jul  1 09:30:30 2026":          new(30 * time.Second),
		"Wed, 01 Jul 2026 09:00:00 GMT":     new(time.Duration(0)),
		"99999999999999999999999":           new(time.Duration(math.MaxInt64)),
		"1.5":                               nil,
		"-1":                                nil,
	}
	for value, want := range cases {
		got := retryAfterOf(value, now)
		if (got == nil) != (want == nil) || got != nil && *got != *want {
			t.Errorf("%q: got %v, want %v", value, got, want)
		}
	}
}

func TestErrorKindsAndUnreachable(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&APIError{Status: 429}, true},
		{&APIError{Status: 503}, true},
		{&APIError{Status: 200, Code: CodeInvalidResponse}, true},
		{&APIError{Status: 404}, false},
		{&ConnectionError{cause: errors.New("x")}, true},
		{&TimeoutError{}, true},
		{&TokenError{message: "x"}, true},
		{&SnapshotError{Code: CodeSnapshotInvalid}, false},
		{&UsageRefusedError{Result: &UsageResult{}}, false},
		{&UsageReplayedError{Result: &UsageResult{}}, false},
		{&UsageSettlementError{cause: &ConnectionError{cause: errors.New("x")}}, false},
		{fmt.Errorf("wrapped: %w", &TimeoutError{}), true},
		{errors.New("other"), false},
		{nil, false},
	}
	for _, c := range cases {
		if IsUnreachable(c.err) != c.want {
			t.Errorf("%T: %v", c.err, !c.want)
		}
	}
	var _ Error = (*APIError)(nil)
	for _, e := range []Error{&APIError{Message: "m"}, &ConnectionError{cause: errors.New("c")}, &TimeoutError{},
		&TokenError{message: "t", cause: errors.New("c")}, &SnapshotError{Message: "s"},
		&UsageRefusedError{Result: &UsageResult{Check: Check{Feature: "ai"}, Refusal: RefusalOverAllowance}},
		&UsageReplayedError{Result: &UsageResult{Outcome: OutcomeSettled}}, &UsageSettlementError{HoldID: "h", cause: errors.New("c")}} {
		e.entitlerError()
		if e.Error() == "" {
			t.Errorf("%T has no message", e)
		}
	}
	if (&UsageRefusedError{Result: &UsageResult{}}).Error() == "" || (&TokenError{message: "t"}).Unwrap() != nil {
		t.Error("messages")
	}
}

func TestOnErrorNeverChangesTheAnswer(t *testing.T) {
	f := &fakeHTTP{}
	f.push(fakeAnswer{status: 500})
	srv := testServer(t, f, WithOnError(func(error) { panic("boom") }))
	if !srv.Customer("u").IsEntitled(ctx, Key("f"), true, nil) {
		t.Error("the default changed")
	}
	srv.c.e.report(nil)
}

func TestIsEntitledPropagatesNoCancellationToOnError(t *testing.T) {
	var reported int
	srv := testServer(t, &fakeHTTP{}, WithOnError(func(error) { reported++ }))
	done, cancel := context.WithCancel(ctx)
	cancel()
	if srv.Customer("u").IsEntitled(done, Key("f"), true, nil) != true || reported != 0 {
		t.Errorf("reported %d", reported)
	}
	if srv.Customer("u").IsEntitled(ctx, Key(" "), false, nil) != false || reported != 1 {
		t.Errorf("a blank feature goes to onError: %d", reported)
	}
	if srv.Customer("u").IsEntitled(ctx, nil, true, nil) != true {
		t.Error("a nil feature answers the default")
	}
}

func TestInvalidUTF8(t *testing.T) {
	f := &fakeHTTP{}
	srv := testServer(t, f)
	bad := "a\xffb"
	checks := []error{
		func() error { _, err := srv.Customer(bad).Check(ctx, Key("f"), nil); return err }(),
		func() error { _, err := srv.Customer("u").Check(ctx, Key(bad), nil); return err }(),
		func() error { _, err := srv.Customers.Page(ctx, &ListCustomersParams{Q: bad}); return err }(),
		func() error { _, err := srv.Customer("u").Cancel(ctx, &CancelParams{Product: &bad}); return err }(),
		func() error { _, err := srv.Customer("u").ReleaseUsage(ctx, bad, nil); return err }(),
	}
	for i, err := range checks {
		if !errors.Is(err, ErrInvalidArgument) || err.Error() != "pass an id that is valid Unicode" {
			t.Errorf("%d: %v", i, err)
		}
	}
	if f.count() != 0 {
		t.Error("a request was sent")
	}
}
