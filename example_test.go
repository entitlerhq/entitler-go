package entitler_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/entitlerhq/entitler-go"
	"github.com/entitlerhq/entitler-go/entitlertest"
)

func signedInUserID() string { return "user_42" }

func ExampleNewServer() {
	srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"), entitler.WithTimeout(5*time.Second))
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()

	customer := srv.Customer(signedInUserID())
	if customer.IsEntitled(context.Background(), entitler.OnOff("export_pdf"), false, nil) {
		fmt.Println("export")
	}
}

func ExampleNewClient() {
	client, err := entitler.NewClient(entitler.TokenFunc(func(ctx context.Context) (string, error) {
		return fetchTokenFromYourServer(ctx)
	}))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	if client.Me().IsEntitled(context.Background(), entitler.OnOff("export_pdf"), false, nil) {
		fmt.Println("export")
	}
}

func fetchTokenFromYourServer(context.Context) (string, error) {
	return "", errors.New("not in this example")
}

func ExampleNewPublishableClient() {
	client, err := entitler.NewPublishableClient(os.Getenv("ENTITLER_PUBLISHABLE_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	pricing, err := client.Pricing(context.Background(), nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, plan := range pricing.Plans {
		fmt.Println(plan.Name)
	}
}

func ExampleCustomer_IsEntitled() {
	customer, _ := entitlertest.FakeCustomer(entitlertest.Values{"export_pdf": true}, nil)

	fmt.Println(customer.IsEntitled(context.Background(), entitler.OnOff("export_pdf"), false, nil))
	fmt.Println(customer.IsEntitled(context.Background(), entitler.OnOff("sso"), false, nil))
	// Output:
	// true
	// false
}

func ExampleCustomer_RecordUsage() {
	ctx := context.Background()
	customer, _ := entitlertest.FakeCustomer(entitlertest.Values{"ai_credits": entitlertest.Metered(100, 90)}, nil)
	credits := entitler.Metered("ai_credits")

	result, err := customer.RecordUsage(ctx, credits, 5, "export-request-7731", nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Outcome, result.Meter.Remaining)

	again, _ := customer.RecordUsage(ctx, credits, 5, "export-request-7731", nil)
	fmt.Println(again.Outcome, again.Replayed)

	refused, _ := customer.RecordUsage(ctx, credits, 50, "export-request-7732", nil)
	fmt.Println(refused.Outcome, refused.Refusal)
	// Output:
	// recorded 5
	// duplicate true
	// refused over_allowance
}

func ExampleCustomer_StartHold() {
	ctx := context.Background()
	customer, _ := entitlertest.FakeCustomer(entitlertest.Values{"ai_credits": entitlertest.Metered(100, 0)}, nil)

	hold, err := customer.StartHold(ctx, entitler.Metered("ai_credits"), 50, "message-123", nil)
	var refused *entitler.UsageRefusedError
	var replayed *entitler.UsageReplayedError
	switch {
	case errors.As(err, &refused):
		fmt.Println("answer 402 with an upgrade prompt")
		return
	case errors.As(err, &replayed):
		fmt.Println("answer the reply this message received before")
		return
	case err != nil:
		log.Fatal(err)
	}
	defer hold.Close(ctx)

	_ = hold.Use(0)
	for _, used := range []int64{10, 25, 32} {
		_ = hold.Use(used)
	}
	result, err := hold.Finish(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Outcome, result.Meter.Used)
	// Output: settled 32
}

func ExampleWithHold() {
	ctx := context.Background()
	customer, _ := entitlertest.FakeCustomer(entitlertest.Values{"ai_credits": entitlertest.Metered(100, 0)}, nil)

	reply, err := entitler.WithHold(ctx, customer, entitler.Metered("ai_credits"), 20, "job-42",
		func(_ context.Context, hold *entitler.Hold) (string, error) {
			return "a reply", hold.Use(7)
		}, nil)
	var refused *entitler.UsageRefusedError
	var replayed *entitler.UsageReplayedError
	switch {
	case errors.As(err, &refused):
		fmt.Println("answer 402 with an upgrade prompt")
	case errors.As(err, &replayed):
		fmt.Println("answer the reply job-42 produced before")
	case err != nil:
		log.Fatal(err)
	default:
		fmt.Println(reply)
	}
	// Output: a reply
}

func ExampleCustomer_Subscribe() {
	customer, _ := entitlertest.FakeCustomer(nil, nil)

	step, err := customer.Subscribe(context.Background(), "pro", &entitler.SubscribeParams{
		Period:    "monthly",
		ReturnURL: "https://app.example.com/billing/return",
	})
	if err != nil {
		log.Fatal(err)
	}
	switch step.Next {
	case entitler.NextDone:
		fmt.Println("now on", step.Change.Plan.Key)
	case entitler.NextPay:
		fmt.Println("send the customer to", step.URL)
	case entitler.NextConfirming:
		fmt.Println("payment pending")
	case entitler.NextManage:
		fmt.Println("change it in the store", step.BilledBy)
	}
	// Output: now on pro
}

func ExampleServerCustomer_SetPlan() {
	customer, fake := entitlertest.FakeCustomer(nil, nil)

	_, err := customer.SetPlan(context.Background(), entitler.Plan("enterprise"), &entitler.SetPlanParams{
		Period:         "yearly",
		Billing:        entitler.BillingEnd,
		Decision:       entitler.Decision{Reason: "Annual contract", Actor: "hubspot"},
		IdempotencyKey: "deal-8841-won",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(fake.Writes()[0].Body["billing"])
	// Output: end
}

func ExampleVerifySnapshot() {
	var keys entitler.SnapshotKeys
	shipped, _ := os.ReadFile("snapshot_keys.json")
	if err := keys.UnmarshalJSON(shipped); err != nil {
		return
	}
	verified, err := entitler.VerifySnapshot(storedSnapshot(), entitler.SnapshotExpectation{
		Keys:        keys.Keys,
		Customer:    signedInUserID(),
		Environment: "env_from_your_build",
	})
	if err != nil {
		return
	}
	fmt.Println(verified.Entitlements.Has(entitler.OnOff("export_pdf")))
}

func storedSnapshot() string { return "" }

func ExampleIsUnreachable() {
	customer, fake := entitlertest.FakeCustomer(entitlertest.Values{"export_pdf": true}, nil)
	fake.Answer("Check", func(entitlertest.Write) (int, []byte) {
		return 503, entitlertest.Error(entitler.CodeUnavailable, "Try again shortly.")
	})

	_, err := customer.Check(context.Background(), entitler.OnOff("export_pdf"), nil)
	fmt.Println(entitler.IsUnreachable(err))
	// Output: true
}
