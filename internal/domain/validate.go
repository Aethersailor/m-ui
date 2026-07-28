package domain

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	shortIDPattern  = regexp.MustCompile(`^[0-9a-f]{16}$`)
	dnsLabelPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
)

func (state DesiredState) Validate() error {
	var validationErrors []error
	if state.AsOf.IsZero() {
		validationErrors = append(validationErrors, errors.New("state as-of time is required"))
	}
	if err := validateControllerAddress(state.ControllerAddress); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if strings.TrimSpace(state.ControllerSecret) == "" {
		validationErrors = append(validationErrors, errors.New("controller secret is required"))
	}
	if err := validateHost("public host", state.PublicHost, true); err != nil {
		validationErrors = append(validationErrors, err)
	}

	listenerIDs := make(map[string]struct{}, len(state.Listeners))
	listenerNames := make(map[string]struct{}, len(state.Listeners))
	ports := make(map[uint16]struct{}, len(state.Listeners))
	for index := range state.Listeners {
		listener := state.Listeners[index]
		prefix := fmt.Sprintf("listener %d", index+1)
		if _, exists := listenerIDs[listener.ID]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("%s ID is duplicated", prefix))
		}
		listenerIDs[listener.ID] = struct{}{}
		if _, exists := listenerNames[listener.Name]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("%s name is duplicated", prefix))
		}
		listenerNames[listener.Name] = struct{}{}
		if _, exists := ports[listener.ListenPort]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("%s listen port conflicts with another listener", prefix))
		}
		ports[listener.ListenPort] = struct{}{}
		if err := validateListener(listener, state.AsOf); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: %w", prefix, err))
		}
	}
	return errors.Join(validationErrors...)
}

func ValidateRealityKey(value string) error {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return errors.New("must be an unpadded URL-safe base64 value encoding 32 bytes")
	}
	return nil
}

func ValidateShortID(value string) error {
	if !shortIDPattern.MatchString(value) {
		return errors.New("must contain exactly 16 lowercase hexadecimal characters")
	}
	return nil
}

func validateListener(listener Listener, asOf time.Time) error {
	var validationErrors []error
	if _, err := uuid.Parse(listener.ID); err != nil {
		validationErrors = append(validationErrors, errors.New("ID must be a UUID"))
	}
	if err := validateName("name", listener.Name); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if net.ParseIP(listener.ListenAddress) == nil {
		validationErrors = append(validationErrors, errors.New("listen address must be an IP address"))
	}
	if listener.ListenPort == 0 {
		validationErrors = append(validationErrors, errors.New("listen port must be between 1 and 65535"))
	}
	if listener.PublicHostOverride != "" {
		if err := validateHost("public host override", listener.PublicHostOverride, true); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}
	if listener.PublicPortOverride != nil && *listener.PublicPortOverride == 0 {
		validationErrors = append(validationErrors, errors.New("public port override must be between 1 and 65535"))
	}
	if err := validateHost("server name", listener.ServerName, false); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if err := validateDestination(listener.RealityDest); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if err := ValidateRealityKey(listener.RealityPrivateKey); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("REALITY private key %w", err))
	}
	if err := ValidateRealityKey(listener.RealityPublicKey); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("REALITY public key %w", err))
	}
	if err := ValidateShortID(listener.ShortID); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("short ID %w", err))
	}

	userIDs := make(map[string]struct{}, len(listener.Users))
	userNames := make(map[string]struct{}, len(listener.Users))
	userUUIDs := make(map[string]struct{}, len(listener.Users))
	for index := range listener.Users {
		user := listener.Users[index]
		prefix := fmt.Sprintf("user %d", index+1)
		if _, exists := userIDs[user.ID]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("%s ID is duplicated", prefix))
		}
		userIDs[user.ID] = struct{}{}
		if _, exists := userNames[user.Name]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("%s name is duplicated", prefix))
		}
		userNames[user.Name] = struct{}{}
		normalizedUUID := strings.ToLower(user.UUID)
		if _, exists := userUUIDs[normalizedUUID]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("%s UUID is duplicated", prefix))
		}
		userUUIDs[normalizedUUID] = struct{}{}
		if err := validateUser(user, listener.ID); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: %w", prefix, err))
		}
	}
	if listener.Enabled && len(listener.EffectiveUsers(asOf)) == 0 {
		validationErrors = append(validationErrors, errors.New("enabled listener must have at least one enabled, unexpired user"))
	}
	return errors.Join(validationErrors...)
}

func validateUser(user User, listenerID string) error {
	var validationErrors []error
	if _, err := uuid.Parse(user.ID); err != nil {
		validationErrors = append(validationErrors, errors.New("ID must be a UUID"))
	}
	if user.ListenerID != listenerID {
		validationErrors = append(validationErrors, errors.New("listener ID does not match the parent listener"))
	}
	if err := validateName("name", user.Name); err != nil {
		validationErrors = append(validationErrors, err)
	}
	parsed, err := uuid.Parse(user.UUID)
	if err != nil {
		validationErrors = append(validationErrors, errors.New("UUID is invalid"))
	} else if parsed.Variant() != uuid.RFC4122 {
		validationErrors = append(validationErrors, errors.New("UUID must use the RFC 4122 variant"))
	} else if parsed.Version() != 4 {
		validationErrors = append(validationErrors, errors.New("UUID must be version 4"))
	}
	return errors.Join(validationErrors...)
}

func validateName(field, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is required and must not have surrounding whitespace", field)
	}
	if len(value) > 64 {
		return fmt.Errorf("%s must not exceed 64 bytes", field)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%s must not contain control characters", field)
		}
	}
	return nil
}

func validateControllerAddress(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return errors.New("controller address must use host:port syntax")
	}
	parsedPort, err := net.LookupPort("tcp", port)
	if err != nil || parsedPort == 0 {
		return errors.New("controller address port must be between 1 and 65535")
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("controller address must use a loopback host")
	}
	return nil
}

func validateDestination(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return errors.New("REALITY destination must use host:port syntax")
	}
	if err := validateHost("REALITY destination host", host, true); err != nil {
		return err
	}
	parsedPort, err := net.LookupPort("tcp", port)
	if err != nil || parsedPort == 0 {
		return errors.New("REALITY destination port must be between 1 and 65535")
	}
	return nil
}

func validateHost(field, value string, allowIP bool) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is required and must not have surrounding whitespace", field)
	}
	host := strings.TrimSuffix(value, ".")
	if allowIP && net.ParseIP(host) != nil {
		return nil
	}
	if len(host) > 253 {
		return fmt.Errorf("%s is too long", field)
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !dnsLabelPattern.MatchString(label) {
			return fmt.Errorf("%s must be a valid DNS name", field)
		}
	}
	return nil
}
