package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	coremanagement "github.com/Aethersailor/m-ui/internal/core"
	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
)

func TestCoreSettingsAndStateRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	var key muicrypto.MasterKey
	sealer, err := muicrypto.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	settings := coremanagement.Settings{
		Channel:       coremanagement.ChannelAlpha,
		AutoUpdate:    true,
		CheckInterval: 6 * time.Hour,
		Managed:       true,
	}
	if err := managed.EnsureCoreSettings(ctx, settings, now); err != nil {
		t.Fatal(err)
	}
	stored, err := managed.CoreSettings(ctx)
	if err != nil || stored != settings {
		t.Fatalf("CoreSettings() = %#v, %v", stored, err)
	}
	identity := coremanagement.ReleaseIdentity{
		Channel:           coremanagement.ChannelAlpha,
		Repository:        coremanagement.UpstreamRepository,
		ReleaseID:         1,
		TagName:           coremanagement.AlphaTag,
		Prerelease:        true,
		PublishedAt:       now,
		AssetID:           2,
		AssetName:         "mihomo-linux-amd64-compatible-alpha.gz",
		AssetSize:         3,
		AssetDigestSHA256: strings.Repeat("a", 64),
	}
	next := now.Add(6 * time.Hour)
	state := coremanagement.State{
		Available:         &identity,
		LastCheckAt:       &now,
		LastCheckResult:   "success",
		LastErrorRedacted: "secret=value",
		NextCheckAt:       &next,
		UpdateInProgress:  true,
	}
	if err := managed.SaveCoreState(ctx, state); err != nil {
		t.Fatal(err)
	}
	storedState, err := managed.CoreState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if storedState.Available == nil ||
		storedState.Available.AssetDigestSHA256 != identity.AssetDigestSHA256 ||
		storedState.LastErrorRedacted == "secret=value" ||
		!storedState.UpdateInProgress {
		t.Fatalf("CoreState() = %#v", storedState)
	}
}

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
		"SELECT protocol_secret_ciphertext FROM nodes LIMIT 1",
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
		strings.Contains(databaseBytes, state.Nodes[0].VLESS.Security.Reality.PrivateKey) {
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
		loaded.Nodes[0].VLESS.Security.Reality.PrivateKey != state.Nodes[0].VLESS.Security.Reality.PrivateKey ||
		loaded.Nodes[0].Users[0].VLESS.UUID != state.Nodes[0].Users[0].VLESS.UUID {
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

func TestManagedStorePersistsR3ProtocolsThroughSQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{22, 23, 24})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	state := managedTestState()
	ids := []string{"19d82af5-2630-4495-bea4-d7b15042b306", "a778ec03-705a-43dd-9c2d-d63370924597", "95467761-0588-42db-9f86-16593a23e04f"}
	userIDs := []string{"5f38aec7-a0ee-4727-a487-eabfc1a29f91", "cc957f54-5354-4510-a342-0a1bef691dc9", "632582e0-34d5-4245-be2f-62e64509a223"}
	profileIDs := []string{"1bf1e6eb-4ec3-47c9-aa47-cf5d3ca17083", "8f6db035-e8ce-484c-bdfb-0569b88840fa", "0d7419bc-bced-4301-9515-260779d1e04d"}
	state.Nodes = []domain.Node{
		{ID: ids[0], Name: "vmess", Enabled: true, ListenAddress: "0.0.0.0", Port: "41001", Protocol: domain.ProtocolVMess, SchemaVersion: domain.NodeSchemaVersion, Generation: 1,
			VMess:          &domain.VMessSpec{Handler: domain.VLESSHandlerSpec{Type: domain.VMessHandlerMKCP, MKCP: &domain.MKCPConfig{Seed: "sqlite-mkcp-seed"}}, Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone}},
			Users:          []domain.NodeUser{{ID: userIDs[0], NodeID: ids[0], Name: "alice", Enabled: true, VMess: &domain.VMessCredential{UUID: "405de9d6-025c-41ed-8033-9b5c0106df18", Cipher: "auto"}}},
			AccessProfiles: []domain.AccessProfile{{ID: profileIDs[0], NodeID: ids[0], Name: "default", Default: true, PublicPort: 41001}}},
		{ID: ids[1], Name: "trojan", Enabled: true, ListenAddress: "0.0.0.0", Port: "41002", Protocol: domain.ProtocolTrojan, SchemaVersion: domain.NodeSchemaVersion, Generation: 1,
			Trojan:         &domain.TrojanSpec{Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw}, Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone}, Shadowsocks: domain.TrojanShadowsocksSpec{Enabled: true, Method: "aes-128-gcm", Password: "sqlite-wrapper-secret"}},
			Users:          []domain.NodeUser{{ID: userIDs[1], NodeID: ids[1], Name: "bob", Enabled: true, Trojan: &domain.TrojanCredential{Password: "sqlite-trojan-secret"}}},
			AccessProfiles: []domain.AccessProfile{{ID: profileIDs[1], NodeID: ids[1], Name: "default", Default: true, PublicPort: 41002}}},
		{ID: ids[2], Name: "shadowsocks", Enabled: true, ListenAddress: "0.0.0.0", Port: "41003", Protocol: domain.ProtocolShadowsocks, SchemaVersion: domain.NodeSchemaVersion, Generation: 1,
			Shadowsocks:    &domain.ShadowsocksSpec{Cipher: "aes-128-gcm", UDP: true, Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone}},
			Users:          []domain.NodeUser{{ID: userIDs[2], NodeID: ids[2], Name: "carol", Enabled: true, Shadowsocks: &domain.ShadowsocksCredential{Password: "sqlite-ss-secret"}}},
			AccessProfiles: []domain.AccessProfile{{ID: profileIDs[2], NodeID: ids[2], Name: "default", Default: true, PublicPort: 41003}}},
	}
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
	read, err := managed.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := read.DesiredState(ctx, state.AsOf)
	if err != nil {
		t.Fatal(err)
	}
	if err := read.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Nodes) != 3 {
		t.Fatalf("loaded nodes = %d", len(loaded.Nodes))
	}
	byKind := make(map[domain.ProtocolKind]domain.Node)
	for _, node := range loaded.Nodes {
		byKind[node.Protocol] = node
	}
	if byKind[domain.ProtocolVMess].VMess.Handler.MKCP.Seed != "sqlite-mkcp-seed" ||
		byKind[domain.ProtocolTrojan].Trojan.Shadowsocks.Password != "sqlite-wrapper-secret" ||
		byKind[domain.ProtocolShadowsocks].Users[0].Shadowsocks.Password != "sqlite-ss-secret" {
		t.Fatalf("round-tripped R3 nodes = %#v", byKind)
	}
	rows, err := database.db.QueryContext(ctx, "SELECT protocol_config_json FROM nodes")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plaintext string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		plaintext += value
	}
	for _, secret := range []string{"sqlite-mkcp-seed", "sqlite-wrapper-secret", "sqlite-trojan-secret", "sqlite-ss-secret"} {
		if strings.Contains(plaintext, secret) {
			t.Fatalf("protocol config leaked %q", secret)
		}
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
		CookieSecure:       true,
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
		!first.CookieSecure ||
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
	updated.CookieSecure = false
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
	if !second.CookieSecure {
		t.Fatal("managed cookie security setting was overwritten by local configuration")
	}
	if second.MihomoConfigPath != updated.MihomoConfigPath {
		t.Fatal("advanced local setting was not reconciled")
	}
}

func TestEndpointSettingsTrackPendingAndLastAppliedSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{7, 8, 9})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.EnsureInitialSettings(ctx, InitialSettings{
		PanelTitle:         "m-ui",
		UILanguage:         "en-US",
		PublicHost:         "node.example.com",
		PanelListenAddress: "0.0.0.0",
		PanelListenPort:    2095,
		MihomoBinaryPath:   "/usr/local/bin/mihomo",
		MihomoConfigDir:    "/etc/mihomo",
		MihomoConfigPath:   "/etc/mihomo/config.yaml",
		ControllerAddress:  "127.0.0.1:9090",
		BootstrapSecret:    "synthetic-controller-bootstrap-secret",
		MihomoServiceName:  "mihomo.service",
		HistoryLimit:       20,
	}, time.Now().UTC()); err != nil {
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

	initial, err := managed.EndpointSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Pending != nil || initial.Active.Generation != 1 {
		t.Fatalf("initial endpoint state = %#v", initial)
	}

	state.AsOf = state.AsOf.Add(time.Minute)
	state.PanelUIBind = domain.Endpoint{Host: "0.0.0.0", Port: 2095}
	state.MihomoExternalControllerBind = domain.Endpoint{Host: "::", Port: 9090}
	state.MihomoControllerConnect = domain.Endpoint{Host: "::1", Port: 9090}
	state.ExternalControllerCORSOrigins = []string{"https://dashboard.example.com"}
	transaction, err = managed.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.ReplaceDesiredState(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	changed, err := managed.EndpointSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Pending == nil || !changed.Pending.RequiresMUIRestart ||
		!changed.Pending.RequiresMihomoRestart ||
		changed.Active.MihomoExternalControllerBind.Host != "::" {
		t.Fatalf("changed endpoint state = %#v", changed)
	}
	if err := managed.ClearEndpointRestartRequirements(
		ctx,
		true,
		false,
		changed.Pending.Generation-1,
		changed.Pending.EndpointSettings,
	); !errors.Is(err, ErrEndpointStateChanged) {
		t.Fatalf("stale endpoint restart clear error = %v, want ErrEndpointStateChanged", err)
	}
	unchanged, err := managed.EndpointSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Pending == nil || !unchanged.Pending.RequiresMUIRestart {
		t.Fatalf("stale clear changed endpoint restart state = %#v", unchanged)
	}

	// Returning to the last applied snapshot resolves the pending change even
	// though the active desired state had already moved to the new candidate.
	state.AsOf = state.AsOf.Add(time.Minute)
	state.PanelUIBind = domain.Endpoint{Host: "0.0.0.0", Port: 2095}
	state.MihomoExternalControllerBind = domain.Endpoint{Host: "127.0.0.1", Port: 9090}
	state.MihomoControllerConnect = domain.Endpoint{Host: "127.0.0.1", Port: 9090}
	state.ExternalControllerCORSOrigins = nil
	transaction, err = managed.BeginImmediate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.ReplaceDesiredState(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	resolved, err := managed.EndpointSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Pending != nil {
		t.Fatalf("resolved endpoint state still pending = %#v", resolved.Pending)
	}
}

func TestEnsureInitialSettingsMigratesLegacyWildcardControllerAddress(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		legacy     string
		wantBind   string
		wantClient string
	}{
		{name: "ipv4", legacy: "0.0.0.0:9090", wantBind: "0.0.0.0", wantClient: "127.0.0.1"},
		{name: "ipv6", legacy: "[::]:9090", wantBind: "::", wantClient: "::1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			database, err := Open(ctx, ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = database.Close() }()
			now := time.Now().UTC()
			if _, err := database.DB().Exec(
				"INSERT INTO settings(key, value, updated_at) VALUES (?, ?, ?)",
				settingControllerAddress,
				test.legacy,
				formatTime(now),
			); err != nil {
				t.Fatal(err)
			}
			sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{41, 42, 43})
			if err != nil {
				t.Fatal(err)
			}
			managed, err := NewManagedStore(database, sealer)
			if err != nil {
				t.Fatal(err)
			}
			if err := managed.EnsureInitialSettings(ctx, InitialSettings{
				PanelTitle:         "m-ui",
				UILanguage:         "en-US",
				PublicHost:         "node.example.com",
				PanelListenAddress: "127.0.0.1",
				PanelListenPort:    2095,
				MihomoBinaryPath:   "/usr/local/bin/mihomo",
				MihomoConfigDir:    "/etc/mihomo",
				MihomoConfigPath:   "/etc/mihomo/config.yaml",
				ControllerAddress:  "127.0.0.1:9090",
				MihomoServiceName:  "mihomo.service",
				HistoryLimit:       20,
			}, now); err != nil {
				t.Fatal(err)
			}
			state, err := managed.EndpointSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if state.Active.MihomoExternalControllerBind.Host != test.wantBind ||
				state.Active.MihomoControllerConnect.Host != test.wantClient {
				t.Fatalf("migrated endpoint state = %#v", state.Active)
			}
		})
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
	nodeID := "8070e289-c5b8-418e-af60-42788dc3c16f"
	return domain.DesiredState{
		AsOf:              time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
		ControllerAddress: "127.0.0.1:9090",
		ControllerSecret:  "managed-controller-secret",
		PublicHost:        "node.example.com",
		Nodes: []domain.Node{{
			ID: nodeID, Name: "primary", Enabled: true, ListenAddress: "0.0.0.0", Port: "443",
			Protocol: domain.ProtocolVLESS, SchemaVersion: domain.NodeSchemaVersion,
			VLESS: &domain.VLESSSpec{Decryption: "none", Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw}, Security: domain.VLESSSecuritySpec{
				Type: domain.VLESSSecurityReality, Reality: &domain.RealityConfig{
					Destination: "www.example.com:443", PrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
					PublicKey: "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE",
					ShortIDs:  []string{"0123456789abcdef"}, ServerNames: []string{"www.example.com"},
				},
			}},
			Users: []domain.NodeUser{{
				ID:      "67610ca7-773a-4f63-be55-c601059528be",
				NodeID:  nodeID,
				Name:    "active",
				Enabled: true,
				VLESS:   &domain.VLESSCredential{UUID: "8b946508-36e4-43a7-9a2d-d34420bf2ad9"},
			}},
			AccessProfiles: []domain.AccessProfile{{
				ID: "8896f7f4-c100-4c11-b6d8-e2e2b342322d", NodeID: nodeID,
				Name: "default", Default: true, PublicPort: 443, ServerName: "www.example.com",
			}},
			Generation: 1,
		}},
	}
}
