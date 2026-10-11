// Metered-work holds credits around streamed work that reports as it goes, wraps work whose cost
// is known only afterwards in WithHold, and records work that already happened in observe mode.
// As a script, it acts for the customer its configuration names: ENTITLER_CUSTOMER, or test_1001.
//
//	ENTITLER_KEY=ent_test_… go run ./metered-work
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

var aiCredits = entitler.Metered("ai_credits")

// streamReply holds the most a reply may cost before it starts, reports the tokens used as they
// stream, and settles what was used however the stream ends.
func streamReply(ctx context.Context, customer entitler.Customer, messageID string, chunks []int64) error {
	hold, err := customer.StartHold(ctx, aiCredits, 50, messageID, nil)
	var refused *entitler.UsageRefusedError
	if errors.As(err, &refused) {
		return fmt.Errorf("out of credits: upgrade to %d plan(s)", len(refused.Result.Upgrades))
	}
	if err != nil {
		return err
	}
	defer hold.Close(ctx)
	if err := hold.Use(0); err != nil {
		return err
	}
	var used int64
	for _, tokens := range chunks {
		used += tokens
		if err := hold.Use(used); err != nil {
			return err
		}
	}
	_, err = hold.Finish(ctx)
	return err
}

type summary struct {
	text   string
	tokens int64
}

func summarise(ctx context.Context, customer entitler.Customer, jobID string) (summary, error) {
	return entitler.WithHold(ctx, customer, aiCredits, 20, jobID,
		func(ctx context.Context, hold *entitler.Hold) (summary, error) {
			s := summary{text: "A short summary.", tokens: 7}
			return s, hold.Use(s.tokens)
		}, nil)
}

func main() {
	ctx := context.Background()
	srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	customer := srv.Customer(cmp.Or(os.Getenv("ENTITLER_CUSTOMER"), "test_1001"))
	run := time.Now().UTC().Format("20060102T150405.000")

	if err := streamReply(ctx, customer, "message-"+run, []int64{3, 4, 2}); err != nil {
		log.Fatal(err)
	}
	s, err := summarise(ctx, customer, "summary-job-"+run)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(s.text)

	result, err := customer.RecordUsage(ctx, aiCredits, 2, "bandwidth-"+run,
		&entitler.RecordUsageParams{Mode: entitler.Observe})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Observed 2 credits; %d over the allowance.\n", result.OverBy)
}
