// Pricing-page prints the pricing as a pricing page would, once signed out with a publishable key
// and once from a server with a visitor id.
//
//	ENTITLER_PUBLISHABLE_KEY=ent_pk_test_… ENTITLER_KEY=ent_test_… go run ./pricing-page
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/entitlerhq/entitler-go"
)

func main() {
	ctx := context.Background()

	if key := os.Getenv("ENTITLER_PUBLISHABLE_KEY"); key != "" {
		client, err := entitler.NewPublishableClient(key)
		if err != nil {
			log.Fatal(err)
		}
		defer client.Close()
		pricing, err := client.Pricing(ctx, nil)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("Signed out:")
		printPricing(pricing)
	}

	srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	visitor := entitler.NewVisitorID()
	pricing, err := srv.Pricing(ctx, &entitler.PricingParams{Visitor: visitor})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("From the server, for visitor", visitor+":")
	printPricing(pricing)
}

func printPricing(pricing *entitler.Pricing) {
	for _, plan := range pricing.Plans {
		if plan.Kind != entitler.PlanKindPlan {
			continue
		}
		fmt.Printf("%s\n", plan.Name)
		for _, period := range plan.Periods {
			fmt.Printf("  %s: %s\n", period.Label, priceFor(plan, period.Key))
		}
	}
}

func priceFor(plan entitler.PricingPlan, period string) string {
	for _, listing := range plan.Listings {
		if listing.Period == nil || listing.Period.Key != period {
			continue
		}
		for _, channel := range listing.Channels {
			if channel.Price != nil && channel.Purchasable {
				p := channel.Price
				return fmt.Sprintf("%d.%02d %s", p.Amount/100, p.Amount%100, strings.ToUpper(p.Currency))
			}
		}
	}
	return "Coming soon"
}
