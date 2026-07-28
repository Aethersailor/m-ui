package crypto

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAndLoadMasterKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state", "master.key")
	generated, err := GenerateMasterKey(path)
	if err != nil {
		t.Fatalf("GenerateMasterKey() error = %v", err)
	}
	loaded, err := LoadMasterKey(path)
	if err != nil {
		t.Fatalf("LoadMasterKey() error = %v", err)
	}
	if generated != loaded {
		t.Fatal("loaded key does not match generated key")
	}
	if _, err := GenerateMasterKey(path); err == nil {
		t.Fatal("second GenerateMasterKey() error = nil, want exclusive-create error")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Size(); got != MasterKeySize {
		t.Fatalf("master key size = %d, want %d", got, MasterKeySize)
	}
}

func TestSealerRoundTripAndAuthentication(t *testing.T) {
	t.Parallel()

	var key MasterKey
	copy(key[:], bytes.Repeat([]byte{0x42}, MasterKeySize))
	sealer, err := newSealer(key, bytes.NewReader(bytes.Repeat([]byte{0x24}, 64)))
	if err != nil {
		t.Fatal(err)
	}

	envelope, err := sealer.Encrypt([]byte("synthetic-secret"), "controller-secret")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := sealer.Decrypt(envelope, "controller-secret")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(plaintext), "synthetic-secret"; got != want {
		t.Fatalf("plaintext = %q, want %q", got, want)
	}
	if _, err := sealer.Decrypt(envelope, "reality-private-key"); err == nil {
		t.Fatal("Decrypt() wrong purpose error = nil")
	}

	tampered := envelope[:len(envelope)-1] + "A"
	if _, err := sealer.Decrypt(tampered, "controller-secret"); err == nil {
		t.Fatal("Decrypt() tampered envelope error = nil")
	}
}

func TestLoadMasterKeyRejectsWrongLength(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMasterKey(path); err == nil {
		t.Fatal("LoadMasterKey() error = nil, want wrong-length error")
	}
}
