// Offline fetches a snapshot and the snapshot keys while online, then verifies the snapshot with
// no request and gates a feature from it when Entitler cannot be reached.
//
//	ENTITLER_KEY=ent_test_… go run ./offline
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/entitlerhq/entitler-go"
)

// signedInUserID stands in for the app's own session: the expected customer never comes from
// the stored snapshot.
func signedInUserID() string { return "test_1001" }

func main() {
	ctx := context.Background()
	srv, err := entitler.NewServer(os.Getenv("ENTITLER_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	customer := srv.Customer(signedInUserID())

	keys, err := srv.SnapshotKeys(ctx)
	if err != nil {
		log.Fatal(err)
	}
	snapshot, err := customer.Snapshot(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	check, err := customer.Check(ctx, entitler.OnOff("export_pdf"), nil)
	if err != nil {
		log.Fatal(err)
	}
	environmentID := check.Environment.ID

	expected := entitler.SnapshotExpectation{
		Keys:        keys.Keys,
		Customer:    signedInUserID(),
		Environment: environmentID,
	}
	exportPDF := entitler.OnOff("export_pdf")
	fmt.Println("Export to PDF:", isEntitled(ctx, customer, exportPDF, snapshot.Token, expected))

	verified, err := entitler.VerifySnapshot(snapshot.Token, expected)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("The snapshot verifies offline until %s.\n", verified.ExpiresAt.Format("2 Jan 2006 15:04 MST"))
}

// isEntitled asks Entitler, and falls back to the snapshot when Entitler cannot be reached.
func isEntitled(ctx context.Context, customer entitler.Customer, feature entitler.Feature,
	snapshot string, expected entitler.SnapshotExpectation) bool {
	check, err := customer.Check(ctx, feature, nil)
	if err == nil {
		return check.Entitled
	}
	if !entitler.IsUnreachable(err) {
		return false
	}
	verified, err := entitler.VerifySnapshot(snapshot, expected)
	return err == nil && verified.Entitlements.Has(feature)
}
