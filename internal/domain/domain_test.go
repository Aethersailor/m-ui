package domain

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDesiredStateValidationAndEffectiveUsers(t *testing.T) {
	t.Parallel()
	state := validState()
	expiredAt := state.AsOf
	state.Listeners[0].Users = append(state.Listeners[0].Users,
		User{
			ID:         "73de80e2-1f03-403f-bf58-dd96f82f0979",
			ListenerID: state.Listeners[0].ID,
			Name:       "expired",
			Enabled:    true,
			UUID:       "3494b4ca-6e48-48a1-8b69-b5d0f148f01b",
			ExpiresAt:  &expiredAt,
		},
		User{
			ID:         "4049e001-3abe-4609-96a1-4012473ef2df",
			ListenerID: state.Listeners[0].ID,
			Name:       "disabled",
			Enabled:    false,
			UUID:       "72844a9a-6b70-4adf-a74e-2a8e5190d525",
		},
	)

	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	effective := state.Listeners[0].EffectiveUsers(state.AsOf)
	if len(effective) != 1 || effective[0].Name != "active" {
		t.Fatalf("EffectiveUsers() = %#v, want only active user", effective)
	}
}

func TestDesiredStateRejectsPortConflictAndListenerWithoutEffectiveUser(t *testing.T) {
	t.Parallel()
	state := validState()
	second := state.Listeners[0]
	second.ID = "f73e3a5a-f2dd-4c04-bfa3-b99e01c34464"
	second.Name = "second"
	second.Users = []User{{
		ID:         "0ab9ea19-11c0-4ef0-90e0-480371515892",
		ListenerID: second.ID,
		Name:       "disabled",
		Enabled:    false,
		UUID:       "abdefc62-7632-4e4f-873b-896c6276f2d6",
	}}
	state.Listeners = append(state.Listeners, second)

	err := state.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want validation failures")
	}
	message := err.Error()
	for _, expected := range []string{
		"listen port conflicts",
		"enabled listener must have at least one enabled, unexpired user",
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("Validate() error %q does not contain %q", message, expected)
		}
	}
}

func TestDesiredStateRejectsNonVersion4UserUUID(t *testing.T) {
	t.Parallel()
	state := validState()
	state.Listeners[0].Users[0].UUID = "00000000-0000-1000-8000-000000000000"

	err := state.Validate()
	if err == nil || !strings.Contains(err.Error(), "UUID must be version 4") {
		t.Fatalf("Validate() error = %v, want UUID version failure", err)
	}
}

func TestGenerateUUIDIsRFC4122Version4(t *testing.T) {
	t.Parallel()
	value, err := GenerateUUID()
	if err != nil {
		t.Fatalf("GenerateUUID() error = %v", err)
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		t.Fatalf("parse generated UUID: %v", err)
	}
	if parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 {
		t.Fatalf("generated UUID version/variant = %v/%v", parsed.Version(), parsed.Variant())
	}
}

func TestGenerateShortIDUsesEightRandomBytes(t *testing.T) {
	t.Parallel()
	value, err := generateShortID(bytes.NewReader([]byte{
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
	}))
	if err != nil {
		t.Fatalf("generateShortID() error = %v", err)
	}
	if value != "0011223344556677" {
		t.Fatalf("generateShortID() = %q", value)
	}
	if err := ValidateShortID(value); err != nil {
		t.Fatalf("ValidateShortID() error = %v", err)
	}
}

func validState() DesiredState {
	return DesiredState{
		AsOf:              time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
		ControllerAddress: "127.0.0.1:9090",
		ControllerSecret:  "controller-test-secret",
		PublicHost:        "node.example.com",
		Listeners: []Listener{{
			ID:                "bf1f792d-7646-45b1-94b4-9845742d5ad1",
			Name:              "primary",
			Enabled:           true,
			ListenAddress:     "0.0.0.0",
			ListenPort:        443,
			ServerName:        "www.example.com",
			RealityDest:       "www.example.com:443",
			RealityPrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			RealityPublicKey:  "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE",
			ShortID:           "0123456789abcdef",
			UDPEnabled:        true,
			Users: []User{{
				ID:         "e8e65007-0350-48a8-a992-33bc795b8ba3",
				ListenerID: "bf1f792d-7646-45b1-94b4-9845742d5ad1",
				Name:       "active",
				Enabled:    true,
				UUID:       "1bd44e59-d67d-4af1-9a6d-b9488c8d8a9f",
			}},
		}},
	}
}
