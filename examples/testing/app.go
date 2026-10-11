// Package app is a small app's gating and billing code, tested with entitlertest.FakeCustomer.
//
//	go test ./testing
package app

import (
	"context"
	"errors"

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

// Summarise spends credits on a summary, charging what it used.
func Summarise(ctx context.Context, customer entitler.Customer, jobID string, words int64) (string, error) {
	summary, err := entitler.WithHold(ctx, customer, aiCredits, 10, jobID,
		func(_ context.Context, hold *entitler.Hold) (string, error) {
			return "summary", hold.Use(min(words/100+1, 10))
		}, nil)
	var refused *entitler.UsageRefusedError
	if errors.As(err, &refused) {
		return "", ErrUpgrade
	}
	return summary, err
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
