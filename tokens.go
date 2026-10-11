package entitler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TokenSource answers a token for an in-app client: a customer token your server minted, or an
// identity token from a sign-in provider. The client asks it for the first request, again when
// the kept token expires within 60 seconds (or half its lifetime, when shorter), and once after a
// 401. Concurrent requests share one pending call, which has its own deadline, the client's
// timeout. Its errors are kept as they are as a [TokenError]'s cause, so what they hold is outside
// the SDK's control: keep tokens out of them.
type TokenSource interface {
	// Token answers a fresh token.
	Token(ctx context.Context) (string, error)
}

// TokenFunc adapts a function to a [TokenSource].
type TokenFunc func(ctx context.Context) (string, error)

// Token calls f.
func (f TokenFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

type staticToken string

func (t staticToken) Token(context.Context) (string, error) { return string(t), nil }

// StaticToken answers a [TokenSource] holding one token, which never refreshes: a 401 fails the call.
func StaticToken(token string) TokenSource { return staticToken(token) }

const refreshBefore = 60 * time.Second

// tokens keeps an in-app client's token and refreshes it through one shared call at a time.
type tokens struct {
	source  TokenSource
	static  bool
	timeout time.Duration
	closing context.Context
	now     func() time.Time

	mu       sync.Mutex
	current  string
	expires  time.Time
	issued   time.Time
	pending  *refresh
	received bool
}

type refresh struct {
	done  chan struct{}
	token string
	err   error
}

func (t *tokens) get(ctx context.Context) (string, error) {
	t.mu.Lock()
	if t.received && (t.static || !t.expiresSoon()) {
		token := t.current
		t.mu.Unlock()
		return token, nil
	}
	return t.refreshLocked(ctx)
}

// after401 answers the token to retry a 401 with: the current one when the request used an older
// one, else a fresh one. ok is false for a token that cannot be refreshed.
func (t *tokens) after401(ctx context.Context, used string) (token string, ok bool, err error) {
	if t.static {
		return "", false, nil
	}
	t.mu.Lock()
	if t.received && t.current != used {
		token := t.current
		t.mu.Unlock()
		return token, true, nil
	}
	token, err = t.refreshLocked(ctx)
	return token, err == nil, err
}

func (t *tokens) expiresSoon() bool {
	lifetime := t.expires.Sub(t.issued)
	return t.expires.Sub(t.now()) <= min(refreshBefore, lifetime/2)
}

// refreshLocked joins the pending refresh, or starts one, and waits for it or the caller's
// context. The refresh belongs to the client, so a caller giving up never cancels it.
func (t *tokens) refreshLocked(ctx context.Context) (string, error) {
	p := t.pending
	if p == nil {
		p = &refresh{done: make(chan struct{})}
		t.pending = p
		go t.ask(p)
	}
	t.mu.Unlock()
	select {
	case <-p.done:
		return p.token, p.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

var errSourceTimeout = errors.New("token source timed out")

func (t *tokens) ask(p *refresh) {
	ctx, cancel := context.WithTimeoutCause(t.closing, clampDuration(t.timeout), errSourceTimeout)
	defer cancel()
	type answer struct {
		token string
		err   error
	}
	answered := make(chan answer, 1)
	go func() {
		token, err := t.source.Token(ctx)
		answered <- answer{token, err}
	}()
	var a answer
	select {
	case a = <-answered:
	case <-ctx.Done():
	}
	token, issued, expires, err := t.check(ctx, a.token, a.err)
	t.mu.Lock()
	t.pending = nil
	if err == nil {
		t.current, t.issued, t.expires, t.received = token, issued, expires, true
	}
	t.mu.Unlock()
	p.token, p.err = token, err
	close(p.done)
}

func (t *tokens) check(ctx context.Context, token string, err error) (string, time.Time, time.Time, error) {
	if t.closing.Err() != nil {
		return "", time.Time{}, time.Time{}, ErrClosed
	}
	if context.Cause(ctx) == errSourceTimeout {
		return "", time.Time{}, time.Time{}, &TokenError{message: fmt.Sprintf("the token source did not answer within %s", t.timeout)}
	}
	if err != nil {
		return "", time.Time{}, time.Time{}, &TokenError{message: "the token source failed", cause: err}
	}
	token = trimCredential(token)
	if token == "" {
		return "", time.Time{}, time.Time{}, &TokenError{message: "the token source answered a blank token"}
	}
	if t.static {
		return token, time.Time{}, time.Time{}, nil
	}
	iat, exp, ok := readClaims(token)
	if !ok {
		return "", time.Time{}, time.Time{}, &TokenError{message: "the token source answered a token that is not a readable JWT"}
	}
	issued := t.now()
	if iat != nil {
		issued = time.Unix(*iat, 0)
	}
	return token, issued, time.Unix(exp, 0), nil
}

// readClaims reads a JWT's iat and exp without verifying it.
func readClaims(token string) (iat *int64, exp int64, ok bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, 0, false
	}
	v, err := parseJSON(payload)
	claims, isObject := v.(map[string]any)
	if err != nil || !isObject {
		return nil, 0, false
	}
	exp, ok = jwtInt(claims["exp"])
	if !ok {
		return nil, 0, false
	}
	if raw, present := claims["iat"]; present {
		i, isInt := jwtInt(raw)
		if !isInt || i >= exp {
			return nil, 0, false
		}
		iat = &i
	}
	return iat, exp, true
}

func jwtInt(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil {
		f, ferr := n.Float64()
		if ferr != nil || f != float64(int64(f)) {
			return 0, false
		}
		i = int64(f)
	}
	if i > maxWhole || i < -maxWhole {
		return 0, false
	}
	return i, true
}
