package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
	"statuspulse/internal/model"
)

//go:embed migrations/*.sql
var migrations embed.FS

type SQLiteStore struct{ db *sql.DB }

// OpenSQLite initializes the schema before the server or worker starts.
func OpenSQLite(ctx context.Context, path string) (*SQLiteStore, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := u.Query()
	// Driver pragmas apply to every newly opened connection, including replacements.
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &SQLiteStore{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize SQLite: %w", err)
	}
	return s, nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

// Ready performs a bounded schema read, not a write or an external endpoint check.
func (s *SQLiteStore) Ready(ctx context.Context) error {
	var count int
	return s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, entry.Name()).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations VALUES (?, ?)`, entry.Name(), time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) Create(ctx context.Context, service model.Service) (model.Service, error) {
	service.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	result, err := s.db.ExecContext(ctx, `INSERT INTO services(name,url,created_at) VALUES(?,?,?)`, service.Name, service.URL, service.CreatedAt.UnixMilli())
	if err != nil {
		return model.Service{}, err
	}
	service.ID, err = result.LastInsertId()
	return service, err
}

type scanner interface{ Scan(...any) error }

func scanService(row scanner) (model.Service, error) {
	var service model.Service
	var timestamp int64
	err := row.Scan(&service.ID, &service.Name, &service.URL, &timestamp)
	service.CreatedAt = time.UnixMilli(timestamp).UTC()
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return service, err
}

func (s *SQLiteStore) Get(ctx context.Context, id int64) (model.Service, error) {
	return scanService(s.db.QueryRowContext(ctx, `SELECT id,name,url,created_at FROM services WHERE id=?`, id))
}

func (s *SQLiteStore) List(ctx context.Context) ([]model.Service, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,url,created_at FROM services ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	services := make([]model.Service, 0)
	for rows.Next() {
		service, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

func requireAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) Delete(ctx context.Context, id int64) error {
	return requireAffected(s.db.ExecContext(ctx, `DELETE FROM services WHERE id=?`, id))
}

func (s *SQLiteStore) SaveCheck(ctx context.Context, check model.HealthCheck) error {
	// The existence check and insert are one statement: deletion during an
	// outbound request cannot resurrect a service or leave an orphan result.
	return requireAffected(s.db.ExecContext(ctx, `INSERT INTO health_checks
	(service_id,status,http_status_code,response_time_ms,checked_at,error_kind,error_message)
	SELECT ?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM services WHERE id=?)`,
		check.ServiceID, check.Status, check.HTTPStatusCode, check.ResponseTimeMS, check.CheckedAt.UnixMilli(), check.ErrorKind, check.ErrorMessage, check.ServiceID))
}

func scanCheck(row scanner) (model.HealthCheck, error) {
	var check model.HealthCheck
	var timestamp int64
	var kind, message sql.NullString
	err := row.Scan(&check.ID, &check.ServiceID, &check.Status, &check.HTTPStatusCode, &check.ResponseTimeMS, &timestamp, &kind, &message)
	check.CheckedAt = time.UnixMilli(timestamp).UTC()
	check.ErrorKind, check.ErrorMessage = kind.String, message.String
	return check, err
}

const checkColumns = `id,service_id,status,http_status_code,response_time_ms,checked_at,error_kind,error_message`

func (s *SQLiteStore) ListChecks(ctx context.Context, id int64, limit, offset int) ([]model.HealthCheck, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := scanService(tx.QueryRowContext(ctx, `SELECT id,name,url,created_at FROM services WHERE id=?`, id)); err != nil {
		return nil, err
	}
	checks := make([]model.HealthCheck, 0)
	if limit <= 0 || offset < 0 {
		return checks, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+checkColumns+` FROM health_checks WHERE service_id=? ORDER BY checked_at DESC,id DESC LIMIT ? OFFSET ?`, id, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		check, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		checks = append(checks, check)
	}
	return checks, rows.Err()
}

// Summary uses a single read transaction for a consistent service, latest
// observation, and sample count. Missing intervals are not counted as uptime.
func (s *SQLiteStore) Summary(ctx context.Context, id int64, now time.Time) (model.ServiceSummary, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.ServiceSummary{}, err
	}
	defer tx.Rollback()
	service, err := scanService(tx.QueryRowContext(ctx, `SELECT id,name,url,created_at FROM services WHERE id=?`, id))
	if err != nil {
		return model.ServiceSummary{}, err
	}
	summary := model.ServiceSummary{Service: service}
	latest, err := scanCheck(tx.QueryRowContext(ctx, `SELECT `+checkColumns+` FROM health_checks WHERE service_id=? ORDER BY checked_at DESC,id DESC LIMIT 1`, id))
	if err == nil {
		summary.LatestCheck = &latest
	} else if !errors.Is(err, sql.ErrNoRows) {
		return summary, err
	}
	var up int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN status='UP' THEN 1 ELSE 0 END),0) FROM health_checks WHERE service_id=? AND checked_at>=? AND checked_at<=?`, id, now.Add(-24*time.Hour).UnixMilli(), now.UnixMilli()).Scan(&summary.SampleCount, &up)
	if err != nil {
		return summary, err
	}
	if summary.SampleCount > 0 {
		percentage := float64(up) * 100 / float64(summary.SampleCount)
		summary.UptimePercent = &percentage
	}
	return summary, nil
}
