package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Aethersailor/m-ui/internal/store"
)

var (
	ErrInvalidCredentials    = errors.New("invalid credentials")
	ErrInvalidCSRF           = errors.New("invalid CSRF token")
	ErrPasswordPolicy        = errors.New("password does not satisfy policy")
	ErrBootstrapCompleted    = errors.New("administrator bootstrap is already completed")
	ErrInvalidBootstrapToken = errors.New("administrator bootstrap token is invalid")
	ErrNoAdministrator       = errors.New("no administrator exists; use web bootstrap")
)

type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return "login is temporarily rate limited"
}

type Repository interface {
	AdminByUsername(context.Context, string) (store.Admin, error)
	AdminByID(context.Context, string) (store.Admin, error)
	ResetAdminPassword(
		context.Context,
		string,
		string,
		time.Time,
	) (store.Admin, bool, error)
	ResetAdminPasswordWithAudit(
		context.Context,
		string,
		string,
		time.Time,
		store.AuditEntry,
	) (store.Admin, bool, error)
	UpdateAdminPassword(context.Context, string, string, time.Time) error
	CreateSession(context.Context, store.Session) error
	AuthSessionByTokenHash(
		context.Context,
		string,
		time.Time,
	) (store.AuthSession, error)
	TouchSession(context.Context, string, time.Time) error
	DeleteSession(context.Context, string) error
	DeleteExpiredSessions(context.Context, time.Time) error
	InsertAudit(context.Context, store.AuditEntry) error
}

type Options struct {
	SessionTTL       time.Duration
	Clock            func() time.Time
	Random           io.Reader
	Limiter          *LoginLimiter
	BootstrapLimiter *LoginLimiter
	PasswordParams   PasswordParams
}

type Service struct {
	repository   Repository
	sessionTTL   time.Duration
	clock        func() time.Time
	random       io.Reader
	limiter      *LoginLimiter
	bootstrap    BootstrapRepository
	setupLimiter *LoginLimiter
	setupSlots   chan struct{}
	dummyHash    string
	params       PasswordParams
}

type Credentials struct {
	Admin        store.Admin
	Session      store.Session
	SessionToken string
	CSRFToken    string
}

func NewService(repository Repository, options Options) (*Service, error) {
	if repository == nil {
		return nil, errors.New("authentication repository is required")
	}
	if options.SessionTTL <= 0 || options.SessionTTL > 24*time.Hour {
		return nil, errors.New("session TTL must be positive and at most 24h")
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.Limiter == nil {
		options.Limiter = NewLoginLimiter(options.Clock)
	}
	if options.BootstrapLimiter == nil {
		options.BootstrapLimiter = NewLoginLimiter(options.Clock)
	}
	if options.PasswordParams.Memory == 0 {
		options.PasswordParams = DefaultPasswordParams
	}

	dummyHash, err := hashPassword(
		"synthetic-dummy-password-value",
		options.PasswordParams,
		options.Random,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize password verifier: %w", err)
	}
	return &Service{
		repository:   repository,
		sessionTTL:   options.SessionTTL,
		clock:        options.Clock,
		random:       options.Random,
		limiter:      options.Limiter,
		bootstrap:    bootstrapRepository(repository),
		setupLimiter: options.BootstrapLimiter,
		setupSlots:   make(chan struct{}, 2),
		dummyHash:    dummyHash,
		params:       options.PasswordParams,
	}, nil
}

func (s *Service) ResetPassword(
	ctx context.Context,
	username string,
	password string,
) (store.Admin, bool, error) {
	if err := ValidateUsername(username); err != nil {
		return store.Admin{}, false, err
	}
	if _, err := s.repository.AdminByUsername(ctx, username); errors.Is(err, store.ErrNotFound) {
		return store.Admin{}, false, ErrNoAdministrator
	} else if err != nil {
		return store.Admin{}, false, fmt.Errorf("lookup administrator: %w", err)
	}
	if err := ValidatePassword(password); err != nil {
		return store.Admin{}, false, err
	}
	passwordHash, err := hashPassword(password, s.params, s.random)
	if err != nil {
		return store.Admin{}, false, err
	}
	now := s.clock().UTC()
	auditID, err := newOpaqueID(s.random)
	if err != nil {
		return store.Admin{}, false, err
	}
	admin, created, err := s.repository.ResetAdminPasswordWithAudit(
		ctx,
		username,
		passwordHash,
		now,
		store.AuditEntry{
			ID:              auditID,
			Action:          "auth.password_reset",
			ResourceType:    "administrator",
			Result:          "success",
			SummaryRedacted: "Administrator password was reset locally.",
			CreatedAt:       now,
		},
	)
	if err != nil {
		return store.Admin{}, false, err
	}
	return admin, created, nil
}

func bootstrapRepository(repository Repository) BootstrapRepository {
	value, _ := repository.(BootstrapRepository)
	return value
}

func (s *Service) SetupStatus(ctx context.Context) (SetupStatus, error) {
	if s.bootstrap == nil {
		return SetupStatus{}, errors.New("bootstrap repository is unavailable")
	}
	state, err := s.bootstrap.BootstrapState(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return SetupStatus{}, errors.New("administrator bootstrap state is unavailable")
	}
	if err != nil {
		return SetupStatus{}, err
	}
	return SetupStatus{Required: state.Required}, nil
}

func (s *Service) CompleteSetup(
	ctx context.Context,
	username string,
	password string,
	sourceIP string,
	userAgent string,
) (Credentials, error) {
	if s.bootstrap == nil {
		return Credentials{}, errors.New("bootstrap repository is unavailable")
	}
	key := "bootstrap\x00" + sourceIP
	if retry := s.setupLimiter.RetryAfter(key); retry > 0 {
		return Credentials{}, &RateLimitError{RetryAfter: retry}
	}
	state, err := s.bootstrap.BootstrapState(ctx)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !state.Required) {
		return Credentials{}, ErrBootstrapCompleted
	}
	if err != nil {
		return Credentials{}, err
	}
	select {
	case s.setupSlots <- struct{}{}:
		defer func() { <-s.setupSlots }()
	default:
		return Credentials{}, &RateLimitError{RetryAfter: time.Second}
	}
	if err := ValidateUsername(username); err != nil {
		s.setupLimiter.Failure(key)
		return Credentials{}, err
	}
	if err := ValidatePassword(password); err != nil {
		s.setupLimiter.Failure(key)
		return Credentials{}, fmt.Errorf("%w: %v", ErrPasswordPolicy, err)
	}
	passwordHash, err := hashPassword(password, s.params, s.random)
	if err != nil {
		return Credentials{}, err
	}
	sessionToken, err := newToken(s.random, sessionTokenBytes)
	if err != nil {
		return Credentials{}, err
	}
	csrfToken, err := newToken(s.random, csrfTokenBytes)
	if err != nil {
		return Credentials{}, err
	}
	adminID, err := newOpaqueID(s.random)
	if err != nil {
		return Credentials{}, err
	}
	sessionID, err := newOpaqueID(s.random)
	if err != nil {
		return Credentials{}, err
	}
	auditID, err := newOpaqueID(s.random)
	if err != nil {
		return Credentials{}, err
	}
	now := s.clock().UTC()
	admin := store.Admin{
		ID:                adminID,
		Username:          username,
		PasswordHash:      passwordHash,
		PasswordChangedAt: now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	session := store.Session{
		ID:               sessionID,
		AdminUserID:      adminID,
		SessionTokenHash: HashToken(sessionToken),
		CSRFTokenHash:    HashToken(csrfToken),
		ExpiresAt:        now.Add(s.sessionTTL),
		LastSeenAt:       now,
		CreatedAt:        now,
		UserAgent:        sanitizeUserAgent(userAgent),
	}
	if err := s.bootstrap.CompleteBootstrap(
		ctx,
		state.TokenHash,
		store.BootstrapCompletion{
			Admin:   admin,
			Session: session,
			Audit: store.AuditEntry{
				ID:              auditID,
				ActorAdminID:    adminID,
				Action:          "auth.bootstrap_complete",
				ResourceType:    "administrator",
				ResourceID:      adminID,
				Result:          "success",
				SummaryRedacted: "Initial administrator was created through the local bootstrap flow.",
				CreatedAt:       now,
			},
		},
		now,
	); errors.Is(err, store.ErrBootstrapCompleted) {
		return Credentials{}, ErrBootstrapCompleted
	} else if errors.Is(err, store.ErrInvalidBootstrapToken) {
		return Credentials{}, ErrBootstrapCompleted
	} else if err != nil {
		return Credentials{}, err
	}
	s.setupLimiter.Success(key)
	return Credentials{
		Admin:        admin,
		Session:      session,
		SessionToken: sessionToken,
		CSRFToken:    csrfToken,
	}, nil
}

func (s *Service) Login(
	ctx context.Context,
	username string,
	password string,
	sourceIP string,
	userAgent string,
) (Credentials, error) {
	username = strings.TrimSpace(username)
	key := loginLimitKey(sourceIP, username)
	if retry := s.limiter.RetryAfter(key); retry > 0 {
		return Credentials{}, &RateLimitError{RetryAfter: retry}
	}

	admin, lookupErr := s.repository.AdminByUsername(ctx, username)
	passwordHash := s.dummyHash
	if lookupErr == nil {
		passwordHash = admin.PasswordHash
	} else if !errors.Is(lookupErr, store.ErrNotFound) {
		return Credentials{}, fmt.Errorf("lookup administrator: %w", lookupErr)
	}
	valid, verifyErr := VerifyPassword(password, passwordHash)
	if verifyErr != nil && lookupErr == nil {
		return Credentials{}, fmt.Errorf("verify stored password: %w", verifyErr)
	}
	if lookupErr != nil || !valid {
		s.limiter.Failure(key)
		_ = s.writeAudit(
			ctx,
			"",
			"auth.login",
			"session",
			"",
			"failure",
			"Authentication failed.",
		)
		return Credentials{}, ErrInvalidCredentials
	}

	sessionToken, err := newToken(s.random, sessionTokenBytes)
	if err != nil {
		return Credentials{}, err
	}
	csrfToken, err := newToken(s.random, csrfTokenBytes)
	if err != nil {
		return Credentials{}, err
	}
	sessionID, err := newOpaqueID(s.random)
	if err != nil {
		return Credentials{}, err
	}
	now := s.clock().UTC()
	session := store.Session{
		ID:               sessionID,
		AdminUserID:      admin.ID,
		SessionTokenHash: HashToken(sessionToken),
		CSRFTokenHash:    HashToken(csrfToken),
		ExpiresAt:        now.Add(s.sessionTTL),
		LastSeenAt:       now,
		CreatedAt:        now,
		UserAgent:        sanitizeUserAgent(userAgent),
	}
	if err := s.repository.CreateSession(ctx, session); err != nil {
		return Credentials{}, err
	}
	if err := s.writeAudit(
		ctx,
		admin.ID,
		"auth.login",
		"session",
		session.ID,
		"success",
		"Administrator signed in.",
	); err != nil {
		_ = s.repository.DeleteSession(ctx, session.ID)
		return Credentials{}, fmt.Errorf("record login audit: %w", err)
	}
	s.limiter.Success(key)
	return Credentials{
		Admin:        admin,
		Session:      session,
		SessionToken: sessionToken,
		CSRFToken:    csrfToken,
	}, nil
}

func (s *Service) Authenticate(
	ctx context.Context,
	sessionToken string,
) (store.AuthSession, error) {
	if sessionToken == "" {
		return store.AuthSession{}, ErrInvalidCredentials
	}
	now := s.clock().UTC()
	authSession, err := s.repository.AuthSessionByTokenHash(
		ctx,
		HashToken(sessionToken),
		now,
	)
	if errors.Is(err, store.ErrNotFound) {
		return store.AuthSession{}, ErrInvalidCredentials
	}
	if err != nil {
		return store.AuthSession{}, err
	}
	if now.Sub(authSession.Session.LastSeenAt) >= 5*time.Minute {
		if err := s.repository.TouchSession(
			ctx,
			authSession.Session.ID,
			now,
		); err != nil {
			return store.AuthSession{}, err
		}
		authSession.Session.LastSeenAt = now
	}
	return authSession, nil
}

func (s *Service) VerifyCSRF(
	session store.Session,
	rawToken string,
) error {
	actual := HashToken(rawToken)
	if subtle.ConstantTimeCompare(
		[]byte(actual),
		[]byte(session.CSRFTokenHash),
	) != 1 {
		return ErrInvalidCSRF
	}
	return nil
}

func (s *Service) Logout(
	ctx context.Context,
	authSession store.AuthSession,
) error {
	if err := s.repository.DeleteSession(ctx, authSession.Session.ID); err != nil {
		return err
	}
	_ = s.writeAudit(
		ctx,
		authSession.Admin.ID,
		"auth.logout",
		"session",
		authSession.Session.ID,
		"success",
		"Administrator signed out.",
	)
	return nil
}

func (s *Service) ChangePassword(
	ctx context.Context,
	authSession store.AuthSession,
	currentPassword string,
	newPassword string,
) error {
	valid, err := VerifyPassword(
		currentPassword,
		authSession.Admin.PasswordHash,
	)
	if err != nil {
		return fmt.Errorf("verify current password: %w", err)
	}
	if !valid {
		_ = s.writeAudit(
			ctx,
			authSession.Admin.ID,
			"auth.change_password",
			"administrator",
			authSession.Admin.ID,
			"failure",
			"Password change authentication failed.",
		)
		return ErrInvalidCredentials
	}
	if err := ValidatePassword(newPassword); err != nil {
		return fmt.Errorf("%w: %v", ErrPasswordPolicy, err)
	}
	newHash, err := hashPassword(newPassword, s.params, s.random)
	if err != nil {
		return err
	}
	if err := s.repository.UpdateAdminPassword(
		ctx,
		authSession.Admin.ID,
		newHash,
		s.clock().UTC(),
	); err != nil {
		return err
	}
	if err := s.writeAudit(
		ctx,
		authSession.Admin.ID,
		"auth.change_password",
		"administrator",
		authSession.Admin.ID,
		"success",
		"Administrator changed the password.",
	); err != nil {
		return fmt.Errorf("record password change audit: %w", err)
	}
	return nil
}

func (s *Service) writeAudit(
	ctx context.Context,
	actorAdminID string,
	action string,
	resourceType string,
	resourceID string,
	result string,
	summary string,
) error {
	id, err := newOpaqueID(s.random)
	if err != nil {
		return err
	}
	return s.repository.InsertAudit(ctx, store.AuditEntry{
		ID:              id,
		ActorAdminID:    actorAdminID,
		Action:          action,
		ResourceType:    resourceType,
		ResourceID:      resourceID,
		Result:          result,
		SummaryRedacted: summary,
		CreatedAt:       s.clock().UTC(),
	})
}

func sanitizeUserAgent(value string) string {
	value = strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return -1
		}
		return char
	}, value)
	if len(value) <= 256 {
		return value
	}
	value = value[:256]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
