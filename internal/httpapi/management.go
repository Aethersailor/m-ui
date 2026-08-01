package httpapi

import (
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
	manager *service.Manager
}

type listenerRequest struct {
	Name               string  `json:"name"`
	ListenAddress      string  `json:"listen_address"`
	ListenPort         uint16  `json:"listen_port"`
	PublicHostOverride string  `json:"public_host_override"`
	PublicPortOverride *uint16 `json:"public_port_override"`
	ServerName         string  `json:"server_name"`
	RealityDest        string  `json:"reality_dest"`
	RealityPrivateKey  string  `json:"reality_private_key"`
	RealityPublicKey   string  `json:"reality_public_key"`
	ShortID            string  `json:"short_id"`
	UDPEnabled         bool    `json:"udp_enabled"`
}

type listenerResponse struct {
	ID                   string         `json:"id"`
	Name                 string         `json:"name"`
	Enabled              bool           `json:"enabled"`
	ListenAddress        string         `json:"listen_address"`
	ListenPort           uint16         `json:"listen_port"`
	PublicHostOverride   string         `json:"public_host_override"`
	PublicPortOverride   *uint16        `json:"public_port_override"`
	ServerName           string         `json:"server_name"`
	RealityDest          string         `json:"reality_dest"`
	RealityPublicKey     string         `json:"reality_public_key"`
	RealityPrivateKeySet bool           `json:"reality_private_key_set"`
	ShortID              string         `json:"short_id"`
	UDPEnabled           bool           `json:"udp_enabled"`
	Users                []userResponse `json:"users"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

type listenersResponse struct {
	Listeners []listenerResponse `json:"listeners"`
}

type listenerMutationResponse struct {
	Listener listenerResponse `json:"listener"`
	Revision revisionResponse `json:"revision"`
}

type userRequest struct {
	Name      string     `json:"name"`
	UUID      string     `json:"uuid"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type userResponse struct {
	ID         string     `json:"id"`
	ListenerID string     `json:"listener_id"`
	Name       string     `json:"name"`
	Enabled    bool       `json:"enabled"`
	UUID       string     `json:"uuid"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type usersResponse struct {
	Users []userResponse `json:"users"`
}

type userMutationResponse struct {
	User     userResponse     `json:"user"`
	Revision revisionResponse `json:"revision"`
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
	PanelTitle string `json:"panel_title"`
	UILanguage string `json:"ui_language"`
	PublicHost string `json:"public_host"`
}

type settingsResponse struct {
	PanelTitle string `json:"panel_title"`
	UILanguage string `json:"ui_language"`
	PublicHost string `json:"public_host"`
}

type settingsMutationResponse struct {
	Settings settingsResponse `json:"settings"`
	Revision revisionResponse `json:"revision"`
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

		protected.Get("/listeners", handler.listListeners)
		protected.Get("/listeners/{listenerID}", handler.getListener)
		protected.Get("/listeners/{listenerID}/users", handler.listUsers)
		protected.Get(
			"/listeners/{listenerID}/users/{userID}/share",
			handler.shareUser,
		)
		protected.Get("/runtime/status", handler.runtimeStatus)
		protected.Get("/runtime/logs", handler.runtimeLogs)
		protected.Get("/config/preview", handler.configPreview)
		protected.Get("/config/revisions", handler.listRevisions)
		protected.Get("/config/revisions/{revisionID}", handler.getRevision)
		protected.Get("/settings", handler.getSettings)
		protected.Get("/system/core", handler.getCore)
		protected.Get("/audit-logs", handler.listAuditEntries)

		protected.Group(func(mutations chi.Router) {
			mutations.Use(auth.requireCSRF)

			mutations.Post("/listeners", handler.createListener)
			mutations.Put("/listeners/{listenerID}", handler.updateListener)
			mutations.Delete("/listeners/{listenerID}", handler.deleteListener)
			mutations.Post(
				"/listeners/{listenerID}/enable",
				handler.enableListener,
			)
			mutations.Post(
				"/listeners/{listenerID}/disable",
				handler.disableListener,
			)
			mutations.Post(
				"/listeners/generate-reality-keypair",
				handler.generateRealityKeypair,
			)

			mutations.Post(
				"/listeners/{listenerID}/users",
				handler.createUser,
			)
			mutations.Put(
				"/listeners/{listenerID}/users/{userID}",
				handler.updateUser,
			)
			mutations.Delete(
				"/listeners/{listenerID}/users/{userID}",
				handler.deleteUser,
			)
			mutations.Post(
				"/listeners/{listenerID}/users/{userID}/enable",
				handler.enableUser,
			)
			mutations.Post(
				"/listeners/{listenerID}/users/{userID}/disable",
				handler.disableUser,
			)
			mutations.Post(
				"/listeners/{listenerID}/users/generate-uuid",
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
	listeners, err := handler.manager.Listeners(request.Context())
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	result := make([]listenerResponse, 0, len(listeners))
	for _, listener := range listeners {
		result = append(result, newListenerResponse(listener))
	}
	writePrivateJSON(response, http.StatusOK, listenersResponse{Listeners: result})
}

func (handler managementHandler) getListener(
	response http.ResponseWriter,
	request *http.Request,
) {
	listener, err := handler.manager.Listener(
		request.Context(),
		chi.URLParam(request, "listenerID"),
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
	listener, revision, err := handler.manager.CreateListener(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		input.listenerSpec(),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusCreated, listenerMutationResponse{
		Listener: newListenerResponse(listener),
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
	listener, revision, err := handler.manager.UpdateListener(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "listenerID"),
		input.listenerSpec(),
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, listenerMutationResponse{
		Listener: newListenerResponse(listener),
		Revision: newRevisionResponse(revision),
	})
}

func (handler managementHandler) deleteListener(
	response http.ResponseWriter,
	request *http.Request,
) {
	_, err := handler.manager.DeleteListener(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "listenerID"),
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
	listener, revision, err := handler.manager.SetListenerEnabled(
		request.Context(),
		currentAuthSession(request.Context()).Admin.ID,
		chi.URLParam(request, "listenerID"),
		enabled,
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, listenerMutationResponse{
		Listener: newListenerResponse(listener),
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
		chi.URLParam(request, "listenerID"),
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
		chi.URLParam(request, "listenerID"),
		service.UserSpec{
			Name:      input.Name,
			UUID:      input.UUID,
			ExpiresAt: input.ExpiresAt,
		},
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
		chi.URLParam(request, "listenerID"),
		chi.URLParam(request, "userID"),
		service.UserSpec{
			Name:      input.Name,
			UUID:      input.UUID,
			ExpiresAt: input.ExpiresAt,
		},
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
		chi.URLParam(request, "listenerID"),
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
		chi.URLParam(request, "listenerID"),
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
	share, err := handler.manager.Share(
		request.Context(),
		chi.URLParam(request, "listenerID"),
		chi.URLParam(request, "userID"),
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
		PanelTitle: settings.PanelTitle,
		UILanguage: settings.UILanguage,
		PublicHost: settings.PublicHost,
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
			PanelTitle: input.PanelTitle,
			UILanguage: input.UILanguage,
			PublicHost: input.PublicHost,
		},
	)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writePrivateJSON(response, http.StatusOK, settingsMutationResponse{
		Settings: settingsResponse(input),
		Revision: newRevisionResponse(revision),
	})
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

func (input listenerRequest) listenerSpec() service.ListenerSpec {
	return service.ListenerSpec{
		Name:               input.Name,
		ListenAddress:      input.ListenAddress,
		ListenPort:         input.ListenPort,
		PublicHostOverride: input.PublicHostOverride,
		PublicPortOverride: input.PublicPortOverride,
		ServerName:         input.ServerName,
		RealityDest:        input.RealityDest,
		RealityPrivateKey:  input.RealityPrivateKey,
		RealityPublicKey:   input.RealityPublicKey,
		ShortID:            input.ShortID,
		UDPEnabled:         input.UDPEnabled,
	}
}

func newListenerResponse(listener domain.Listener) listenerResponse {
	users := make([]userResponse, 0, len(listener.Users))
	for _, user := range listener.Users {
		users = append(users, newUserResponse(user))
	}
	return listenerResponse{
		ID:                   listener.ID,
		Name:                 listener.Name,
		Enabled:              listener.Enabled,
		ListenAddress:        listener.ListenAddress,
		ListenPort:           listener.ListenPort,
		PublicHostOverride:   listener.PublicHostOverride,
		PublicPortOverride:   listener.PublicPortOverride,
		ServerName:           listener.ServerName,
		RealityDest:          listener.RealityDest,
		RealityPublicKey:     listener.RealityPublicKey,
		RealityPrivateKeySet: listener.RealityPrivateKey != "",
		ShortID:              listener.ShortID,
		UDPEnabled:           listener.UDPEnabled,
		Users:                users,
		CreatedAt:            listener.CreatedAt,
		UpdatedAt:            listener.UpdatedAt,
	}
}

func newUserResponse(user domain.User) userResponse {
	return userResponse{
		ID:         user.ID,
		ListenerID: user.ListenerID,
		Name:       user.Name,
		Enabled:    user.Enabled,
		UUID:       user.UUID,
		ExpiresAt:  user.ExpiresAt,
		CreatedAt:  user.CreatedAt,
		UpdatedAt:  user.UpdatedAt,
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
