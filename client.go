package entitler

import (
	"context"
	"strings"
)

// Client is the in-app client for one signed-in customer, for browsers, mobile and desktop apps
// written in Go, built from a customer token your server minted. It acts on the customer the token
// names, through Me. Create it when the customer signs in, keep it where the app keeps state for
// the session (so screens share one cache, one token and one visitor), and close it when they sign
// out: the next sign-in creates a new client, so nothing kept for one person is answered to another.
// It is safe for concurrent use.
//
// An in-app client never holds a secret key: it takes customer tokens and publishable keys, which
// hold product scopes only, and refuses a secret key at construction, so a server key pasted into
// an app fails on the developer's machine, never in a shipped app.
type Client struct {
	c  caller
	me *appCustomer
}

// IdentityClient is the in-app client built from a publishable key and a sign-in provider's
// identity token, sent as Entitler-Identity-Token on every request. It adds Register.
type IdentityClient struct {
	*Client
}

// PublishableClient is the signed-out in-app client, built from a publishable key alone, for a
// paywall or pricing page in an app with no backend route for it.
type PublishableClient struct {
	c caller
}

type appCustomer struct{ *customer }

func clientSettings(opts []ClientOption) (settings, error) {
	s := defaults()
	for _, o := range opts {
		if o != nil {
			o.applyClient(&s)
		}
	}
	if s.err != nil {
		return s, s.err
	}
	if s.visitor == "" {
		s.visitor = NewVisitorID()
	}
	return s, nil
}

func publishableKey(key string) (string, error) {
	key = trimCredential(key)
	if key == "" || !headerSafe(key) {
		return "", argError("provide an Entitler API key from the dashboard")
	}
	return key, nil
}

func checkPublishable(key string) error {
	if !strings.HasPrefix(key, "ent_pk_") {
		return argError("a secret key belongs on your server, in entitler.Server. Use a publishable key (ent_pk_…) in an app")
	}
	return nil
}

func tokenOf(source TokenSource, blankMessage string) (*tokens, error) {
	if source == nil {
		return nil, argError(blankMessage)
	}
	t := &tokens{source: source}
	if s, ok := source.(staticToken); ok {
		token := trimCredential(string(s))
		if token == "" || !headerSafe(token) {
			return nil, argError(blankMessage)
		}
		t.static, t.current, t.received = true, token, true
	}
	return t, nil
}

func newAppClient(s settings, creds *credentials, description string) *Client {
	e := newEngine(s, creds, true, description)
	c := caller{e: e, timeout: s.timeout}
	return &Client{c: c, me: &appCustomer{newSelf(c)}}
}

// NewClient creates an in-app client from a customer token your server minted with
// ServerCustomer.Token: a [TokenSource] that fetches one from your server, which the client asks
// again before the token expires and after a 401, or [StaticToken], which never refreshes.
// Construction errors wrap [ErrInvalidArgument].
func NewClient(token TokenSource, opts ...ClientOption) (*Client, error) {
	t, err := tokenOf(token, "provide a customer token minted by your server")
	if err != nil {
		return nil, err
	}
	s, err := clientSettings(opts)
	if err != nil {
		return nil, err
	}
	return newAppClient(s, &credentials{kind: principalCustomer, token: t}, "customer token"), nil
}

// NewIdentityClient creates an in-app client from a publishable key (ent_pk_…) and the identity
// token a sign-in provider registered on the project issued, or a [TokenSource] that answers a
// fresh one. Call Register after each sign-in. Sign in with Apple's identity tokens last ten
// minutes and cannot be refreshed on the device, so use a provider that refreshes its own, such as
// Firebase Auth.
func NewIdentityClient(key string, identityToken TokenSource, opts ...ClientOption) (*IdentityClient, error) {
	key, err := publishableKey(key)
	if err != nil {
		return nil, err
	}
	t, err := tokenOf(identityToken, "provide the identity token your sign-in provider issued")
	if err != nil {
		return nil, err
	}
	if err := checkPublishable(key); err != nil {
		return nil, err
	}
	s, err := clientSettings(opts)
	if err != nil {
		return nil, err
	}
	return &IdentityClient{newAppClient(s, &credentials{kind: principalIdentity, key: key, token: t}, "identity token")}, nil
}

// NewPublishableClient creates a signed-out in-app client from a publishable key (ent_pk_…)
// alone, for pricing pages and paywalls.
func NewPublishableClient(key string, opts ...ClientOption) (*PublishableClient, error) {
	key, err := publishableKey(key)
	if err != nil {
		return nil, err
	}
	if err := checkPublishable(key); err != nil {
		return nil, err
	}
	s, err := clientSettings(opts)
	if err != nil {
		return nil, err
	}
	e := newEngine(s, &credentials{kind: principalKey, key: key}, true, "publishable key")
	return &PublishableClient{c: caller{e: e, timeout: s.timeout}}, nil
}

// Me answers the signed-in customer: Customer.ID is the id the latest answer named.
func (c *Client) Me() Customer { return c.me }

// Visitor answers the visitor id the client sends on every request: the one given with
// WithVisitor, else one generated for the client's lifetime. Keep it where the app keeps its own
// data and pass it back with WithVisitor, so an experiment's arm stays the same across launches.
func (c *Client) Visitor() string { return c.c.e.visitor }

// Scopes answers the scopes the token holds, for deciding which controls to show, and for an
// identity client whether a new person may register. Each call asks again.
func (c *Client) Scopes(ctx context.Context) (*Scopes, error) { return scopes(ctx, c.c) }

// SnapshotKeys answers the public keys that verify snapshots. It sends no credential and never
// asks the token source, so it works while a token cannot be had.
func (c *Client) SnapshotKeys(ctx context.Context) (*SnapshotKeys, error) {
	return snapshotKeys(ctx, c.c)
}

// With answers a client derived from this one, sharing its transport, cache, tokens and closed
// state, as Server.With does.
func (c *Client) With(opts ...Option) *Client {
	derived := c.c.with(opts)
	return &Client{c: derived, me: &appCustomer{newSelf(derived)}}
}

// Close cancels every call in flight and the pending token refresh, drops the in-memory cache,
// and makes every later call, of this client and every client derived with With, fail with
// [ErrClosed]. Call it at sign-out. Closing twice is safe.
func (c *Client) Close() error {
	c.c.e.shutdown()
	return nil
}

// String describes the client by kind and base URL, never its credential.
func (c *Client) String() string {
	return "entitler.Client(" + c.c.e.description + ", " + c.c.e.baseURL + ")"
}

// IdentityRegisterParams are the options of IdentityClient.Register; nil for none.
type IdentityRegisterParams struct {
	// IdempotencyKey names this registration; a new UUID when "".
	IdempotencyKey string
}

// Register registers the signed-in person as a customer: call it after each sign-in, since an
// existing customer is answered without being created again. It answers 403 registration_closed
// when the sign-in provider does not let people register themselves (Scopes' Registration says so
// ahead), and 403 scope_required when the key lacks customers:register.
func (c *IdentityClient) Register(ctx context.Context, p *IdentityRegisterParams) (*RegisteredCustomer, error) {
	var given string
	if p != nil {
		given = p.IdempotencyKey
	}
	key, err := optionalKey(given)
	if err != nil {
		return nil, err
	}
	r, err := c.me.write("PUT", "", key, nil, true)
	if err != nil {
		return nil, err
	}
	registered, m, err := fetch(ctx, c.c, r, decodeRegisteredCustomer)
	if err != nil {
		return nil, err
	}
	registered.Replayed = m.replayed
	return &registered, nil
}

// With answers a client derived from this one, as Server.With does.
func (c *IdentityClient) With(opts ...Option) *IdentityClient {
	return &IdentityClient{c.Client.With(opts...)}
}

// Pricing answers the pricing on sale, signed out, with the client's visitor, through the answer
// cache.
func (c *PublishableClient) Pricing(ctx context.Context, p *PricingParams) (*Pricing, error) {
	return pricing(ctx, c.c, p)
}

// Visitor answers the visitor id the client sends on every request.
func (c *PublishableClient) Visitor() string { return c.c.e.visitor }

// SnapshotKeys answers the public keys that verify snapshots.
func (c *PublishableClient) SnapshotKeys(ctx context.Context) (*SnapshotKeys, error) {
	return snapshotKeys(ctx, c.c)
}

// With answers a client derived from this one, as Server.With does.
func (c *PublishableClient) With(opts ...Option) *PublishableClient {
	return &PublishableClient{c: c.c.with(opts)}
}

// Close cancels every call in flight, drops the in-memory cache and makes every later call fail
// with [ErrClosed]. Closing twice is safe.
func (c *PublishableClient) Close() error {
	c.c.e.shutdown()
	return nil
}

// String describes the client by kind and base URL, never its key.
func (c *PublishableClient) String() string {
	return "entitler.PublishableClient(" + c.c.e.baseURL + ")"
}
