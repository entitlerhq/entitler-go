package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const features = `{"environment":{"id":"e","name":"development","kind":"test"},"track":{"id":"t","name":"All customers"},"release":RELEASE,"change":null,"features":[
{"id":"f1","key":"export_pdf","name":"Export to PDF","type":"boolean","description":"","unit":"","resetEvery":null,"archived":false,"includes":[]},
{"id":"f2","key":"ai_credits","name":"AI credits","type":"metered","description":"","unit":"credits","resetEvery":null,"archived":false,"includes":[]}]}`

const noFeatures = `{"environment":{"id":"e","name":"development","kind":"test"},"track":{"id":"t","name":"All customers"},"release":2,"change":null,"features":[]}`

const keys = `{"keys":[{"use":"sig","kty":"EC","crv":"P-256","x":"aa","y":"bb","kid":"k1","alg":"ES256"}]}`

func api(t *testing.T, status int, body string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cli(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, func(k string) string { return env[k] }, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestGenerateWritesChecksAndKeepsNames(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "entitler_features.go")
	srv := api(t, 200, strings.Replace(features, "RELEASE", "2", 1))
	env := map[string]string{"ENTITLER_KEY": "ent_test_key", "GOPACKAGE": "billing"}
	code, stdout, stderr := cli(t, env, "generate", "--out", out, "--base-url="+srv.URL)
	if code != 0 || stdout != "Wrote 2 features to "+out+".\n" || stderr != "" {
		t.Fatalf("%d %q %q", code, stdout, stderr)
	}
	file, _ := os.ReadFile(out)
	if !strings.Contains(string(file), "package billing") || !strings.Contains(string(file), "FeatureAiCredits = entitler.Metered(\"ai_credits\")") {
		t.Fatal(string(file))
	}
	code, stdout, _ = cli(t, map[string]string{}, "generate", "-out", out, "-base-url", srv.URL, "-key", "ent_test_key", "-check")
	if code != 0 || stdout != out+" is up to date with 2 features.\n" {
		t.Fatalf("%d %q", code, stdout)
	}
	renamed := strings.ReplaceAll(string(file), "FeatureExportPdf", "ExportPDF")
	_ = os.WriteFile(out, []byte(renamed), 0o644)
	newRelease := api(t, 200, strings.Replace(features, "RELEASE", "3", 1))
	if code, _, _ := cli(t, env, "generate", "-out", out, "-base-url", newRelease.URL, "-check"); code != 0 {
		t.Error("the Read from line is ignored by -check")
	}
	if code, _, _ := cli(t, env, "generate", "-out", out, "-base-url", newRelease.URL); code != 0 {
		t.Fatal(code)
	}
	file, _ = os.ReadFile(out)
	if !strings.Contains(string(file), "ExportPDF = entitler.OnOff(\"export_pdf\")") || !strings.Contains(string(file), "release 3") {
		t.Errorf("a kept name was renamed:\n%s", file)
	}
	empty := api(t, 200, noFeatures)
	code, _, stderr = cli(t, env, "generate", "-out", out, "-base-url", empty.URL)
	if code != 1 || stderr != "Entitler listed no features, so "+out+" was left as it is. Check the key's environment, or pass --allow-empty.\n" {
		t.Fatalf("%d %q", code, stderr)
	}
	code, stdout, _ = cli(t, env, "generate", "-out", out, "-base-url", empty.URL, "--allow-empty")
	if code != 0 || stdout != "Wrote 0 features to "+out+".\n" {
		t.Fatalf("%d %q", code, stdout)
	}
	code, _, stderr = cli(t, env, "generate", "-out", out, "-base-url", srv.URL, "-check")
	if code != 1 || stderr != out+" is out of date. Run go tool entitler generate to update it.\n" {
		t.Fatalf("%d %q", code, stderr)
	}
	one := api(t, 200, strings.Replace(strings.Replace(features, "RELEASE", "2", 1), `,
{"id":"f2","key":"ai_credits","name":"AI credits","type":"metered","description":"","unit":"credits","resetEvery":null,"archived":false,"includes":[]}`, "", 1))
	if _, stdout, _ := cli(t, env, "generate", "-out", out, "-base-url", one.URL); stdout != "Wrote 1 feature to "+out+".\n" {
		t.Error(stdout)
	}
}

func TestGenerateFailures(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "f.go")
	env := map[string]string{"ENTITLER_KEY": "ent_test_key"}
	cases := []struct {
		name   string
		env    map[string]string
		args   []string
		stderr string
	}{
		{"no key", map[string]string{}, []string{"generate", "-out", out}, "Provide an API key with --key or set ENTITLER_KEY. The key needs the plans:read scope.\n"},
		{"api error", env, []string{"generate", "-out", out, "-base-url", api(t, 403, `{"error":{"code":"scope_required","message":"The key lacks plans:read."}}`).URL},
			"Entitler request failed: The key lacks plans:read (scope_required).\n"},
		{"no code", env, []string{"generate", "-out", out, "-base-url", api(t, 404, ``).URL},
			"Entitler request failed: Entitler answered with HTTP 404 (http_error).\n"},
		{"publishable key", map[string]string{"ENTITLER_KEY": "ent_pk_test_x"}, []string{"generate", "-out", out},
			"a publishable key belongs in entitler.Client. Use a secret key from the dashboard on your server\n"},
		{"bad base url", env, []string{"generate", "-out", out, "-base-url", "ftp://x"}, "pass baseURL as an absolute http or https URL\n"},
		{"connection", env, []string{"generate", "-out", out, "-base-url", "http://127.0.0.1:1"}, "(connection_failed)"},
		{"bad flag", env, []string{"generate", "-nope"}, "flag provided but not defined: -nope\n"},
	}
	for _, c := range cases {
		code, _, stderr := cli(t, c.env, c.args...)
		if code != 1 || !strings.Contains(stderr, c.stderr) {
			t.Errorf("%s: %d %q", c.name, code, stderr)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a failed run wrote a file")
	}
}

func TestSnapshotKeys(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "keys.json")
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(keys))
	}))
	defer srv.Close()
	code, stdout, _ := cli(t, nil, "snapshot-keys", "--out", out, "--base-url", srv.URL)
	if code != 0 || stdout != "Wrote 1 snapshot key to "+out+".\n" || auth[0] != "" {
		t.Fatalf("%d %q %v", code, stdout, auth)
	}
	file, _ := os.ReadFile(out)
	want := "{\n  \"keys\": [\n    {\n      \"use\": \"sig\",\n      \"kty\": \"EC\",\n      \"crv\": \"P-256\",\n      \"x\": \"aa\",\n      \"y\": \"bb\",\n      \"kid\": \"k1\",\n      \"alg\": \"ES256\"\n    }\n  ]\n}\n"
	if string(file) != want {
		t.Errorf("members in Entitler's order, two-space indented:\n%s", file)
	}
	two := api(t, 200, `{"keys":[`+keys[9:len(keys)-2]+`,`+keys[9:len(keys)-2]+`]}`)
	if _, stdout, _ := cli(t, nil, "snapshot-keys", "-out", out, "-base-url", two.URL); stdout != "Wrote 2 snapshot keys to "+out+".\n" {
		t.Error(stdout)
	}
	empty := api(t, 200, `{"keys":[]}`)
	code, _, stderr := cli(t, nil, "snapshot-keys", "-out", out, "-base-url", empty.URL)
	if code != 1 || stderr != "Entitler published no snapshot keys, so "+out+" was left as it is.\n" {
		t.Fatalf("%d %q", code, stderr)
	}
	failing := api(t, 503, `{"error":{"code":"unavailable","message":"Try later."}}`)
	if code, _, stderr := cli(t, nil, "snapshot-keys", "-out", out, "-base-url", failing.URL); code != 1 || stderr != "Entitler request failed: Try later (unavailable).\n" {
		t.Errorf("%d %q", code, stderr)
	}
	if code, _, _ := cli(t, nil, "snapshot-keys", "-out", filepath.Join(dir, "missing", "k.json"), "-base-url", srv.URL); code != 1 {
		t.Error("an unwritable path fails")
	}
	if code, _, _ := cli(t, nil, "snapshot-keys", "-x"); code != 1 {
		t.Error("bad flag")
	}
}

func TestUsageAndExitCodes(t *testing.T) {
	if code, _, stderr := cli(t, nil); code != 1 || !strings.Contains(stderr, "Usage: go tool entitler") {
		t.Error("no command prints usage and exits 1")
	}
	for _, help := range [][]string{{"--help"}, {"-h"}, {"help"}, {"generate", "--help"}, {"snapshot-keys", "-help"}} {
		if code, stdout, _ := cli(t, nil, help...); code != 0 || !strings.Contains(stdout, "Usage: go tool entitler") {
			t.Errorf("%v: %d", help, code)
		}
	}
	if code, _, stderr := cli(t, nil, "frobnicate"); code != 1 || !strings.Contains(stderr, `Unknown command "frobnicate"`) {
		t.Error(stderr)
	}
}

// TestCommandSpelling fails on any spelling of the command other than `go tool entitler` in the
// docs, samples and README, since a bare name could resolve to someone else's package.
func TestCommandSpelling(t *testing.T) {
	bare := regexp.MustCompile(`(?m)(^|[^/\w-])entitler (generate|snapshot-keys)`)
	root := filepath.Join("..", "..")
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		data, _ := os.ReadFile(path)
		for _, m := range bare.FindAllIndex(data, -1) {
			before := string(data[max(m[0]-20, 0):m[1]])
			if !strings.Contains(before, "go tool entitler") && !strings.Contains(before, "generated by entitler generate") {
				t.Errorf("%s: %q", path, strings.TrimSpace(string(data[m[0]:m[1]])))
			}
		}
		return nil
	})
}
