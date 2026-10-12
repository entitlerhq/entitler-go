// Package entitler is the official Entitler SDK for Go: check what a customer is entitled to,
// record metered usage, show pricing, and manage customers' plans.
//
// # Clients
//
// A server builds one [Server] from a secret key and shares it, as it would an http.Client. An app
// written in Go builds an in-app client per signed-in person: [NewClient] from a customer token
// its server minted, [NewIdentityClient] from a publishable key and a sign-in provider's identity
// token, or [NewPublishableClient] from a publishable key alone for a signed-out pricing page. A
// secret key never goes into an app: each client refuses the other kind of key at construction.
//
// Both kinds answer a [Customer] — Server.Customer(id) on a server, Client.Me() in an app — so code
// that gates features, records usage and offers the customer's own billing choices is written once:
//
//	srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"))
//	if err != nil {
//		log.Fatal(err)
//	}
//	customer := srv.Customer(userID)
//	if customer.IsEntitled(ctx, features.FeatureExportPdf, false, nil) {
//		// export
//	}
//	_, err = customer.RecordUsage(ctx, features.FeatureAiCredits, 25, requestID, nil)
//
// # Reliability
//
// Every call takes a context first and blocks. Each attempt has a deadline (10 seconds by
// default, [WithTimeout]); connection failures, timeouts and 408, 429, 500, 502, 503 and 504
// answers are retried with backoff, honouring Retry-After; every write sends an idempotency key,
// so a retry never applies twice. Reads of checks, entitlements, plans and pricing go through an
// answer cache that honours max-age and revalidates with ETags, and a kept answer stands in,
// marked Stale, while Entitler is unreachable. [Customer.IsEntitled] never fails because of
// Entitler: it answers its default and passes the error to the function given with [WithOnError].
//
// # Enums
//
// Every enum type is an open set: a value this version does not know is kept as it is, so the API
// can grow without breaking an older SDK. Compare values with the constants.
//
// # Errors
//
// Failures from Entitler are an [Error]: [*APIError], [*ConnectionError], [*TimeoutError],
// [*TokenError], [*SnapshotError], [*UsageRefusedError], [*UsageReplayedError] and
// [*UsageSettlementError]. Match them with errors.As. Arguments the SDK refuses before any request
// wrap [ErrInvalidArgument], a closed client's calls answer [ErrClosed], and the caller's own
// cancellation answers ctx.Err().
//
// # Testing
//
// The entitlertest package answers a [*ServerCustomer] from values held in memory, and writes
// complete answer bodies for tests that fake the HTTP transport.
package entitler
