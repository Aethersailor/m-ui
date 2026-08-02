package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Aethersailor/m-ui/migrations"
)

var (
	ErrNotFound                = errors.New("record not found")
	ErrSingleAdminConflict     = errors.New("a different administrator already exists")
	ErrMultipleAdministrators  = errors.New("multiple administrators exist")
	ErrBootstrapCompleted      = errors.New("administrator bootstrap is already completed")
	ErrBootstrapUnavailable    = errors.New("administrator bootstrap is unavailable")
	ErrInvalidBootstrapToken   = errors.New("administrator bootstrap token is invalid")
	ErrMultipleActiveRevisions = errors.New("multiple active configuration revisions found")
)

const databaseTimeFormat = "2006-01-02T15:04:05.000000000Z"

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, databasePath string) (*Store, error) {
	if strings.TrimSpace(databasePath) == "" {
		return nil, errors.New("database path is required")
	}

	dsn := databasePath
	isMemory := databasePath == ":memory:" ||
		strings.Contains(databasePath, "mode=memory")
	if !isMemory {
		absolute, err := filepath.Abs(databasePath)
		if err != nil {
			return nil, fmt.Errorf("resolve database path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
		dsn = "file:" + filepath.ToSlash(absolute)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db}
	if err := store.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if !isMemory {
		if err := os.Chmod(databasePath, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("set database permissions: %w", err)
		}
	}
	return store, nil
}

func (s *Store) initialize(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping SQLite: %w", err)
	}
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure SQLite with %q: %w", statement, err)
		}
	}
	if err := s.migrate(ctx); err != nil {
		return fmt.Errorf("migrate SQLite: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}

	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	type migration struct {
		version int
		name    string
	}
	var ordered []migration
	seen := make(map[int]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return fmt.Errorf("migration %q has no numeric prefix", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil || version <= 0 {
			return fmt.Errorf("migration %q has invalid version", entry.Name())
		}
		if previous, exists := seen[version]; exists {
			return fmt.Errorf(
				"migrations %q and %q use version %d",
				previous,
				entry.Name(),
				version,
			)
		}
		seen[version] = entry.Name()
		ordered = append(ordered, migration{version: version, name: entry.Name()})
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].version < ordered[j].version
	})

	for _, item := range ordered {
		var applied int
		err := s.db.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
			item.version,
		).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", item.name, err)
		}
		if applied != 0 {
			continue
		}

		content, err := migrations.Files.ReadFile(item.name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", item.name, err)
		}
		transaction, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", item.name, err)
		}
		if _, err := transaction.ExecContext(ctx, string(content)); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("execute migration %s: %w", item.name, err)
		}
		if _, err := transaction.ExecContext(
			ctx,
			`INSERT INTO schema_migrations(version, name, applied_at)
			 VALUES (?, ?, ?)`,
			item.version,
			item.name,
			formatTime(time.Now()),
		); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("record migration %s: %w", item.name, err)
		}
		if err := transaction.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", item.name, err)
		}
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(databaseTimeFormat)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored UTC time: %w", err)
	}
	return parsed.UTC(), nil
}
