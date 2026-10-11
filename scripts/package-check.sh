#!/bin/sh
set -eu

root=$(git rev-parse --show-toplevel)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

cd "$root"
git archive --format=tar HEAD | tar -x -C "$work"
git archive --format=zip -o "$work/module.zip" HEAD -- . ':(exclude)examples'

cd "$work"
go mod verify
GOFLAGS=-mod=readonly go build ./...
GOFLAGS=-mod=readonly go vet ./...
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' . | grep -v '^github.com/entitlerhq/entitler-go' && {
  echo "The package imports a module outside the standard library."
  exit 1
}
(cd examples && GOFLAGS=-mod=readonly go build ./...)
go build -trimpath -ldflags='-s -w' -o "$work/entitler" ./cmd/entitler

zip_bytes=$(wc -c < "$work/module.zip")
cli_bytes=$(wc -c < "$work/entitler")
echo "Module zip: $((zip_bytes / 1024)) KiB"
echo "entitler command: $((cli_bytes / 1024)) KiB"
