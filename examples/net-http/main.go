// Net-http gates routes of a net/http server: middleware that answers 401 when nobody is signed
// in and 403 with the upgrade when the customer lacks the feature, a metered route, and the route
// that mints customer tokens for the app's in-app client.
//
//	ENTITLER_KEY=ent_test_… go run ./net-http
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/entitlerhq/entitler-go"
)

type contextKey struct{}

// sessions stands in for your session store: it maps a session cookie your app issued at sign-in
// to the user it signed in.
var sessions = map[string]string{"session-of-test_1001": "test_1001"}

// signedInUserID stands in for your session middleware: it answers the user the session cookie's
// session belongs to, and false when nobody is signed in. The cookie never names the user itself.
func signedInUserID(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("session")
	if err != nil {
		return "", false
	}
	userID, ok := sessions[cookie.Value]
	return userID, ok
}

// customerOf answers the customer the request is for, which requireFeature put on its context.
func customerOf(r *http.Request) *entitler.ServerCustomer {
	return r.Context().Value(contextKey{}).(*entitler.ServerCustomer)
}

// requireFeature lets a request through only when the signed-in customer is entitled to the
// feature. It fails closed: when Entitler cannot be asked, the customer is not entitled.
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

func exportPDF(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/pdf")
	_, _ = w.Write([]byte("%PDF-1.7\n"))
}

func summarise(w http.ResponseWriter, r *http.Request) {
	requestID := r.Header.Get("Idempotency-Key")
	if requestID == "" {
		http.Error(w, "Send an Idempotency-Key.", http.StatusBadRequest)
		return
	}
	result, err := customerOf(r).RecordUsage(r.Context(), entitler.Metered("ai_credits"), 5, requestID, nil)
	if err != nil {
		http.Error(w, "Try again shortly.", http.StatusServiceUnavailable)
		return
	}
	if result.Outcome == entitler.OutcomeRefused {
		http.Error(w, "Out of AI credits.", http.StatusPaymentRequired)
		return
	}
	_, _ = w.Write([]byte("A short summary.\n"))
}

// mintToken mints a customer token only for the signed-in customer, for the app's in-app client.
func mintToken(srv *entitler.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := signedInUserID(r)
		if !ok {
			http.Error(w, "Sign in first.", http.StatusUnauthorized)
			return
		}
		token, err := srv.Customer(userID).Token(r.Context(), nil)
		if err != nil {
			http.Error(w, "Try again shortly.", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": token.Token})
	}
}

func main() {
	srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()

	mux := http.NewServeMux()
	mux.Handle("GET /export.pdf", requireFeature(srv, entitler.OnOff("export_pdf"), http.HandlerFunc(exportPDF)))
	mux.Handle("POST /summaries", requireFeature(srv, entitler.Metered("ai_credits"), http.HandlerFunc(summarise)))
	mux.Handle("GET /api/entitler-token", mintToken(srv))

	server := &http.Server{Addr: "localhost:8080", Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Println("Listening on http://localhost:8080")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
