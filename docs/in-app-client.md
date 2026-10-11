# The in-app client

Go apps that run on a person's device (desktop apps, command-line tools, agents) use an in-app
client, built from one of:

- `entitler.NewClient(token)`: a customer token your server minted with `ServerCustomer.Token`;
- `entitler.NewIdentityClient(key, identityToken)`: a publishable key and an identity token from a
  sign-in provider registered on the project, sent as `Entitler-Identity-Token`;
- `entitler.NewPublishableClient(key)`: a publishable key alone, for a signed-out pricing page.

An in-app client never holds a secret key. It takes customer tokens and publishable keys, which
hold product scopes only, and refuses a secret key at construction, so a server key pasted into an
app fails on the developer's machine, never in a shipped app.

## Customer tokens from your server

Mint tokens with the fewest scopes: `Token` without `Scopes` mints `entitlements:read` alone. Ask
for `usage:write` only when the app records usage itself, and for `billing:self` only for people
who may buy for the customer. Mint a token only for the signed-in customer your server
authenticated: a route that minted one for any id it was sent would hand every customer's access
to anyone.

```go
func tokenRoute(srv *entitler.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := signedInUserID(r)
		if !ok {
			http.Error(w, "Sign in first.", http.StatusUnauthorized)
			return
		}
		token, err := srv.Customer(userID).Token(r.Context(), &entitler.TokenParams{
			Scopes: []entitler.Scope{entitler.ScopeEntitlementsRead, entitler.ScopeUsageWrite},
		})
		if err != nil {
			http.Error(w, "Could not mint a token.", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": token.Token})
	}
}
```

## Token sources

Give the client a `TokenSource` that fetches a fresh token from your server. The client asks it
for the first request, again when the kept token expires within 60 seconds (or half its lifetime,
when shorter), and once after a `401`. Concurrent requests share one pending call, which has its
own deadline, the client's timeout; a caller giving up never cancels it. `StaticToken` holds one
token that never refreshes. A source that fails, or answers a blank or unreadable token, fails the
call with a `*entitler.TokenError` holding the source's own error, so keep tokens out of your
errors.

Identity tokens: Sign in with Apple's identity tokens last ten minutes and the device cannot
refresh them, so use a sign-in provider that refreshes its own, such as Firebase Auth, and call
`Register` after each sign-in (an existing customer is answered without being created again).

## One client per signed-in customer

A client acts for one signed-in customer. Create it when the customer signs in, keep it where the
app keeps state for the session (owned by the application, not by one window or command, so they
share one cache, one token and one visitor), and close it when the customer signs out:

```go
if err := client.Close(); err != nil {
	log.Fatal(err)
}
```

Closing cancels every call in flight and the pending token refresh, drops the in-memory cache, and
makes every later call answer `entitler.ErrClosed`. The next sign-in creates a new client, so
nothing kept for one person is ever answered to another.

## What survives a restart

An in-app client keeps answers in memory only: its principal changes with every token, so a store
kept across launches would never answer it again. Only [offline snapshots](offline-snapshots.md)
survive a restart or the loss of the process.

`client.Me().Plans` and in-app billing need the organisation's customer portal capability
(`409 capability_required` otherwise).
