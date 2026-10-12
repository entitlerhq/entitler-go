# net/http

A server written with `net/http` shares one `*entitler.Server` and gates routes with middleware.
The customer id always comes from the authenticated session, never from a header, query string or
body the caller controls, and a request with nobody signed in is answered `401`.

```go
func requireFeature(srv *entitler.Server, feature entitler.Feature, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := signedInUserID(r)
		if !ok {
			http.Error(w, "Sign in first.", http.StatusUnauthorized)
			return
		}
		customer := srv.Customer(userID)
		check, err := customer.Check(r.Context(), feature, nil)
		if err != nil || !check.Entitled {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			var upgrades []entitler.Upgrade
			if err == nil {
				upgrades = check.Upgrades
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "upgrade", "upgrades": upgrades})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, customer)))
	})
}
```

Register routes with it:

```go
mux := http.NewServeMux()
mux.Handle("GET /export.pdf", requireFeature(srv, entitler.OnOff("export_pdf"), http.HandlerFunc(exportPDF)))
mux.Handle("POST /summaries", requireFeature(srv, entitler.Metered("ai_credits"), http.HandlerFunc(summarise)))
mux.Handle("GET /api/entitler-token", mintToken(srv))
```

The token route mints customer tokens for the app's in-app client, only for the signed-in
customer: a route that minted one for any id it was sent would hand every customer's access to
anyone. The whole server is in [`examples/net-http`](../examples/net-http/main.go).
