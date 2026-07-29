package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
)

func TestManagedStoreRoundTripsEncryptedStateAndRevisionTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = database.Close()
	}()
	var key muicrypto.MasterKey
	for index := range key {
		key[index] = byte(index + 1)
	}
	sealer, err := muicrypto.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	state := managedTestState()
	transaction, err := managed.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.ReplaceDesiredState(ctx, state); err != nil {
		t.Fatalf("ReplaceDesiredState() error = %v", err)
	}
	revision := domain.Revision{
		ID:             "fdb9b3cc-b97a-41c0-bf79-d9b237fbab7c",
		RevisionNumber: 1,
		SHA256:         strings.Repeat("a", 64),
		FilePath:       "/var/lib/m-ui/revisions/one.yaml",
		StateFilePath:  "/var/lib/m-ui/revisions/one.json",
		Status:         domain.RevisionPending,
		Reason:         "test publication",
		CreatedAt:      state.AsOf,
	}
	if err := transaction.InsertRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if err := transaction.ActivateRevision(ctx, revision.ID, state.AsOf); err != nil {
		t.Fatal(err)
	}
	if err := transaction.InsertAudit(ctx, AuditEntry{
		ID:              "b84de3ee-1606-47cd-b542-089497f745c1",
		Action:          "config.publish",
		ResourceType:    "config_revision",
		ResourceID:      revision.ID,
		Result:          "success",
		SummaryRedacted: "published test revision",
		CreatedAt:       state.AsOf,
	}); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var controllerCiphertext, privateCiphertext string
	if err := database.db.QueryRowContext(
		ctx,
		"SELECT value FROM settings WHERE key = ?",
		settingControllerSecret,
	).Scan(&controllerCiphertext); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(
		ctx,
		"SELECT reality_private_key_ciphertext FROM listeners LIMIT 1",
	).Scan(&privateCiphertext); err != nil {
		t.Fatal(err)
	}
	for name, ciphertext := range map[string]string{
		"controller": controllerCiphertext,
		"private":    privateCiphertext,
	} {
		if !strings.HasPrefix(ciphertext, "v1.") {
			t.Errorf("%s ciphertext = %q, want versioned envelope", name, ciphertext)
		}
	}
	databaseBytes := controllerCiphertext + privateCiphertext
	if strings.Contains(databaseBytes, state.ControllerSecret) ||
		strings.Contains(databaseBytes, state.Listeners[0].RealityPrivateKey) {
		t.Fatal("encrypted database fields contain plaintext secret material")
	}

	readTransaction, err := managed.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := readTransaction.DesiredState(ctx, state.AsOf)
	if err != nil {
		t.Fatal(err)
	}
	if err := readTransaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if loaded.ControllerSecret != state.ControllerSecret ||
		loaded.Listeners[0].RealityPrivateKey != state.Listeners[0].RealityPrivateKey ||
		loaded.Listeners[0].Users[0].UUID != state.Listeners[0].Users[0].UUID {
		t.Fatalf("round-tripped state differs: %#v", loaded)
	}
	storedRevision, err := managed.Revision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedRevision.Status != domain.RevisionActive ||
		storedRevision.ActivatedAt == nil {
		t.Fatalf("stored revision = %#v", storedRevision)
	}
}

func TestInitialSettingsEncryptStableControllerBootstrapSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = database.Close()
	}()
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{4, 5, 6})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 28, 4, 0, 0, 0, time.UTC)
	initial := InitialSettings{
		PanelTitle:         "m-ui",
		UILanguage:         "en-US",
		PublicHost:         "vpn.example.com",
		PanelListenAddress: "127.0.0.1",
		PanelListenPort:    2095,
		TrustedProxyCIDRs:  []string{"192.0.2.0/24"},
		MihomoBinaryPath:   "/usr/local/bin/mihomo",
		MihomoConfigDir:    "/etc/mihomo",
		MihomoConfigPath:   "/etc/mihomo/config.yaml",
		ControllerAddress:  "127.0.0.1:9090",
		BootstrapSecret:    "synthetic-controller-bootstrap-secret",
		MihomoServiceName:  "mihomo.service",
		HistoryLimit:       20,
	}
	if err := managed.EnsureInitialSettings(ctx, initial, now); err != nil {
		t.Fatal(err)
	}
	first, err := managed.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.ControllerSecret != initial.BootstrapSecret ||
		first.PanelTitle != "m-ui" ||
		first.MihomoConfigPath != "/etc/mihomo/config.yaml" {
		t.Fatalf("initial settings = %#v", first)
	}
	var storedSecret string
	if err := database.db.QueryRowContext(
		ctx,
		"SELECT value FROM settings WHERE key = ?",
		settingControllerSecret,
	).Scan(&storedSecret); err != nil {
		t.Fatal(err)
	}
	if storedSecret == first.ControllerSecret ||
		strings.Contains(storedSecret, first.ControllerSecret) {
		t.Fatal("database contains plaintext Controller secret")
	}

	updated := initial
	updated.PanelTitle = "local-file-title-must-not-overwrite-managed"
	updated.MihomoConfigPath = "/etc/mihomo/next.yaml"
	if err := managed.EnsureInitialSettings(
		ctx,
		updated,
		now.Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	second, err := managed.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.ControllerSecret != first.ControllerSecret {
		t.Fatal("Controller secret changed during settings reconciliation")
	}
	if second.PanelTitle != initial.PanelTitle {
		t.Fatal("managed display setting was overwritten by local configuration")
	}
	if second.MihomoConfigPath != updated.MihomoConfigPath {
		t.Fatal("advanced local setting was not reconciled")
	}
}

func TestManagedStoreSerializesImmediateTransactions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = database.Close()
	}()
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{1})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	first, err := managed.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	blockedContext, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := managed.BeginImmediate(blockedContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second BeginImmediate() error = %v, want deadline", err)
	}
	if err := first.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestManagedStoreDegradedStateAndRetentionNeverDeleteActive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = database.Close()
	}()
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{2})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	if err := managed.MarkDegraded(ctx, "recovery failed", "revision-1", now); err != nil {
		t.Fatal(err)
	}
	systemState, err := managed.SystemState(ctx)
	if err != nil || !systemState.Degraded ||
		systemState.DegradedRevisionID != "revision-1" {
		t.Fatalf("SystemState() = %#v, %v", systemState, err)
	}
	if err := managed.ClearDegraded(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	systemState, err = managed.SystemState(ctx)
	if err != nil || systemState.Degraded {
		t.Fatalf("cleared SystemState() = %#v, %v", systemState, err)
	}

	for number := int64(1); number <= 2; number++ {
		transaction, err := managed.BeginImmediate(ctx)
		if err != nil {
			t.Fatal(err)
		}
		revision := domain.Revision{
			ID:             "revision-" + string(rune('0'+number)),
			RevisionNumber: number,
			SHA256:         strings.Repeat("b", 64),
			FilePath:       "/revision.yaml",
			StateFilePath:  "/revision.json",
			Status:         domain.RevisionPending,
			Reason:         "retention test",
			CreatedAt:      now.Add(time.Duration(number) * time.Minute),
		}
		if err := transaction.InsertRevision(ctx, revision); err != nil {
			t.Fatal(err)
		}
		if err := transaction.ActivateRevision(ctx, revision.ID, revision.CreatedAt); err != nil {
			t.Fatal(err)
		}
		if err := transaction.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	expired, err := managed.InactiveRevisionsBeyond(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 0 {
		t.Fatalf("InactiveRevisionsBeyond() = %#v", expired)
	}
	if err := managed.DeleteRevision(ctx, "revision-2"); err == nil {
		t.Fatal("DeleteRevision() deleted the active revision")
	}
}

func TestInactiveRevisionRetentionIncludesFailedAndRolledBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openTestStore(t)
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{3})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)

	insertSuccessful := func(number int64) {
		t.Helper()
		transaction, err := managed.BeginImmediate(ctx)
		if err != nil {
			t.Fatal(err)
		}
		revision := retentionRevision(number, domain.RevisionPending, now)
		if err := transaction.InsertRevision(ctx, revision); err != nil {
			t.Fatal(err)
		}
		if err := transaction.ActivateRevision(ctx, revision.ID, revision.CreatedAt); err != nil {
			t.Fatal(err)
		}
		if err := transaction.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	insertFailed := func(number int64) {
		t.Helper()
		if err := managed.RecordFailedRevision(
			ctx,
			retentionRevision(number, domain.RevisionFailed, now),
		); err != nil {
			t.Fatal(err)
		}
	}

	insertSuccessful(1)
	insertFailed(2)
	insertSuccessful(3)
	insertFailed(4)
	insertSuccessful(5)

	expired, err := managed.InactiveRevisionsBeyond(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 2 ||
		expired[0].ID != "revision-2" ||
		expired[1].ID != "revision-1" {
		t.Fatalf("InactiveRevisionsBeyond() = %#v", expired)
	}
	if err := managed.DeleteRevision(ctx, "revision-5"); err == nil {
		t.Fatal("DeleteRevision() deleted the active revision")
	}
}

func TestPublicationSnapshotRejectsMultipleActiveRevisions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openTestStore(t)
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{7})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	state := managedTestState()
	transaction, err := managed.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.ReplaceDesiredState(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for number := int64(1); number <= 2; number++ {
		revision := retentionRevision(number, domain.RevisionActive, state.AsOf)
		if _, err := database.DB().ExecContext(
			ctx,
			`INSERT INTO config_revisions(
				id, revision_number, sha256, file_path, state_file_path,
				status, reason, created_at, activated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			revision.ID,
			revision.RevisionNumber,
			revision.SHA256,
			revision.FilePath,
			revision.StateFilePath,
			revision.Status,
			revision.Reason,
			formatTime(revision.CreatedAt),
			formatTime(revision.CreatedAt),
		); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := managed.ReadPublicationSnapshot(
		ctx,
		state.AsOf,
	); !errors.Is(err, ErrMultipleActiveRevisions) {
		t.Fatalf("ReadPublicationSnapshot() error = %v, want ErrMultipleActiveRevisions", err)
	}
}

func retentionRevision(
	number int64,
	status domain.RevisionStatus,
	now time.Time,
) domain.Revision {
	return domain.Revision{
		ID:             fmt.Sprintf("revision-%d", number),
		RevisionNumber: number,
		SHA256:         strings.Repeat("c", 64),
		FilePath:       fmt.Sprintf("/revision-%d.yaml", number),
		StateFilePath:  fmt.Sprintf("/revision-%d.json", number),
		Status:         status,
		Reason:         "retention test",
		CreatedAt:      now.Add(time.Duration(number) * time.Minute),
	}
}

func managedTestState() domain.DesiredState {
	listenerID := "8070e289-c5b8-418e-af60-42788dc3c16f"
	return domain.DesiredState{
		AsOf:              time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
		ControllerAddress: "127.0.0.1:9090",
		ControllerSecret:  "managed-controller-secret",
		PublicHost:        "node.example.com",
		Listeners: []domain.Listener{{
			ID:                listenerID,
			Name:              "primary",
			Enabled:           true,
			ListenAddress:     "0.0.0.0",
			ListenPort:        443,
			ServerName:        "www.example.com",
			RealityDest:       "www.example.com:443",
			RealityPrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			RealityPublicKey:  "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE",
			ShortID:           "0123456789abcdef",
			UDPEnabled:        true,
			Users: []domain.User{{
				ID:         "67610ca7-773a-4f63-be55-c601059528be",
				ListenerID: listenerID,
				Name:       "active",
				Enabled:    true,
				UUID:       "8b946508-36e4-43a7-9a2d-d34420bf2ad9",
			}},
		}},
	}
}
