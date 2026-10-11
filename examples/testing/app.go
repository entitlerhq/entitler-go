// Package app is a small app's gating and billing code, tested with entitlertest.FakeCustomer.
//
//	go test ./testing
package app

import (
	"context"
	"errors"
	"sync"

	"github.com/entitlerhq/entitler-go"
)

// ErrUpgrade means the customer needs another plan.
var ErrUpgrade = errors.New("upgrade to use this")

var (
	exportPDF = entitler.OnOff("export_pdf")
	aiCredits = entitler.Metered("ai_credits")
)

// Export renders a document as a PDF for customers entitled to it.
func Export(ctx context.Context, customer entitler.Customer, document string) (string, error) {
	if !customer.IsEntitled(ctx, exportPDF, false, nil) {
		return "", ErrUpgrade
	}
	return "%PDF " + document, nil
}

// summaries stands in for the app's own store of finished summaries, by job id.
var summaries sync.Map

// Summarise spends credits on a summary, charging what it used. A job that already finished
// answers its summary again without charging twice.
func Summarise(ctx context.Context, customer entitler.Customer, jobID string, words int64) (string, error) {
	summary, err := entitler.WithHold(ctx, customer, aiCredits, 10, jobID,
		func(_ context.Context, hold *entitler.Hold) (string, error) {
			return "summary of " + jobID, hold.Use(min(words/100+1, 10))
		}, nil)
	var refused *entitler.UsageRefusedError
	var replayed *entitler.UsageReplayedError
	switch {
	case errors.As(err, &refused):
		return "", ErrUpgrade
	case errors.As(err, &replayed):
		if earlier, ok := summaries.Load(jobID); ok {
			return earlier.(string), nil
		}
		return "", err
	case err != nil:
		return "", err
	}
	summaries.Store(jobID, summary)
	return summary, nil
}

// Upgrade moves the customer to a plan and answers where to send them next.
func Upgrade(ctx context.Context, customer entitler.Customer, plan string) (string, error) {
	step, err := customer.Subscribe(ctx, plan, &entitler.SubscribeParams{Period: "monthly", ReturnURL: "https://app.example.com/billing"})
	if err != nil {
		return "", err
	}
	if step.Next == entitler.NextPay {
		return step.URL, nil
	}
	return "/billing", nil
}
