package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aethersailor/m-ui/internal/config"
	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
)

func TestResetAdminPasswordValueKeepsFirstAdministratorInWebBootstrap(t *testing.T) {
	root := t.TempDir()
	masterKeyPath := filepath.Join(root, "master.key")
	if _, err := muicrypto.GenerateMasterKey(masterKeyPath); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Storage.DatabasePath = filepath.Join(root, "m-ui.db")
	cfg.Storage.MasterKeyPath = masterKeyPath

	created, err := ResetAdminPasswordValue(
		context.Background(),
		cfg,
		"admin",
		"too-short",
	)
	if err == nil || !strings.Contains(err.Error(), "at least 12 characters") {
		t.Fatalf("short password error = %v", err)
	}
	if created {
		t.Fatal("invalid password unexpectedly created an administrator")
	}

	created, err = ResetAdminPasswordValue(
		context.Background(),
		cfg,
		"admin",
		"synthetic-password-one",
	)
	if err == nil || !strings.Contains(err.Error(), "use web bootstrap") {
		t.Fatalf("first-administrator recovery error = %v", err)
	}
	if created {
		t.Fatal("password recovery bypassed first-administrator bootstrap")
	}
}
