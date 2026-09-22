CREATE TABLE services (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE health_checks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('UP', 'DOWN')),
    http_status_code INTEGER,
    response_time_ms INTEGER,
    checked_at INTEGER NOT NULL,
    error_kind TEXT,
    error_message TEXT
);

CREATE INDEX health_checks_service_time ON health_checks(service_id, checked_at DESC, id DESC);
