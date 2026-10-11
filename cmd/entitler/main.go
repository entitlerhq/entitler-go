// Command entitler writes the files an app built with the Entitler SDK ships: typed feature
// constants, and the snapshot keys that verify offline snapshots.
//
// Add it to a module as a tool and run it from the SDK's own package:
//
//	go get -tool github.com/entitlerhq/entitler-go/cmd/entitler
//	go tool entitler generate -out entitler_features.go -package features
//	go tool entitler snapshot-keys -out snapshot_keys.json
//
// or from a //go:generate line:
//
//	//go:generate go tool entitler generate -out entitler_features.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/entitlerhq/entitler-go"
	"github.com/entitlerhq/entitler-go/generate"
)

const usage = `Usage: go tool entitler <command> [options]

Commands:
  generate        Write typed feature constants for this module.
  snapshot-keys   Write the snapshot keys an app ships to verify offline snapshots.

generate options:
  -out <file>        The file to write (default entitler_features.go).
  -package <name>    The file's package (default $GOPACKAGE, the existing file's, or features).
  -key <key>         An API key with plans:read (default $ENTITLER_KEY).
  -base-url <url>    The API's base URL (default https://api.entitler.dev).
  -check             Fail when the file is out of date, writing nothing.
  -allow-empty       Write the file even when Entitler lists no features.

snapshot-keys options:
  -out <file>        The file to write (default entitler-snapshot-keys.json).
  -base-url <url>    The API's base URL (default https://api.entitler.dev).

Options take -flag or --flag, with =value or a separate value.
`

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 1
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case "generate":
		return runGenerate(ctx, args[1:], getenv, stdout, stderr)
	case "snapshot-keys":
		return runSnapshotKeys(ctx, args[1:], stdout, stderr)
	}
	_, _ = fmt.Fprintf(stderr, "Unknown command %q.\n\n%s", args[0], usage)
	return 1
}

func flags(name string, stdout, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, usage) }
	return fs
}

// parsed reports how a flag parse ended: -1 to go on, else the exit code (0 after --help).
func parsed(err error) int {
	switch {
	case err == nil:
		return -1
	case errors.Is(err, flag.ErrHelp):
		return 0
	}
	return 1
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// failure prints an API failure as Entitler request failed: <message> (<code>), and any other
// failure as its own message, never inventing a code.
func failure(stderr io.Writer, err error) int {
	var apiErr *entitler.APIError
	var timeout *entitler.TimeoutError
	var conn *entitler.ConnectionError
	switch {
	case errors.As(err, &apiErr):
		_, _ = fmt.Fprintf(stderr, "Entitler request failed: %s (%s).\n", strings.TrimSuffix(apiErr.Message, "."), apiErr.Code)
	case errors.As(err, &timeout):
		_, _ = fmt.Fprintf(stderr, "Entitler request failed: %s (%s).\n", strings.TrimSuffix(timeout.Error(), "."), entitler.CodeTimedOut)
	case errors.As(err, &conn):
		_, _ = fmt.Fprintf(stderr, "Entitler request failed: %s (%s).\n", strings.TrimSuffix(conn.Error(), "."), entitler.CodeConnectionFailed)
	default:
		_, _ = fmt.Fprintln(stderr, err)
	}
	return 1
}

var packageClause = regexp.MustCompile(`(?m)^package\s+([A-Za-z_][A-Za-z0-9_]*)`)

func runGenerate(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flags("generate", stdout, stderr)
	out := fs.String("out", "entitler_features.go", "")
	pkg := fs.String("package", "", "")
	key := fs.String("key", getenv("ENTITLER_KEY"), "")
	baseURL := fs.String("base-url", "", "")
	check := fs.Bool("check", false, "")
	allowEmpty := fs.Bool("allow-empty", false, "")
	if code := parsed(fs.Parse(args)); code >= 0 {
		return code
	}
	if strings.TrimSpace(*key) == "" {
		_, _ = fmt.Fprintln(stderr, "Provide an API key with --key or set ENTITLER_KEY. The key needs the plans:read scope.")
		return 1
	}
	existing, err := os.ReadFile(*out)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if *pkg == "" {
		*pkg = getenv("GOPACKAGE")
	}
	if m := packageClause.FindSubmatch(existing); *pkg == "" && m != nil {
		*pkg = string(m[1])
	}
	if *pkg == "" {
		*pkg = "features"
	}
	opts := []entitler.ServerOption{entitler.WithoutCache()}
	if *baseURL != "" {
		opts = append(opts, entitler.WithBaseURL(*baseURL))
	}
	srv, err := entitler.NewServer(*key, opts...)
	if err != nil {
		return failure(stderr, err)
	}
	defer func() { _ = srv.Close() }()
	list, err := srv.Features(ctx)
	if err != nil {
		return failure(stderr, err)
	}
	if len(list.Features) == 0 && generate.Count(existing) > 0 && !*allowEmpty {
		_, _ = fmt.Fprintf(stderr, "Entitler listed no features, so %s was left as it is. Check the key's environment, or pass --allow-empty.\n", *out)
		return 1
	}
	file, err := generate.Render(list, generate.Options{Package: *pkg, Existing: existing})
	if err != nil {
		return failure(stderr, err)
	}
	features := plural(len(list.Features), "feature", "features")
	if *check {
		if existing != nil && generate.SameIgnoringSource(existing, file) {
			_, _ = fmt.Fprintf(stdout, "%s is up to date with %s.\n", *out, features)
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "%s is out of date. Run go tool entitler generate to update it.\n", *out)
		return 1
	}
	if err := writeFile(*out, file); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Wrote %s to %s.\n", features, *out)
	return 0
}

func runSnapshotKeys(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flags("snapshot-keys", stdout, stderr)
	out := fs.String("out", "entitler-snapshot-keys.json", "")
	baseURL := fs.String("base-url", "", "")
	if code := parsed(fs.Parse(args)); code >= 0 {
		return code
	}
	var opts []entitler.ClientOption
	if *baseURL != "" {
		opts = append(opts, entitler.WithBaseURL(*baseURL))
	}
	client, err := entitler.NewPublishableClient("ent_pk_snapshot_keys", append(opts, entitler.WithoutCache())...)
	if err != nil {
		return failure(stderr, err)
	}
	defer func() { _ = client.Close() }()
	keys, err := client.SnapshotKeys(ctx)
	if err != nil {
		return failure(stderr, err)
	}
	if len(keys.Keys) == 0 {
		_, _ = fmt.Fprintf(stderr, "Entitler published no snapshot keys, so %s was left as it is.\n", *out)
		return 1
	}
	compact, err := json.Marshal(keys)
	if err != nil {
		return failure(stderr, err)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, compact, "", "  "); err != nil {
		return failure(stderr, err)
	}
	if err := writeFile(*out, []byte(indented.String()+"\n")); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Wrote %s to %s.\n", plural(len(keys.Keys), "snapshot key", "snapshot keys"), *out)
	return 0
}

// writeFile writes to a temporary file beside the target and moves it into place, so a failed run
// never leaves a partial file.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
