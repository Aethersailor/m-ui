package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Aethersailor/m-ui/internal/auth"
	"github.com/Aethersailor/m-ui/internal/store"
)

const (
	sessionCookieName = "m_ui_session"
	csrfCookieName    = "m_ui_csrf"
	csrfHeaderName    = "X-CSRF-Token"
	maxJSONBody       = 64 << 10
)

type authContextKey struct{}

type authHandler struct {
	service         *auth.Service
	cookieSecure    bool
	languageDefault func(context.Context) (string, error)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type adminResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type loginResponse struct {
	Admin     adminResponse `json:"admin"`
	CSRFToken string        `json:"csrf_token"`
	ExpiresAt time.Time     `json:"expires_at"`
}

type setupStatusResponse struct {
	State           string `json:"state"`
	LanguageDefault string `json:"language_default"`
	PasswordPolicy  struct {
		MinimumCharacters int `json:"minimum_characters"`
		MaximumBytes      int `json:"maximum_bytes"`
	} `json:"password_policy"`
}

type setupRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type meResponse struct {
	Admin adminResponse `json:"admin"`
}

type apiErrorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code      string   `json:"code"`
	Message   string   `json:"message"`
	RequestID string   `json:"request_id"`
	Details   struct{} `json:"details"`
}

func mountAuthRoutes(router chi.Router, handler authHandler) {
	router.Route("/auth", func(authRouter chi.Router) {
		authRouter.Post("/login", handler.login)
		authRouter.Group(func(protected chi.Router) {
			protected.Use(handler.authenticate)
			protected.Get("/me", handler.me)
			protected.Group(func(mutations chi.Router) {
				mutations.Use(handler.requireCSRF)
				mutations.Post("/logout", handler.logout)
				mutations.Post("/change-password", handler.changePassword)
			})
		})
	})
}

func mountSetupRoutes(router chi.Router, handler authHandler) {
	router.Route("/setup", func(setupRouter chi.Router) {
		setupRouter.Get("/status", handler.setupStatus)
		setupRouter.Post("/complete", handler.completeSetup)
	})
}

func (h authHandler) setupStatus(
	response http.ResponseWriter,
	request *http.Request,
) {
	status, err := h.service.SetupStatus(request.Context())
	if err != nil {
		writeAPIError(
			response,
			request,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Setup status could not be read.",
		)
		return
	}
	state := "complete"
	if status.Required {
		state = "required"
	}
	response.Header().Set("Cache-Control", "no-store")
	languageDefault := "auto"
	if h.languageDefault != nil {
		languageDefault, err = h.languageDefault(request.Context())
		if err != nil {
			writeAPIError(
				response,
				request,
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"UI preferences could not be read.",
			)
			return
		}
	}
	result := setupStatusResponse{
		State:           state,
		LanguageDefault: languageDefault,
	}
	result.PasswordPolicy.MinimumCharacters = auth.MinimumPasswordCharacters
	result.PasswordPolicy.MaximumBytes = auth.MaximumPasswordBytes
	writeJSON(response, http.StatusOK, result)
}

func (h authHandler) completeSetup(
	response http.ResponseWriter,
	request *http.Request,
) {
	if !setupTransportAllowed(request) {
		writeAPIError(
			response,
			request,
			http.StatusForbidden,
			"SETUP_TRANSPORT_NOT_ALLOWED",
			"First administrator setup requires a same-origin browser request.",
		)
		return
	}
	if !isJSONContentType(request.Header.Get("Content-Type")) {
		writeAPIError(
			response,
			request,
			http.StatusBadRequest,
			"INVALID_REQUEST",
			"Request body must use application/json.",
		)
		return
	}
	var input setupRequest
	if err := decodeJSON(response, request, &input); err != nil {
		writeAPIError(
			response,
			request,
			http.StatusBadRequest,
			"INVALID_REQUEST",
			"Request body is invalid.",
		)
		return
	}
	credentials, err := h.service.CompleteSetup(
		request.Context(),
		input.Username,
		input.Password,
		remoteIP(request.RemoteAddr),
		request.UserAgent(),
	)
	var rateLimit *auth.RateLimitError
	switch {
	case errors.As(err, &rateLimit):
		seconds := int(math.Ceil(rateLimit.RetryAfter.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		response.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeAPIError(
			response,
			request,
			http.StatusTooManyRequests,
			"SETUP_RATE_LIMITED",
			"Setup is temporarily rate limited. Try again later.",
		)
	case errors.Is(err, auth.ErrBootstrapCompleted):
		writeAPIError(
			response,
			request,
			http.StatusConflict,
			"SETUP_ALREADY_COMPLETED",
			"The first administrator has already been created.",
		)
	case errors.Is(err, auth.ErrPasswordPolicy):
		writeAPIError(
			response,
			request,
			http.StatusBadRequest,
			"PASSWORD_POLICY_FAILED",
			"Password does not satisfy the password policy.",
		)
	case errors.Is(err, auth.ErrNoAdministrator):
		writeAPIError(
			response,
			request,
			http.StatusConflict,
			"SETUP_ALREADY_COMPLETED",
			"The first administrator has already been created.",
		)
	case err != nil:
		writeAPIError(
			response,
			request,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Administrator setup could not be completed.",
		)
	default:
		h.setAuthCookies(response, credentials)
		response.Header().Set("Cache-Control", "no-store")
		writeJSON(response, http.StatusCreated, loginResponse{
			Admin: adminResponse{
				ID:       credentials.Admin.ID,
				Username: credentials.Admin.Username,
			},
			CSRFToken: credentials.CSRFToken,
			ExpiresAt: credentials.Session.ExpiresAt,
		})
	}
}

func setupTransportAllowed(request *http.Request) bool {
	origins := request.Header.Values("Origin")
	if len(origins) != 1 || origins[0] == "" {
		return false
	}
	origin, err := url.Parse(origins[0])
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") ||
		origin.Host == "" || origin.User != nil ||
		origin.Opaque != "" || origin.Path != "" || origin.RawQuery != "" ||
		origin.Fragment != "" {
		return false
	}
	originHost, originPort, ok := setupHost(origin.Host, origin.Scheme)
	if !ok {
		return false
	}
	requestHost, requestPort, ok := setupHost(request.Host, origin.Scheme)
	if !ok || !strings.EqualFold(originHost, requestHost) || originPort != requestPort {
		return false
	}
	if fetchSite := request.Header.Get("Sec-Fetch-Site"); fetchSite != "" &&
		fetchSite != "same-origin" && fetchSite != "none" {
		return false
	}
	return true
}

func setupHost(value string, scheme string) (string, string, bool) {
	if value == "" || strings.TrimSpace(value) != value ||
		strings.ContainsAny(value, "\r\n,/") {
		return "", "", false
	}
	hostname := value
	port := ""
	if strings.HasPrefix(value, "[") {
		closing := strings.IndexByte(value, ']')
		if closing <= 1 {
			return "", "", false
		}
		hostname = value[1:closing]
		remainder := value[closing+1:]
		if remainder != "" {
			if !strings.HasPrefix(remainder, ":") {
				return "", "", false
			}
			port = remainder[1:]
		}
	} else if strings.Count(value, ":") > 0 {
		parsedHost, parsedPort, err := net.SplitHostPort(value)
		if err != nil {
			return "", "", false
		}
		hostname, port = parsedHost, parsedPort
	}
	if hostname == "" || strings.TrimSpace(hostname) != hostname {
		return "", "", false
	}
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", "", false
	}
	return hostname, strconv.Itoa(portNumber), true
}

func isJSONContentType(value string) bool {
	parts := strings.Split(value, ";")
	return strings.EqualFold(strings.TrimSpace(parts[0]), "application/json")
}

func (h authHandler) login(response http.ResponseWriter, request *http.Request) {
	var input loginRequest
	if err := decodeJSON(response, request, &input); err != nil {
		writeAPIError(
			response,
			request,
			http.StatusBadRequest,
			"INVALID_REQUEST",
			"Request body is invalid.",
		)
		return
	}
	credentials, err := h.service.Login(
		request.Context(),
		input.Username,
		input.Password,
		remoteIP(request.RemoteAddr),
		request.UserAgent(),
	)
	var rateLimit *auth.RateLimitError
	switch {
	case errors.As(err, &rateLimit):
		seconds := int(math.Ceil(rateLimit.RetryAfter.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		response.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeAPIError(
			response,
			request,
			http.StatusTooManyRequests,
			"LOGIN_RATE_LIMITED",
			"Too many attempts. Try again later.",
		)
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeAPIError(
			response,
			request,
			http.StatusUnauthorized,
			"AUTHENTICATION_FAILED",
			"Username or password is incorrect.",
		)
		return
	case err != nil:
		writeAPIError(
			response,
			request,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Authentication could not be completed.",
		)
		return
	}

	h.setAuthCookies(response, credentials)
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, loginResponse{
		Admin: adminResponse{
			ID:       credentials.Admin.ID,
			Username: credentials.Admin.Username,
		},
		CSRFToken: credentials.CSRFToken,
		ExpiresAt: credentials.Session.ExpiresAt,
	})
}

func (h authHandler) me(response http.ResponseWriter, request *http.Request) {
	current := currentAuthSession(request.Context())
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, meResponse{
		Admin: adminResponse{
			ID:       current.Admin.ID,
			Username: current.Admin.Username,
		},
	})
}

func (h authHandler) logout(response http.ResponseWriter, request *http.Request) {
	current := currentAuthSession(request.Context())
	if err := h.service.Logout(request.Context(), current); err != nil {
		writeAPIError(
			response,
			request,
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"Logout could not be completed.",
		)
		return
	}
	h.clearAuthCookies(response)
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func (h authHandler) changePassword(
	response http.ResponseWriter,
	request *http.Request,
) {
	var input changePasswordRequest
	if err := decodeJSON(response, request, &input); err != nil {
		writeAPIError(
			response,
			request,
			http.StatusBadRequest,
			"INVALID_REQUEST",
			"Request body is invalid.",
		)
		return
	}
	current := currentAuthSession(request.Context())
	err := h.service.ChangePassword(
		request.Context(),
		current,
		input.CurrentPassword,
		input.NewPassword,
	)
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeAPIError(
			response,
			request,
			http.StatusUnauthorized,
			"AUTHENTICATION_FAILED",
			"Current password is incorrect.",
		)
	case errors.Is(err, auth.ErrPasswordPolicy):
		writeAPIError(
			response,
			request,
			http.StatusBadRequest,
			"PASSWORD_POLICY_FAILED",
			"New password does not satisfy the password policy.",
		)
	case err != nil:
		writeAPIError(
			response,
			request,
			http.StatusInternalServerError,
			"PASSWORD_CHANGE_FAILED",
			"Password change could not be completed.",
		)
	default:
		h.clearAuthCookies(response)
		response.Header().Set("Cache-Control", "no-store")
		response.WriteHeader(http.StatusNoContent)
	}
}

func (h authHandler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(sessionCookieName)
		if err != nil {
			writeAPIError(
				response,
				request,
				http.StatusUnauthorized,
				"AUTHENTICATION_REQUIRED",
				"Authentication is required.",
			)
			return
		}
		current, err := h.service.Authenticate(request.Context(), cookie.Value)
		if errors.Is(err, auth.ErrInvalidCredentials) {
			h.clearAuthCookies(response)
			writeAPIError(
				response,
				request,
				http.StatusUnauthorized,
				"AUTHENTICATION_REQUIRED",
				"Authentication is required.",
			)
			return
		}
		if err != nil {
			writeAPIError(
				response,
				request,
				http.StatusInternalServerError,
				"INTERNAL_ERROR",
				"Session validation failed.",
			)
			return
		}
		ctx := context.WithValue(request.Context(), authContextKey{}, current)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func (h authHandler) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current := currentAuthSession(request.Context())
		if err := h.service.VerifyCSRF(
			current.Session,
			request.Header.Get(csrfHeaderName),
		); err != nil {
			writeAPIError(
				response,
				request,
				http.StatusForbidden,
				"CSRF_VALIDATION_FAILED",
				"CSRF validation failed.",
			)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func currentAuthSession(ctx context.Context) store.AuthSession {
	current, ok := ctx.Value(authContextKey{}).(store.AuthSession)
	if !ok {
		panic("authenticated session missing from request context")
	}
	return current
}

func (h authHandler) setAuthCookies(
	response http.ResponseWriter,
	credentials auth.Credentials,
) {
	maxAge := int(time.Until(credentials.Session.ExpiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    credentials.SessionToken,
		Path:     "/",
		Expires:  credentials.Session.ExpiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(response, &http.Cookie{
		Name:     csrfCookieName,
		Value:    credentials.CSRFToken,
		Path:     "/",
		Expires:  credentials.Session.ExpiresAt,
		MaxAge:   maxAge,
		HttpOnly: false,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h authHandler) clearAuthCookies(response http.ResponseWriter) {
	for _, name := range []string{sessionCookieName, csrfCookieName} {
		http.SetCookie(response, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			Expires:  time.Unix(1, 0),
			MaxAge:   -1,
			HttpOnly: name == sessionCookieName,
			Secure:   h.cookieSecure,
			SameSite: http.SameSiteStrictMode,
		})
	}
}

func decodeJSON(
	response http.ResponseWriter,
	request *http.Request,
	target any,
) error {
	reader := http.MaxBytesReader(response, request.Body, maxJSONBody)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeAPIError(
	response http.ResponseWriter,
	request *http.Request,
	status int,
	code string,
	message string,
) {
	response.Header().Set("Cache-Control", "no-store")
	requestID := ""
	if request != nil {
		requestID = middleware.GetReqID(request.Context())
	}
	writeJSON(response, status, apiErrorResponse{
		Error: apiError{
			Code:      code,
			Message:   message,
			RequestID: requestID,
			Details:   struct{}{},
		},
	})
}

func remoteIP(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return host
	}
	return strings.TrimSpace(remoteAddress)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; connect-src 'self'; object-src 'none'; "+
				"base-uri 'none'; frame-ancestors 'none'; form-action 'self'",
		)
		// Browsers ignore COOP on insecure origins and report it as a console
		// error. Emit it only when the request is directly TLS-protected or the
		// reverse proxy reports an HTTPS origin.
		forwardedProto, _, _ := strings.Cut(
			request.Header.Get("X-Forwarded-Proto"),
			",",
		)
		if request.TLS != nil || strings.EqualFold(strings.TrimSpace(forwardedProto), "https") {
			response.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		}
		response.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		response.Header().Set(
			"Permissions-Policy",
			"camera=(), geolocation=(), microphone=()",
		)
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(response, request)
	})
}
