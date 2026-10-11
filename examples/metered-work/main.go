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

var errOutOfCredits = errors.New("out of credits")

// streamReply holds the most a reply may cost before it starts, reports the tokens used as they
// stream, and settles what was used however the stream ends. A server answers errOutOfCredits with
// 402 and an upgrade prompt.
func streamReply(ctx context.Context, customer entitler.Customer, messageID string, chunks []int64) error {
	hold, err := customer.StartHold(ctx, aiCredits, 50, messageID, nil)
	var refused *entitler.UsageRefusedError
	var replayed *entitler.UsageReplayedError
	switch {
	case errors.As(err, &refused):
		return fmt.Errorf("%w: upgrade to %d plan(s)", errOutOfCredits, len(refused.Result.Upgrades))
	case errors.As(err, &replayed):
		fmt.Printf("Message %s was answered before (%s).\n", messageID, replayed.Result.Outcome)
		return nil
	case err != nil:
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

// summaries stands in for the app's own store of finished work, by job id.
var summaries = map[string]summary{}

// summarise charges for a summary whose cost is known only afterwards. A job replayed after its
// hold settled answers the summary it produced before.
func summarise(ctx context.Context, customer entitler.Customer, jobID string) (summary, error) {
	s, err := entitler.WithHold(ctx, customer, aiCredits, 20, jobID,
		func(ctx context.Context, hold *entitler.Hold) (summary, error) {
			s := summary{text: "A short summary.", tokens: 7}
			return s, hold.Use(s.tokens)
		}, nil)
	var refused *entitler.UsageRefusedError
	var replayed *entitler.UsageReplayedError
	switch {
	case errors.As(err, &refused):
		return summary{}, errOutOfCredits
	case errors.As(err, &replayed):
		return summaries[jobID], nil
	case err != nil:
		return summary{}, err
	}
	summaries[jobID] = s
	return s, nil
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

	if err := streamReply(ctx, customer, "message-"+run, []int64{3, 4, 2}); errors.Is(err, errOutOfCredits) {
		fmt.Println(err)
	} else if err != nil {
		log.Fatal(err)
	}
	for range 2 {
		s, err := summarise(ctx, customer, "summary-job-"+run)
		if errors.Is(err, errOutOfCredits) {
			fmt.Println(err)
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(s.text)
	}

	result, err := customer.RecordUsage(ctx, aiCredits, 2, "bandwidth-"+run,
		&entitler.RecordUsageParams{Mode: entitler.Observe})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Observed 2 credits; %d over the allowance.\n", result.OverBy)
}
