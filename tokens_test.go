package entitler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func checkAnswers(f *fakeHTTP) {
	f.handle = func(*http.Request, string) fakeAnswer { return fakeAnswer{status: 200, body: checkBody("me", "f")} }
}

func TestTokenRefreshBeforeExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		f := &fakeHTTP{}
		checkAnswers(f)
		client, _ := NewClient(TokenFunc(func(context.Context) (string, error) {
			calls.Add(1)
			now := time.Now()
			return tokenWith(now, now.Add(time.Hour), "s"), nil
		}), WithHTTPClient(f.client()), WithoutCache())
		defer client.Close()
		me := client.Me()
		_, _ = me.Check(ctx, Key("f"), nil)
		time.Sleep(58 * time.Minute)
		_, _ = me.Check(ctx, Key("f"), nil)
		if calls.Load() != 1 {
			t.Fatalf("asked %d times before the window", calls.Load())
		}
		time.Sleep(time.Minute)
		_, _ = me.Check(ctx, Key("f"), nil)
		if calls.Load() != 2 {
			t.Fatalf("asked %d times within 60 s of expiry", calls.Load())
		}
	})
}

func TestShortTokenIsUsedForTheRequestThatFetchedIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		f := &fakeHTTP{}
		checkAnswers(f)
		client, _ := NewClient(TokenFunc(func(context.Context) (string, error) {
			calls.Add(1)
			now := time.Now()
			return tokenWith(now.Add(-time.Hour), now.Add(10*time.Second), "s"), nil
		}), WithHTTPClient(f.client()), WithoutCache())
		defer client.Close()
		for range 3 {
			if _, err := client.Me().Check(ctx, Key("f"), nil); err != nil {
				t.Fatal(err)
			}
		}
		if calls.Load() != 3 || f.count() != 3 {
			t.Fatalf("%d calls for %d requests", calls.Load(), f.count())
		}
	})
}

func TestConcurrentRequestsShareOneRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		f := &fakeHTTP{}
		checkAnswers(f)
		client, _ := NewClient(TokenFunc(func(context.Context) (string, error) {
			calls.Add(1)
			time.Sleep(time.Second)
			return tokenFor(time.Now().Add(time.Hour)), nil
		}), WithHTTPClient(f.client()), WithoutCache())
		defer client.Close()
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() { _, _ = client.Me().Check(ctx, Key("f"), nil) })
		}
		wg.Wait()
		if calls.Load() != 1 || f.count() != 10 {
			t.Fatalf("%d calls", calls.Load())
		}
	})
}

func TestWaiterCancellingNeverCancelsTheRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		f := &fakeHTTP{}
		checkAnswers(f)
		client, _ := NewClient(TokenFunc(func(ctx context.Context) (string, error) {
			calls.Add(1)
			select {
			case <-time.After(2 * time.Second):
				return tokenFor(time.Now().Add(time.Hour)), nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}), WithHTTPClient(f.client()), WithoutCache())
		defer client.Close()
		short, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		done := make(chan error)
		go func() { _, err := client.Me().Check(short, Key("f"), nil); done <- err }()
		go func() { _, err := client.Me().Check(ctx, Key("f"), nil); done <- err }()
		first, second := <-done, <-done
		if !errors.Is(first, context.DeadlineExceeded) && !errors.Is(second, context.DeadlineExceeded) {
			t.Fatalf("%v / %v", first, second)
		}
		if first != nil && second != nil {
			t.Fatal("the other waiter failed too")
		}
		if calls.Load() != 1 {
			t.Fatalf("%d calls", calls.Load())
		}
	})
}

func TestFailedRefreshIsNotKept(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		f := &fakeHTTP{}
		checkAnswers(f)
		client, _ := NewClient(TokenFunc(func(context.Context) (string, error) {
			if calls.Add(1) == 1 {
				return "", errors.New("offline")
			}
			return tokenFor(time.Now().Add(time.Hour)), nil
		}), WithHTTPClient(f.client()), WithoutCache())
		defer client.Close()
		if _, err := client.Me().Check(ctx, Key("f"), nil); !IsUnreachable(err) {
			t.Fatalf("got %v", err)
		}
		if _, err := client.Me().Check(ctx, Key("f"), nil); err != nil {
			t.Fatalf("the next request asks again: %v", err)
		}
	})
}

func TestHangingSourceTimesOutAsATokenError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ignoresItsContext := make(chan struct{})
		defer close(ignoresItsContext)
		client, _ := NewClient(TokenFunc(func(context.Context) (string, error) {
			<-ignoresItsContext
			return "", nil
		}), WithHTTPClient((&fakeHTTP{}).client()), WithTimeout(3*time.Second))
		defer client.Close()
		started := time.Now()
		_, err := client.Me().Check(ctx, Key("f"), nil)
		var tokenErr *TokenError
		if !errors.As(err, &tokenErr) || time.Since(started) != 3*time.Second || !strings.Contains(err.Error(), "did not answer within 3s") {
			t.Fatalf("got %v after %s", err, time.Since(started))
		}
	})
}

func TestUnusableTokens(t *testing.T) {
	for name, token := range map[string]string{
		"blank":      "  ",
		"two parts":  "a.b",
		"not base64": "a.!!!.c",
		"no exp":     "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1In0.s",
		"iat after":  tokenWith(time.Unix(2000, 0), time.Unix(1000, 0), "s"),
		"exp string": "eyJhbGciOiJIUzI1NiJ9.eyJleHAiOiIxIn0.s",
		"array":      "eyJhbGciOiJIUzI1NiJ9.WzFd.s",
	} {
		client, _ := NewClient(TokenFunc(func(context.Context) (string, error) { return token, nil }),
			WithHTTPClient((&fakeHTTP{}).client()))
		_, err := client.Me().Check(ctx, Key("f"), nil)
		var tokenErr *TokenError
		if !errors.As(err, &tokenErr) || strings.Contains(err.Error(), "eyJ") {
			t.Errorf("%s: %v", name, err)
		}
		_ = client.Close()
	}
	if _, exp, ok := readClaims("eyJhbGciOiJIUzI1NiJ9.eyJleHAiOjEuNWU5fQ.s"); !ok || exp != 1500000000 {
		t.Error("a whole number written with an exponent is an integer")
	}
}

func TestUnauthorisedRetriesOnceWithAFreshToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		f := &fakeHTTP{}
		f.push(fakeAnswer{status: 503}, fakeAnswer{status: 401}, fakeAnswer{status: 200, body: checkBody("me", "f")})
		client, _ := NewClient(TokenFunc(func(context.Context) (string, error) {
			n := calls.Add(1)
			return tokenWith(time.Now(), time.Now().Add(time.Hour), "sig"+string('0'+rune(n))), nil
		}), WithHTTPClient(f.client()), WithoutCache())
		defer client.Close()
		if _, err := client.Me().Check(ctx, Key("f"), nil); err != nil {
			t.Fatal(err)
		}
		seen := f.seen()
		if calls.Load() != 2 || len(seen) != 3 || seen[1].header.Get("Authorization") == seen[2].header.Get("Authorization") {
			t.Fatalf("%d calls, %d attempts", calls.Load(), len(seen))
		}
	})
}

func TestUnauthorisedWithAnOlderTokenUsesTheCurrentOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		f := &fakeHTTP{}
		release := make(chan struct{})
		f.handle = func(r *http.Request, _ string) fakeAnswer {
			if strings.HasSuffix(r.Header.Get("Authorization"), "sig1") {
				<-release
				return fakeAnswer{status: 401}
			}
			return fakeAnswer{status: 200, body: checkBody("me", "f")}
		}
		client, _ := NewClient(TokenFunc(func(context.Context) (string, error) {
			n := calls.Add(1)
			return tokenWith(time.Now(), time.Now().Add(time.Hour), "sig"+string('0'+rune(n))), nil
		}), WithHTTPClient(f.client()), WithoutCache())
		defer client.Close()
		done := make(chan error, 2)
		go func() { _, err := client.Me().Check(ctx, Key("f"), nil); done <- err }()
		synctest.Wait()
		_, _, _ = client.c.e.creds.token.after401(ctx, client.c.e.creds.token.current)
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 2 {
			t.Fatalf("%d calls: the stale 401 asked again", calls.Load())
		}
	})
}

func TestForbiddenCredentialIsNeverRefreshed(t *testing.T) {
	var calls atomic.Int32
	f := &fakeHTTP{}
	f.push(fakeAnswer{status: 403, body: `{"error":{"code":"credential_not_allowed","message":"No."}}`})
	client, _ := NewClient(TokenFunc(func(context.Context) (string, error) {
		calls.Add(1)
		return tokenFor(time.Now().Add(time.Hour)), nil
	}), WithHTTPClient(f.client()))
	defer client.Close()
	_, err := client.Me().Check(ctx, Key("f"), nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != CodeCredentialNotAllowed || calls.Load() != 1 || f.count() != 1 {
		t.Fatalf("%v, %d calls", err, calls.Load())
	}
}

func TestSnapshotKeysNeedNoCredential(t *testing.T) {
	f := &fakeHTTP{}
	f.push(fakeAnswer{status: 200, body: `{"keys":[]}`})
	client, _ := NewIdentityClient("ent_pk_test_x", TokenFunc(func(context.Context) (string, error) {
		return "", errors.New("signed out")
	}), WithHTTPClient(f.client()), WithVisitor("visitor_0123456789abcdef"))
	defer client.Close()
	if _, err := client.SnapshotKeys(ctx); err != nil {
		t.Fatal(err)
	}
	h := f.seen()[0].header
	for _, name := range []string{"Authorization", "Entitler-Identity-Token", "Entitler-Visitor", "Entitler-As-Of"} {
		if h.Get(name) != "" {
			t.Errorf("sent %s", name)
		}
	}
}

func TestClosingCancelsThePendingRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, _ := NewClient(TokenFunc(func(ctx context.Context) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}), WithHTTPClient((&fakeHTTP{}).client()), WithTimeout(time.Hour))
		done := make(chan error)
		go func() { _, err := client.Me().Check(ctx, Key("f"), nil); done <- err }()
		time.Sleep(time.Second)
		_ = client.Close()
		if err := <-done; !errors.Is(err, ErrClosed) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestStaticTokenNeverRefreshes(t *testing.T) {
	f := &fakeHTTP{}
	f.push(fakeAnswer{status: 401})
	client, err := NewClient(StaticToken("  not-a-jwt\n"), WithHTTPClient(f.client()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.Me().Check(ctx, Key("f"), nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || f.count() != 1 || f.seen()[0].header.Get("Authorization") != "Bearer not-a-jwt" {
		t.Fatal(err)
	}
	if s, _ := StaticToken("x").Token(ctx); s != "x" {
		t.Error(s)
	}
}

func TestCredentialsAHeaderCannotCarry(t *testing.T) {
	bad := map[string]string{"space": " ", "tab": "\t", "non-ASCII": "é", "control": "\x01"}
	for name, c := range bad {
		if _, err := NewServer("ent_test_a" + c + "b"); err == nil || err.Error() != "provide an Entitler API key from the dashboard" {
			t.Errorf("server key with a %s: %v", name, err)
		}
		if _, err := NewPublishableClient("ent_pk_test_a" + c + "b"); err == nil || err.Error() != "provide an Entitler API key from the dashboard" {
			t.Errorf("publishable key with a %s: %v", name, err)
		}
		if _, err := NewIdentityClient("ent_pk_test_a"+c+"b", StaticToken(tokenFor(time.Now().Add(time.Hour)))); err == nil || err.Error() != "provide an Entitler API key from the dashboard" {
			t.Errorf("identity client's key with a %s: %v", name, err)
		}
		if _, err := NewClient(StaticToken("a" + c + "b")); err == nil || err.Error() != "provide a customer token minted by your server" {
			t.Errorf("static token with a %s: %v", name, err)
		}
		if _, err := NewIdentityClient("ent_pk_test_k", StaticToken("a"+c+"b")); err == nil || err.Error() != "provide the identity token your sign-in provider issued" {
			t.Errorf("static identity token with a %s: %v", name, err)
		}

		f := &fakeHTTP{}
		token := tokenFor(time.Now().Add(time.Hour))
		answer := token[:10] + c + token[10:]
		client, err := NewClient(TokenFunc(func(context.Context) (string, error) { return answer, nil }), WithHTTPClient(f.client()))
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Me().Check(ctx, Key("f"), nil)
		var tokenErr *TokenError
		if !errors.As(err, &tokenErr) || err.Error() != "the token source answered a token that is not a readable JWT" || f.count() != 0 {
			t.Errorf("provider answer with a %s: %v after %d requests", name, err, f.count())
		}
		_ = client.Close()
	}
}
