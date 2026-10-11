# Feature constants and the command-line tool

Feature arguments take typed constants: `entitler.OnOffFeature`, `entitler.ConfigFeature`,
`entitler.MeteredFeature` and `entitler.GroupFeature`, or a plain `entitler.Key`. Usage methods
take only a `MeteredFeature`. Declare one by hand with `entitler.Metered("ai_credits")`, or
generate them all.

## go tool entitler

Add the tool to your module, then generate:

```sh
go get -tool github.com/entitlerhq/entitler-go/cmd/entitler
go tool entitler generate -out entitler_features.go -package features
```

or from a `//go:generate` line, where the package defaults to the file's own:

```go
//go:generate go tool entitler generate -out features/entitler_features.go -package features
```

The file holds one constant per feature, sorted by key, named after it (`export_pdf` is
`FeatureExportPdf`, `support_sla_hours` is `FeatureSupportSLAHours`), with its name, description,
unit and a group's members in its doc comment, and an archived feature marked `Deprecated`. When
the file exists, each feature keeps its constant's name, so adding a feature never renames another.

| Option | Default | Meaning |
| --- | --- | --- |
| `-out` | `entitler_features.go` | the file to write |
| `-package` | `$GOPACKAGE`, the file's package, or `features` | the package |
| `-key` | `$ENTITLER_KEY` | a key with `plans:read` |
| `-base-url` | `https://api.entitler.dev` | the API |
| `-check` | | fail when the file is out of date, ignoring the `Read from` line |
| `-allow-empty` | | write the file even when Entitler lists no features |

Generate from the live environment, with a key holding only `plans:read`: a test environment's All
customers track may follow an open change. Run `-check` nightly and on `main` rather than as a
required pull request check, since pull requests from forks have no key. When Entitler lists no
features and the file holds some, nothing is written unless you pass `-allow-empty`.

## Snapshot keys

```sh
go tool entitler snapshot-keys -out snapshot_keys.json
```

writes the snapshot keys an app ships (see [offline snapshots](offline-snapshots.md)). Run it in
the app's build, before `go build`, so key pinning needs no hand-written download. It needs no key.
