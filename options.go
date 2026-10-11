package entitler

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL       = "https://api.entitler.dev"
	defaultTimeout       = 10 * time.Second
	defaultMaxRetries    = 2
	defaultMaxRetryDelay = 10 * time.Second
	defaultCacheSize     = 1000
	defaultStaleFor      = 24 * time.Hour
)

type settings struct {
	baseURL       string
	timeout       time.Duration
	maxRetries    int
	maxRetryDelay time.Duration
	cacheSize     int
	store         Cache
	staleFor      time.Duration
	onError       func(error)
	httpClient    *http.Client
	visitor       string
	perCall       bool
	err           error
}

func defaults() settings {
	return settings{
		baseURL:       defaultBaseURL,
		timeout:       defaultTimeout,
		maxRetries:    defaultMaxRetries,
		maxRetryDelay: defaultMaxRetryDelay,
		cacheSize:     defaultCacheSize,
		staleFor:      defaultStaleFor,
	}
}

func (s *settings) fail(err error) {
	if s.err == nil {
		s.err = err
	}
}

// ServerOption configures [NewServer]. Every [Option] is one, and so is [WithCache].
type ServerOption interface{ applyServer(*settings) }

// ClientOption configures the in-app clients. Every [Option] is one, and so is [WithVisitor].
type ClientOption interface{ applyClient(*settings) }

// Option configures any client: it is both a [ServerOption] and a [ClientOption].
type Option interface {
	ServerOption
	ClientOption
}

type option func(*settings)

func (o option) applyServer(s *settings) { o(s) }
func (o option) applyClient(s *settings) { o(s) }

type serverOption func(*settings)

func (o serverOption) applyServer(s *settings) { o(s) }

type clientOption func(*settings)

func (o clientOption) applyClient(s *settings) { o(s) }

func shared(s *settings, name string) bool {
	if s.perCall {
		s.fail(argError("pass only WithTimeout to With: " + name + " belongs to the client"))
		return true
	}
	return false
}

// WithBaseURL sets the API's base URL, https://api.entitler.dev by default. Trailing slashes are removed.
func WithBaseURL(baseURL string) Option {
	return option(func(s *settings) {
		if shared(s, "WithBaseURL") {
			return
		}
		u, err := url.Parse(baseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			s.fail(argError("pass baseURL as an absolute http or https URL"))
			return
		}
		s.baseURL = strings.TrimRight(baseURL, "/")
	})
}

// WithTimeout sets each attempt's deadline, from connecting to the last byte of the answer: 10
// seconds by default. It must be above 0. Derive a client with it for one call's deadline:
// srv.With(entitler.WithTimeout(2 * time.Second)).
func WithTimeout(timeout time.Duration) Option {
	return option(func(s *settings) {
		if timeout <= 0 {
			s.fail(argError("pass timeout as a positive duration"))
			return
		}
		s.timeout = timeout
	})
}

// WithMaxRetries sets how many times a call is retried after its first attempt, 2 by default.
func WithMaxRetries(n int) Option {
	return option(func(s *settings) {
		if shared(s, "WithMaxRetries") {
			return
		}
		if n < 0 {
			s.fail(argError("pass maxRetries as a whole number of 0 or more"))
			return
		}
		s.maxRetries = n
	})
}

// WithMaxRetryDelay sets the longest Retry-After the SDK waits for, 10 seconds by default. An
// answer asking for longer fails at once, with RetryAfter telling the caller when to try again.
func WithMaxRetryDelay(d time.Duration) Option {
	return option(func(s *settings) {
		if shared(s, "WithMaxRetryDelay") {
			return
		}
		if d < 0 {
			s.fail(argError("pass maxRetryDelay as a duration of 0 or more"))
			return
		}
		s.maxRetryDelay = d
	})
}

// WithMemoryCache sets the size of the in-memory answer cache, 1,000 answers by default.
func WithMemoryCache(size int) Option {
	return option(func(s *settings) {
		if shared(s, "WithMemoryCache") {
			return
		}
		if size < 1 {
			s.fail(argError("pass the cache size as a whole number of 1 or more"))
			return
		}
		s.cacheSize, s.store = size, nil
	})
}

// WithoutCache turns the answer cache off: every read goes to Entitler, and none can stand in
// while Entitler is unreachable.
func WithoutCache() Option {
	return option(func(s *settings) {
		if shared(s, "WithoutCache") {
			return
		}
		s.cacheSize, s.store = 0, nil
	})
}

// WithCache gives the server client a custom answer store, such as one backed by Redis, so
// servers share answers. A failing Get counts as a miss and a failing Set is skipped; both go to
// onError. In-app clients keep answers in memory only, so this is a ServerOption alone.
func WithCache(store Cache) ServerOption {
	return serverOption(func(s *settings) {
		if shared(s, "WithCache") {
			return
		}
		if store == nil {
			s.fail(argError("pass a cache store, or use WithoutCache"))
			return
		}
		s.store = store
	})
}

// WithStaleFor sets how long a kept answer may stand in while Entitler is unreachable, 24 hours by default.
func WithStaleFor(d time.Duration) Option {
	return option(func(s *settings) {
		if shared(s, "WithStaleFor") {
			return
		}
		if d < 0 {
			s.fail(argError("pass staleFor as a duration of 0 or more"))
			return
		}
		s.staleFor = d
	})
}

// WithOnError sets a function called with each error a fallback absorbed: a stale answer, an
// IsEntitled default, a hold's failed release or disposal, and a custom store's own failures. It
// never changes a call's answer; a panic in it is recovered and ignored.
func WithOnError(onError func(error)) Option {
	return option(func(s *settings) {
		if shared(s, "WithOnError") {
			return
		}
		s.onError = onError
	})
}

// WithHTTPClient sends requests through the given client, for tests, proxies and
// instrumentation. The SDK uses a copy that never follows redirects, so no credential reaches
// another origin, and leaves the client open when it closes. Its Transport must not cache
// answers: the SDK's own cache is the only one.
func WithHTTPClient(client *http.Client) Option {
	return option(func(s *settings) {
		if shared(s, "WithHTTPClient") {
			return
		}
		if client == nil {
			s.fail(argError("pass an HTTP client"))
			return
		}
		s.httpClient = client
	})
}

// WithVisitor sets the visitor id an in-app client sends on every request, so an experiment's arm
// stays the same before and after registration. Keep Client.Visitor() where the app keeps its own
// data and pass it back here at the next launch.
func WithVisitor(visitor string) ClientOption {
	return clientOption(func(s *settings) {
		if err := checkVisitor(visitor); err != nil {
			s.fail(err)
			return
		}
		s.visitor = visitor
	})
}
