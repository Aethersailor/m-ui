package app

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/Aethersailor/m-ui/internal/auth"
	"github.com/Aethersailor/m-ui/internal/config"
	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
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

func SetupLink(ctx context.Context, cfg config.Config) (string, error) {
	database, sealer, err := openBootstrapPlane(ctx, cfg)
	if err != nil {
		return "", err
	}
	defer func() { _ = database.Close() }()
	if err := auth.EnsureBootstrap(ctx, database, sealer, nil, time.Now); err != nil {
		return "", fmt.Errorf("initialize administrator bootstrap: %w", err)
	}
	state, err := database.BootstrapState(ctx)
	if err != nil {
		return "", fmt.Errorf("read administrator bootstrap state: %w", err)
	}
	token, err := auth.ReadBootstrapToken(state, sealer)
	if err != nil {
		return "", err
	}
	return setupURL(cfg, token), nil
}

func RotateSetupLink(ctx context.Context, cfg config.Config) (string, error) {
	database, sealer, err := openBootstrapPlane(ctx, cfg)
	if err != nil {
		return "", err
	}
	defer func() { _ = database.Close() }()
	if err := auth.EnsureBootstrap(ctx, database, sealer, nil, time.Now); err != nil {
		return "", fmt.Errorf("initialize administrator bootstrap: %w", err)
	}
	token, err := auth.RotateBootstrapToken(ctx, database, sealer, nil, time.Now)
	if err != nil {
		return "", fmt.Errorf("rotate administrator bootstrap token: %w", err)
	}
	return setupURL(cfg, token), nil
}

func openBootstrapPlane(
	ctx context.Context,
	cfg config.Config,
) (*store.Store, *muicrypto.Sealer, error) {
	masterKey, err := muicrypto.LoadMasterKey(cfg.Storage.MasterKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load master key: %w", err)
	}
	sealer, err := muicrypto.NewSealer(masterKey)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize field encryption: %w", err)
	}
	database, err := store.Open(ctx, cfg.Storage.DatabasePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open store: %w", err)
	}
	return database, sealer, nil
}

func setupURL(cfg config.Config, token string) string {
	host := cfg.Server.ListenAddress
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		host = "127.0.0.1"
	}
	return (&url.URL{
		Scheme:   "http",
		Host:     net.JoinHostPort(host, fmt.Sprint(cfg.Server.Port)),
		Path:     "/setup",
		Fragment: "token=" + token,
	}).String()
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
