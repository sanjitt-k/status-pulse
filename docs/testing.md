# Testing StatusPulse

The Phase 6 suite uses Go's standard `testing` and `httptest` packages. Tests use
loopback HTTP servers, temporary SQLite files, and injected failures. They do not
contact public endpoints or modify the application's database.

## Run the checks

From the project root with Go on PATH:

```powershell
go test -count=1 -timeout=60s ./...
go vet ./...
go build ./cmd/statuspulse
go test -race -timeout=120s ./...
```

`-count=1` bypasses cached results. The race detector requires CGo and a supported
C compiler; on Windows it needs a compatible GCC toolchain on PATH and
`CGO_ENABLED=1`. Ordinary application builds remain CGo-free. A successful normal
test run is not a substitute for a successful race-detector run.

If Git ownership restrictions in a sandbox prevent VCS stamping, add
`-buildvcs=false` to Go test/vet/build commands; changing global Git trust is not
necessary for verification.

## Inspect coverage

Quote the coverage flag in PowerShell:

```powershell
go test '-coverprofile=coverage.out' ./...
go tool cover '-func=coverage.out'
go tool cover '-html=coverage.out'
```

Coverage shows which statements ran, not whether every important behavior is
correct. There is no arbitrary coverage threshold. The command entry point is
not exercised by these package tests; OS-signal and process lifecycle behavior
still require a separate manual check. Generated coverage files are ignored.

## What each layer proves

| Tests | Behaviors |
| --- | --- |
| `internal/config` | Defaults, environment overrides, invalid and overflowing durations |
| `internal/web` | JSON CRUD, IDs, pagination, malformed/oversized bodies, validation, error sanitization, HTML escaping, forms, origin protection |
| `internal/monitor` | HTTP success/failure, redirects, timeouts, connection/DNS/TLS failure, cancellation, sequential scheduling, deletion during checks, continuation after save failure |
| `internal/store` | Concurrent operations, memory copy isolation/retention, SQLite reopening, migrations, foreign keys, pagination, nullable fields, uptime windows |
| `TestSQLiteMonitoringEndToEnd` | HTTP registration → checker → SQLite → close/reopen → HTTP history and summary |

Scheduler tests supply ticks through a channel and coordinate with request-start
signals. They do not sleep to guess when a cycle finishes. Small deadline guards
cause broken synchronization to fail rather than hang. The timeout test uses a
local handler that waits for request cancellation. TLS uses an untrusted local
test certificate; DNS failure is supplied by a test transport.

Temporary directories isolate database tests. Concurrent-store tests use a wait
group to join workers and an error channel to report failures on the test goroutine.
`t.Setenv` restores configuration after tests; these tests are intentionally not
parallel because the environment is process-wide.

## Manual lifecycle check

Start the application with a temporary database path, add a local service, and
wait for a result. Press Ctrl+C, confirm the shutdown log, restart with the same
database, and verify the service/history remain. Do not use your real database
for destructive test scenarios.

## Phase 6 verification on this workspace

The ordinary test suite passes. The Windows race-detector attempt is blocked
because GCC is not installed; enabling CGo alone is insufficient. Run the race
command on a machine with a supported compiler before treating that check as
complete. Docker, CI, and deployment setup remain later phases.
