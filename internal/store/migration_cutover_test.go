package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/migrations"
)

func TestR3CutoverMigratesV023DatabaseWithoutCollateralDataLoss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "m-ui-v0.2.3.db")
	seedV023Database(t, databasePath)

	migrated, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open(v0.2.3 database) error = %v", err)
	}
	t.Cleanup(func() { _ = migrated.Close() })

	assertRowCount(t, migrated.DB(), "schema_migrations", 8)
	for table, expected := range map[string]int{
		"nodes": 0, "node_users": 0, "access_profiles": 0,
		"config_revisions": 0,
		"admin_users":      1, "sessions": 1, "settings": 2,
		"audit_logs": 1, "system_state": 1,
		"core_settings": 1, "core_state": 1,
		"endpoint_settings_pending":      1,
		"endpoint_settings_last_applied": 1,
		"bootstrap_state":                1,
		"runtime_convergence_state":      1,
	} {
		assertRowCount(t, migrated.DB(), table, expected)
	}
	for _, removed := range []string{"listeners", "listener_users"} {
		var count int
		if err := migrated.DB().QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			removed,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("legacy table %q still exists", removed)
		}
	}

	assertScalar(t, migrated.DB(), "SELECT username FROM admin_users", "admin")
	assertScalar(t, migrated.DB(), "SELECT session_token_hash FROM sessions", "session-token-hash")
	assertScalar(t, migrated.DB(), "SELECT value FROM settings WHERE key = 'panel_title'", "v0.2.3 panel")
	assertScalar(t, migrated.DB(), "SELECT action FROM audit_logs", "v0.2.3.audit")
	assertScalar(t, migrated.DB(), "SELECT degraded_reason FROM system_state WHERE id = 1", "v0.2.3-system-state")
	assertScalar(t, migrated.DB(), "SELECT channel FROM core_settings WHERE id = 1", "alpha")
	assertScalar(t, migrated.DB(), "SELECT last_check_result FROM core_state WHERE id = 1", "v0.2.3-core-ok")
	assertScalar(t, migrated.DB(), "SELECT panel_ui_bind_host FROM endpoint_settings_pending WHERE id = 1", "0.0.0.0")
	assertScalar(t, migrated.DB(), "SELECT panel_ui_bind_host FROM endpoint_settings_last_applied WHERE id = 1", "127.0.0.1")
	assertScalar(t, migrated.DB(), "SELECT token_hash FROM bootstrap_state WHERE id = 1", "bootstrap-token-hash")
	assertScalar(t, migrated.DB(), "SELECT pending_reason FROM runtime_convergence_state WHERE id = 1", R3ProtocolCutoverReason)
	assertScalar(t, migrated.DB(), "SELECT CAST(r3_cutover_pending AS TEXT) FROM runtime_convergence_state WHERE id = 1", "1")

	assertR3ProtocolConstraints(t, migrated.DB())
	assertSQLiteIntegrity(t, migrated.DB())
}

func TestR3CutoverCreatesUsableFreshDatabase(t *testing.T) {
	t.Parallel()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	assertRowCount(t, database.DB(), "schema_migrations", 8)
	for _, table := range []string{
		"admin_users", "sessions", "settings", "audit_logs", "system_state",
		"core_settings", "core_state", "endpoint_settings_pending",
		"endpoint_settings_last_applied", "bootstrap_state", "nodes",
		"node_users", "access_profiles", "config_revisions", "runtime_convergence_state",
	} {
		var count int
		if err := database.DB().QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("fresh database table %q count = %d", table, count)
		}
	}
	assertR3ProtocolConstraints(t, database.DB())
	assertSQLiteIntegrity(t, database.DB())
}

func Test0008ConvergesDatabaseThatAlreadyRanOld0007(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "old-0007.db")
	seedV023Database(t, databasePath)
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	for _, migration := range []struct {
		version int
		name    string
	}{{6, "0006_nodes_v2.sql"}, {7, "0007_r3_protocol_cutover.sql"}} {
		content, readErr := migrations.Files.ReadFile(migration.name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		transaction, beginErr := database.BeginTx(ctx, nil)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if _, execErr := transaction.ExecContext(ctx, string(content)); execErr != nil {
			_ = transaction.Rollback()
			t.Fatal(execErr)
		}
		if _, execErr := transaction.ExecContext(
			ctx,
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)",
			migration.version,
			migration.name,
			"2026-08-06T00:00:00.000000000Z",
		); execErr != nil {
			_ = transaction.Rollback()
			t.Fatal(execErr)
		}
		if commitErr := transaction.Commit(); commitErr != nil {
			t.Fatal(commitErr)
		}
	}
	now := "2026-08-06T00:00:00.000000000Z"
	if _, err := database.ExecContext(ctx, `INSERT INTO nodes(
		id, name, enabled, listen_address, port, protocol_kind,
		protocol_schema_version, protocol_config_json,
		protocol_secret_ciphertext, generation, created_at, updated_at
	) VALUES ('post-0007-node', 'post-0007', 0, '0.0.0.0', '443',
		'vmess', 2, '{}', 'ciphertext', 1, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO node_users(
		id, node_id, name, enabled, credential_kind, credential_ciphertext,
		created_at, updated_at
	) VALUES ('post-0007-user', 'post-0007-node', 'user', 0, 'vmess',
		'ciphertext', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO access_profiles(
		id, node_id, name, is_default, public_port, allow_insecure,
		created_at, updated_at
	) VALUES ('post-0007-profile', 'post-0007-node', 'default', 1, 443, 0, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO config_revisions(
		id, revision_number, sha256, file_path, state_file_path, status,
		reason, created_at, activated_at
	) VALUES ('post-0007-revision', 1, ?, '/stale.yaml', '/stale.json',
		'active', 'post 0007', ?, ?)`, fmt.Sprintf("%064d", 1), now, now); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrated.Close() })
	for _, table := range []string{"nodes", "node_users", "access_profiles", "config_revisions"} {
		assertRowCount(t, migrated.DB(), table, 0)
	}
	assertScalar(t, migrated.DB(), "SELECT CAST(r3_cutover_pending AS TEXT) FROM runtime_convergence_state WHERE id = 1", "1")
}

func TestCompleteR3ProtocolCutoverIsAtomicAndDoesNotClearUnrelatedDegradedState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "convergence.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{8, 8, 8})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	if err := managed.MarkDegraded(ctx, "unrelated safety failure", "", now); err != nil {
		t.Fatal(err)
	}
	if err := managed.CompleteR3ProtocolCutover(ctx, now.Add(time.Minute)); err == nil {
		t.Fatal("CompleteR3ProtocolCutover() cleared an unrelated degraded state")
	}
	convergence, err := managed.RuntimeConvergenceState(ctx)
	if err != nil || !convergence.R3CutoverPending {
		t.Fatalf("runtime convergence state = %#v, %v", convergence, err)
	}
	if err := managed.ClearDegraded(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := managed.MarkDegraded(
		ctx,
		R3ProtocolCutoverReason+": prior startup validation failed",
		"",
		now.Add(3*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	if err := managed.CompleteR3ProtocolCutover(ctx, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := managed.CompleteR3ProtocolCutover(ctx, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("idempotent CompleteR3ProtocolCutover() error = %v", err)
	}
	convergence, err = managed.RuntimeConvergenceState(ctx)
	if err != nil || convergence.R3CutoverPending || convergence.PendingReason != "" {
		t.Fatalf("completed runtime convergence state = %#v, %v", convergence, err)
	}
	systemState, err := managed.SystemState(ctx)
	if err != nil || systemState.Degraded {
		t.Fatalf("completed system state = %#v, %v", systemState, err)
	}
}

func seedV023Database(t *testing.T, databasePath string) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close v0.2.3 database: %v", err)
		}
	}()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	for version, name := range []string{
		"0001_initial.sql",
		"0002_core_management.sql",
		"0003_endpoint_settings_pending.sql",
		"0004_endpoint_settings_last_applied.sql",
		"0005_bootstrap_state.sql",
	} {
		content, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		transaction, err := database.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := transaction.ExecContext(ctx, string(content)); err != nil {
			_ = transaction.Rollback()
			t.Fatalf("apply v0.2.3 migration %s: %v", name, err)
		}
		if _, err := transaction.ExecContext(
			ctx,
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)",
			version+1,
			name,
			"2026-08-03T00:00:00.000000000Z",
		); err != nil {
			_ = transaction.Rollback()
			t.Fatal(err)
		}
		if err := transaction.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	now := "2026-08-03T00:00:00.000000000Z"
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO admin_users(id, username, password_hash, password_changed_at, created_at, updated_at)
			VALUES ('admin-id', 'admin', 'password-hash', ?, ?, ?)`, []any{now, now, now}},
		{`INSERT INTO sessions(id, admin_user_id, session_token_hash, csrf_token_hash, expires_at, last_seen_at, created_at, user_agent)
			VALUES ('session-id', 'admin-id', 'session-token-hash', 'csrf-token-hash', ?, ?, ?, 'v0.2.3 test')`, []any{now, now, now}},
		{`INSERT INTO settings(key, value, updated_at) VALUES
			('panel_title', 'v0.2.3 panel', ?),
			('controller_secret_ciphertext', 'settings-ciphertext', ?)`, []any{now, now}},
		{`INSERT INTO listeners(id, name, enabled, listen_address, listen_port, server_name, reality_dest,
			reality_private_key_ciphertext, reality_public_key, short_id, udp_enabled, created_at, updated_at)
			VALUES ('listener-id', 'legacy', 1, '0.0.0.0', 443, 'example.com', 'example.com:443',
			'legacy-key-ciphertext', 'legacy-public-key', '0123456789abcdef', 1, ?, ?)`, []any{now, now}},
		{`INSERT INTO listener_users(id, listener_id, name, enabled, uuid, created_at, updated_at)
			VALUES ('legacy-user-id', 'listener-id', 'legacy-user', 1,
			'2b26a842-8bd1-493a-978b-ee5e546cf508', ?, ?)`, []any{now, now}},
		{`INSERT INTO config_revisions(id, revision_number, sha256, file_path, state_file_path, status,
			reason, actor_admin_id, created_at, activated_at)
			VALUES ('legacy-revision', 1, ?, '/legacy.yaml', '/legacy.json', 'active',
			'legacy revision', 'admin-id', ?, ?)`, []any{fmt.Sprintf("%064d", 1), now, now}},
		{`INSERT INTO audit_logs(id, actor_admin_id, action, resource_type, resource_id, result, summary_redacted, created_at)
			VALUES ('audit-id', 'admin-id', 'v0.2.3.audit', 'system', 'sentinel', 'success', 'preserve this audit', ?)`, []any{now}},
		{`UPDATE system_state SET degraded = 0, degraded_reason = 'v0.2.3-system-state', degraded_revision_id = NULL, updated_at = ? WHERE id = 1`, []any{now}},
		{`INSERT INTO core_settings(id, channel, auto_update, check_interval_seconds, managed, external_path, updated_at)
			VALUES (1, 'alpha', 1, 21600, 1, '', ?)`, []any{now}},
		{`UPDATE core_state SET current_manifest_json = '{}', last_check_at = ?, last_check_result = 'v0.2.3-core-ok',
			next_check_at = ?, update_in_progress = 0 WHERE id = 1`, []any{now, now}},
		{`INSERT INTO endpoint_settings_pending(id, panel_ui_bind_host, panel_ui_bind_port,
			mihomo_external_controller_bind_host, mihomo_external_controller_bind_port,
			mihomo_controller_connect_host, mihomo_controller_connect_port,
			external_controller_cors_origins_json, generation, requires_mui_restart,
			requires_mihomo_restart, updated_at)
			VALUES (1, '0.0.0.0', 2095, '127.0.0.1', 9090, '127.0.0.1', 9090,
			'[]', 2, 1, 0, ?)`, []any{now}},
		{`INSERT INTO endpoint_settings_last_applied(id, panel_ui_bind_host, panel_ui_bind_port,
			mihomo_external_controller_bind_host, mihomo_external_controller_bind_port,
			mihomo_controller_connect_host, mihomo_controller_connect_port,
			external_controller_cors_origins_json, generation, updated_at)
			VALUES (1, '127.0.0.1', 2095, '127.0.0.1', 9090, '127.0.0.1', 9090,
			'[]', 1, ?)`, []any{now}},
		{`INSERT INTO bootstrap_state(id, token_hash, token_ciphertext, created_at)
			VALUES (1, 'bootstrap-token-hash', 'bootstrap-token-ciphertext', ?)`, []any{now}},
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed v0.2.3 database with %q: %v", statement.query, err)
		}
	}
}

func assertR3ProtocolConstraints(t *testing.T, database *sql.DB) {
	t.Helper()
	now := "2026-08-06T00:00:00.000000000Z"
	protocols := []string{"vless", "hysteria2", "vmess", "trojan", "shadowsocks"}
	for index, protocol := range protocols {
		nodeID := fmt.Sprintf("constraint-node-%d", index)
		if _, err := database.Exec(
			`INSERT INTO nodes(id, name, enabled, listen_address, port, protocol_kind,
				protocol_schema_version, protocol_config_json, protocol_secret_ciphertext,
				generation, created_at, updated_at)
			 VALUES (?, ?, 0, '0.0.0.0', ?, ?, 2, '{}', 'ciphertext', 1, ?, ?)`,
			nodeID, protocol, fmt.Sprint(20000+index), protocol, now, now,
		); err != nil {
			t.Fatalf("insert %s node through migrated constraint: %v", protocol, err)
		}
		if _, err := database.Exec(
			`INSERT INTO node_users(id, node_id, name, enabled, credential_kind,
				credential_ciphertext, created_at, updated_at)
			 VALUES (?, ?, 'user', 0, ?, 'credential-ciphertext', ?, ?)`,
			fmt.Sprintf("constraint-user-%d", index), nodeID, protocol, now, now,
		); err != nil {
			t.Fatalf("insert %s user through migrated constraint: %v", protocol, err)
		}
	}
}

func assertRowCount(t *testing.T, database *sql.DB, table string, expected int) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != expected {
		t.Fatalf("%s row count = %d, want %d", table, count, expected)
	}
}

func assertScalar(t *testing.T, database *sql.DB, query, expected string) {
	t.Helper()
	var actual string
	if err := database.QueryRow(query).Scan(&actual); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	if actual != expected {
		t.Fatalf("query %q = %q, want %q", query, actual, expected)
	}
}

func assertSQLiteIntegrity(t *testing.T, database *sql.DB) {
	t.Helper()
	assertScalar(t, database, "PRAGMA integrity_check", "ok")
	rows, err := database.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Fatal("PRAGMA foreign_key_check reported a violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
