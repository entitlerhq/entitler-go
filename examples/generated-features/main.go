// Generated-features uses the typed feature constants `go tool entitler generate` wrote to
// features/entitler_features.go.
// As a script, it acts for the customer its configuration names: ENTITLER_CUSTOMER, or test_1001.
//
//	ENTITLER_KEY=ent_test_… go generate ./generated-features && go run ./generated-features
package main

//go:generate go tool entitler generate -out features/entitler_features.go -package features

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/entitlerhq/entitler-go"
	"github.com/entitlerhq/entitler-go/examples/generated-features/features"
)

func main() {
	ctx := context.Background()
	srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	customer := srv.Customer(cmp.Or(os.Getenv("ENTITLER_CUSTOMER"), "test_1001"))

	check, err := customer.Check(ctx, features.FeatureAiCredits, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("AI credits: %s of %s remain\n", check.Meter.Remaining, check.Value)

	seats, err := customer.Check(ctx, features.FeatureTeamSeats, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Team seats:", seats.Value)
	fmt.Println("Collaboration covers", features.FeatureCollaboration.Includes())

	key := "generated-" + time.Now().UTC().Format("20060102T150405.000")
	if _, err := customer.RecordUsage(ctx, features.FeatureAiCredits, 1, key, nil); err != nil {
		log.Fatal(err)
	}
}
