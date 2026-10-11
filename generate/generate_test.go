package generate_test

import (
	"flag"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entitlerhq/entitler-go"
	"github.com/entitlerhq/entitler-go/generate"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func release(n int64) *int64 { return &n }

func sample() *entitler.FeatureList {
	return &entitler.FeatureList{
		Environment: entitler.Environment{Name: "development"},
		Track:       entitler.Track{Name: "All customers"},
		Release:     release(2),
		Features: []entitler.FeatureListing{
			{Key: "team_seats", Name: "Team seats", Type: entitler.FeatureTypeConfig, Description: "People who can join.", Unit: "seats"},
			{Key: "ai_credits", Name: "AI credits", Type: entitler.FeatureTypeMetered, Unit: "credits"},
			{Key: "export_pdf", Name: "Export to PDF", Type: entitler.FeatureTypeBoolean, Description: "Line one.\nLine two."},
			{Key: "support_sla_hours", Name: "Support SLA", Type: entitler.FeatureTypeConfig},
			{Key: "collaboration", Name: "Collaboration", Type: entitler.FeatureTypeGroup, Includes: []string{"team_seats", "shared_folders"}},
			{Key: "team_essentials", Name: "Team essentials", Type: entitler.FeatureTypeGroup, Includes: []string{"collaboration", "export_pdf", "missing", "team_essentials"}},
			{Key: "shared_folders", Name: "", Type: entitler.FeatureTypeBoolean, Archived: true},
			{Key: "tiers", Name: "Tiers", Type: "tiered"},
			{Key: "api_access", Name: "API access", Type: entitler.FeatureTypeBoolean},
		},
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("%s differs:\n%s", name, got)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), name, got, parser.ParseComments); err != nil {
		t.Errorf("%s does not parse: %v", name, err)
	}
}

func TestRenderSample(t *testing.T) {
	out, err := generate.Render(sample(), generate.Options{Package: "features"})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "sample.go.golden", out)
}

func TestNames(t *testing.T) {
	for key, want := range map[string]string{
		"export_pdf": "FeatureExportPdf", "support_sla_hours": "FeatureSupportSLAHours", "api_access": "FeatureAPIAccess",
		"user_id": "FeatureUserID", "café": "FeatureCaf", "2fa": "Feature2fa", "": "Feature",
	} {
		if got := generate.Name(key); got != want {
			t.Errorf("%q: %s, want %s", key, got, want)
		}
	}
}

func TestKeptNamesAndEscapes(t *testing.T) {
	existing := []byte(`package features

var (
	TheOldName = entitler.OnOff("export_pdf")
	FeatureAICredits = entitler.Metered("ai_credits")
	FeatureGone = entitler.OnOff("gone")
)
`)
	list := &entitler.FeatureList{Change: new("chg_1"), Features: []entitler.FeatureListing{
		{Key: "export_pdf", Type: entitler.FeatureTypeBoolean},
		{Key: "ai_credits", Type: entitler.FeatureTypeMetered},
		{Key: "a-b", Type: entitler.FeatureTypeBoolean},
		{Key: "a_b", Type: entitler.FeatureTypeBoolean},
		{Key: "new_one", Type: entitler.FeatureTypeBoolean},
	}}
	out, err := generate.Render(list, generate.Options{Package: "features", Existing: existing})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "kept.go.golden", out)
	if generate.Count(out) != 5 || generate.KeptNames(out)["export_pdf"] != "TheOldName" {
		t.Error(generate.KeptNames(out))
	}
	if _, err := generate.Render(list, generate.Options{Package: "1bad"}); err == nil {
		t.Error("a bad package name")
	}
}

func TestUntrustedTextIsEscaped(t *testing.T) {
	hostile := "Evil */ name\r\nwith \"quotes\" \\ and line paragraph\u0085next\x00\x07control`tick"
	list := &entitler.FeatureList{
		Environment: entitler.Environment{Name: hostile},
		Track:       entitler.Track{Name: "track\npackage main"},
		Features: []entitler.FeatureListing{
			{Key: "key\"with`quote \x01", Name: hostile, Description: hostile, Unit: hostile, Type: entitler.FeatureTypeBoolean},
			{Key: "group", Name: hostile, Type: entitler.FeatureTypeGroup, Includes: []string{"key\"with`quote \x01", "x*/y"}},
		},
	}
	out, err := generate.Render(list, generate.Options{Package: "features"})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "escaped.go.golden", out)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "package ") && line != "package features" {
			t.Errorf("text broke out of a comment: %q", line)
		}
	}
}

func TestSameIgnoringSource(t *testing.T) {
	a, _ := generate.Render(sample(), generate.Options{Package: "features"})
	other := sample()
	other.Release = release(3)
	b, _ := generate.Render(other, generate.Options{Package: "features"})
	if string(a) == string(b) || !generate.SameIgnoringSource(a, b) {
		t.Error("a new release with the same features stays up to date")
	}
	other.Features = other.Features[1:]
	c, _ := generate.Render(other, generate.Options{Package: "features"})
	if generate.SameIgnoringSource(a, c) {
		t.Error("a feature gone is a change")
	}
}

func TestEmptyList(t *testing.T) {
	out, err := generate.Render(&entitler.FeatureList{}, generate.Options{Package: "features"})
	if err != nil || generate.Count(out) != 0 || strings.Contains(string(out), "import") {
		t.Fatalf("%s %v", out, err)
	}
}
