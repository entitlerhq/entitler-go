// Quickstart registers a customer, checks a feature and records usage, as a server does. As a
// script, it acts for the customer its configuration names: ENTITLER_CUSTOMER, or
// sdk-go-quickstart.
//
//	ENTITLER_KEY=ent_test_… go run ./quickstart
package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/entitlerhq/entitler-go"
)

func main() {
	ctx := context.Background()
	srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()

	customer := srv.Customer(cmp.Or(os.Getenv("ENTITLER_CUSTOMER"), "sdk-go-quickstart"))
	if _, err := customer.Register(ctx, &entitler.RegisterParams{
		Name:  new("Ada Lovelace"),
		Email: new("ada@example.com"),
	}); err != nil {
		log.Fatal(err)
	}

	if customer.IsEntitled(ctx, entitler.OnOff("export_pdf"), false, nil) {
		fmt.Println("Export to PDF is on.")
	} else {
		fmt.Println("Export to PDF needs an upgrade.")
	}

	requestID := "quickstart-" + time.Now().UTC().Format("20060102T150405.000")
	result, err := customer.RecordUsage(ctx, entitler.Metered("ai_credits"), 1, requestID, nil)
	if err != nil {
		log.Fatal(err)
	}
	switch result.Outcome {
	case entitler.OutcomeRecorded:
		fmt.Printf("Recorded 1 credit; %s remain.\n", result.Meter.Remaining)
	case entitler.OutcomeRefused:
		fmt.Println("No credits left:", result.Refusal)
	}
}
