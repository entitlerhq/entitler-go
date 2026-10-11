package entitler_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/entitlerhq/entitler-go"
)

var features = struct {
	FeatureExportPdf     entitler.OnOffFeature
	FeatureAiCredits     entitler.MeteredFeature
	FeatureCollaboration entitler.GroupFeature
}{entitler.OnOff("export_pdf"), entitler.Metered("ai_credits"), entitler.Group("collaboration", "team_seats", "shared_folders")}

var errOutOfCredits = errors.New("out of credits")

func renderPDF(string) error                             { return nil }
func upgradePrompt() error                               { return nil }
func showCollaboration()                                 {}
func instrumented(t http.RoundTripper) http.RoundTripper { return t }

func docQuickStart(ctx context.Context) (*entitler.UsageResult, error) {
	srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()

	customer := srv.Customer(signedInUserID())
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
	return result, err
}

func docIsEntitled(ctx context.Context, customer entitler.Customer, document string) error {
	if customer.IsEntitled(ctx, features.FeatureExportPdf, false, nil) {
		return renderPDF(document)
	}
	return upgradePrompt()
}

func docCheck(ctx context.Context, customer entitler.Customer) error {
	check, err := customer.Check(ctx, features.FeatureAiCredits, nil)
	if err != nil {
		return err
	}
	if !check.Entitled {
		for _, upgrade := range check.Upgrades {
			fmt.Printf("%s %s (%s)\n", upgrade.Move, upgrade.Name, upgrade.Action)
		}
	}
	if check.Meter != nil {
		fmt.Printf("%s left, %d on hold\n", check.Meter.Remaining, check.Meter.Held)
	}
	return nil
}

func docEntitlements(ctx context.Context, customer entitler.Customer) error {
	list, err := customer.Entitlements(ctx, nil)
	if err != nil {
		return err
	}
	if list.Has(features.FeatureCollaboration) {
		showCollaboration()
	}
	return nil
}

func docRevalidate(ctx context.Context, customer entitler.Customer) (*entitler.Check, error) {
	check, err := customer.Check(ctx, features.FeatureExportPdf, &entitler.ReadParams{Revalidate: true})
	return check, err
}

func docRecord(ctx context.Context, customer entitler.Customer, requestID string) error {
	result, err := customer.RecordUsage(ctx, features.FeatureAiCredits, 25, requestID, nil)
	if err != nil {
		return err
	}
	if result.Outcome == entitler.OutcomeRefused {
		return errOutOfCredits
	}
	if result.Replayed {
		log.Printf("request %s was recorded before", requestID)
	}
	return nil
}

func docObserve(ctx context.Context, customer entitler.Customer, tokens int64, streamID string, finishedAt time.Time) error {
	_, err := customer.RecordUsage(ctx, features.FeatureAiCredits, tokens, streamID,
		&entitler.RecordUsageParams{Mode: entitler.Observe, OccurredAt: finishedAt})
	return err
}

func docBatch(ctx context.Context, srv *entitler.Server) error {
	result, err := srv.RecordUsageBatch(ctx, []entitler.BatchEvent{
		{Customer: "user_42", Feature: features.FeatureAiCredits, Amount: 120, IdempotencyKey: "invoice-77-line-1"},
		{Customer: "user_43", Feature: features.FeatureAiCredits, Amount: 80, IdempotencyKey: "invoice-77-line-2"},
	}, nil)
	if err != nil {
		return err
	}
	for i, r := range result.Results {
		if r.Outcome == entitler.OutcomeError {
			log.Printf("event %d: %s (%s)", i, r.Error.Message, r.Error.Code)
		}
	}
	return nil
}

func docRegisterVisitor(ctx context.Context, srv *entitler.Server, userID, name, email, visitorFromSignUp string) error {
	_, err := srv.Customer(userID).Register(ctx, &entitler.RegisterParams{
		Name:    new(name),
		Email:   new(email),
		Visitor: visitorFromSignUp,
	})
	return err
}

func docCancelAddOn(ctx context.Context, customer entitler.Customer) (*entitler.PlanChange, error) {
	change, err := customer.Cancel(ctx, &entitler.CancelParams{AddOn: new("sso_addon")})
	return change, err
}

type storeTransaction struct {
	ID        string
	ProductID string
	ExpiresAt time.Time
}

func docApple(ctx context.Context, customer *entitler.ServerCustomer, transaction storeTransaction) error {
	_, err := customer.SetPlan(ctx, entitler.SKU{
		Connector: "apple",
		IDs:       map[string]string{"productId": transaction.ProductID},
	}, &entitler.SetPlanParams{
		Until:          transaction.ExpiresAt,
		Decision:       entitler.Decision{Reason: "App Store purchase", Actor: "app-store"},
		IdempotencyKey: "apple-" + transaction.ID,
	})
	return err
}

func docErase(ctx context.Context, customer *entitler.ServerCustomer) error {
	err := customer.Erase(ctx, nil)
	return err
}

func docWith(ctx context.Context, srv *entitler.Server, userID string) (*entitler.Check, error) {
	fast := srv.With(entitler.WithTimeout(2 * time.Second))
	check, err := fast.Customer(userID).Check(ctx, features.FeatureExportPdf, nil)
	return check, err
}

func docOnError(key string) (*entitler.Server, error) {
	srv, err := entitler.NewServer(key, entitler.WithOnError(func(err error) {
		slog.Warn("entitler fallback", "error", err)
	}))
	return srv, err
}

func docErrors(err error) {
	var apiErr *entitler.APIError
	if errors.As(err, &apiErr) && apiErr.Code == entitler.CodeScopeRequired {
		log.Printf("the key lacks a scope (request %s)", apiErr.RequestID)
	}
}

func docAsOf(ctx context.Context, customer *entitler.ServerCustomer) (*entitler.CustomerPlans, error) {
	nextMonth, err := customer.Plans(ctx, &entitler.ReadParams{AsOf: time.Now().AddDate(0, 1, 0)})
	return nextMonth, err
}

func docTracks(ctx context.Context, customer *entitler.ServerCustomer) error {
	if _, err := customer.SetTrack(ctx, new("Beta"), nil); err != nil {
		return err
	}
	_, err := customer.SetTrack(ctx, nil, nil)
	return err
}

func docScopes(ctx context.Context, client *entitler.Client) error {
	scopes, err := client.Scopes(ctx)
	if err != nil {
		return err
	}
	showBuyButton := scopes.Has(entitler.ScopeBillingSelf)
	fmt.Println(showBuyButton)
	return nil
}

func docConfiguration() (*entitler.Server, error) {
	srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"),
		entitler.WithTimeout(5*time.Second),
		entitler.WithMaxRetries(1),
		entitler.WithHTTPClient(&http.Client{Transport: instrumented(http.DefaultTransport)}),
	)
	return srv, err
}

var entitlerServer = sync.OnceValues(func() (*entitler.Server, error) {
	return entitler.NewServer(os.Getenv("ENTITLER_KEY"))
})

var (
	_ = docQuickStart
	_ = docIsEntitled
	_ = docRecord
	_ = docObserve
	_ = docCheck
	_ = docEntitlements
	_ = docRevalidate
	_ = docBatch
	_ = docRegisterVisitor
	_ = docCancelAddOn
	_ = docApple
	_ = docErase
	_ = docWith
	_ = docOnError
	_ = docErrors
	_ = docAsOf
	_ = docTracks
	_ = docScopes
	_ = docConfiguration
	_ = entitlerServer
)
