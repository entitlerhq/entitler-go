# Versioning and support

The SDK follows semantic versioning; before 1.0.0, a minor version may change the API, and the
changelog says how. It supports the two Go releases the Go team supports (Go 1.26 and 1.27 today);
`go.mod` names the older. CI tests both on Linux, and the newer on macOS and Windows. The module
has no dependency outside the standard library.

Enums, codes and answer fields are open: a value this version does not know is kept as it is, and
unknown fields are ignored, so an older SDK keeps working as the API grows.
