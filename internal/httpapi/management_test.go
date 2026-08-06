package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aethersailor/m-ui/internal/auth"
	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/protocol"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/service"
	"github.com/Aethersailor/m-ui/internal/store"
)

func TestCapabilitiesExposeStructuredSchemaWithoutProtocolSecrets(t *testing.T) {
	t.Parallel()
	environment := newManagementTestEnvironment(t)
	sessionCookie, _ := managementLogin(t, environment.handler)

	response := performJSONRequest(
		t, environment.handler, http.MethodGet, "/api/v1/capabilities", nil, sessionCookie, "",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d; body=%s", response.Code, response.Body)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("capabilities Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	raw := append([]byte(nil), response.Body.Bytes()...)
	var manifest protocol.CapabilityManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != protocol.CapabilitySchemaVersion || len(manifest.Protocols) != 5 {
		t.Fatalf("capability manifest = %#v", manifest)
	}
	for _, forbidden := range []string{"controller-test-secret", environment.cli.keypair.PrivateKey} {
		if forbidden != "" && strings.Contains(string(raw), forbidden) {
			t.Fatalf("capabilities exposed %q", forbidden)
		}
	}
}

func TestOnboardingCreatesReadyListenerUserAndShareOnce(t *testing.T) {
	t.Parallel()
	environment := newManagementTestEnvironment(t)
	sessionCookie, csrfToken := managementLogin(t, environment.handler)
	payload := onboardingRequest{PublicHost: "node.example.com", Node: listenerRequest{
		Name: "default", Enabled: true, ListenAddress: "0.0.0.0", Port: "443",
		Protocol: domain.ProtocolVLESS,
		VLESS: &domain.VLESSSpec{Decryption: "none", Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw}, Security: domain.VLESSSecuritySpec{
			Type: domain.VLESSSecurityReality, Reality: &domain.RealityConfig{
				Destination: "www.example.com:443", ServerNames: []string{"www.example.com"},
			},
		}},
		Users: []userRequest{{Name: "first-user", Enabled: true}},
	}}

	withoutCSRF := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/onboarding",
		payload,
		sessionCookie,
		"",
	)
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("onboarding without CSRF = %d", withoutCSRF.Code)
	}

	created := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/onboarding",
		payload,
		sessionCookie,
		csrfToken,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("onboarding status = %d; body=%s", created.Code, created.Body)
	}
	var body onboardingResponse
	if err := json.NewDecoder(created.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Node.Enabled || len(body.Node.Users) != 1 ||
		!body.User.Enabled || body.Share.URI == "" || body.Share.QRContent != body.Share.URI ||
		!strings.Contains(body.Share.URI, "node.example.com:443") {
		t.Fatalf("onboarding response = %#v", body)
	}

	repeated := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/onboarding",
		payload,
		sessionCookie,
		csrfToken,
	)
	if repeated.Code != http.StatusConflict {
		t.Fatalf("repeated onboarding = %d; body=%s", repeated.Code, repeated.Body)
	}
	listeners, err := environment.manager.Nodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listeners) != 1 || len(listeners[0].Users) != 1 {
		t.Fatalf("onboarding left partial or duplicate state: %#v", listeners)
	}
}

func TestManagementCRUDUsesAuthenticationCSRFAndPublisher(t *testing.T) {
	t.Parallel()
	environment := newManagementTestEnvironment(t)

	unauthenticated := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/nodes",
		nil,
		nil,
		"",
	)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.Code)
	}
	unauthenticatedCore := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/system/core",
		nil,
		nil,
		"",
	)
	if unauthenticatedCore.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated core status = %d", unauthenticatedCore.Code)
	}
	unauthenticatedRestart := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/system/restart",
		nil,
		nil,
		"synthetic-csrf",
	)
	if unauthenticatedRestart.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated application restart = %d", unauthenticatedRestart.Code)
	}

	sessionCookie, csrfToken := managementLogin(t, environment.handler)
	blockedApplicationRestart := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/system/restart",
		nil,
		sessionCookie,
		"",
	)
	if blockedApplicationRestart.Code != http.StatusForbidden {
		t.Fatalf("application restart without CSRF = %d", blockedApplicationRestart.Code)
	}
	settingsUpdate := performJSONRequest(
		t,
		environment.handler,
		http.MethodPut,
		"/api/v1/settings",
		settingsRequest{
			PanelTitle:   "m-ui",
			UILanguage:   "en-US",
			PublicHost:   "vpn.example.com",
			CookieSecure: true,
		},
		sessionCookie,
		csrfToken,
	)
	if settingsUpdate.Code != http.StatusOK ||
		!strings.Contains(settingsUpdate.Body.String(), `"cookie_secure":true`) ||
		!strings.Contains(settingsUpdate.Body.String(), `"requires_mui_restart":true`) {
		t.Fatalf("cookie security settings response = %d %q", settingsUpdate.Code, settingsUpdate.Body)
	}
	applicationRestart := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/system/restart",
		nil,
		sessionCookie,
		csrfToken,
	)
	if applicationRestart.Code != http.StatusAccepted ||
		!environment.restartRequested.Load() {
		t.Fatalf("application restart response = %d %q", applicationRestart.Code, applicationRestart.Body)
	}
	release, err := environment.manager.ReserveApplicationRestart()
	if err != nil {
		t.Fatal(err)
	}
	busyRestart := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/system/restart",
		nil,
		sessionCookie,
		csrfToken,
	)
	release()
	if busyRestart.Code != http.StatusConflict ||
		!strings.Contains(busyRestart.Body.String(), "CORE_OPERATION_IN_PROGRESS") {
		t.Fatalf("busy application restart response = %d %q", busyRestart.Code, busyRestart.Body)
	}
	endpointGet := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/settings/endpoints",
		nil,
		sessionCookie,
		"",
	)
	if endpointGet.Code != http.StatusOK {
		t.Fatalf("endpoint settings GET status = %d; body=%s", endpointGet.Code, endpointGet.Body)
	}
	if !strings.Contains(endpointGet.Body.String(), `"external_controller_cors_origins":[]`) ||
		strings.Contains(endpointGet.Body.String(), `"external_controller_cors_origins":null`) {
		t.Fatalf("empty endpoint CORS origins must use a stable JSON array: %s", endpointGet.Body)
	}
	var endpointState endpointSettingsStateResponse
	if err := json.NewDecoder(endpointGet.Body).Decode(&endpointState); err != nil {
		t.Fatal(err)
	}
	blockedEndpointUpdate := performJSONRequest(
		t,
		environment.handler,
		http.MethodPut,
		"/api/v1/settings/endpoints",
		endpointSettingsRequest{Generation: endpointState.Active.Generation},
		sessionCookie,
		"",
	)
	if blockedEndpointUpdate.Code != http.StatusForbidden {
		t.Fatalf("endpoint settings PUT without CSRF status = %d", blockedEndpointUpdate.Code)
	}
	endpointPayload := endpointSettingsRequest{
		PanelUIBind: endpointResponse{Host: "0.0.0.0", Port: 2095},
		MihomoExternalControllerBind: endpointResponse{
			Host: "::",
			Port: 9090,
		},
		MihomoControllerConnect: endpointResponse{
			Host: "::1",
			Port: 9090,
		},
		ExternalControllerCORSOrigins: []string{"https://dashboard.example.com"},
		Generation:                    endpointState.Active.Generation,
	}
	updatedEndpoints := performJSONRequest(
		t,
		environment.handler,
		http.MethodPut,
		"/api/v1/settings/endpoints",
		endpointPayload,
		sessionCookie,
		csrfToken,
	)
	if updatedEndpoints.Code != http.StatusOK {
		t.Fatalf("endpoint settings PUT status = %d; body=%s", updatedEndpoints.Code, updatedEndpoints.Body)
	}
	if !strings.Contains(updatedEndpoints.Body.String(), `"requires_mui_restart":true`) ||
		!strings.Contains(updatedEndpoints.Body.String(), `"requires_mihomo_restart":true`) {
		t.Fatalf("endpoint settings response did not record restart requirements: %s", updatedEndpoints.Body.String())
	}
	var updatedEndpointState endpointSettingsStateResponse
	if err := json.NewDecoder(strings.NewReader(updatedEndpoints.Body.String())).Decode(&updatedEndpointState); err != nil {
		t.Fatal(err)
	}
	blockedCoreUpdate := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/system/core/update",
		nil,
		sessionCookie,
		"",
	)
	if blockedCoreUpdate.Code != http.StatusForbidden {
		t.Fatalf("core update without CSRF status = %d", blockedCoreUpdate.Code)
	}
	listenerPayload := listenerRequest{
		Name: "edge", Enabled: false, ListenAddress: "0.0.0.0", Port: "443",
		Protocol: domain.ProtocolVLESS,
		VLESS: &domain.VLESSSpec{Decryption: "none", Handler: domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw}, Security: domain.VLESSSecuritySpec{
			Type: domain.VLESSSecurityReality, Reality: &domain.RealityConfig{
				Destination: "www.example.com:443", ServerNames: []string{"www.example.com"},
			},
		}},
	}
	blocked := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/nodes",
		listenerPayload,
		sessionCookie,
		"",
	)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("create without CSRF status = %d", blocked.Code)
	}
	blockedPublish := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/nodes",
		listenerPayload,
		sessionCookie,
		csrfToken,
	)
	if blockedPublish.Code != http.StatusConflict ||
		!strings.Contains(blockedPublish.Body.String(), `"MIHOMO_RESTART_REQUIRED"`) {
		t.Fatalf("publication during pending Mihomo restart = %d %q", blockedPublish.Code, blockedPublish.Body)
	}
	restoreEndpoints := performJSONRequest(
		t,
		environment.handler,
		http.MethodPut,
		"/api/v1/settings/endpoints",
		endpointSettingsRequest{
			PanelUIBind:                   endpointState.Active.PanelUIBind,
			MihomoExternalControllerBind:  endpointState.Active.MihomoExternalControllerBind,
			MihomoControllerConnect:       endpointState.Active.MihomoControllerConnect,
			ExternalControllerCORSOrigins: endpointState.Active.ExternalControllerCORSOrigins,
			Generation:                    updatedEndpointState.Active.Generation,
		},
		sessionCookie,
		csrfToken,
	)
	if restoreEndpoints.Code != http.StatusOK {
		t.Fatalf("restore endpoint settings status = %d; body=%s", restoreEndpoints.Code, restoreEndpoints.Body)
	}
	createdListener := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/nodes",
		listenerPayload,
		sessionCookie,
		csrfToken,
	)
	if createdListener.Code != http.StatusCreated {
		t.Fatalf(
			"create listener status = %d; body=%s",
			createdListener.Code,
			createdListener.Body,
		)
	}
	if strings.Contains(
		createdListener.Body.String(),
		environment.cli.keypair.PrivateKey,
	) {
		t.Fatal("listener response exposed the REALITY private key")
	}
	var listenerBody listenerMutationResponse
	if err := json.NewDecoder(createdListener.Body).Decode(&listenerBody); err != nil {
		t.Fatal(err)
	}
	listenerID := listenerBody.Node.ID
	if listenerID == "" || listenerBody.Node.Enabled {
		t.Fatalf("created node = %#v", listenerBody.Node)
	}
	updatedNode := performJSONRequest(
		t,
		environment.handler,
		http.MethodPut,
		"/api/v1/nodes/"+listenerID,
		listenerRequest{
			Name: "edge-updated", Enabled: false,
			ListenAddress: listenerBody.Node.ListenAddress,
			Port:          listenerBody.Node.Port, Protocol: listenerBody.Node.Protocol,
			VLESS: listenerBody.Node.VLESS, Generation: listenerBody.Node.Generation,
		},
		sessionCookie,
		csrfToken,
	)
	if updatedNode.Code != http.StatusOK {
		t.Fatalf("redacted node update status = %d; body=%s", updatedNode.Code, updatedNode.Body)
	}

	createdUser := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/nodes/"+listenerID+"/users",
		userRequest{Name: "alice", Enabled: true},
		sessionCookie,
		csrfToken,
	)
	if createdUser.Code != http.StatusCreated {
		t.Fatalf(
			"create user status = %d; body=%s",
			createdUser.Code,
			createdUser.Body,
		)
	}
	var userBody userMutationResponse
	if err := json.NewDecoder(createdUser.Body).Decode(&userBody); err != nil {
		t.Fatal(err)
	}
	if userBody.User.VLESS == nil || userBody.User.VLESS.UUID != "" ||
		!userBody.User.SecretsSet["vless.uuid"] || !userBody.User.Enabled {
		t.Fatalf("created user = %#v", userBody.User)
	}
	storedWithUser, err := environment.manager.Node(context.Background(), listenerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(storedWithUser.Users) != 1 || storedWithUser.Users[0].VLESS == nil ||
		storedWithUser.Users[0].VLESS.UUID == "" {
		t.Fatalf("stored user = %#v", storedWithUser.Users)
	}
	userUUID := storedWithUser.Users[0].VLESS.UUID

	enabled := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/nodes/"+listenerID+"/enable",
		nil,
		sessionCookie,
		csrfToken,
	)
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable listener status = %d; body=%s", enabled.Code, enabled.Body)
	}
	share := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/nodes/"+listenerID+"/users/"+userBody.User.ID+"/share",
		nil,
		sessionCookie,
		"",
	)
	if share.Code != http.StatusOK ||
		share.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(share.Body.String(), "vless://") {
		t.Fatalf("share response = %d %q", share.Code, share.Body)
	}
	preview := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/config/preview",
		nil,
		sessionCookie,
		"",
	)
	if preview.Code != http.StatusOK ||
		preview.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("preview response = %d %q", preview.Code, preview.Body)
	}
	for _, secret := range []string{
		environment.cli.keypair.PrivateKey,
		userUUID,
	} {
		if strings.Contains(preview.Body.String(), secret) {
			t.Fatalf("redacted preview contains %q", secret)
		}
	}
	if !strings.Contains(preview.Body.String(), "[redacted") {
		t.Fatalf("preview does not contain redaction markers: %q", preview.Body)
	}

	rejectedDisable := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/nodes/"+listenerID+"/users/"+userBody.User.ID+"/disable",
		nil,
		sessionCookie,
		csrfToken,
	)
	if rejectedDisable.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(
			rejectedDisable.Body.String(),
			"CONFIG_VALIDATION_FAILED",
		) {
		t.Fatalf(
			"last-user disable response = %d %q",
			rejectedDisable.Code,
			rejectedDisable.Body,
		)
	}
	storedListener, err := environment.manager.Node(
		context.Background(),
		listenerID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !storedListener.Enabled || !storedListener.Users[0].Enabled {
		t.Fatal("rejected mutation changed managed state")
	}

	disabledListener := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/nodes/"+listenerID+"/disable",
		nil,
		sessionCookie,
		csrfToken,
	)
	if disabledListener.Code != http.StatusOK {
		t.Fatalf(
			"disable listener status = %d; body=%s",
			disabledListener.Code,
			disabledListener.Body,
		)
	}
	disabledUser := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/nodes/"+listenerID+"/users/"+userBody.User.ID+"/disable",
		nil,
		sessionCookie,
		csrfToken,
	)
	if disabledUser.Code != http.StatusOK {
		t.Fatalf(
			"disable user status = %d; body=%s",
			disabledUser.Code,
			disabledUser.Body,
		)
	}

	revealBlocked := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/config/preview?reveal=true",
		nil,
		sessionCookie,
		"",
	)
	if revealBlocked.Code != http.StatusForbidden {
		t.Fatalf("unconfirmed reveal status = %d", revealBlocked.Code)
	}
	validated := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/config/validate",
		nil,
		sessionCookie,
		csrfToken,
	)
	if validated.Code != http.StatusOK ||
		!strings.Contains(validated.Body.String(), `"valid":true`) {
		t.Fatalf("validate response = %d %q", validated.Code, validated.Body)
	}
	runtimeStatus := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/runtime/status",
		nil,
		sessionCookie,
		"",
	)
	if runtimeStatus.Code != http.StatusOK ||
		!strings.Contains(runtimeStatus.Body.String(), `"active":true`) {
		t.Fatalf(
			"runtime status response = %d %q",
			runtimeStatus.Code,
			runtimeStatus.Body,
		)
	}
	restarted := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/runtime/restart",
		nil,
		sessionCookie,
		csrfToken,
	)
	if restarted.Code != http.StatusNoContent {
		t.Fatalf("runtime restart status = %d", restarted.Code)
	}
	updatedSettings := performJSONRequest(
		t,
		environment.handler,
		http.MethodPut,
		"/api/v1/settings",
		settingsRequest{
			PanelTitle: "m-ui test",
			UILanguage: "zh-CN",
			PublicHost: "new.example.com",
		},
		sessionCookie,
		csrfToken,
	)
	if updatedSettings.Code != http.StatusOK {
		t.Fatalf(
			"settings update response = %d %q",
			updatedSettings.Code,
			updatedSettings.Body,
		)
	}
	settings := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/settings",
		nil,
		sessionCookie,
		"",
	)
	if settings.Code != http.StatusOK ||
		!strings.Contains(settings.Body.String(), `"ui_language":"zh-CN"`) {
		t.Fatalf("settings response = %d %q", settings.Code, settings.Body)
	}

	revisions := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/config/revisions",
		nil,
		sessionCookie,
		"",
	)
	if revisions.Code != http.StatusOK ||
		!strings.Contains(revisions.Body.String(), `"status":"active"`) {
		t.Fatalf("revisions response = %d %q", revisions.Code, revisions.Body)
	}
	audit := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/audit-logs",
		nil,
		sessionCookie,
		"",
	)
	if audit.Code != http.StatusOK ||
		!strings.Contains(audit.Body.String(), "node.create") {
		t.Fatalf("audit response = %d %q", audit.Code, audit.Body)
	}
}

type managementTestEnvironment struct {
	handler          http.Handler
	manager          *service.Manager
	cli              *managementCLI
	database         *store.Store
	managed          *store.ManagedStore
	controller       *managementController
	process          *managementProcess
	databasePath     string
	configPath       string
	restartRequested *atomic.Bool
}

func newManagementTestEnvironment(t *testing.T) managementTestEnvironment {
	t.Helper()
	ctx := context.Background()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "m-ui.db")
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	sealer, err := muicrypto.NewSealer(muicrypto.MasterKey{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := store.NewManagedStore(database, sealer)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "mihomo", "config.yaml")
	if err := managed.EnsureInitialSettings(
		ctx,
		store.InitialSettings{
			PanelTitle:         "m-ui",
			UILanguage:         "en-US",
			PublicHost:         "vpn.example.com",
			PanelListenAddress: "127.0.0.1",
			PanelListenPort:    2095,
			TrustedProxyCIDRs:  []string{},
			MihomoBinaryPath:   filepath.Join(directory, "mihomo"),
			MihomoConfigDir:    filepath.Dir(configPath),
			MihomoConfigPath:   configPath,
			ControllerAddress:  "127.0.0.1:9090",
			MihomoServiceName:  "mihomo.service",
			HistoryLimit:       20,
		},
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	privateBytes := make([]byte, 32)
	privateBytes[0] = 1
	publicBytes := make([]byte, 32)
	publicBytes[0] = 2
	cli := &managementCLI{keypair: domain.Keypair{
		PrivateKey: base64.RawURLEncoding.EncodeToString(privateBytes),
		PublicKey:  base64.RawURLEncoding.EncodeToString(publicBytes),
	}}
	controller := &managementController{state: &managementControllerState{}}
	process := &managementProcess{
		active: true,
		state:  &managementProcessState{active: true},
	}
	configurationPublisher, err := publisher.New(
		managed,
		publisher.YAMLCompiler{},
		cli,
		controller,
		process,
		publisher.Options{
			ConfigPath:        configPath,
			RevisionDirectory: filepath.Join(directory, "revisions"),
			HistoryLimit:      20,
			HealthTimeout:     50 * time.Millisecond,
			HealthInterval:    time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := configurationPublisher.ReconcileStartupBeforeRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	if err := configurationPublisher.ReconcileStartup(ctx); err != nil {
		t.Fatal(err)
	}
	runtimeMonitor, err := service.NewRuntimeMonitor(
		controller,
		process,
		service.RuntimeMonitorOptions{},
	)
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
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
		t.Fatal(err)
	}
	seedAdministrator(t, database, authService)
	restartRequested := &atomic.Bool{}
	return managementTestEnvironment{
		handler: New(Options{
			Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
			Auth:       authService,
			Management: manager,
			RequestRestart: func(release func()) {
				restartRequested.Store(true)
				release()
			},
		}),
		manager:          manager,
		cli:              cli,
		database:         database,
		managed:          managed,
		controller:       controller,
		process:          process,
		databasePath:     databasePath,
		configPath:       configPath,
		restartRequested: restartRequested,
	}
}

func managementLogin(
	t *testing.T,
	handler http.Handler,
) (*http.Cookie, string) {
	t.Helper()
	login := performJSONRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]string{
			"username": "admin",
			"password": "initial-test-password",
		},
		nil,
		"",
	)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d; body=%s", login.Code, login.Body)
	}
	var body loginResponse
	if err := json.NewDecoder(login.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return responseCookie(t, login.Result(), sessionCookieName), body.CSRFToken
}

type managementCLI struct {
	keypair domain.Keypair
}

func (*managementCLI) Validate(context.Context, string) error { return nil }
func (*managementCLI) Version(context.Context) (string, error) {
	return "Mihomo Meta test", nil
}
func (cli *managementCLI) GenerateRealityKeypair(
	context.Context,
) (domain.Keypair, error) {
	return cli.keypair, nil
}

type managementController struct {
	state *managementControllerState
}

type managementControllerState struct {
	mutex        sync.Mutex
	reloadErrors []error
	reloads      int
}

func (managementController) Version(
	context.Context,
) (mihomo.Version, error) {
	return mihomo.Version{Meta: true, Version: "test"}, nil
}
func (managementController) Traffic(
	context.Context,
) (mihomo.TrafficSnapshot, error) {
	return mihomo.TrafficSnapshot{}, nil
}
func (managementController) Memory(
	context.Context,
) (mihomo.MemorySnapshot, error) {
	return mihomo.MemorySnapshot{}, nil
}
func (managementController) Connections(
	context.Context,
) (mihomo.ConnectionsSnapshot, error) {
	return mihomo.ConnectionsSnapshot{}, nil
}
func (controller managementController) Reload(context.Context, string) error {
	if controller.state == nil {
		return nil
	}
	controller.state.mutex.Lock()
	defer controller.state.mutex.Unlock()
	controller.state.reloads++
	if len(controller.state.reloadErrors) == 0 {
		return nil
	}
	err := controller.state.reloadErrors[0]
	controller.state.reloadErrors = controller.state.reloadErrors[1:]
	return err
}
func (managementController) Restart(context.Context, string) error {
	return nil
}

type managementProcess struct {
	active bool
	state  *managementProcessState
}

type managementProcessState struct {
	mutex         sync.Mutex
	active        bool
	restartErrors []error
}

func (process managementProcess) IsActive(context.Context) (bool, error) {
	if process.state == nil {
		return process.active, nil
	}
	process.state.mutex.Lock()
	defer process.state.mutex.Unlock()
	return process.state.active, nil
}
func (process managementProcess) Start(context.Context) error {
	if process.state == nil {
		return nil
	}
	process.state.mutex.Lock()
	defer process.state.mutex.Unlock()
	process.state.active = true
	return nil
}
func (process managementProcess) Stop(context.Context) error {
	if process.state == nil {
		return nil
	}
	process.state.mutex.Lock()
	defer process.state.mutex.Unlock()
	process.state.active = false
	return nil
}
func (process managementProcess) Restart(context.Context) error {
	if process.state == nil {
		return nil
	}
	process.state.mutex.Lock()
	defer process.state.mutex.Unlock()
	if len(process.state.restartErrors) != 0 {
		err := process.state.restartErrors[0]
		process.state.restartErrors = process.state.restartErrors[1:]
		return err
	}
	process.state.active = true
	return nil
}
func (managementProcess) Reload(context.Context) error { return nil }
func (managementProcess) RecentLogs(
	context.Context,
	int,
) ([]mihomo.LogEntry, error) {
	return []mihomo.LogEntry{{
		Timestamp: time.Date(2026, 7, 28, 4, 0, 0, 0, time.UTC),
		Message:   "synthetic Mihomo log",
	}}, nil
}
