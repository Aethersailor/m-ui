package app

import (
	"context"
	"fmt"
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
