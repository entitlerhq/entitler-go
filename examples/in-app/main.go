// In-app shows the in-app client: your server mints a customer token for the signed-in customer,
// and the app's client fetches it through a token source, checks a feature, records usage, and is
// closed at sign-out.
//
//	ENTITLER_KEY=ent_test_… go run ./in-app
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/entitlerhq/entitler-go"
)

// signedInUserID stands in for your app's session: it answers the user the session cookie names,
// and false when nobody is signed in.
func signedInUserID(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("session")
	if err != nil || cookie.Value != "session-of-test_1001" {
		return "", false
	}
	return "test_1001", true
}

// tokenRoute mints a customer token only for the signed-in customer. A route that minted one for
// any id it was sent would hand every customer's access to anyone.
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

func main() {
	srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	backend := httptest.NewServer(tokenRoute(srv))
	defer backend.Close()

	client, err := entitler.NewClient(entitler.TokenFunc(func(ctx context.Context) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, backend.URL, nil)
		if err != nil {
			return "", err
		}
		req.AddCookie(&http.Cookie{Name: "session", Value: "session-of-test_1001"})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("the token route answered %s", resp.Status)
		}
		var body struct{ Token string }
		err = json.NewDecoder(resp.Body).Decode(&body)
		return body.Token, err
	}))
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	me := client.Me()
	fmt.Println("Export to PDF:", me.IsEntitled(ctx, entitler.OnOff("export_pdf"), false, nil))
	key := "in-app-" + time.Now().UTC().Format("20060102T150405.000")
	if result, err := me.RecordUsage(ctx, entitler.Metered("ai_credits"), 1, key, nil); err == nil {
		fmt.Printf("%s recorded for %s\n", result.Outcome, me.ID())
	} else {
		fmt.Println("Could not record usage:", err)
	}

	if err := client.Close(); err != nil {
		log.Fatal(err)
	}
}
