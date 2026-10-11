# Configuration

Options are functional: an `entitler.Option` configures any client, `WithCache` only the server
and `WithVisitor` only the in-app clients.

| Option | Default | Meaning |
| --- | --- | --- |
| `WithBaseURL(url)` | `https://api.entitler.dev` | the API's base URL; trailing slashes removed |
| `WithTimeout(d)` | 10 seconds | each attempt's deadline |
| `WithMaxRetries(n)` | 2 | retries after the first attempt |
| `WithMaxRetryDelay(d)` | 10 seconds | the longest `Retry-After` waited for |
| `WithMemoryCache(n)` | 1,000 answers | the in-memory store's size |
| `WithoutCache()` | | no answer cache |
| `WithCache(store)` | | a custom store, server only |
| `WithStaleFor(d)` | 24 hours | how long a kept answer may stand in while Entitler is unreachable |
| `WithOnError(f)` | none | receives each error a fallback absorbed |
| `WithHTTPClient(c)` | a client of the SDK's own | the HTTP client, for tests, proxies and instrumentation |
| `WithVisitor(id)` | a generated id | the in-app client's visitor |

```go
srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"),
	entitler.WithTimeout(5*time.Second),
	entitler.WithMaxRetries(1),
	entitler.WithHTTPClient(&http.Client{Transport: instrumented(http.DefaultTransport)}),
)
```

A client given with `WithHTTPClient` is copied: the SDK never follows a redirect, so no credential
reaches another origin, and closing the SDK's client leaves yours open. Its `Transport` must not
cache answers.

`String()` on any client shows its kind and base URL, never its credential.

## Creating the server client

A server shares one `*entitler.Server`, as it would an `http.Client`; it is safe for concurrent
use. Create it at start-up from configuration, or on first use with `sync.OnceValues` where code
runs without the key (a build step, a tool), so the key is read only when a request needs it:

```go
var entitlerServer = sync.OnceValues(func() (*entitler.Server, error) {
	return entitler.NewServer(os.Getenv("ENTITLER_KEY"))
})
```

Close it at shutdown with `srv.Close()`.
