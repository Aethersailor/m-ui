package app

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aethersailor/m-ui/internal/config"
	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
)

func TestSetupLinkUsesOneTimeFragmentCapability(t *testing.T) {
	root := t.TempDir()
	masterKeyPath := filepath.Join(root, "master.key")
	if _, err := muicrypto.GenerateMasterKey(masterKeyPath); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Storage.DatabasePath = filepath.Join(root, "m-ui.db")
	cfg.Storage.MasterKeyPath = masterKeyPath
	first, err := SetupLink(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(first)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.RawQuery != "" || parsed.Path != "/setup" || !strings.HasPrefix(parsed.Fragment, "token=") {
		t.Fatalf("unsafe setup link: %q", first)
	}
	second, err := SetupLink(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("setup-link regenerated the capability on restart")
	}
	rotated, err := RotateSetupLink(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rotated == first {
		t.Fatal("rotating setup token did not change the link")
	}
}
