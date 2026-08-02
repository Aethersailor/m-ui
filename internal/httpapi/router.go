package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Aethersailor/m-ui/internal/auth"
	"github.com/Aethersailor/m-ui/internal/httpapi/ui"
	"github.com/Aethersailor/m-ui/internal/service"
	"github.com/Aethersailor/m-ui/internal/version"
)

type Options struct {
	Logger          *slog.Logger
	Build           version.Info
	Auth            *auth.Service
	Management      *service.Manager
	LanguageDefault func(context.Context) (string, error)
	CookieSecure    bool
}

type healthResponse struct {
	Status string       `json:"status"`
	Time   time.Time    `json:"time"`
	Build  version.Info `json:"build"`
}

func New(options Options) http.Handler {
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(recoverer(logger))
	router.Use(accessLog(logger))
	router.Use(securityHeaders)

	health := func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, healthResponse{
			Status: "ok",
			Time:   time.Now().UTC(),
			Build:  options.Build,
		})
	}
	router.Get("/healthz", health)
	router.Route("/api/v1", func(api chi.Router) {
		api.Get("/health", health)
		if options.Auth != nil {
			authentication := authHandler{
				service:         options.Auth,
				cookieSecure:    options.CookieSecure,
				languageDefault: options.LanguageDefault,
			}
			mountAuthRoutes(api, authentication)
			mountSetupRoutes(api, authentication)
			if options.Management != nil {
				mountManagementRoutes(
					api,
					authentication,
					managementHandler{manager: options.Management},
				)
			}
		}
		api.NotFound(func(response http.ResponseWriter, request *http.Request) {
			writeJSON(response, http.StatusNotFound, apiErrorResponse{
				Error: apiError{
					Code:      "NOT_FOUND",
					Message:   "The requested API resource was not found.",
					RequestID: middleware.GetReqID(request.Context()),
					Details:   struct{}{},
				},
			})
		})
	})
	router.NotFound(ui.SPAHandler())

	return router
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		slog.Error("encode HTTP response", "error", err)
	}
}

func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Error(
						"recovered HTTP panic",
						"request_id",
						middleware.GetReqID(request.Context()),
					)
					http.Error(
						response,
						http.StatusText(http.StatusInternalServerError),
						http.StatusInternalServerError,
					)
				}
			}()
			next.ServeHTTP(response, request)
		})
	}
}

func accessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			started := time.Now()
			next.ServeHTTP(response, request)
			logger.Info(
				"HTTP request",
				"request_id",
				middleware.GetReqID(request.Context()),
				"method",
				request.Method,
				"path",
				request.URL.Path,
				"duration_ms",
				time.Since(started).Milliseconds(),
			)
		})
	}
}
