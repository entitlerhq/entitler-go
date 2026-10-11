# Contributing

## Setup

Install Go 1.26 or later. The module uses the standard library alone; the examples are a separate
module in `examples/` that uses this one through a `replace` directive.

## Commands

Run these before pushing. They are the checks CI runs:

```sh
gofmt -l . examples
go vet ./... && go vet -tags integration ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
GOTOOLCHAIN=go1.26.9 go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 -tags integration ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
go test -race ./...
go test -race -coverpkg=./... -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1
(cd examples && go vet ./... && go build ./... && go test ./...)
sh scripts/package-check.sh
```

`gofmt -l` prints nothing when the code is formatted. Staticcheck runs on Go 1.26, since v0.8.1
cannot read Go 1.27's export data. Coverage stays at 90% or more.

## Tests

- Unit tests make no network request. The conformance cases in `testdata/conformance` are shared
  by every Entitler SDK; `conformance_test.go` runs each of them.
- The live API tests run against a test environment:

  ```sh
  ENTITLER_TEST_KEY=ent_test_… go test -tags integration -run Live -v ./...
  ```

  `ENTITLER_TEST_PUBLISHABLE_KEY` adds the publishable-key tests.
- The golden files for the generator are in `generate/testdata`.

## Releasing

1. Move the `Unreleased` entries in `CHANGELOG.md` into a section for the new version, with the
   date.
2. Set `Version` in `version.go` to the same version, and merge both changes.
3. Tag the merge commit `v` followed by the version (for example `v0.2.0`) and push the tag.

`release.yml` checks that the tag matches `version.go`, runs every check in `ci.yml`, creates the
GitHub release from the changelog section, and requests the version from proxy.golang.org so
pkg.go.dev lists it.
