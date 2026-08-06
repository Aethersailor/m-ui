package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	coremanagement "github.com/Aethersailor/m-ui/internal/core"
	"github.com/Aethersailor/m-ui/internal/domain"
	"github.com/Aethersailor/m-ui/internal/mihomo"
	"github.com/Aethersailor/m-ui/internal/operation"
	"github.com/Aethersailor/m-ui/internal/publisher"
	"github.com/Aethersailor/m-ui/internal/service"
	"github.com/Aethersailor/m-ui/internal/store"
)

const revealConfigConfirmation = "reveal-current-config"

type managementHandler struct {
	manager        *service.Manager
	cookieSecure   bool
	requestRestart func(func())
}

type listenerRequest struct {
	Name           string                  `json:"name"`
	Enabled        bool                    `json:"enabled"`
	ListenAddress  string                  `json:"listen"`
	Port           string                  `json:"port"`
	Protocol       domain.ProtocolKind     `json:"protocol"`
	VLESS          *domain.VLESSSpec       `json:"vless"`
	Hysteria2      *domain.Hysteria2Spec   `json:"hysteria2"`
	VMess          *domain.VMessSpec       `json:"vmess"`
	Trojan         *domain.TrojanSpec      `json:"trojan"`
	Shadowsocks    *domain.ShadowsocksSpec `json:"shadowsocks"`
	Users          []userRequest           `json:"users"`
	AccessProfiles []accessProfileRequest  `json:"access_profiles"`
	Generation     int64                   `json:"generation"`
}

type listenerResponse struct {
	ID             string                  `json:"id"`
	Name           string                  `json:"name"`
	Enabled        bool                    `json:"enabled"`
	ListenAddress  string                  `json:"listen"`
	Port           string                  `json:"port"`
	Protocol       domain.ProtocolKind     `json:"protocol"`
	SchemaVersion  int                     `json:"schema_version"`
	VLESS          *domain.VLESSSpec       `json:"vless,omitempty"`
	Hysteria2      *domain.Hysteria2Spec   `json:"hysteria2,omitempty"`
	VMess          *domain.VMessSpec       `json:"vmess,omitempty"`
	Trojan         *domain.TrojanSpec      `json:"trojan,omitempty"`
	Shadowsocks    *domain.ShadowsocksSpec `json:"shadowsocks,omitempty"`
	Users          []userResponse          `json:"users"`
	AccessProfiles []accessProfileResponse `json:"access_profiles"`
	SecretsSet     map[string]bool         `json:"secrets_set"`
	Generation     int64                   `json:"generation"`
	CreatedAt      time.Time               `json:"created_at"`
	UpdatedAt      time.Time               `json:"updated_at"`
}

type listenersResponse struct {
	Nodes []listenerResponse `json:"nodes"`
}

type listenerMutationResponse struct {
	Node     listenerResponse `json:"node"`
	Revision revisionResponse `json:"revision"`
}

type userRequest struct {
	Name        string                        `json:"name"`
	Enabled     bool                          `json:"enabled"`
	VLESS       *domain.VLESSCredential       `json:"vless"`
	Hysteria2   *domain.Hysteria2Credential   `json:"hysteria2"`
	VMess       *domain.VMessCredential       `json:"vmess"`
	Trojan      *domain.TrojanCredential      `json:"trojan"`
	Shadowsocks *domain.ShadowsocksCredential `json:"shadowsocks"`
	ExpiresAt   *time.Time                    `json:"expires_at"`
}

type userResponse struct {
	ID          string                        `json:"id"`
	NodeID      string                        `json:"node_id"`
	Name        string                        `json:"name"`
	Enabled     bool                          `json:"enabled"`
	VLESS       *domain.VLESSCredential       `json:"vless,omitempty"`
	Hysteria2   *domain.Hysteria2Credential   `json:"hysteria2,omitempty"`
	VMess       *domain.VMessCredential       `json:"vmess,omitempty"`
	Trojan      *domain.TrojanCredential      `json:"trojan,omitempty"`
	Shadowsocks *domain.ShadowsocksCredential `json:"shadowsocks,omitempty"`
	ExpiresAt   *time.Time                    `json:"expires_at"`
	CreatedAt   time.Time                     `json:"created_at"`
	UpdatedAt   time.Time                     `json:"updated_at"`
}

type accessProfileRequest struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Default        bool   `json:"default"`
	PublicHost     string `json:"public_host"`
	PublicPort     uint16 `json:"public_port"`
	ServerName     string `json:"server_name"`
	Fingerprint    string `json:"fingerprint"`
	PacketEncoding string `json:"packet_encoding"`
	AllowInsecure  bool   `json:"allow_insecure"`
}

type accessProfileResponse struct {
	ID             string    `json:"id"`
	NodeID         string    `json:"node_id"`
	Name           string    `json:"name"`
	Default        bool      `json:"default"`
	PublicHost     string    `json:"public_host"`
	PublicPort     uint16    `json:"public_port"`
	ServerName     string    `json:"server_name"`
	Fingerprint    string    `json:"fingerprint"`
	PacketEncoding string    `json:"packet_encoding"`
	AllowInsecure  bool      `json:"allow_insecure"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type usersResponse struct {
	Users []userResponse `json:"users"`
}

type userMutationResponse struct {
	User     userResponse     `json:"user"`
	Revision revisionResponse `json:"revision"`
}

type onboardingRequest struct {
	PublicHost string          `json:"public_host"`
	Node       listenerRequest `json:"node"`
}

type onboardingResponse struct {
	Node     listenerResponse `json:"node"`
	User     userResponse     `json:"user"`
	Revision revisionResponse `json:"revision"`
	Share    shareResponse    `json:"share"`
}

type generatedKeypairResponse struct {
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	ShortID    string `json:"short_id"`
}

type generatedUUIDResponse struct {
	UUID string `json:"uuid"`
}

type shareResponse struct {
	URI        string `json:"uri"`
	QRContent  string `json:"qr_content"`
	ClientYAML string `json:"client_yaml"`
}

type configPreviewResponse struct {
	YAML     string `json:"yaml"`
	SHA256   string `json:"sha256"`
	Revealed bool   `json:"revealed"`
}

type configValidationResponse struct {
	Valid  bool   `json:"valid"`
	SHA256 string `json:"sha256"`
}

type revisionResponse struct {
	ID             string                `json:"id"`
	RevisionNumber int64                 `json:"revision_number"`
	SHA256         string                `json:"sha256"`
	Status         domain.RevisionStatus `json:"status"`
	Reason         string                `json:"reason"`
	ActorAdminID   string                `json:"actor_admin_id"`
	ErrorMessage   string                `json:"error_message"`
	CreatedAt      time.Time             `json:"created_at"`
	ActivatedAt    *time.Time            `json:"activated_at"`
}

type revisionsResponse struct {
	Revisions []revisionResponse `json:"revisions"`
}

type rollbackResponse struct {
	Revision revisionResponse `json:"revision"`
}

type settingsRequest struct {
	PanelTitle   string `json:"panel_title"`
	UILanguage   string `json:"ui_language"`
	PublicHost   string `json:"public_host"`
	CookieSecure bool   `json:"cookie_secure"`
}

type settingsResponse struct {
	PanelTitle         string `json:"panel_title"`
	UILanguage         string `json:"ui_language"`
	PublicHost         string `json:"public_host"`
	CookieSecure       bool   `json:"cookie_secure"`
	RequiresMUIRestart bool   `json:"requires_mui_restart"`
}

type settingsMutationResponse struct {
	Settings settingsResponse `json:"settings"`
	Revision revisionResponse `json:"revision"`
}

type applicationRestartResponse struct {
	Restarting bool `json:"restarting"`
}

type endpointResponse struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

type endpointSettingsResponse struct {
	PanelUIBind                   endpointResponse `json:"panel_ui_bind"`
	MihomoExternalControllerBind  endpointResponse `json:"mihomo_external_controller_bind"`
	MihomoControllerConnect       endpointResponse `json:"mihomo_controller_connect"`
	ExternalControllerCORSOrigins []string         `json:"external_controller_cors_origins"`
	Generation                    int64            `json:"generation"`
	RequiresMUIRestart            bool             `json:"requires_mui_restart"`
	RequiresMihomoRestart         bool             `json:"requires_mihomo_restart"`
	UpdatedAt                     *time.Time       `json:"updated_at,omitempty"`
}

type endpointSettingsStateResponse struct {
	Active  endpointSettingsResponse  `json:"active"`
	Pending *endpointSettingsResponse `json:"pending"`
}

type endpointSettingsRequest struct {
	PanelUIBind                   endpointResponse `json:"panel_ui_bind"`
	MihomoExternalControllerBind  endpointResponse `json:"mihomo_external_controller_bind"`
	MihomoControllerConnect       endpointResponse `json:"mihomo_controller_connect"`
	ExternalControllerCORSOrigins []string         `json:"external_controller_cors_origins"`
	Generation                    int64            `json:"generation"`
}

type coreTestResponse struct {
	Version string `json:"version"`
}

type controllerTestResponse struct {
	Version mihomo.Version `json:"version"`
}

type coreSettingsRequest struct {
	Channel       string `json:"channel"`
	AutoUpdate    bool   `json:"auto_update"`
	CheckInterval string `json:"check_interval"`
}

type coreUpdateResponse struct {
	Changed  bool                    `json:"changed"`
	Manifest coremanagement.Manifest `json:"manifest"`
}

type runtimeStatusResponse struct {
	Active          bool                   `json:"active"`
	Degraded        bool                   `json:"degraded"`
	DegradedReason  string                 `json:"degraded_reason"`
	Version         mihomo.Version         `json:"version"`
	Traffic         mihomo.TrafficSnapshot `json:"traffic"`
	Memory          mihomo.MemorySnapshot  `json:"memory"`
	ConnectionCount int                    `json:"connection_count"`
	DownloadTotal   int64                  `json:"download_total"`
	UploadTotal     int64                  `json:"upload_total"`
	ObservedAt      time.Time              `json:"observed_at"`
}

type runtimeLogResponse struct {
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
}

type runtimeLogsResponse struct {
	Logs []runtimeLogResponse `json:"logs"`
}

type auditEntryResponse struct {
	ID           string    `json:"id"`
	ActorAdminID string    `json:"actor_admin_id"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	Result       string    `json:"result"`
	Summary      string    `json:"summary"`
	CreatedAt    time.Time `json:"created_at"`
}

type auditEntriesResponse struct {
	Entries []auditEntryResponse `json:"entries"`
}

func mountManagementRoutes(
	router chi.Router,
	auth authHandler,
	handler managementHandler,
) {
	router.Group(func(protected chi.Router) {
		protected.Use(auth.authenticate)

		protected.Get("/capabilities", handler.capabilities)
		protected.Get("/nodes", handler.listListeners)
		protected.Get("/nodes/{nodeID}", handler.getListener)
		protected.Get("/nodes/{nodeID}/users", handler.listUsers)
		protected.Get(
			"/nodes/{nodeID}/users/{userID}/share",
			handler.shareUser,
		)
		protected.Get("/runtime/status", handler.runtimeStatus)
		protected.Get("/runtime/logs", handler.runtimeLogs)
		protected.Get("/config/preview", handler.configPreview)
		protected.Get("/config/revisions", handler.listRevisions)
		protected.Get("/config/revisions/{revisionID}", handler.getRevision)
		protected.Get("/settings", handler.getSettings)
		protected.Get("/settings/endpoints", handler.getEndpointSettings)
		protected.Get("/system/core", handler.getCore)
		protected.Get("/audit-logs", handler.listAuditEntries)

		protected.Group(func(mutations chi.Router) {
			mutations.Use(auth.requireCSRF)

			mutations.Post("/onboarding", handler.completeOnboarding)
			mutations.Post("/nodes", handler.createListener)
			mutations.Post("/nodes/batch-enabled", handler.setNodesEnabled)
			mutations.Put("/nodes/{nodeID}", handler.updateListener)
			mutations.Delete("/nodes/{nodeID}", handler.deleteListener)
			mutations.Post("/nodes/{nodeID}/clone", handler.cloneNode)
			mutations.Post(
				"/nodes/{nodeID}/enable",
				handler.enableListener,
			)
			mutations.Post(
				"/nodes/{nodeID}/disable",
				handler.disableListener,
			)
			mutations.Post(
				"/nodes/generate-reality-keypair",
				handler.generateRealityKeypair,
			)

			mutations.Post(
				"/nodes/{nodeID}/users",
				handler.createUser,
			)
			mutations.Post(
				"/nodes/{nodeID}/users/batch",
				handler.createUsers,
			)
			mutations.Post(
				"/nodes/{nodeID}/users/batch-enabled",
				handler.setUsersEnabled,
			)
			mutations.Put(
				"/nodes/{nodeID}/users/{userID}",
				handler.updateUser,
			)
			mutations.Delete(
				"/nodes/{nodeID}/users/{userID}",
				handler.deleteUser,
			)
			mutations.Post(
				"/nodes/{nodeID}/users/{userID}/enable",
				handler.enableUser,
			)
			mutations.Post(
				"/nodes/{nodeID}/users/{userID}/disable",
				handler.disableUser,
			)
			mutations.Post(
				"/nodes/{nodeID}/users/generate-uuid",
				handler.generateUUID,
			)

			mutations.Post("/runtime/start", handler.startRuntime)
			mutations.Post("/runtime/stop", handler.stopRuntime)
			mutations.Post("/runtime/restart", handler.restartRuntime)
			mutations.Post("/runtime/reload", handler.reloadRuntime)

			mutations.Post("/config/validate", handler.validateConfig)
			mutations.Post(
				"/config/revisions/{revisionID}/rollback",
				handler.rollbackRevision,
			)

			mutations.Put("/settings", handler.updateSettings)
			mutations.Post("/system/restart", handler.restartApplication)
			mutations.Put("/settings/endpoints", handler.updateEndpointSettings)
			mutations.Post("/settings/test-core", handler.testCore)
			mutations.Post(
				"/settings/test-controller",
				handler.testController,
			)
			mutations.Put("/system/core/settings", handler.updateCoreSettings)
			mutations.Post("/system/core/check", handler.checkCore)
			mutations.Post("/system/core/update", handler.updateCore)
			mutations.Post("/system/core/rollback", handler.rollbackCore)
		})
	})
}

func (handler managementHandler) completeOnboarding(
	response http.ResponseWriter,
	request *http.Request,
) {
	var input onboardingRequest
	if decodeJSON(response, request, &input) != nil {
		writeInvalidRequest(response, request)
		return
	}
	result, err := handler.manager.CompleteOnboarding(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		service.OnboardingSpec{
			PublicHost: input.PublicHost,
			Node:       input.Node.listenerSpec(),
		},
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusCreated, onboardingResponse{
		Node:     newListenerResponse(result.Node),
		User:     newUserResponse(result.User),
		Revision: newRevisionResponse(result.Revision),
		Share: shareResponse{
			URI:        result.Share.URI,
			QRContent:  result.Share.QRContent,
			ClientYAML: result.Share.ClientYAML,
		},
	})
}

func (handler managementHandler) capabilities(
	response http.ResponseWriter,
	request *http.Request,
) {
	writePrivateJSON(response, http.StatusOK, handler.manager.Capabilities())
}

func (handler managementHandler) getCore(
	response http.ResponseWriter,
	request *http.Request,
) {
	status, err := handler.manager.CoreStatus(request.Context())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, status)
}

func (handler managementHandler) updateCoreSettings(
	response http.ResponseWriter,
	request *http.Request,
) {
	var input coreSettingsRequest
	if decodeJSON(response, request, &input) != nil {
		return
	}
	channel, err := coremanagement.ParseChannel(input.Channel)
	if err != nil {
		handler.writeError(response, request, service.ErrValidation)
		return
	}
	interval, err := time.ParseDuration(input.CheckInterval)
	if err != nil || coremanagement.ValidateCheckInterval(interval) != nil {
		handler.writeError(response, request, service.ErrValidation)
		return
	}
	current, err := handler.manager.CoreSettings(request.Context())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	current.Channel = channel
	current.AutoUpdate = input.AutoUpdate
	current.CheckInterval = interval
	if err := handler.manager.UpdateCoreSettings(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		current,
	); err != nil {
		handler.writeError(response, request, err)
		return
	}
	status, err := handler.manager.CoreStatus(request.Context())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, status)
}

func (handler managementHandler) checkCore(
	response http.ResponseWriter,
	request *http.Request,
) {
	identity, err := handler.manager.CheckCore(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, identity)
}

func (handler managementHandler) updateCore(
	response http.ResponseWriter,
	request *http.Request,
) {
	manifest, changed, err := handler.manager.UpdateCore(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, coreUpdateResponse{
		Changed:  changed,
		Manifest: manifest,
	})
}

func (handler managementHandler) rollbackCore(
	response http.ResponseWriter,
	request *http.Request,
) {
	manifest, err := handler.manager.RollbackCore(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, manifest)
}

func (handler managementHandler) listListeners(
	response http.ResponseWriter,
	request *http.Request,
) {
	listeners, err := handler.manager.Nodes(request.Context())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	result := make([]listenerResponse, 0, len(listeners))
	for _, listener := range listeners {
		result = append(result, newListenerResponse(listener))
	}
	writePrivateJSON(response, http.StatusOK, listenersResponse{Nodes: result})
}

func (handler managementHandler) getListener(
	response http.ResponseWriter,
	request *http.Request,
) {
	listener, err := handler.manager.Node(
		request.Context(),
		chi.URLParam(request, "nodeID"),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, newListenerResponse(listener))
}

func (handler managementHandler) createListener(
	response http.ResponseWriter,
	request *http.Request,
) {
	var input listenerRequest
	if decodeJSON(response, request, &input) != nil {
		writeInvalidRequest(response, request)
		return
	}
	listener, revision, err := handler.manager.CreateNode(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		input.listenerSpec(),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusCreated, listenerMutationResponse{
		Node:     newListenerResponse(listener),
		Revision: newRevisionResponse(revision),
	})
}

func (handler managementHandler) updateListener(
	response http.ResponseWriter,
	request *http.Request,
) {
	var input listenerRequest
	if decodeJSON(response, request, &input) != nil {
		writeInvalidRequest(response, request)
		return
	}
	listener, revision, err := handler.manager.UpdateNode(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "nodeID"),
		input.listenerSpec(),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, listenerMutationResponse{
		Node:     newListenerResponse(listener),
		Revision: newRevisionResponse(revision),
	})
}

func (handler managementHandler) deleteListener(
	response http.ResponseWriter,
	request *http.Request,
) {
	_, err := handler.manager.DeleteNode(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "nodeID"),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func (handler managementHandler) enableListener(
	response http.ResponseWriter,
	request *http.Request,
) {
	handler.setListenerEnabled(response, request, true)
}

func (handler managementHandler) disableListener(
	response http.ResponseWriter,
	request *http.Request,
) {
	handler.setListenerEnabled(response, request, false)
}

func (handler managementHandler) setListenerEnabled(
	response http.ResponseWriter,
	request *http.Request,
	enabled bool,
) {
	listener, revision, err := handler.manager.SetNodeEnabled(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "nodeID"),
		enabled,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, listenerMutationResponse{
		Node:     newListenerResponse(listener),
		Revision: newRevisionResponse(revision),
	})
}

func (handler managementHandler) generateRealityKeypair(
	response http.ResponseWriter,
	request *http.Request,
) {
	keypair, shortID, err := handler.manager.GenerateRealityKeypair(
		request.Context(),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, generatedKeypairResponse{
		PrivateKey: keypair.PrivateKey,
		PublicKey:  keypair.PublicKey,
		ShortID:    shortID,
	})
}

func (handler managementHandler) listUsers(
	response http.ResponseWriter,
	request *http.Request,
) {
	users, err := handler.manager.Users(
		request.Context(),
		chi.URLParam(request, "nodeID"),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	result := make([]userResponse, 0, len(users))
	for _, user := range users {
		result = append(result, newUserResponse(user))
	}
	writePrivateJSON(response, http.StatusOK, usersResponse{Users: result})
}

func (handler managementHandler) createUser(
	response http.ResponseWriter,
	request *http.Request,
) {
	var input userRequest
	if decodeJSON(response, request, &input) != nil {
		writeInvalidRequest(response, request)
		return
	}
	user, revision, err := handler.manager.CreateUser(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "nodeID"),
		input.userSpec(),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusCreated, userMutationResponse{
		User:     newUserResponse(user),
		Revision: newRevisionResponse(revision),
	})
}

func (handler managementHandler) updateUser(
	response http.ResponseWriter,
	request *http.Request,
) {
	var input userRequest
	if decodeJSON(response, request, &input) != nil {
		writeInvalidRequest(response, request)
		return
	}
	user, revision, err := handler.manager.UpdateUser(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "nodeID"),
		chi.URLParam(request, "userID"),
		input.userSpec(),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, userMutationResponse{
		User:     newUserResponse(user),
		Revision: newRevisionResponse(revision),
	})
}

func (handler managementHandler) deleteUser(
	response http.ResponseWriter,
	request *http.Request,
) {
	_, err := handler.manager.DeleteUser(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "nodeID"),
		chi.URLParam(request, "userID"),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func (handler managementHandler) enableUser(
	response http.ResponseWriter,
	request *http.Request,
) {
	handler.setUserEnabled(response, request, true)
}

func (handler managementHandler) disableUser(
	response http.ResponseWriter,
	request *http.Request,
) {
	handler.setUserEnabled(response, request, false)
}

func (handler managementHandler) setUserEnabled(
	response http.ResponseWriter,
	request *http.Request,
	enabled bool,
) {
	user, revision, err := handler.manager.SetUserEnabled(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "nodeID"),
		chi.URLParam(request, "userID"),
		enabled,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, userMutationResponse{
		User:     newUserResponse(user),
		Revision: newRevisionResponse(revision),
	})
}

func (handler managementHandler) generateUUID(
	response http.ResponseWriter,
	request *http.Request,
) {
	value, err := handler.manager.GenerateUUID()
	if err != nil {
		writeAPIError(
			response,
			request,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"UUID generation failed.",
		)
		return
	}
	writePrivateJSON(response, http.StatusOK, generatedUUIDResponse{UUID: value})
}

func (handler managementHandler) shareUser(
	response http.ResponseWriter,
	request *http.Request,
) {
	share, err := handler.manager.ShareWithProfile(
		request.Context(),
		chi.URLParam(request, "nodeID"),
		chi.URLParam(request, "userID"),
		request.URL.Query().Get("profile_id"),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, shareResponse{
		URI:        share.URI,
		QRContent:  share.QRContent,
		ClientYAML: share.ClientYAML,
	})
}

func (handler managementHandler) runtimeStatus(
	response http.ResponseWriter,
	request *http.Request,
) {
	status, err := handler.manager.RuntimeStatus(request.Context())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, runtimeStatusResponse{
		Active:          status.Active,
		Degraded:        status.Degraded,
		DegradedReason:  status.DegradedReason,
		Version:         status.Version,
		Traffic:         status.Traffic,
		Memory:          status.Memory,
		ConnectionCount: status.ConnectionCount,
		DownloadTotal:   status.DownloadTotal,
		UploadTotal:     status.UploadTotal,
		ObservedAt:      status.ObservedAt,
	})
}

func (handler managementHandler) runtimeLogs(
	response http.ResponseWriter,
	request *http.Request,
) {
	limit, _, ok := pagination(request, 200, 1000)
	if !ok {
		writeInvalidRequest(response, request)
		return
	}
	logs, err := handler.manager.RuntimeLogs(request.Context(), limit)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	result := make([]runtimeLogResponse, 0, len(logs))
	for _, entry := range logs {
		result = append(result, runtimeLogResponse{
			Timestamp: entry.Timestamp,
			Message:   entry.Message,
		})
	}
	writePrivateJSON(response, http.StatusOK, runtimeLogsResponse{Logs: result})
}

func (handler managementHandler) startRuntime(
	response http.ResponseWriter,
	request *http.Request,
) {
	handler.runRuntimeAction(response, request, "start")
}

func (handler managementHandler) stopRuntime(
	response http.ResponseWriter,
	request *http.Request,
) {
	handler.runRuntimeAction(response, request, "stop")
}

func (handler managementHandler) restartRuntime(
	response http.ResponseWriter,
	request *http.Request,
) {
	handler.runRuntimeAction(response, request, "restart")
}

func (handler managementHandler) reloadRuntime(
	response http.ResponseWriter,
	request *http.Request,
) {
	handler.runRuntimeAction(response, request, "reload")
}

func (handler managementHandler) runRuntimeAction(
	response http.ResponseWriter,
	request *http.Request,
	action string,
) {
	if err := handler.manager.RuntimeAction(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		action,
	); err != nil {
		handler.writeError(response, request, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func (handler managementHandler) configPreview(
	response http.ResponseWriter,
	request *http.Request,
) {
	reveal := request.URL.Query().Get("reveal") == "true"
	if reveal &&
		request.Header.Get("X-Confirm-Sensitive") != revealConfigConfirmation {
		writeAPIError(
			response,
			request,
			http.StatusForbidden,
			"SENSITIVE_CONFIRMATION_REQUIRED",
			"Explicit confirmation is required to reveal configuration secrets.",
		)
		return
	}
	compiled, hash, err := handler.manager.PreviewConfig(
		request.Context(),
		reveal,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, configPreviewResponse{
		YAML:     string(compiled),
		SHA256:   hash,
		Revealed: reveal,
	})
}

func (handler managementHandler) validateConfig(
	response http.ResponseWriter,
	request *http.Request,
) {
	hash, err := handler.manager.ValidateConfig(request.Context())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, configValidationResponse{
		Valid:  true,
		SHA256: hash,
	})
}

func (handler managementHandler) listRevisions(
	response http.ResponseWriter,
	request *http.Request,
) {
	limit, offset, ok := pagination(request, 50, 100)
	if !ok {
		writeInvalidRequest(response, request)
		return
	}
	revisions, err := handler.manager.Revisions(
		request.Context(),
		limit,
		offset,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	result := make([]revisionResponse, 0, len(revisions))
	for _, revision := range revisions {
		result = append(result, newRevisionResponse(revision))
	}
	writePrivateJSON(response, http.StatusOK, revisionsResponse{
		Revisions: result,
	})
}

func (handler managementHandler) getRevision(
	response http.ResponseWriter,
	request *http.Request,
) {
	revision, err := handler.manager.Revision(
		request.Context(),
		chi.URLParam(request, "revisionID"),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, newRevisionResponse(revision))
}

func (handler managementHandler) rollbackRevision(
	response http.ResponseWriter,
	request *http.Request,
) {
	revision, err := handler.manager.Rollback(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "revisionID"),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, rollbackResponse{
		Revision: newRevisionResponse(revision),
	})
}

func (handler managementHandler) getSettings(
	response http.ResponseWriter,
	request *http.Request,
) {
	settings, err := handler.manager.EditableSettings(request.Context())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, settingsResponse{
		PanelTitle:         settings.PanelTitle,
		UILanguage:         settings.UILanguage,
		PublicHost:         settings.PublicHost,
		CookieSecure:       settings.CookieSecure,
		RequiresMUIRestart: settings.CookieSecure != handler.cookieSecure,
	})
}

func (handler managementHandler) updateSettings(
	response http.ResponseWriter,
	request *http.Request,
) {
	var input settingsRequest
	if decodeJSON(response, request, &input) != nil {
		writeInvalidRequest(response, request)
		return
	}
	revision, err := handler.manager.UpdateSettings(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		service.EditableSettings{
			PanelTitle:   input.PanelTitle,
			UILanguage:   input.UILanguage,
			PublicHost:   input.PublicHost,
			CookieSecure: input.CookieSecure,
		},
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, settingsMutationResponse{
		Settings: settingsResponse{
			PanelTitle:         input.PanelTitle,
			UILanguage:         input.UILanguage,
			PublicHost:         input.PublicHost,
			CookieSecure:       input.CookieSecure,
			RequiresMUIRestart: input.CookieSecure != handler.cookieSecure,
		},
		Revision: newRevisionResponse(revision),
	})
}

func (handler managementHandler) restartApplication(
	response http.ResponseWriter,
	request *http.Request,
) {
	if handler.requestRestart == nil {
		writeAPIError(
			response,
			request,
			http.StatusServiceUnavailable,
			"APPLICATION_RESTART_UNAVAILABLE",
			"The current deployment does not provide an application supervisor.",
		)
		return
	}
	release, err := handler.manager.ReserveApplicationRestart()
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusAccepted, applicationRestartResponse{
		Restarting: true,
	})
	handler.requestRestart(release)
}

func (handler managementHandler) getEndpointSettings(
	response http.ResponseWriter,
	request *http.Request,
) {
	state, err := handler.manager.EndpointSettings(request.Context())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, endpointSettingsStateResponse{
		Active: endpointSettingsResponseFromService(state.Active),
		Pending: func() *endpointSettingsResponse {
			if state.Pending == nil {
				return nil
			}
			value := endpointSettingsResponseFromService(state.Pending.EndpointSettings)
			value.RequiresMUIRestart = state.Pending.RequiresMUIRestart
			value.RequiresMihomoRestart = state.Pending.RequiresMihomoRestart
			updated := state.Pending.UpdatedAt
			value.UpdatedAt = &updated
			return &value
		}(),
	})
}

func (handler managementHandler) updateEndpointSettings(
	response http.ResponseWriter,
	request *http.Request,
) {
	var input endpointSettingsRequest
	if decodeJSON(response, request, &input) != nil {
		writeInvalidRequest(response, request)
		return
	}
	state, err := handler.manager.UpdateEndpointSettings(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		service.EndpointSettings{
			PanelUIBind: domain.Endpoint{
				Host: input.PanelUIBind.Host,
				Port: input.PanelUIBind.Port,
			},
			MihomoExternalControllerBind: domain.Endpoint{
				Host: input.MihomoExternalControllerBind.Host,
				Port: input.MihomoExternalControllerBind.Port,
			},
			MihomoControllerConnect: domain.Endpoint{
				Host: input.MihomoControllerConnect.Host,
				Port: input.MihomoControllerConnect.Port,
			},
			ExternalControllerCORSOrigins: input.ExternalControllerCORSOrigins,
		},
		input.Generation,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, endpointSettingsStateResponse{
		Active: endpointSettingsResponseFromService(state.Active),
		Pending: func() *endpointSettingsResponse {
			if state.Pending == nil {
				return nil
			}
			value := endpointSettingsResponseFromService(state.Pending.EndpointSettings)
			value.RequiresMUIRestart = state.Pending.RequiresMUIRestart
			value.RequiresMihomoRestart = state.Pending.RequiresMihomoRestart
			updated := state.Pending.UpdatedAt
			value.UpdatedAt = &updated
			return &value
		}(),
	})
}

func endpointSettingsResponseFromService(
	settings service.EndpointSettings,
) endpointSettingsResponse {
	return endpointSettingsResponse{
		PanelUIBind: endpointResponse{
			Host: settings.PanelUIBind.Host,
			Port: settings.PanelUIBind.Port,
		},
		MihomoExternalControllerBind: endpointResponse{
			Host: settings.MihomoExternalControllerBind.Host,
			Port: settings.MihomoExternalControllerBind.Port,
		},
		MihomoControllerConnect: endpointResponse{
			Host: settings.MihomoControllerConnect.Host,
			Port: settings.MihomoControllerConnect.Port,
		},
		ExternalControllerCORSOrigins: append([]string(nil), settings.ExternalControllerCORSOrigins...),
		Generation:                    settings.Generation,
	}
}

func (handler managementHandler) testCore(
	response http.ResponseWriter,
	request *http.Request,
) {
	version, err := handler.manager.TestCore(request.Context())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, coreTestResponse{Version: version})
}

func (handler managementHandler) testController(
	response http.ResponseWriter,
	request *http.Request,
) {
	version, err := handler.manager.TestController(request.Context())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, controllerTestResponse{
		Version: version,
	})
}

func (handler managementHandler) listAuditEntries(
	response http.ResponseWriter,
	request *http.Request,
) {
	limit, offset, ok := pagination(request, 100, 200)
	if !ok {
		writeInvalidRequest(response, request)
		return
	}
	entries, err := handler.manager.AuditEntries(
		request.Context(),
		limit,
		offset,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	result := make([]auditEntryResponse, 0, len(entries))
	for _, entry := range entries {
		result = append(result, newAuditEntryResponse(entry))
	}
	writePrivateJSON(response, http.StatusOK, auditEntriesResponse{
		Entries: result,
	})
}

func (handler managementHandler) writeError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		writeAPIError(
			response,
			request,
			http.StatusNotFound,
			"RESOURCE_NOT_FOUND",
			"The requested managed resource was not found.",
		)
	case errors.Is(err, publisher.ErrMihomoRestartRequired):
		writeAPIError(
			response,
			request,
			http.StatusConflict,
			"MIHOMO_RESTART_REQUIRED",
			"Restart Mihomo before publishing another configuration change.",
		)
	case errors.Is(err, coremanagement.ErrMihomoRestartRequired):
		writeAPIError(
			response,
			request,
			http.StatusConflict,
			"MIHOMO_RESTART_REQUIRED",
			"Restart Mihomo before changing the managed core.",
		)
	case errors.Is(err, service.ErrConflict):
		writeAPIError(
			response,
			request,
			http.StatusConflict,
			"ENDPOINT_SETTINGS_CONFLICT",
			"Endpoint settings changed; reload the current settings and try again.",
		)
	case errors.Is(err, service.ErrValidation),
		errors.Is(err, publisher.ErrCandidateValidation):
		writeAPIError(
			response,
			request,
			http.StatusUnprocessableEntity,
			"CONFIG_VALIDATION_FAILED",
			"The requested change does not produce a valid managed configuration.",
		)
	case errors.Is(err, publisher.ErrDegraded):
		writeAPIError(
			response,
			request,
			http.StatusServiceUnavailable,
			"SYSTEM_DEGRADED",
			"Configuration changes are blocked until recovery is completed.",
		)
	case errors.Is(err, operation.ErrBusy):
		writeAPIError(
			response,
			request,
			http.StatusConflict,
			"CORE_OPERATION_IN_PROGRESS",
			"Another Mihomo runtime operation is already in progress.",
		)
	case errors.Is(err, coremanagement.ErrDegraded):
		writeAPIError(
			response,
			request,
			http.StatusServiceUnavailable,
			"SYSTEM_DEGRADED",
			"Core updates are blocked until degraded state is recovered.",
		)
	case errors.Is(err, coremanagement.ErrExternal):
		writeAPIError(
			response,
			request,
			http.StatusUnprocessableEntity,
			"CORE_NOT_MANAGED",
			"The configured external Mihomo core is read-only and cannot be updated.",
		)
	case errors.Is(err, coremanagement.ErrNoBackup):
		writeAPIError(
			response,
			request,
			http.StatusConflict,
			"CORE_BACKUP_UNAVAILABLE",
			"No previous managed Mihomo core backup is available.",
		)
	default:
		writeAPIError(
			response,
			request,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"The requested operation could not be completed.",
		)
	}
}

func (input listenerRequest) listenerSpec() service.NodeSpec {
	var users []service.UserSpec
	if input.Users != nil {
		users = make([]service.UserSpec, 0, len(input.Users))
		for _, user := range input.Users {
			users = append(users, service.UserSpec{
				Name: user.Name, Enabled: user.Enabled,
				VLESS: user.VLESS, Hysteria2: user.Hysteria2,
				VMess: user.VMess, Trojan: user.Trojan,
				Shadowsocks: user.Shadowsocks,
				ExpiresAt:   user.ExpiresAt,
			})
		}
	}
	var profiles []service.AccessProfileSpec
	if input.AccessProfiles != nil {
		profiles = make([]service.AccessProfileSpec, 0, len(input.AccessProfiles))
		for _, profile := range input.AccessProfiles {
			profiles = append(profiles, service.AccessProfileSpec{
				ID: profile.ID, Name: profile.Name, Default: profile.Default,
				PublicHost: profile.PublicHost, PublicPort: profile.PublicPort,
				ServerName: profile.ServerName, Fingerprint: profile.Fingerprint,
				PacketEncoding: profile.PacketEncoding,
				AllowInsecure:  profile.AllowInsecure,
			})
		}
	}
	return service.NodeSpec{
		Name: input.Name, Enabled: input.Enabled,
		ListenAddress: input.ListenAddress, Port: input.Port,
		Protocol: input.Protocol, VLESS: input.VLESS,
		Hysteria2: input.Hysteria2, VMess: input.VMess,
		Trojan: input.Trojan, Shadowsocks: input.Shadowsocks,
		Generation: input.Generation,
		Users:      users, AccessProfiles: profiles,
	}
}

func newListenerResponse(node domain.Node) listenerResponse {
	users := make([]userResponse, 0, len(node.Users))
	for _, user := range node.Users {
		users = append(users, newUserResponse(user))
	}
	profiles := make([]accessProfileResponse, 0, len(node.AccessProfiles))
	for _, profile := range node.AccessProfiles {
		profiles = append(profiles, accessProfileResponse{
			ID: profile.ID, NodeID: profile.NodeID, Name: profile.Name,
			Default: profile.Default, PublicHost: profile.PublicHost,
			PublicPort: profile.PublicPort, ServerName: profile.ServerName,
			Fingerprint:    profile.Fingerprint,
			PacketEncoding: profile.PacketEncoding,
			AllowInsecure:  profile.AllowInsecure,
			CreatedAt:      profile.CreatedAt, UpdatedAt: profile.UpdatedAt,
		})
	}
	vless := cloneJSON(node.VLESS)
	hysteria2 := cloneJSON(node.Hysteria2)
	vmess := cloneJSON(node.VMess)
	trojan := cloneJSON(node.Trojan)
	shadowsocks := cloneJSON(node.Shadowsocks)
	secrets := make(map[string]bool)
	redactNodeSecrets(vless, hysteria2, vmess, trojan, shadowsocks, secrets)
	return listenerResponse{
		ID: node.ID, Name: node.Name, Enabled: node.Enabled,
		ListenAddress: node.ListenAddress, Port: node.Port,
		Protocol: node.Protocol, SchemaVersion: node.SchemaVersion,
		VLESS: vless, Hysteria2: hysteria2, VMess: vmess,
		Trojan: trojan, Shadowsocks: shadowsocks,
		Users: users, AccessProfiles: profiles, SecretsSet: secrets,
		Generation: node.Generation,
		CreatedAt:  node.CreatedAt, UpdatedAt: node.UpdatedAt,
	}
}

func newUserResponse(user domain.NodeUser) userResponse {
	return userResponse{
		ID: user.ID, NodeID: user.NodeID, Name: user.Name,
		Enabled: user.Enabled, VLESS: user.VLESS, Hysteria2: user.Hysteria2,
		VMess: user.VMess, Trojan: user.Trojan, Shadowsocks: user.Shadowsocks,
		ExpiresAt: user.ExpiresAt,
		CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt,
	}
}

func cloneJSON[T any](value *T) *T {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var cloned T
	if json.Unmarshal(encoded, &cloned) != nil {
		return nil
	}
	return &cloned
}

func redactNodeSecrets(
	vless *domain.VLESSSpec,
	hysteria2 *domain.Hysteria2Spec,
	vmess *domain.VMessSpec,
	trojan *domain.TrojanSpec,
	shadowsocks *domain.ShadowsocksSpec,
	secrets map[string]bool,
) {
	if vless != nil {
		redactSecuritySecrets("vless.security", &vless.Security, secrets)
	}
	if hysteria2 != nil {
		secrets["hysteria2.private_key"] = hysteria2.PrivateKey != ""
		secrets["hysteria2.ech_key"] = hysteria2.ECHKey != ""
		secrets["hysteria2.obfs_password"] = hysteria2.ObfsPassword != ""
		hysteria2.PrivateKey = ""
		hysteria2.ECHKey = ""
		hysteria2.ObfsPassword = ""
		if hysteria2.Realm != nil {
			secrets["hysteria2.realm.token"] = hysteria2.Realm.Token != ""
			secrets["hysteria2.realm.private_key"] = hysteria2.Realm.PrivateKey != ""
			hysteria2.Realm.Token = ""
			hysteria2.Realm.PrivateKey = ""
		}
	}
	if vmess != nil {
		redactSecuritySecrets("vmess.security", &vmess.Security, secrets)
		if vmess.Handler.MKCP != nil {
			secrets["vmess.handler.mkcp.seed"] = vmess.Handler.MKCP.Seed != ""
			vmess.Handler.MKCP.Seed = ""
		}
	}
	if trojan != nil {
		redactSecuritySecrets("trojan.security", &trojan.Security, secrets)
		secrets["trojan.shadowsocks.password"] = trojan.Shadowsocks.Password != ""
		trojan.Shadowsocks.Password = ""
	}
	if shadowsocks != nil {
		redactSecuritySecrets("shadowsocks.security", &shadowsocks.Security, secrets)
	}
}

func redactSecuritySecrets(
	prefix string,
	security *domain.VLESSSecuritySpec,
	secrets map[string]bool,
) {
	if security.TLS != nil {
		secrets[prefix+".tls.private_key"] = security.TLS.PrivateKey != ""
		secrets[prefix+".tls.ech_key"] = security.TLS.ECHKey != ""
		security.TLS.PrivateKey = ""
		security.TLS.ECHKey = ""
	}
	if security.Reality != nil {
		secrets[prefix+".reality.private_key"] = security.Reality.PrivateKey != ""
		security.Reality.PrivateKey = ""
	}
	if security.ShadowTLS != nil {
		secrets[prefix+".shadow_tls.password"] = security.ShadowTLS.Password != ""
		usersSet := false
		security.ShadowTLS.Password = ""
		for index := range security.ShadowTLS.Users {
			usersSet = usersSet || security.ShadowTLS.Users[index].Password != ""
			security.ShadowTLS.Users[index].Password = ""
		}
		secrets[prefix+".shadow_tls.users"] = usersSet
	}
	if security.ResTLS != nil {
		secrets[prefix+".res_tls.password"] = security.ResTLS.Password != ""
		security.ResTLS.Password = ""
	}
	if security.JLS != nil {
		usersSet := false
		for index := range security.JLS.Users {
			usersSet = usersSet || security.JLS.Users[index].Password != ""
			security.JLS.Users[index].Password = ""
		}
		secrets[prefix+".jls.users"] = usersSet
	}
}

func newRevisionResponse(revision domain.Revision) revisionResponse {
	return revisionResponse{
		ID:             revision.ID,
		RevisionNumber: revision.RevisionNumber,
		SHA256:         revision.SHA256,
		Status:         revision.Status,
		Reason:         revision.Reason,
		ActorAdminID:   revision.ActorAdminID,
		ErrorMessage:   revision.ErrorMessageRedacted,
		CreatedAt:      revision.CreatedAt,
		ActivatedAt:    revision.ActivatedAt,
	}
}

func newAuditEntryResponse(entry store.AuditEntry) auditEntryResponse {
	return auditEntryResponse{
		ID:           entry.ID,
		ActorAdminID: entry.ActorAdminID,
		Action:       entry.Action,
		ResourceType: entry.ResourceType,
		ResourceID:   entry.ResourceID,
		Result:       entry.Result,
		Summary:      entry.SummaryRedacted,
		CreatedAt:    entry.CreatedAt,
	}
}

func pagination(
	request *http.Request,
	defaultLimit, maxLimit int,
) (int, int, bool) {
	limit := defaultLimit
	offset := 0
	var err error
	if value := request.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, false
		}
	}
	if value := request.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, false
		}
	}
	return limit, offset, limit >= 1 && limit <= maxLimit && offset >= 0
}

func writePrivateJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, status, value)
}

func writeInvalidRequest(
	response http.ResponseWriter,
	request *http.Request,
) {
	writeAPIError(
		response,
		request,
		http.StatusBadRequest,
		"INVALID_REQUEST",
		"Request parameters or body are invalid.",
	)
}
