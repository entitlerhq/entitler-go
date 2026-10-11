package entitler

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var userAgent = "entitler-go/" + Version + " go/" + strings.TrimPrefix(runtime.Version(), "go")

// principal kinds, as cache keys name them.
const (
	principalKey      = "key"
	principalCustomer = "customer-token"
	principalIdentity = "identity"
)

// authorisation is a request's credential headers and the credential its cached answers are kept under.
type authorisation struct {
	kind       string
	credential string
	bearer     string
	identity   string
}

// credentials sign a client's requests.
type credentials struct {
	kind  string
	key   string
	token *tokens
}

func (c *credentials) authorise(ctx context.Context) (authorisation, error) {
	switch c.kind {
	case principalCustomer:
		token, err := c.token.get(ctx)
		if err != nil {
			return authorisation{}, err
		}
		return authorisation{kind: c.kind, credential: token, bearer: token}, nil
	case principalIdentity:
		token, err := c.token.get(ctx)
		if err != nil {
			return authorisation{}, err
		}
		return authorisation{kind: c.kind, credential: c.key + "\n" + token, bearer: c.key, identity: token}, nil
	}
	return authorisation{kind: c.kind, credential: c.key, bearer: c.key}, nil
}

// after401 answers the authorisation to retry a 401 with, and false when it cannot be refreshed.
func (c *credentials) after401(ctx context.Context, used authorisation) (authorisation, bool, error) {
	if c.token == nil {
		return authorisation{}, false, nil
	}
	usedToken := used.bearer
	if c.kind == principalIdentity {
		usedToken = used.identity
	}
	token, ok, err := c.token.after401(ctx, usedToken)
	if err != nil || !ok {
		return authorisation{}, false, err
	}
	if c.kind == principalIdentity {
		return authorisation{kind: c.kind, credential: c.key + "\n" + token, bearer: c.key, identity: token}, true, nil
	}
	return authorisation{kind: c.kind, credential: token, bearer: token}, true, nil
}

// engine is what a client and every client derived from it with With share: the HTTP client,
// the cache, write generations, the outage window, tokens and the closed state.
type engine struct {
	http          *http.Client
	ownTransport  *http.Transport
	baseURL       string
	cache         Cache
	memory        *MemoryCache
	gens          *generations
	outage        outage
	staleFor      time.Duration
	maxRetries    int
	maxRetryDelay time.Duration
	onError       func(error)
	creds         *credentials
	visitor       string
	inApp         bool
	description   string

	closing   context.Context
	close     context.CancelFunc
	closed    atomic.Bool
	closeOnce sync.Once

	now     func() time.Time
	jitter  func(limit time.Duration) time.Duration
	keySeen func(key string)
}

func newEngine(s settings, creds *credentials, inApp bool, description string) *engine {
	e := &engine{
		baseURL:       s.baseURL,
		staleFor:      s.staleFor,
		maxRetries:    s.maxRetries,
		maxRetryDelay: s.maxRetryDelay,
		onError:       s.onError,
		creds:         creds,
		visitor:       s.visitor,
		inApp:         inApp,
		description:   description,
		now:           time.Now,
		jitter: func(limit time.Duration) time.Duration {
			return time.Duration(rand.Int64N(int64(limit) + 1)) //nolint:gosec // backoff jitter needs no cryptographic randomness
		},
	}
	e.closing, e.close = context.WithCancel(context.Background())
	if s.httpClient != nil {
		client := *s.httpClient
		e.http = &client
	} else {
		e.ownTransport = http.DefaultTransport.(*http.Transport).Clone()
		e.http = &http.Client{Transport: e.ownTransport}
	}
	e.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	switch {
	case s.store != nil:
		e.cache = s.store
	case s.cacheSize > 0:
		e.memory = NewMemoryCache(s.cacheSize)
		e.cache = e.memory
	}
	if e.cache != nil {
		e.gens = newGenerations(trackedCustomers)
	}
	if creds.token != nil {
		creds.token.closing = e.closing
		creds.token.timeout = s.timeout
		creds.token.now = func() time.Time { return e.now() }
	}
	return e
}

func (e *engine) shutdown() {
	e.closeOnce.Do(func() {
		e.closed.Store(true)
		e.close()
		if e.memory != nil {
			e.memory.clear()
		}
		if e.ownTransport != nil {
			e.ownTransport.CloseIdleConnections()
		}
	})
}

// report passes an absorbed error to onError, which can never change a call's outcome.
func (e *engine) report(err error) {
	if e.onError == nil || err == nil {
		return
	}
	defer func() { _ = recover() }()
	e.onError(err)
}

// caller is a client's view of its engine: the shared engine plus its own per-call settings.
type caller struct {
	e       *engine
	timeout time.Duration
	err     error
}

func (c caller) with(opts []Option) caller {
	s := settings{timeout: c.timeout, perCall: true}
	for _, o := range opts {
		if o != nil {
			o.applyServer(&s)
		}
	}
	derived := caller{e: c.e, timeout: s.timeout, err: c.err}
	if derived.err == nil {
		derived.err = s.err
	}
	return derived
}

// request is one call.
type request struct {
	method     string
	path       string
	query      []string
	body       []byte
	key        string
	visitor    string
	asOf       string
	open       bool
	cached     bool
	revalidate bool
	customer   string
	writes     []string
	empty      bool
}

type received struct {
	status int
	header http.Header
	body   []byte
}

type meta struct {
	stale    bool
	replayed bool
}

func (r *request) url(base string) string {
	u := base + r.path
	if len(r.query) > 0 {
		u += "?" + strings.Join(r.query, "&")
	}
	return u
}

// begin checks the call can start and joins the caller's context with the client's closing.
func (c caller) begin(ctx context.Context) (context.Context, func(), error) {
	if c.e.closed.Load() {
		return nil, nil, ErrClosed
	}
	if c.err != nil {
		return nil, nil, c.err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	joined, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.e.closing, cancel)
	return joined, func() { stop(); cancel() }, nil
}

// settle maps a failure once the call ends: the client closing, then the caller's own context.
func (c caller) settle(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if c.e.closed.Load() {
		return ErrClosed
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// fetch makes a call and decodes its answer; a read marked cached goes through the answer cache.
func fetch[T any](ctx context.Context, c caller, r *request, decode func(object) T) (T, meta, error) {
	return fetchBody(ctx, c, r, func(body []byte) (T, error) { return decodeBody(body, decode) })
}

// fetchBody is fetch with a decoder of the whole body.
func fetchBody[T any](ctx context.Context, c caller, r *request, decode func([]byte) (T, error)) (T, meta, error) {
	var zero T
	joined, done, err := c.begin(ctx)
	if err != nil {
		return zero, meta{}, err
	}
	defer done()
	out, m, err := c.fetchJoined(joined, r, func(body []byte) (any, error) { return decode(body) })
	if err != nil {
		return zero, meta{}, c.settle(ctx, err)
	}
	if out == nil {
		return zero, m, nil
	}
	return out.(T), m, nil
}

func (c caller) fetchJoined(ctx context.Context, r *request, decode decoder) (any, meta, error) {
	e := c.e
	if e.gens != nil {
		for _, customer := range r.writes {
			e.gens.bump(customer, e.now())
		}
	}
	auth := authorisation{}
	if !r.open {
		var err error
		if auth, err = e.creds.authorise(ctx); err != nil {
			return nil, meta{}, err
		}
	}
	if r.cached && e.cache != nil {
		return c.cachedRead(ctx, r, auth, decode)
	}
	got, _, err := c.exchange(ctx, r, auth, "")
	if err != nil {
		return nil, meta{}, err
	}
	if got.status == http.StatusNotModified {
		return nil, meta{}, apiErrorOf(got, r.key, e.now())
	}
	m := meta{replayed: got.header.Get("Idempotent-Replayed") == "true"}
	if r.empty && len(got.body) == 0 {
		return nil, m, nil
	}
	out, err := decodeWith(got.body, decode)
	if err != nil {
		return nil, meta{}, invalidResponse(got, r.key, err)
	}
	return out, m, nil
}

type decoder func(body []byte) (any, error)

func decodeWith(body []byte, decode decoder) (any, error) { return decode(body) }

func invalidResponse(got received, key string, cause error) error {
	return &APIError{
		Status:         got.status,
		Code:           CodeInvalidResponse,
		Message:        "Entitler sent an answer this SDK cannot read",
		RequestID:      got.header.Get("X-Request-Id"),
		IdempotencyKey: key,
		cause:          cause,
	}
}

func (c caller) cacheKey(r *request, auth authorisation, u string) string {
	asOf, visitor := any(nil), any(nil)
	if r.asOf != "" {
		asOf = r.asOf
	}
	if v := c.visitorOf(r); v != "" {
		visitor = v
	}
	return sha256Hex(serialise([]any{
		"entitler-cache-v1", r.method, u, auth.kind, sha256Hex(auth.credential), asOf, visitor,
	}))
}

func (c caller) visitorOf(r *request) string {
	if r.open {
		return ""
	}
	if r.visitor != "" {
		return r.visitor
	}
	return c.e.visitor
}

func (c caller) cachedRead(ctx context.Context, r *request, auth authorisation, decode decoder) (any, meta, error) {
	e := c.e
	key := c.cacheKey(r, auth, r.url(e.baseURL))
	if e.keySeen != nil {
		e.keySeen(key)
	}
	started := e.now()
	mark := e.gens.mark(r.customer)
	kept, ok := c.cacheGet(ctx, key)
	usable := ok && e.now().Sub(kept.ReceivedAt) < e.staleFor
	if ok && !r.revalidate && c.fresh(kept, r.customer) {
		if out, err := decodeWith([]byte(kept.Body), decode); err == nil {
			return out, meta{}, nil
		}
	}
	stale, probe := e.outage.enter(e.now(), usable)
	if stale {
		if out, err := decodeWith([]byte(kept.Body), decode); err == nil {
			return out, meta{stale: true}, nil
		}
	}
	defer e.outage.leave(probe)
	etag := ""
	if ok {
		etag = kept.ETag
	}
	got, used, err := c.exchange(ctx, r, auth, etag)
	var out any
	if err == nil {
		switch {
		case got.status == http.StatusNotModified && !ok:
			return nil, meta{}, apiErrorOf(got, "", e.now())
		case got.status == http.StatusNotModified:
			out, err = decodeWith([]byte(kept.Body), decode)
		default:
			out, err = decodeWith(got.body, decode)
		}
		if err != nil {
			err = invalidResponse(got, "", err)
		}
	}
	if err != nil {
		if ctx.Err() != nil || !unreachableAnswer(err) {
			return nil, meta{}, err
		}
		var apiErr *APIError
		var retryAfter *time.Duration
		if errors.As(err, &apiErr) {
			retryAfter = apiErr.RetryAfter
		}
		e.outage.fail(e.now(), retryAfter, e.staleFor)
		if usable && used.credential == auth.credential {
			if out, derr := decodeWith([]byte(kept.Body), decode); derr == nil {
				e.report(err)
				return out, meta{stale: true}, nil
			}
		}
		return nil, meta{}, err
	}
	receivedAt := e.now()
	if e.gens.moved(r.customer, mark) {
		receivedAt = started
	}
	entry := CacheEntry{V: 1, Body: string(got.body), ETag: got.header.Get("ETag"),
		CacheControl: got.header.Get("Cache-Control"), Age: ageOf(got.header), ReceivedAt: receivedAt}
	if got.status == http.StatusNotModified {
		entry.Body = kept.Body
		if entry.ETag == "" {
			entry.ETag = kept.ETag
		}
		if _, sent := got.header["Cache-Control"]; !sent {
			entry.CacheControl = kept.CacheControl
			if _, sentAge := got.header["Age"]; !sentAge {
				entry.Age = kept.Age
			}
		}
	}
	maxAge, hasMaxAge := maxAgeOf(entry.CacheControl)
	if !directive(entry.CacheControl, "no-store") && (entry.ETag != "" || hasMaxAge) {
		if err := e.cache.Set(ctx, key, entry, clampDuration(e.staleFor+maxAge)); err != nil {
			e.report(err)
		}
	}
	return out, meta{}, nil
}

// unreachableAnswer is the unreachable rule for a request that was sent: a token source's own
// failure never lets an older credential's answer stand in.
func unreachableAnswer(err error) bool {
	var token *TokenError
	return IsUnreachable(err) && !errors.As(err, &token)
}

func (c caller) cacheGet(ctx context.Context, key string) (CacheEntry, bool) {
	entry, ok, err := c.e.cache.Get(ctx, key)
	if err != nil {
		c.e.report(err)
		return CacheEntry{}, false
	}
	if !ok || entry.V != 1 || entry.Body == "" || entry.ReceivedAt.IsZero() {
		return CacheEntry{}, false
	}
	return entry, true
}

func (c caller) fresh(entry CacheEntry, customer string) bool {
	maxAge, ok := maxAgeOf(entry.CacheControl)
	if !ok || directive(entry.CacheControl, "no-cache") {
		return false
	}
	age := c.e.now().Sub(entry.ReceivedAt) + clampDuration(time.Duration(entry.Age)*time.Second)
	if age >= maxAge {
		return false
	}
	return c.e.gens.keptAfterWrites(customer, entry.ReceivedAt)
}

func ageOf(h http.Header) int64 {
	if age, ok := secondsOf(strings.TrimSpace(h.Get("Age"))); ok {
		return int64(age / time.Second)
	}
	return 0
}

var retried = map[int]bool{408: true, 429: true, 500: true, 502: true, 503: true, 504: true}

// exchange sends a request with retries, and the one retry after a 401 with a refreshed token. It
// answers the last attempt's answer, a 2xx or a 304, and the authorisation it was sent with.
func (c caller) exchange(ctx context.Context, r *request, auth authorisation, etag string) (received, authorisation, error) {
	e := c.e
	refreshed := false
	for retry := 0; ; {
		got, err := c.attempt(ctx, r, auth, etag)
		var failure error
		var retryAfter *time.Duration
		if err == nil {
			if got.status < 300 || got.status == http.StatusNotModified {
				e.outage.recover()
				return got, auth, nil
			}
			apiErr := apiErrorOf(got, r.key, e.now())
			if got.status < 400 {
				return received{}, auth, apiErr
			}
			if got.status == http.StatusUnauthorized && !refreshed && !r.open {
				refreshed = true
				next, ok, rerr := e.creds.after401(ctx, auth)
				if rerr != nil {
					return received{}, auth, rerr
				}
				if ok {
					auth = next
					continue
				}
			}
			if !retried[got.status] {
				return received{}, auth, apiErr
			}
			failure, retryAfter = apiErr, apiErr.RetryAfter
		} else {
			var conn *ConnectionError
			var timeout *TimeoutError
			if !errors.As(err, &conn) && !errors.As(err, &timeout) {
				return received{}, auth, err
			}
			failure = err
		}
		if retryAfter != nil && *retryAfter > e.maxRetryDelay || retry >= e.maxRetries {
			return received{}, auth, failure
		}
		var wait time.Duration
		if retryAfter != nil {
			wait = *retryAfter
		} else {
			wait = e.jitter(min(8*time.Second, (500*time.Millisecond)<<retry))
		}
		if err := sleep(ctx, wait); err != nil {
			return received{}, auth, err
		}
		retry++
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(clampDuration(d))
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var errAttemptTimeout = errors.New("attempt timed out")

func (c caller) attempt(ctx context.Context, r *request, auth authorisation, etag string) (received, error) {
	e := c.e
	actx, cancel := context.WithTimeoutCause(ctx, clampDuration(c.timeout), errAttemptTimeout)
	defer cancel()
	var body io.Reader
	if r.body != nil {
		body = strings.NewReader(string(r.body))
	}
	req, err := http.NewRequestWithContext(actx, r.method, r.url(e.baseURL), body)
	if err != nil {
		return received{}, &ConnectionError{IdempotencyKey: r.key, cause: errors.New("the request could not be built")}
	}
	h := req.Header
	h.Set("Accept", "application/json")
	h.Set("User-Agent", userAgent)
	if r.body != nil {
		h.Set("Content-Type", "application/json")
	}
	if r.key != "" {
		h.Set("Idempotency-Key", r.key)
	}
	if !r.open {
		h.Set("Authorization", "Bearer "+auth.bearer)
		if auth.identity != "" {
			h.Set("Entitler-Identity-Token", auth.identity)
		}
		if v := c.visitorOf(r); v != "" {
			h.Set("Entitler-Visitor", v)
		}
		if r.asOf != "" {
			h.Set("Entitler-As-Of", r.asOf)
		}
	}
	if etag != "" {
		h.Set("If-None-Match", etag)
	}
	resp, err := e.http.Do(req)
	if err == nil {
		var data []byte
		data, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err == nil {
			return received{status: resp.StatusCode, header: resp.Header, body: data}, nil
		}
	}
	if ctx.Err() != nil {
		return received{}, ctx.Err()
	}
	if errors.Is(context.Cause(actx), errAttemptTimeout) {
		return received{}, &TimeoutError{IdempotencyKey: r.key, timeout: c.timeout}
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return received{}, &ConnectionError{IdempotencyKey: r.key, cause: err}
}

func apiErrorOf(got received, key string, now time.Time) *APIError {
	e := &APIError{
		Status:         got.status,
		Code:           CodeHTTPError,
		Message:        "Entitler answered with HTTP " + strconv.Itoa(got.status),
		RequestID:      got.header.Get("X-Request-Id"),
		RetryAfter:     retryAfterOf(got.header.Get("Retry-After"), now),
		IdempotencyKey: key,
	}
	if got.status >= 300 && got.status < 400 {
		return e
	}
	v, err := parseJSON(got.body)
	body, _ := v.(map[string]any)
	detail, _ := body["error"].(map[string]any)
	if err != nil || detail == nil {
		return e
	}
	if code, ok := detail["code"].(string); ok && code != "" {
		e.Code = Code(code)
	}
	if message, ok := detail["message"].(string); ok && message != "" {
		e.Message = message
	}
	if payment, ok := detail["payment"].(map[string]any); ok && got.status == http.StatusPaymentRequired {
		status, _ := payment["status"].(string)
		switch PaymentStatus(status) {
		case PaymentDeclined, PaymentRequiresAction, PaymentProcessing, PaymentPending:
			link, _ := payment["url"].(string)
			e.Payment = &Payment{Status: PaymentStatus(status), URL: link}
		}
	}
	return e
}

// retryAfterOf reads Retry-After: one or more ASCII digits of seconds, or an HTTP date in any of
// RFC 9110's three forms (a past date meaning 0). Any other value counts as absent. A value too
// large for a duration saturates.
func retryAfterOf(v string, now time.Time) *time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if d, ok := secondsOf(v); ok {
		if d == maxDuration {
			d = time.Duration(1<<63 - 1)
		}
		return &d
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return nil
	}
	d := max(t.Sub(now), 0)
	return &d
}
