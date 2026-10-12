# Changelog

All notable changes to this project are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-11

### Added

- `NewServer` for servers, and `NewClient`, `NewIdentityClient` and `NewPublishableClient` for apps
  on a person's device, sharing one `Customer` interface.
- Checks, entitlements, plans, pricing and usage reads, with an answer cache, write generations,
  reads at another instant and stale answers while Entitler is unreachable.
- Usage reports in gate and observe modes, holds with `StartHold` and `WithHold`, settlements,
  releases and batches, each with idempotency keys.
- Billing: `Subscribe`, `Cancel`, `UndoPendingChange`, `BillingPortal` and `SyncBilling`.
- The company's decisions: `SetPlan`, `SetAddOn`, grants, meter adjustments and cancelled usage.
- Customer registration, details, erasure, customer tokens, tracks and visitors.
- Offline snapshots with `VerifySnapshot` and the environment's snapshot keys.
- Retries with backoff and `Retry-After`, timeouts, typed errors and `IsUnreachable`.
- The `entitler` command, which generates feature constants and saves snapshot keys.
- `entitlertest`, with answer builders and `FakeCustomer`.

[Unreleased]: https://github.com/entitlerhq/entitler-go/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/entitlerhq/entitler-go/releases/tag/v0.1.0
