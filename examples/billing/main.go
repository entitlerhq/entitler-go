// Billing builds a billing page from Plans, subscribes handling every next step, syncs on the
// return page, cancels and undoes, opens the billing portal, and makes the company's decisions.
// As a script, it acts for the customer its configuration names: ENTITLER_CUSTOMER, or test_1002.
//
//	ENTITLER_KEY=ent_test_… go run ./billing
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/entitlerhq/entitler-go"
)

const returnURL = "https://app.example.com/billing/return"

func billingPage(ctx context.Context, customer entitler.Customer) error {
	plans, err := customer.Plans(ctx, nil)
	if err != nil {
		return err
	}
	for _, held := range plans.Held {
		switch held.BilledBy {
		case entitler.ProviderApple, entitler.ProviderGoogle:
			fmt.Printf("%s: manage it in the %s store\n", held.Plan.Name, held.BilledBy)
		default:
			fmt.Printf("%s: manage it in the billing portal\n", held.Plan.Name)
		}
	}
	for _, option := range plans.Options {
		switch option.Action {
		case entitler.ActionBuy:
			fmt.Printf("[%s %s]\n", option.Move, option.Plan.Name)
		case entitler.ActionContact:
			fmt.Printf("[Talk to sales about %s]\n", option.Plan.Name)
		default:
			fmt.Printf("%s is unavailable\n", option.Plan.Name)
		}
	}
	return nil
}

func subscribe(ctx context.Context, customer entitler.Customer, plan, period string) error {
	step, err := customer.Subscribe(ctx, plan, &entitler.SubscribeParams{Period: period, ReturnURL: returnURL})
	var apiErr *entitler.APIError
	if errors.As(err, &apiErr) && apiErr.Payment != nil && apiErr.Payment.Status == entitler.PaymentDeclined {
		fmt.Println("The card was declined.")
		return nil
	}
	if err != nil {
		return err
	}
	switch step.Next {
	case entitler.NextDone:
		fmt.Printf("Now on %s (%s)\n", plan, step.Change.Effective)
	case entitler.NextPay:
		fmt.Println("Send the customer to", step.URL)
	case entitler.NextConfirming:
		fmt.Println("The bank is confirming the payment.")
	case entitler.NextManage:
		fmt.Println("Change it in the store:", step.BilledBy)
	default:
		fmt.Println("Show the plans again: next step", step.Next)
	}
	return nil
}

func returnPage(ctx context.Context, customer entitler.Customer) error {
	if _, err := customer.SyncBilling(ctx); err != nil {
		return err
	}
	plans, err := customer.Plans(ctx, &entitler.ReadParams{Revalidate: true})
	if err != nil {
		return err
	}
	fmt.Println("Plans held:", len(plans.Held))
	return nil
}

func openPortal(ctx context.Context, customer entitler.Customer) {
	portal, err := customer.BillingPortal(ctx, returnURL)
	var apiErr *entitler.APIError
	switch {
	case err == nil:
		fmt.Println("Open", portal.URL)
	case errors.As(err, &apiErr) && apiErr.Code == entitler.CodeNotFound:
		fmt.Println("Nothing billed yet, so there is no portal.")
	case errors.As(err, &apiErr) && apiErr.Code == entitler.CodeStale:
		fmt.Println("No payment provider is connected in this environment.")
	default:
		fmt.Println("The portal is unavailable:", err)
	}
}

func main() {
	ctx := context.Background()
	srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	customer := srv.Customer(cmp.Or(os.Getenv("ENTITLER_CUSTOMER"), "test_1002"))
	run := time.Now().UTC().Format("20060102T150405.000")

	steps := []func() error{
		func() error { return billingPage(ctx, customer) },
		func() error { return subscribe(ctx, customer, "pro", "monthly") },
		func() error { return returnPage(ctx, customer) },
		func() error {
			change, err := customer.Cancel(ctx, nil)
			if err == nil {
				fmt.Println("Cancel takes effect", change.Effective, change.At.Format(time.DateOnly))
			}
			return err
		},
		func() error { _, err := customer.UndoPendingChange(ctx, nil); return err },
		func() error { openPortal(ctx, customer); return nil },
		func() error {
			_, err := customer.SetPlan(ctx, entitler.Plan("pro"), &entitler.SetPlanParams{
				Period:         "yearly",
				Billing:        entitler.BillingEnd,
				Decision:       entitler.Decision{Reason: "Annual contract signed", Actor: "hubspot"},
				IdempotencyKey: "deal-" + run,
			})
			return err
		},
		func() error {
			change, err := customer.Grant(ctx, entitler.Config("support_contacts"), &entitler.GrantParams{
				Value:          entitler.Amount(10),
				Days:           30,
				Decision:       entitler.Decision{Reason: "Goodwill for the outage", Actor: "agent_417"},
				IdempotencyKey: "ticket-" + run,
			})
			if err == nil {
				fmt.Println("Keep the grant's id to revoke it:", change.Grant.ID)
			}
			return err
		},
		func() error {
			_, err := customer.SetPlan(ctx, entitler.Plan("free"), &entitler.SetPlanParams{When: entitler.WhenNow,
				Decision: entitler.Decision{Reason: "Back to free after the example", Actor: "billing-example"}})
			return err
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			log.Fatal(err)
		}
	}
}
