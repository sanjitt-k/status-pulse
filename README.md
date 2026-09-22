# StatusPulse

StatusPulse is a lightweight uptime and service monitoring application written in Go. Phase 4 persists services and HTTP check history in SQLite and provides observed uptime summaries.

Services and check history survive restarts in a local SQLite database. Run this version locally or on a trusted private network; registered URLs cause outbound requests, including to private addresses.

## Requirements

- Go 1.22 or newer

## Run locally

From the repository root:

```powershell
go run ./cmd/statuspulse
```

The server listens on `:8080` by default. Open <http://localhost:8080/> to verify that it is running. Press Ctrl+C to shut it down gracefully.

## Configuration

Set `STATUSPULSE_ADDR` to change the listen address:

```powershell
$env:STATUSPULSE_ADDR = ":9090"
go run ./cmd/statuspulse
```

## Service API

Create a service:

```powershell
$body = @{
    name = "Example"
    url  = "https://example.com"
} | ConvertTo-Json

Invoke-RestMethod `
    -Method Post `
    -Uri http://localhost:8080/api/services `
    -ContentType "application/json" `
    -Body $body
```

List all services:

```powershell
Invoke-RestMethod http://localhost:8080/api/services
```

Get one service:

```powershell
Invoke-RestMethod http://localhost:8080/api/services/1
```

Delete a service:

```powershell
Invoke-RestMethod -Method Delete http://localhost:8080/api/services/1
```

### Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/` | Verify that StatusPulse is running |
| `POST` | `/api/services` | Register a service |
| `GET` | `/api/services` | List registered services |
| `GET` | `/api/services/{id}` | Get one service |
| `DELETE` | `/api/services/{id}` | Delete one service |
| `GET` | `/api/services/{id}/checks?limit=50&offset=0` | Recent checks, newest first |
| `GET` | `/api/services/{id}/summary` | Latest observation and 24-hour observed uptime |

## Monitoring

The scheduler performs a cycle at startup, then checks all registered services
sequentially every 60 seconds. Services added during a cycle join the next one.
Cycles never overlap; slow endpoints can delay checks and ticks may be dropped.

Each GET request has a five-second timeout and follows at most five redirects.
The final HTTP response is UP for 200–299 and DOWN otherwise. Request failures
are DOWN with a diagnostic reason. TLS certificate verification remains enabled.
Response time measures time to response headers (including redirects), not body
download. HTTP failures such as 503 have latency; network failures have null
HTTP status and latency. Bodies are closed without downloading their contents.

Configure durations with Go duration syntax; invalid or nonpositive values fail startup:

```powershell
$env:STATUSPULSE_CHECK_INTERVAL = "10s"
$env:STATUSPULSE_REQUEST_TIMEOUT = "2s"
go run ./cmd/statuspulse
```

After registering a service, wait for the next cycle and inspect its history:

```powershell
Invoke-RestMethod 'http://localhost:8080/api/services/1/checks?limit=10'
```

An empty array means the service has not been checked yet. Use `limit=1` for its
latest observation, and inspect `checked_at` to determine how old it is.
SQLite retains all checks; `limit` accepts 1–100
and `offset` is nonnegative. Deleting a service removes its history, and results
from checks still in flight are discarded. Shutdown cancels active requests
without recording a false DOWN result and waits for the worker to exit.

## Verification

```powershell
go test ./...
go vet ./...
go build ./cmd/statuspulse
```

Focused monitoring tests use local HTTP servers for success, HTTP errors,
redirects, timeout, connection failure, cancellation, and API/history integration.

## SQLite persistence

The default file is `statuspulse.db` in the working directory. Set an explicit
path to use the same database when launching from another directory:

```powershell
$env:STATUSPULSE_DB_PATH = 'C:\Users\sanji\Projects\StatusPulse\statuspulse.db'
go run ./cmd/statuspulse
```

The parent directory must exist and be writable. Startup fails if the database
cannot be opened or migrated. Embedded, numbered SQL migrations create `services`,
`health_checks`, and a `schema_migrations` tracking table before HTTP requests or
monitoring start. Each migration is recorded in the same transaction as its SQL.
Do not edit applied migrations; add the next numbered file for schema changes.

The application uses `database/sql` with the CGo-free `modernc.org/sqlite` driver.
One database connection serializes short operations; HTTP checks run outside
transactions. Foreign keys are enabled on each connection, and deletion cascades
to history. Checks are ordered by check timestamp descending, then ID descending.
UTC timestamps are stored as Unix milliseconds. Database files are ignored by Git.

```powershell
Invoke-RestMethod 'http://localhost:8080/api/services/1/summary'
```

The summary includes `latest_check`, `uptime_percent_24h`, and `sample_count_24h`.
Observed uptime is UP samples divided by all recorded samples in the last 24
hours, multiplied by 100. Zero samples yield null uptime; an unchecked service
has a null latest check. Missed checks while the application was stopped are
unknown, not successful samples. The latest check may be old: inspect its timestamp.

There is no automatic history retention yet, so the database grows over time.
For a simple backup, stop the application cleanly before copying the database.
The memory store remains for lightweight tests; production startup uses SQLite.
SQLite tests cover reopening files, migration tracking, cascade deletion,
nullable values, history ordering/pagination, and uptime window boundaries.
