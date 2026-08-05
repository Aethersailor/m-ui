package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"

	"github.com/Aethersailor/m-ui/internal/auth"
	"github.com/Aethersailor/m-ui/internal/config"
	"github.com/Aethersailor/m-ui/internal/store"
)

func ResetAdminPassword(
	ctx context.Context,
	cfg config.Config,
	username string,
	passwordFile string,
) (bool, error) {
	password, err := readPasswordFile(passwordFile)
	if err != nil {
		return false, err
	}
	return ResetAdminPasswordValue(ctx, cfg, username, password)
}

func ResetAdminPasswordValue(
	ctx context.Context,
	cfg config.Config,
	username string,
	password string,
) (bool, error) {
	if err := auth.ValidatePassword(password); err != nil {
		return false, fmt.Errorf("validate administrator password: %w", err)
	}
	database, err := store.Open(ctx, cfg.Storage.DatabasePath)
	if err != nil {
		return false, fmt.Errorf("open store: %w", err)
	}
	defer func() {
		_ = database.Close()
	}()
	sessionTTL, err := cfg.SessionTTL()
	if err != nil {
		return false, err
	}
	service, err := auth.NewService(database, auth.Options{
		SessionTTL: sessionTTL,
	})
	if err != nil {
		return false, err
	}
	_, created, err := service.ResetPassword(ctx, username, password)
	if err != nil {
		return false, fmt.Errorf("reset administrator password: %w", err)
	}
	return created, nil
}

func SetupLink(_ context.Context, cfg config.Config) (string, error) {
	return setupURL(cfg, "")
}

func SetupLinkForBaseURL(
	_ context.Context,
	cfg config.Config,
	baseURL string,
) (string, error) {
	return setupURL(cfg, baseURL)
}

func RotateSetupLink(_ context.Context, cfg config.Config) (string, error) {
	return setupURL(cfg, "")
}

func RotateSetupLinkForBaseURL(
	_ context.Context,
	cfg config.Config,
	baseURL string,
) (string, error) {
	return setupURL(cfg, baseURL)
}

func setupURL(cfg config.Config, baseURL string) (string, error) {
	if baseURL != "" {
		parsed, err := url.Parse(baseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" ||
			parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", errors.New("base URL must be an absolute HTTP or HTTPS URL")
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/setup"
		parsed.RawPath = ""
		return parsed.String(), nil
	}
	host := cfg.Server.ListenAddress
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		host = "127.0.0.1"
	}
	return (&url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, fmt.Sprint(cfg.Server.Port)),
		Path:   "/setup",
	}).String(), nil
}

func readPasswordFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("stat password file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("password file is not a regular file")
	}
	if info.Size() > 4096 {
		return "", fmt.Errorf("password file is too large")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("password file permissions must be 0600")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read password file: %w", err)
	}
	password := strings.TrimSuffix(string(content), "\n")
	password = strings.TrimSuffix(password, "\r")
	if err := auth.ValidatePassword(password); err != nil {
		return "", fmt.Errorf("validate password file: %w", err)
	}
	return password, nil
}
