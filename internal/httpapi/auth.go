package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
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
	service      *auth.Service
	cookieSecure bool
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
	writeJSON(response, http.StatusOK, map[string]any{
		"admin": adminResponse{
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
	writeJSON(response, status, map[string]any{
		"error": map[string]any{
			"code":       code,
			"message":    message,
			"request_id": middleware.GetReqID(request.Context()),
			"details":    map[string]any{},
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
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(response, request)
	})
}
