package entitler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func cached(body, etag, control string) fakeAnswer {
	return fakeAnswer{status: 200, body: body, header: map[string]string{"ETag": etag, "Cache-Control": control}}
}

func notModified(header map[string]string) fakeAnswer {
	return fakeAnswer{status: 304, header: header}
}

func TestFreshEntryAnswersWithoutARequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeHTTP{}
		f.push(cached(checkBody("u", "f"), `"v1"`, "private, max-age=60"))
		cu := testServer(t, f).Customer("u")
		for range 3 {
			if _, err := cu.Check(ctx, Key("f"), nil); err != nil {
				t.Fatal(err)
			}
		}
		if f.count() != 1 {
			t.Fatalf("%d requests", f.count())
		}
		f.push(notModified(nil))
		if _, err := cu.Check(ctx, Key("f"), &ReadParams{Revalidate: true}); err != nil {
			t.Fatal(err)
		}
		if f.count() != 2 || f.seen()[1].header.Get("If-None-Match") != `"v1"` {
			t.Fatal("revalidate sends the ETag")
		}
		time.Sleep(61 * time.Second)
		f.push(notModified(map[string]string{"ETag": `"v1"`}))
		check, err := cu.Check(ctx, Key("f"), nil)
		if err != nil || check.Feature != "f" || f.count() != 3 {
			t.Fatalf("an aged entry is revalidated: %v %d", err, f.count())
		}
		if _, err := cu.Check(ctx, Key("f"), nil); err != nil || f.count() != 3 {
			t.Fatalf("a 304 keeps the stored Cache-Control: %d", f.count())
		}
	})
}

func TestAnswersFromTheCacheAreCopies(t *testing.T) {
	f := &fakeHTTP{}
	f.push(cached(checkBody("u", "f"), `"v1"`, "max-age=60"))
	cu := testServer(t, f).Customer("u")
	first, _ := cu.Check(ctx, Key("f"), nil)
	first.Feature = "changed"
	second, _ := cu.Check(ctx, Key("f"), nil)
	if second.Feature != "f" || f.count() != 1 {
		t.Fatal("a caller changed a kept answer")
	}
}

func TestNoCacheAndAgeAndNoStore(t *testing.T) {
	f := &fakeHTTP{}
	f.push(cached(checkBody("u", "f"), `"v1"`, "private, no-cache, max-age=60"), notModified(nil))
	cu := testServer(t, f).Customer("u")
	_, _ = cu.Check(ctx, Key("f"), nil)
	if _, err := cu.Check(ctx, Key("f"), nil); err != nil || f.count() != 2 {
		t.Fatalf("no-cache revalidates every time: %v %d", err, f.count())
	}
	f.push(fakeAnswer{status: 200, body: checkBody("u", "g"), header: map[string]string{"Cache-Control": "max-age=60", "Age": "60"}})
	_, _ = cu.Check(ctx, Key("g"), nil)
	f.push(fakeAnswer{status: 200, body: checkBody("u", "g")})
	_, _ = cu.Check(ctx, Key("g"), nil)
	if f.count() != 4 {
		t.Fatal("an answer aged past max-age is not fresh")
	}
	f.push(fakeAnswer{status: 200, body: checkBody("u", "h"), header: map[string]string{"Cache-Control": "no-store, max-age=60", "ETag": `"x"`}})
	_, _ = cu.Check(ctx, Key("h"), nil)
	f.push(fakeAnswer{status: 200, body: checkBody("u", "h")})
	_, _ = cu.Check(ctx, Key("h"), nil)
	if f.count() != 6 || f.seen()[5].header.Get("If-None-Match") != "" {
		t.Fatal("no-store is never kept")
	}
}

func TestNotModifiedWithNothingKept(t *testing.T) {
	f := &fakeHTTP{}
	f.push(notModified(nil))
	_, err := testServer(t, f).Customer("u").Check(ctx, Key("f"), nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 304 || apiErr.Code != CodeHTTPError {
		t.Fatal(err)
	}
}

func TestWritesRevalidateTheCustomer(t *testing.T) {
	f := &fakeHTTP{handle: func(r *http.Request, _ string) fakeAnswer {
		if r.Method == "GET" {
			return cached(checkBody("u", "f"), `"v1"`, "max-age=600")
		}
		return fakeAnswer{status: 200, body: `{"changed":true}`}
	}}
	srv := testServer(t, f)
	cu := srv.Customer("u")
	_, _ = cu.Check(ctx, Key("f"), nil)
	_, _ = srv.Customer("other").SyncBilling(ctx)
	_, _ = cu.Check(ctx, Key("f"), nil)
	if f.count() != 2 {
		t.Fatalf("a write for another customer changes nothing: %d", f.count())
	}
	_, _ = cu.SyncBilling(ctx)
	_, _ = cu.Check(ctx, Key("f"), nil)
	if f.count() != 4 || f.seen()[3].header.Get("If-None-Match") != `"v1"` {
		t.Fatalf("a write revalidates: %d", f.count())
	}
	_, _ = cu.Token(ctx, nil)
	_, _ = cu.Check(ctx, Key("f"), nil)
	if f.count() != 5 {
		t.Fatalf("token changes no answer: %d", f.count())
	}
}

func TestReadRacingAWriteIsNeverKeptFresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var srv *Server
		release := make(chan struct{})
		f := &fakeHTTP{handle: func(r *http.Request, _ string) fakeAnswer {
			if r.Method == "GET" {
				<-release
				return cached(checkBody("u", "f"), `"v1"`, "max-age=600")
			}
			return fakeAnswer{status: 200, body: `{"changed":true}`}
		}}
		srv = testServer(t, f)
		cu := srv.Customer("u")
		done := make(chan struct{})
		go func() { _, _ = cu.Check(ctx, Key("f"), nil); close(done) }()
		synctest.Wait()
		go func() { _, _ = cu.SyncBilling(ctx) }()
		synctest.Wait()
		close(release)
		<-done
		time.Sleep(time.Second)
		before := f.count()
		_, _ = cu.Check(ctx, Key("f"), nil)
		if f.count() != before+1 {
			t.Fatal("the read that raced a write was kept as fresh")
		}
	})
}

func TestGenerationsAreBoundedWithAFloor(t *testing.T) {
	g := newGenerations(3)
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	g.bump("a", t0)
	mark := g.mark("a")
	for i, c := range []string{"b", "c", "d"} {
		g.bump(c, t0.Add(time.Duration(i+1)*time.Second))
	}
	if g.keptAfterWrites("a", t0) || g.keptAfterWrites("zzz", t0) {
		t.Error("an entry from before a forgotten customer's write is revalidated")
	}
	if !g.keptAfterWrites("zzz", t0.Add(time.Second)) || g.keptAfterWrites("d", t0.Add(2*time.Second)) {
		t.Error("floor and per-customer bump")
	}
	if !g.moved("a", mark) {
		t.Error("a customer forgotten since the mark counts as moved")
	}
	g.bump("a", t0.Add(10*time.Second))
	if g.mark("a").number <= mark.number {
		t.Error("generations come from one counter")
	}
	started := time.Now()
	big := newGenerations(trackedCustomers)
	for i := range 200000 {
		big.bump(fmt.Sprint("customer_", i), t0.Add(time.Duration(i)))
	}
	if big.order.Len() != trackedCustomers || time.Since(started) > 5*time.Second {
		t.Errorf("tracked %d in %s", big.order.Len(), time.Since(started))
	}
}

func TestNoTrackingWithTheCacheOff(t *testing.T) {
	srv := testServer(t, &fakeHTTP{}, WithoutCache())
	if srv.c.e.gens != nil || srv.c.e.cache != nil {
		t.Fatal("tracking with the cache off")
	}
}

func TestMemoryCacheEvictsAndExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := NewMemoryCache(2)
		for _, k := range []string{"a", "b"} {
			_ = m.Set(ctx, k, CacheEntry{V: 1, Body: k}, time.Minute)
		}
		_, _, _ = m.Get(ctx, "a")
		_ = m.Set(ctx, "c", CacheEntry{V: 1, Body: "c"}, time.Minute)
		if _, ok, _ := m.Get(ctx, "b"); ok {
			t.Error("the least recently used entry stays")
		}
		_ = m.Set(ctx, "a", CacheEntry{V: 1, Body: "a2"}, time.Minute)
		if e, ok, _ := m.Get(ctx, "a"); !ok || e.Body != "a2" {
			t.Error("replace")
		}
		time.Sleep(2 * time.Minute)
		if _, ok, _ := m.Get(ctx, "a"); ok {
			t.Error("an expired entry answers")
		}
		if NewMemoryCache(0).size != 1 {
			t.Error("size")
		}
	})
}

type failingStore struct{ get, set error }

func (s failingStore) Get(context.Context, string) (CacheEntry, bool, error) {
	return CacheEntry{V: 2, Body: "x", ReceivedAt: time.Now()}, s.get == nil, s.get
}

func (s failingStore) Set(context.Context, string, CacheEntry, time.Duration) error { return s.set }

func TestFailingCustomStoreCountsAsAMiss(t *testing.T) {
	var reported []error
	f := &fakeHTTP{}
	f.push(cached(checkBody("u", "f"), `"v1"`, "max-age=60"), cached(checkBody("u", "f"), `"v1"`, "max-age=60"))
	srv := testServer(t, f, WithCache(failingStore{get: errors.New("get"), set: errors.New("set")}),
		WithOnError(func(err error) { reported = append(reported, err) }))
	if _, err := srv.Customer("u").Check(ctx, Key("f"), nil); err != nil {
		t.Fatal(err)
	}
	if len(reported) != 2 {
		t.Fatalf("reported %v", reported)
	}
	srv2 := testServer(t, f, WithCache(failingStore{}))
	if _, err := srv2.Customer("u").Check(ctx, Key("f"), nil); err != nil || f.count() != 2 {
		t.Fatalf("an entry of another version is a miss: %v", err)
	}
}

func TestCustomStoreTTLIsStaleForPlusMaxAge(t *testing.T) {
	var ttl time.Duration
	store := recordingStore{set: func(d time.Duration) { ttl = d }}
	f := &fakeHTTP{}
	f.push(cached(checkBody("u", "f"), `"v1"`, "max-age=60"))
	srv := testServer(t, f, WithCache(store), WithStaleFor(time.Hour))
	_, _ = srv.Customer("u").Check(ctx, Key("f"), nil)
	if ttl != time.Hour+time.Minute {
		t.Fatal(ttl)
	}
	_ = srv.Close()
	if store.cleared {
		t.Error("closing leaves a custom store as it is")
	}
}

type recordingStore struct {
	set     func(time.Duration)
	cleared bool
}

func (s recordingStore) Get(context.Context, string) (CacheEntry, bool, error) {
	return CacheEntry{}, false, nil
}

func (s recordingStore) Set(_ context.Context, _ string, _ CacheEntry, ttl time.Duration) error {
	s.set(ttl)
	return nil
}

func TestStaleAnswersAndTheOutageWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var reported []error
		f := &fakeHTTP{}
		f.push(cached(checkBody("u", "f"), `"v1"`, "private, no-cache"))
		srv := testServer(t, f, WithOnError(func(err error) { reported = append(reported, err) }))
		cu := srv.Customer("u")
		if _, err := cu.Check(ctx, Key("f"), nil); err != nil {
			t.Fatal(err)
		}
		f.push(fakeAnswer{status: 503, header: map[string]string{"Retry-After": "120"}})
		check, err := cu.Check(ctx, Key("f"), nil)
		if err != nil || !check.Stale || len(reported) != 1 {
			t.Fatalf("stale: %v %+v %v", err, check, reported)
		}
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() {
				if c, err := cu.Check(ctx, Key("f"), nil); err != nil || !c.Stale {
					t.Error("not stale", err)
				}
			})
		}
		wg.Wait()
		if f.count() != 2 {
			t.Fatalf("%d requests during the window", f.count())
		}
		f.push(fakeAnswer{status: 502})
		if _, err := cu.Check(ctx, Key("g"), nil); err == nil || f.count() != 3 {
			t.Fatalf("a read with no entry: %v %d", err, f.count())
		}
		time.Sleep(31 * time.Second)
		gate := make(chan struct{})
		f.handle = func(*http.Request, string) fakeAnswer {
			<-gate
			return fakeAnswer{status: 503}
		}
		results := make(chan *Check, 20)
		for range 20 {
			go func() { c, _ := cu.Check(ctx, Key("f"), nil); results <- c }()
		}
		synctest.Wait()
		if f.count() != 4 {
			t.Fatalf("%d test requests", f.count()-3)
		}
		close(gate)
		for range 20 {
			if c := <-results; c == nil || !c.Stale {
				t.Fatal("a read failed during the test")
			}
		}
		time.Sleep(31 * time.Second)
		f.handle = nil
		f.push(cached(checkBody("u", "f"), `"v2"`, "no-cache"))
		if c, err := cu.Check(ctx, Key("f"), nil); err != nil || c.Stale {
			t.Fatalf("after the window: %v", err)
		}
		f.push(cached(checkBody("u", "f"), `"v2"`, "no-cache"))
		if c, _ := cu.Check(ctx, Key("f"), nil); c.Stale || f.count() != 6 {
			t.Fatal("a success ends the window at once")
		}
	})
}

func TestOutageWindowEndsOnCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeHTTP{}
		f.push(cached(checkBody("u", "f"), `"v1"`, "no-cache"), fakeAnswer{status: 500})
		cu := testServer(t, f).Customer("u")
		_, _ = cu.Check(ctx, Key("f"), nil)
		_, _ = cu.Check(ctx, Key("f"), nil)
		time.Sleep(31 * time.Second)
		f.push(fakeAnswer{failure: "hang"})
		cancelled, cancel := context.WithCancel(ctx)
		go func() { time.Sleep(time.Second); cancel() }()
		if _, err := cu.Check(cancelled, Key("f"), nil); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		f.push(cached(checkBody("u", "f"), `"v1"`, "no-cache"))
		if c, err := cu.Check(ctx, Key("f"), nil); err != nil || c.Stale {
			t.Fatalf("a cancelled test lets the next read ask: %v %+v", err, c)
		}
	})
}

func TestOutageWindowArithmeticSaturates(t *testing.T) {
	var o outage
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	huge := time.Duration(1<<63 - 1)
	o.fail(now, &huge, time.Hour)
	if o.until != now.Add(time.Hour) {
		t.Fatalf("the window outlasts staleFor: %s", o.until)
	}
	o.fail(now, nil, time.Second)
	if o.until != now.Add(time.Second) {
		t.Fatal("never longer than staleFor")
	}
}

func TestStaleNeverCrossesCredentials(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		tokens := []string{tokenFor(now.Add(time.Hour)), tokenFor(now.Add(2 * time.Hour))}
		calls := 0
		f := &fakeHTTP{}
		f.push(cached(checkBody("me", "f"), `"v1"`, "no-cache"), fakeAnswer{status: 401}, fakeAnswer{status: 503})
		client, err := NewClient(TokenFunc(func(context.Context) (string, error) {
			calls++
			return tokens[min(calls-1, 1)], nil
		}), WithHTTPClient(f.client()), WithMaxRetries(0))
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		if _, err := client.Me().Check(ctx, Key("f"), nil); err != nil {
			t.Fatal(err)
		}
		_, err = client.Me().Check(ctx, Key("f"), nil)
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 503 {
			t.Fatalf("an entry kept under the old token stood in: %v", err)
		}
		if client.Me().ID() != "me" {
			t.Errorf("ID %q", client.Me().ID())
		}
	})
}

func TestFailingTokenSourceNeverAnswersStale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fail := false
		f := &fakeHTTP{}
		f.push(cached(checkBody("me", "f"), `"v1"`, "no-cache"))
		client, _ := NewClient(TokenFunc(func(context.Context) (string, error) {
			if fail {
				return "", errors.New("offline")
			}
			return tokenWith(time.Now(), time.Now().Add(60*time.Second), "s"), nil
		}), WithHTTPClient(f.client()), WithMaxRetries(0))
		defer client.Close()
		_, _ = client.Me().Check(ctx, Key("f"), nil)
		fail = true
		time.Sleep(45 * time.Second)
		var reported bool
		client.c.e.onError = func(error) { reported = true }
		_, err := client.Me().Check(ctx, Key("f"), nil)
		var tokenErr *TokenError
		if !errors.As(err, &tokenErr) || tokenErr.Unwrap().Error() != "offline" {
			t.Fatalf("got %v", err)
		}
		if client.Me().IsEntitled(ctx, Key("f"), true, nil) != true || !reported {
			t.Error("IsEntitled answers its default")
		}
	})
}

func TestAnyAnswerEndsTheOutageWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeHTTP{}
		f.push(cached(checkBody("u", "f"), `"v1"`, "private, no-cache"), fakeAnswer{status: 503})
		cu := testServer(t, f).Customer("u")
		_, _ = cu.Check(ctx, Key("f"), nil)
		if c, err := cu.Check(ctx, Key("f"), nil); err != nil || !c.Stale {
			t.Fatalf("the window did not open: %v %+v", err, c)
		}
		f.push(fakeAnswer{status: 200, body: usageBody("recorded", nil, time.Time{}, 1)})
		if _, err := cu.RecordUsage(ctx, Metered("f"), 1, "k1", nil); err != nil {
			t.Fatal(err)
		}
		f.push(cached(checkBody("u", "f"), `"v2"`, "private, no-cache"))
		if c, err := cu.Check(ctx, Key("f"), nil); err != nil || c.Stale || f.count() != 4 {
			t.Fatalf("a successful write leaves the window open: %v %+v", err, c)
		}
	})
}

func TestHeaderNumbersSaturate(t *testing.T) {
	long := strings.Repeat("9", 400)
	if d, ok := secondsOf(long); !ok || d != maxDuration {
		t.Errorf("seconds: %v %v", d, ok)
	}
	if d, ok := maxAgeOf("private, max-age=" + long); !ok || d != maxDuration {
		t.Errorf("max-age: %v %v", d, ok)
	}
	if d := retryAfterOf(long, time.Now()); d == nil || *d != time.Duration(1<<63-1) {
		t.Errorf("Retry-After: %v", d)
	}
	f := &fakeHTTP{}
	f.push(fakeAnswer{status: 200, body: checkBody("u", "f"), header: map[string]string{"Cache-Control": "max-age=" + long, "Age": long}},
		fakeAnswer{status: 200, body: checkBody("u", "f"), header: map[string]string{"Cache-Control": "max-age=" + long}})
	cu := testServer(t, f).Customer("u")
	for range 3 {
		if _, err := cu.Check(ctx, Key("f"), nil); err != nil {
			t.Fatal(err)
		}
	}
	if f.count() != 2 {
		t.Errorf("an answer as old as its max-age is stale, a fresh one is kept: %d requests", f.count())
	}
}

func TestCustomerForgottenInFlightIsRevalidated(t *testing.T) {
	g := newGenerations(2)
	at := time.Now()
	g.bump("x", at)
	tracked := g.mark("x")
	untracked := g.mark("y")
	g.bump("a", at)
	g.bump("b", at)
	if !g.moved("x", tracked) {
		t.Error("a tracked customer forgotten in flight counts as moved")
	}
	if !g.moved("y", untracked) {
		t.Error("a customer untracked at the mark counts as moved after any forgetting")
	}
	if g.moved("b", g.mark("b")) {
		t.Error("no write since the mark")
	}
}

func TestWritesAndReadsOrderWithinOneClockTick(t *testing.T) {
	f := &fakeHTTP{handle: func(r *http.Request, _ string) fakeAnswer {
		if r.Method == "GET" {
			return cached(checkBody("u", "f"), `"v1"`, "max-age=600")
		}
		return fakeAnswer{status: 200, body: `{"changed":true}`}
	}}
	srv := testServer(t, f)
	frozen := time.Now()
	srv.c.e.now = func() time.Time { return frozen }
	cu := srv.Customer("u")
	_, _ = cu.Check(ctx, Key("f"), nil)
	_, _ = cu.SyncBilling(ctx)
	_, _ = cu.Check(ctx, Key("f"), nil)
	_, _ = cu.Check(ctx, Key("f"), nil)
	if f.count() != 3 {
		t.Fatalf("an answer read after a write in the same clock tick is kept fresh: %d requests", f.count())
	}
	_, _ = cu.SyncBilling(ctx)
	_, _ = cu.Check(ctx, Key("f"), nil)
	if f.count() != 5 {
		t.Fatalf("a write in the same clock tick still revalidates: %d requests", f.count())
	}
}

func TestAnAnswerAsOldAsItsMaxAgeIsStaleWithinOneClockTick(t *testing.T) {
	f := &fakeHTTP{}
	f.push(fakeAnswer{status: 200, body: checkBody("u", "f"), header: map[string]string{"Cache-Control": "max-age=60", "Age": "60"}},
		fakeAnswer{status: 200, body: checkBody("u", "f"), header: map[string]string{"Cache-Control": "max-age=60", "Age": "60"}})
	srv := testServer(t, f)
	frozen := time.Now()
	srv.c.e.now = func() time.Time { return frozen }
	cu := srv.Customer("u")
	_, _ = cu.SyncBilling(ctx)
	_, _ = cu.Check(ctx, Key("f"), nil)
	_, _ = cu.Check(ctx, Key("f"), nil)
	if f.count() != 3 {
		t.Fatalf("an answer as old as its max-age was answered fresh: %d requests", f.count())
	}
}

func TestCustomerEvictedWhileTheirReadIsInFlightIsRevalidated(t *testing.T) {
	inFlight, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f := &fakeHTTP{handle: func(r *http.Request, _ string) fakeAnswer {
		if r.Method == "GET" {
			once.Do(func() {
				close(inFlight)
				<-release
			})
			return cached(checkBody("u", "f"), `"v1"`, "max-age=600")
		}
		return fakeAnswer{status: 200, body: `{"changed":true}`}
	}}
	srv := testServer(t, f)
	frozen := time.Now()
	srv.c.e.now = func() time.Time { return frozen }
	srv.c.e.gens = newGenerations(2)
	done := make(chan error)
	go func() {
		_, err := srv.Customer("u").Check(ctx, Key("f"), nil)
		done <- err
	}()
	<-inFlight
	for _, id := range []string{"u", "a", "b"} {
		if _, err := srv.Customer(id).SyncBilling(ctx); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	before := f.count()
	if _, err := srv.Customer("u").Check(ctx, Key("f"), nil); err != nil {
		t.Fatal(err)
	}
	if f.count() != before+1 {
		t.Fatal("an answer whose customer was evicted while it was in flight was kept fresh")
	}
}
