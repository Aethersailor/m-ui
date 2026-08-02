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

func TestManagementCRUDUsesAuthenticationCSRFAndPublisher(t *testing.T) {
	t.Parallel()
	environment := newManagementTestEnvironment(t)

	unauthenticated := performJSONRequest(
		t,
		environment.handler,
		http.MethodGet,
		"/api/v1/listeners",
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

	sessionCookie, csrfToken := managementLogin(t, environment.handler)
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
		Name:          "edge",
		ListenAddress: "0.0.0.0",
		ListenPort:    443,
		ServerName:    "www.example.com",
		RealityDest:   "www.example.com:443",
		UDPEnabled:    true,
	}
	blocked := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/listeners",
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
		"/api/v1/listeners",
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
		"/api/v1/listeners",
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
	listenerID := listenerBody.Listener.ID
	if listenerID == "" || listenerBody.Listener.Enabled {
		t.Fatalf("created listener = %#v", listenerBody.Listener)
	}

	createdUser := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/listeners/"+listenerID+"/users",
		userRequest{Name: "alice"},
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
	if userBody.User.UUID == "" || !userBody.User.Enabled {
		t.Fatalf("created user = %#v", userBody.User)
	}

	enabled := performJSONRequest(
		t,
		environment.handler,
		http.MethodPost,
		"/api/v1/listeners/"+listenerID+"/enable",
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
		"/api/v1/listeners/"+listenerID+"/users/"+userBody.User.ID+"/share",
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
		userBody.User.UUID,
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
		"/api/v1/listeners/"+listenerID+"/users/"+userBody.User.ID+"/disable",
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
	storedListener, err := environment.manager.Listener(
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
		"/api/v1/listeners/"+listenerID+"/disable",
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
		"/api/v1/listeners/"+listenerID+"/users/"+userBody.User.ID+"/disable",
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
		!strings.Contains(audit.Body.String(), "listener.create") {
		t.Fatalf("audit response = %d %q", audit.Code, audit.Body)
	}
}

type managementTestEnvironment struct {
	handler http.Handler
	manager *service.Manager
	cli     *managementCLI
}

func newManagementTestEnvironment(t *testing.T) managementTestEnvironment {
	t.Helper()
	ctx := context.Background()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "m-ui.db"))
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
	controller := managementController{}
	process := managementProcess{active: true}
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
	if _, _, err := authService.ResetPassword(
		ctx,
		"admin",
		"initial-test-password",
	); err != nil {
		t.Fatal(err)
	}
	return managementTestEnvironment{
		handler: New(Options{
			Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
			Auth:       authService,
			Management: manager,
		}),
		manager: manager,
		cli:     cli,
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

type managementController struct{}

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
func (managementController) Reload(context.Context, string) error { return nil }
func (managementController) Restart(context.Context, string) error {
	return nil
}

type managementProcess struct {
	active bool
}

func (process managementProcess) IsActive(context.Context) (bool, error) {
	return process.active, nil
}
func (managementProcess) Start(context.Context) error   { return nil }
func (managementProcess) Stop(context.Context) error    { return nil }
func (managementProcess) Restart(context.Context) error { return nil }
func (managementProcess) Reload(context.Context) error  { return nil }
func (managementProcess) RecentLogs(
	context.Context,
	int,
) ([]mihomo.LogEntry, error) {
	return []mihomo.LogEntry{{
		Timestamp: time.Date(2026, 7, 28, 4, 0, 0, 0, time.UTC),
		Message:   "synthetic Mihomo log",
	}}, nil
}
