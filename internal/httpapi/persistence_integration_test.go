package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/auth"
	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/service"
	"github.com/Aethersailor/m-ui/internal/store"
)

// TestHTTPAPIEndToEndPersistsFiveProtocolsAndR3Operations is deliberately
// broader than the focused handler tests. It starts at the one-time browser
// setup endpoint, crosses the real authentication and CSRF middleware, and
// publishes every mutation through a file-backed SQLite ManagedStore and the
// production Publisher. Closing and rebuilding the whole stack proves the API
// did not merely mutate an in-memory fixture.
func TestHTTPAPIEndToEndPersistsFiveProtocolsAndR3Operations(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	key := persistentAPIKey()

	first := openPersistentAPIStack(t, directory, key)
	defer func() {
		if first != nil {
			_ = first.database.Close()
		}
	}()

	status := performJSONRequest(
		t, first.handler, http.MethodGet, "/api/v1/setup/status", nil, nil, "",
	)
	if status.Code != http.StatusOK || !bytes.Contains(status.Body.Bytes(), []byte(`"state":"required"`)) {
		t.Fatalf("initial setup status = %d; body=%s", status.Code, status.Body)
	}
	unauthenticated := performJSONRequest(
		t, first.handler, http.MethodPost, "/api/v1/nodes",
		persistentProtocolRequests()[0], nil, "",
	)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("node creation before setup status = %d, want %d", unauthenticated.Code, http.StatusUnauthorized)
	}

	setup := performSetupRequest(
		t,
		first.handler,
		"http://127.0.0.1:2095/api/v1/setup/complete",
		"127.0.0.1:40000",
		"http://127.0.0.1:2095",
	)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup status = %d; body=%s", setup.Code, setup.Body)
	}
	var setupBody loginResponse
	if err := json.NewDecoder(setup.Body).Decode(&setupBody); err != nil {
		t.Fatal(err)
	}
	if setupBody.Admin.Username != "admin" || setupBody.CSRFToken == "" {
		t.Fatalf("setup response = %#v", setupBody)
	}
	sessionCookie := responseCookie(t, setup.Result(), sessionCookieName)
	csrfToken := setupBody.CSRFToken

	blocked := performJSONRequest(
		t, first.handler, http.MethodPost, "/api/v1/nodes",
		persistentProtocolRequests()[0], sessionCookie, "",
	)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("node creation without CSRF status = %d, want %d", blocked.Code, http.StatusForbidden)
	}

	created := make(map[domain.ProtocolKind]listenerMutationResponse, 5)
	// Revision 1 is the durable empty R3 cutover baseline installed by the
	// production startup path in openPersistentAPIStack.
	latestRevision := int64(1)
	for _, input := range persistentProtocolRequests() {
		response := performJSONRequest(
			t, first.handler, http.MethodPost, "/api/v1/nodes",
			input, sessionCookie, csrfToken,
		)
		if response.Code != http.StatusCreated {
			t.Fatalf("create %s status = %d; body=%s", input.Protocol, response.Code, response.Body)
		}
		var result listenerMutationResponse
		decodeOperationResponse(t, response.Body.Bytes(), &result)
		latestRevision++
		if result.Node.Protocol != input.Protocol || len(result.Node.Users) != 1 ||
			result.Revision.Status != domain.RevisionActive ||
			result.Revision.RevisionNumber != latestRevision {
			t.Fatalf(
				"create %s result protocol=%q users=%d revision=%d status=%q",
				input.Protocol, result.Node.Protocol, len(result.Node.Users),
				result.Revision.RevisionNumber, result.Revision.Status,
			)
		}
		created[input.Protocol] = result
	}

	cloneResponse := performJSONRequest(
		t, first.handler, http.MethodPost,
		"/api/v1/nodes/"+created[domain.ProtocolVLESS].Node.ID+"/clone",
		cloneNodeRequest{Name: "vless-clone", Port: "32201", IncludeUsers: true},
		sessionCookie, csrfToken,
	)
	if cloneResponse.Code != http.StatusCreated {
		t.Fatalf("clone status = %d; body=%s", cloneResponse.Code, cloneResponse.Body)
	}
	var clone listenerMutationResponse
	decodeOperationResponse(t, cloneResponse.Body.Bytes(), &clone)
	latestRevision++
	if clone.Node.Enabled || clone.Node.Name != "vless-clone" || clone.Node.Port != "32201" ||
		len(clone.Node.Users) != 1 || clone.Node.Users[0].ID == created[domain.ProtocolVLESS].Node.Users[0].ID ||
		clone.Revision.RevisionNumber != latestRevision {
		t.Fatalf(
			"clone result name=%q port=%q enabled=%v users=%d revision=%d",
			clone.Node.Name, clone.Node.Port, clone.Node.Enabled,
			len(clone.Node.Users), clone.Revision.RevisionNumber,
		)
	}

	nodesBatchResponse := performJSONRequest(
		t, first.handler, http.MethodPost, "/api/v1/nodes/batch-enabled",
		nodesEnabledRequest{NodeIDs: []string{
			created[domain.ProtocolVLESS].Node.ID,
			created[domain.ProtocolVMess].Node.ID,
		}, Enabled: true},
		sessionCookie, csrfToken,
	)
	if nodesBatchResponse.Code != http.StatusOK {
		t.Fatalf("batch-enable nodes status = %d; body=%s", nodesBatchResponse.Code, nodesBatchResponse.Body)
	}
	var nodesBatch nodesMutationResponse
	decodeOperationResponse(t, nodesBatchResponse.Body.Bytes(), &nodesBatch)
	latestRevision++
	if len(nodesBatch.Nodes) != 2 {
		t.Fatalf("batch-enable nodes count=%d, want 2", len(nodesBatch.Nodes))
	}
	if !nodesBatch.Nodes[0].Enabled || !nodesBatch.Nodes[1].Enabled ||
		nodesBatch.Revision.RevisionNumber != latestRevision {
		t.Fatalf(
			"batch-enable nodes count=%d enabled=%v/%v revision=%d",
			len(nodesBatch.Nodes), nodesBatch.Nodes[0].Enabled,
			nodesBatch.Nodes[1].Enabled, nodesBatch.Revision.RevisionNumber,
		)
	}

	usersCreateResponse := performJSONRequest(
		t, first.handler, http.MethodPost,
		"/api/v1/nodes/"+clone.Node.ID+"/users/batch",
		usersCreateRequest{Users: []userRequest{
			{Name: "clone-batch-a", Enabled: true},
			{Name: "clone-batch-b", Enabled: true},
		}},
		sessionCookie, csrfToken,
	)
	if usersCreateResponse.Code != http.StatusCreated {
		t.Fatalf("batch-create users status = %d; body=%s", usersCreateResponse.Code, usersCreateResponse.Body)
	}
	var usersCreated usersMutationResponse
	decodeOperationResponse(t, usersCreateResponse.Body.Bytes(), &usersCreated)
	latestRevision++
	if len(usersCreated.Users) != 2 {
		t.Fatalf("batch-create users count=%d, want 2", len(usersCreated.Users))
	}
	if usersCreated.Users[0].VLESS == nil ||
		usersCreated.Users[1].VLESS == nil || usersCreated.Revision.RevisionNumber != latestRevision {
		t.Fatalf(
			"batch-create users count=%d vless=%v/%v revision=%d",
			len(usersCreated.Users), usersCreated.Users[0].VLESS != nil,
			usersCreated.Users[1].VLESS != nil, usersCreated.Revision.RevisionNumber,
		)
	}

	usersToggleResponse := performJSONRequest(
		t, first.handler, http.MethodPost,
		"/api/v1/nodes/"+clone.Node.ID+"/users/batch-enabled",
		usersEnabledRequest{UserIDs: []string{
			usersCreated.Users[0].ID,
			usersCreated.Users[1].ID,
		}, Enabled: false},
		sessionCookie, csrfToken,
	)
	if usersToggleResponse.Code != http.StatusOK {
		t.Fatalf("batch-disable users status = %d; body=%s", usersToggleResponse.Code, usersToggleResponse.Body)
	}
	var usersToggled usersMutationResponse
	decodeOperationResponse(t, usersToggleResponse.Body.Bytes(), &usersToggled)
	latestRevision++
	if len(usersToggled.Users) != 2 {
		t.Fatalf("batch-disable users count=%d, want 2", len(usersToggled.Users))
	}
	if usersToggled.Users[0].Enabled || usersToggled.Users[1].Enabled ||
		usersToggled.Revision.RevisionNumber != latestRevision {
		t.Fatalf(
			"batch-disable users count=%d enabled=%v/%v revision=%d",
			len(usersToggled.Users), usersToggled.Users[0].Enabled,
			usersToggled.Users[1].Enabled, usersToggled.Revision.RevisionNumber,
		)
	}

	beforeClose := readPersistentAPIState(t, first, sessionCookie, latestRevision)
	assertPersistentProtocolState(t, beforeClose, created, clone.Node.ID)
	if err := first.database.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}
	first = nil

	reopened := openPersistentAPIStack(t, directory, key)
	defer func() { _ = reopened.database.Close() }()
	if err := reopened.configurationPublisher.ReconcileStartupBeforeRuntime(ctx); err != nil {
		t.Fatalf("reconcile reopened publication: %v", err)
	}

	reopenedSetup := performJSONRequest(
		t, reopened.handler, http.MethodGet, "/api/v1/setup/status", nil, nil, "",
	)
	if reopenedSetup.Code != http.StatusOK ||
		!bytes.Contains(reopenedSetup.Body.Bytes(), []byte(`"state":"complete"`)) {
		t.Fatalf("reopened setup status = %d; body=%s", reopenedSetup.Code, reopenedSetup.Body)
	}
	me := performJSONRequest(
		t, reopened.handler, http.MethodGet, "/api/v1/auth/me", nil, sessionCookie, "",
	)
	if me.Code != http.StatusOK {
		t.Fatalf("persisted setup session status = %d; body=%s", me.Code, me.Body)
	}

	afterReopen := readPersistentAPIState(t, reopened, sessionCookie, latestRevision)
	assertPersistentProtocolState(t, afterReopen, created, clone.Node.ID)
	if got, want := afterReopen.SHA256, beforeClose.SHA256; got != want {
		t.Fatalf("reopened state SHA-256 = %q, want %q", got, want)
	}

	// Reuse the setup-created session and CSRF token after rebuilding the stack.
	// This proves their hashes were persisted in SQLite rather than retained by
	// the first handler instance.
	postReopenMutation := performJSONRequest(
		t, reopened.handler, http.MethodPost, "/api/v1/nodes/batch-enabled",
		nodesEnabledRequest{NodeIDs: []string{
			created[domain.ProtocolVLESS].Node.ID,
			created[domain.ProtocolVMess].Node.ID,
		}, Enabled: false},
		sessionCookie, csrfToken,
	)
	if postReopenMutation.Code != http.StatusOK {
		t.Fatalf("post-reopen CSRF mutation status = %d; body=%s", postReopenMutation.Code, postReopenMutation.Body)
	}
	var postReopen nodesMutationResponse
	decodeOperationResponse(t, postReopenMutation.Body.Bytes(), &postReopen)
	latestRevision++
	if postReopen.Revision.RevisionNumber != latestRevision ||
		postReopen.Revision.Status != domain.RevisionActive {
		t.Fatalf(
			"post-reopen mutation revision=%d status=%q",
			postReopen.Revision.RevisionNumber, postReopen.Revision.Status,
		)
	}
	finalState := readPersistentAPIState(t, reopened, sessionCookie, latestRevision)
	for _, kind := range []domain.ProtocolKind{domain.ProtocolVLESS, domain.ProtocolVMess} {
		node := findPersistentNode(t, finalState.State, created[kind].Node.ID)
		if node.Enabled {
			t.Fatalf("post-reopen %s node is still enabled", kind)
		}
	}
}

type persistentAPIStack struct {
	handler                http.Handler
	database               *store.Store
	managed                *store.ManagedStore
	configurationPublisher *publisher.Publisher
}

func openPersistentAPIStack(
	t *testing.T,
	directory string,
	key muicrypto.MasterKey,
) *persistentAPIStack {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(directory, "m-ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	fail := func(err error) {
		_ = database.Close()
		t.Fatal(err)
	}
	sealer, err := muicrypto.NewSealer(key)
	if err != nil {
		fail(err)
	}
	if err := auth.EnsureBootstrap(
		ctx,
		database,
		sealer,
		bytes.NewReader(bytes.Repeat([]byte{0x5a}, 64)),
		time.Now,
	); err != nil {
		fail(err)
	}
	managed, err := store.NewManagedStore(database, sealer)
	if err != nil {
		fail(err)
	}
	configPath := filepath.Join(directory, "mihomo", "config.yaml")
	binaryPath := os.Getenv("M_UI_TEST_MIHOMO_BINARY")
	if binaryPath == "" {
		binaryPath = filepath.Join(directory, "mihomo-test-double")
	}
	if err := managed.EnsureInitialSettings(ctx, store.InitialSettings{
		PanelTitle:         "m-ui persistent API test",
		UILanguage:         "en-US",
		PublicHost:         "vpn.example.com",
		PanelListenAddress: "127.0.0.1",
		PanelListenPort:    2095,
		TrustedProxyCIDRs:  []string{},
		MihomoBinaryPath:   binaryPath,
		MihomoConfigDir:    filepath.Dir(configPath),
		MihomoConfigPath:   configPath,
		ControllerAddress:  "127.0.0.1:39091",
		MihomoServiceName:  "mihomo.service",
		HistoryLimit:       50,
	}, time.Now().UTC()); err != nil {
		fail(err)
	}

	var cli mihomo.CoreCLI = &managementCLI{}
	if realBinary := os.Getenv("M_UI_TEST_MIHOMO_BINARY"); realBinary != "" {
		cli, err = mihomo.NewCLI(realBinary)
		if err != nil {
			fail(err)
		}
	}
	controller := &managementController{}
	process := &managementProcess{active: true}
	configurationPublisher, err := publisher.New(
		managed,
		publisher.YAMLCompiler{},
		cli,
		controller,
		process,
		publisher.Options{
			ConfigPath:        configPath,
			RevisionDirectory: filepath.Join(directory, "revisions"),
			HistoryLimit:      50,
			HealthTimeout:     100 * time.Millisecond,
			HealthInterval:    time.Millisecond,
			Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	)
	if err != nil {
		fail(err)
	}
	if err := configurationPublisher.ReconcileStartupBeforeRuntime(ctx); err != nil {
		fail(err)
	}
	if err := configurationPublisher.ReconcileStartup(ctx); err != nil {
		fail(err)
	}
	runtimeMonitor, err := service.NewRuntimeMonitor(
		controller,
		process,
		service.RuntimeMonitorOptions{},
	)
	if err != nil {
		fail(err)
	}
	runtimeMonitor.CollectOnce(ctx)
	manager, err := service.NewManager(service.ManagerOptions{
		Store:      managed,
		Publisher:  configurationPublisher,
		CLI:        cli,
		Controller: controller,
		Process:    process,
		Runtime:    runtimeMonitor,
		ReadyGuard: func(context.Context) (func() error, error) {
			return func() error { return nil }, nil
		},
	})
	if err != nil {
		fail(err)
	}
	authService, err := auth.NewService(database, auth.Options{
		SessionTTL: 12 * time.Hour,
		PasswordParams: auth.PasswordParams{
			Memory:      8 * 1024,
			Iterations:  1,
			Parallelism: 1,
			SaltLength:  16,
			KeyLength:   32,
		},
	})
	if err != nil {
		fail(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &persistentAPIStack{
		handler: New(Options{
			Logger:     logger,
			Auth:       authService,
			Management: manager,
			RequestRestart: func(release func()) {
				release()
			},
		}),
		database:               database,
		managed:                managed,
		configurationPublisher: configurationPublisher,
	}
}

func persistentAPIKey() muicrypto.MasterKey {
	var key muicrypto.MasterKey
	for index := range key {
		key[index] = byte(index + 1)
	}
	return key
}

func persistentProtocolRequests() []listenerRequest {
	return []listenerRequest{
		{
			Name: "persist-vless", ListenAddress: "127.0.0.1", Port: "32101",
			Protocol: domain.ProtocolVLESS,
			VLESS: &domain.VLESSSpec{
				Decryption: "none",
				Handler:    domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
				Security:   domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
			},
			Users: []userRequest{{Name: "vless-user", Enabled: true}},
		},
		{
			Name: "persist-hysteria2", ListenAddress: "127.0.0.1", Port: "32102",
			Protocol: domain.ProtocolHysteria2,
			Hysteria2: &domain.Hysteria2Spec{
				Certificate: "/test/cert.pem", PrivateKey: "/test/key.pem",
				ALPN: []string{"h3"},
			},
			Users: []userRequest{{Name: "hysteria2-user", Enabled: true}},
		},
		{
			Name: "persist-vmess", ListenAddress: "127.0.0.1", Port: "32103",
			Protocol: domain.ProtocolVMess,
			VMess: &domain.VMessSpec{
				Handler: domain.VLESSHandlerSpec{
					Type: domain.VMessHandlerMKCP,
					MKCP: &domain.MKCPConfig{
						MTU: 1350, TTI: 50, UplinkCapacity: 5, DownlinkCapacity: 20,
						WriteBuffer: 2097152, ReadBuffer: 2097152,
						Seed: "persistent-api-mkcp-seed", Header: "wireguard",
					},
				},
				Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
			},
			Users: []userRequest{{Name: "vmess-user", Enabled: true}},
		},
		{
			Name: "persist-trojan", ListenAddress: "127.0.0.1", Port: "32104",
			Protocol: domain.ProtocolTrojan,
			Trojan: &domain.TrojanSpec{
				Handler:  domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
				Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
			},
			Users: []userRequest{{Name: "trojan-user", Enabled: true}},
		},
		{
			Name: "persist-shadowsocks", ListenAddress: "127.0.0.1", Port: "32105",
			Protocol: domain.ProtocolShadowsocks,
			Shadowsocks: &domain.ShadowsocksSpec{
				Cipher: "2022-blake3-aes-256-gcm", UDP: true,
				Security:   domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
				SimpleObfs: domain.SimpleObfsSpec{Enabled: true, Mode: "http"},
			},
			Users: []userRequest{{Name: "shadowsocks-user", Enabled: true}},
		},
	}
}

type persistentAPIState struct {
	State  domain.DesiredState
	SHA256 string
}

func readPersistentAPIState(
	t *testing.T,
	stack *persistentAPIStack,
	sessionCookie *http.Cookie,
	wantRevision int64,
) persistentAPIState {
	t.Helper()
	nodesResponse := performJSONRequest(
		t, stack.handler, http.MethodGet, "/api/v1/nodes", nil, sessionCookie, "",
	)
	if nodesResponse.Code != http.StatusOK {
		t.Fatalf("list persisted nodes status = %d; body=%s", nodesResponse.Code, nodesResponse.Body)
	}
	var nodes listenersResponse
	decodeOperationResponse(t, nodesResponse.Body.Bytes(), &nodes)
	if len(nodes.Nodes) != 6 {
		t.Fatalf("persisted API nodes = %d, want 6", len(nodes.Nodes))
	}

	revisionsRecorder := performJSONRequest(
		t, stack.handler, http.MethodGet, "/api/v1/config/revisions?limit=100",
		nil, sessionCookie, "",
	)
	if revisionsRecorder.Code != http.StatusOK {
		t.Fatalf("list persisted revisions status = %d; body=%s", revisionsRecorder.Code, revisionsRecorder.Body)
	}
	var revisions revisionsResponse
	decodeOperationResponse(t, revisionsRecorder.Body.Bytes(), &revisions)
	if len(revisions.Revisions) != int(wantRevision) {
		t.Fatalf("persisted revisions = %d, want %d", len(revisions.Revisions), wantRevision)
	}
	activeCount := 0
	for _, revision := range revisions.Revisions {
		if revision.Status == domain.RevisionActive {
			activeCount++
			if revision.RevisionNumber != wantRevision {
				t.Fatalf("active revision number = %d, want %d", revision.RevisionNumber, wantRevision)
			}
		}
	}
	if activeCount != 1 {
		t.Fatalf("active revision count = %d, want 1", activeCount)
	}

	state, err := stack.managed.ReadDesiredState(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	active, err := stack.managed.LatestActiveRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if active.RevisionNumber != wantRevision {
		t.Fatalf("durable active revision = %d, want %d", active.RevisionNumber, wantRevision)
	}
	return persistentAPIState{State: state, SHA256: active.SHA256}
}

func assertPersistentProtocolState(
	t *testing.T,
	got persistentAPIState,
	created map[domain.ProtocolKind]listenerMutationResponse,
	cloneID string,
) {
	t.Helper()
	if len(got.State.Nodes) != 6 {
		t.Fatalf("durable nodes = %d, want 6", len(got.State.Nodes))
	}
	for _, kind := range []domain.ProtocolKind{
		domain.ProtocolVLESS,
		domain.ProtocolHysteria2,
		domain.ProtocolVMess,
		domain.ProtocolTrojan,
		domain.ProtocolShadowsocks,
	} {
		node := findPersistentNode(t, got.State, created[kind].Node.ID)
		if node.Protocol != kind || len(node.Users) != 1 {
			t.Fatalf("durable %s node protocol=%q users=%d", kind, node.Protocol, len(node.Users))
		}
		switch kind {
		case domain.ProtocolVLESS:
			if node.VLESS == nil || node.Users[0].VLESS == nil || node.Users[0].VLESS.UUID == "" {
				t.Fatal("durable VLESS specification or credential is missing")
			}
		case domain.ProtocolHysteria2:
			if node.Hysteria2 == nil || node.Hysteria2.PrivateKey != "/test/key.pem" ||
				node.Users[0].Hysteria2 == nil || node.Users[0].Hysteria2.Password == "" {
				t.Fatal("durable Hysteria2 specification or credential is missing")
			}
		case domain.ProtocolVMess:
			if node.VMess == nil || node.VMess.Handler.MKCP == nil ||
				node.VMess.Handler.MKCP.Seed != "persistent-api-mkcp-seed" ||
				node.Users[0].VMess == nil || node.Users[0].VMess.UUID == "" {
				t.Fatal("durable VMess mKCP specification or credential is missing")
			}
		case domain.ProtocolTrojan:
			if node.Trojan == nil || node.Users[0].Trojan == nil || node.Users[0].Trojan.Password == "" {
				t.Fatal("durable Trojan specification or credential is missing")
			}
		case domain.ProtocolShadowsocks:
			if node.Shadowsocks == nil || !node.Shadowsocks.SimpleObfs.Enabled ||
				node.Users[0].Shadowsocks == nil || node.Users[0].Shadowsocks.Password == "" {
				t.Fatal("durable Shadowsocks simple-obfs specification or credential is missing")
			}
		}
	}
	clone := findPersistentNode(t, got.State, cloneID)
	if clone.Enabled || clone.Protocol != domain.ProtocolVLESS || len(clone.Users) != 3 {
		t.Fatalf(
			"durable clone protocol=%q enabled=%v users=%d",
			clone.Protocol, clone.Enabled, len(clone.Users),
		)
	}
	disabledBatchUsers := 0
	for _, user := range clone.Users {
		if user.Name == "clone-batch-a" || user.Name == "clone-batch-b" {
			if user.Enabled {
				t.Fatalf("durable batch user %q is enabled", user.Name)
			}
			disabledBatchUsers++
		}
	}
	if disabledBatchUsers != 2 {
		t.Fatalf("durable disabled batch users = %d, want 2", disabledBatchUsers)
	}
}

func findPersistentNode(
	t *testing.T,
	state domain.DesiredState,
	nodeID string,
) domain.Node {
	t.Helper()
	for _, node := range state.Nodes {
		if node.ID == nodeID {
			return node
		}
	}
	t.Fatalf("durable node %q not found", nodeID)
	return domain.Node{}
}
