package app

import (
	"context"
	"errors"
	"testing"

	"github.com/entitlerhq/entitler-go"
	"github.com/entitlerhq/entitler-go/entitlertest"
)

func TestExportNeedsTheFeature(t *testing.T) {
	ctx := context.Background()
	paying, _ := entitlertest.FakeCustomer(entitlertest.Values{"export_pdf": true}, nil)
	if _, err := Export(ctx, paying, "report"); err != nil {
		t.Fatal(err)
	}
	free, _ := entitlertest.FakeCustomer(entitlertest.Values{"export_pdf": false}, nil)
	if _, err := Export(ctx, free, "report"); !errors.Is(err, ErrUpgrade) {
		t.Fatalf("got %v", err)
	}
}

func TestSummariseChargesWhatItUsed(t *testing.T) {
	ctx := context.Background()
	customer, _ := entitlertest.FakeCustomer(entitlertest.Values{"ai_credits": entitlertest.Metered(100, 0)}, nil)
	if _, err := Summarise(ctx, customer, "job-1", 250); err != nil {
		t.Fatal(err)
	}
	check, _ := customer.Check(ctx, entitler.Metered("ai_credits"), nil)
	if check.Meter.Used != 3 {
		t.Fatalf("used %d", check.Meter.Used)
	}
	if _, err := Summarise(ctx, customer, "job-1", 250); err == nil {
		t.Fatal("the same job's key replays: the work already happened")
	}
	empty, _ := entitlertest.FakeCustomer(entitlertest.Values{"ai_credits": entitlertest.Metered(5, 5)}, nil)
	if _, err := Summarise(ctx, empty, "job-2", 10); !errors.Is(err, ErrUpgrade) {
		t.Fatalf("got %v", err)
	}
}

func TestUpgradeSendsTheCustomerToPay(t *testing.T) {
	ctx := context.Background()
	customer, fake := entitlertest.FakeCustomer(nil, nil)
	fake.Answer("Subscribe", func(entitlertest.Write) (int, []byte) {
		return 200, entitlertest.SubscribeStep(entitler.SubscribeStep{Next: entitler.NextPay, URL: "https://checkout.example.com/pay"})
	})
	next, err := Upgrade(ctx, customer, "pro")
	if err != nil || next != "https://checkout.example.com/pay" {
		t.Fatalf("%q %v", next, err)
	}
	writes := fake.Writes()
	if len(writes) != 1 || writes[0].Method != "Subscribe" || writes[0].Body["plan"] != "pro" {
		t.Fatalf("%+v", writes)
	}
}
