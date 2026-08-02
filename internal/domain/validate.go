package domain

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	shortIDPattern  = regexp.MustCompile(`^[0-9a-f]{16}$`)
	dnsLabelPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
)

func (state DesiredState) Validate() error {
	normalized, err := state.NormalizeLegacy()
	if err != nil {
		return fmt.Errorf("controller endpoint is invalid: %w", err)
	}
	state = normalized
	var validationErrors []error
	if state.AsOf.IsZero() {
		validationErrors = append(validationErrors, errors.New("state as-of time is required"))
	}
	if err := ValidateBindEndpoint(state.PanelUIBind, "panel UI bind endpoint"); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if err := ValidateBindEndpoint(
		state.MihomoExternalControllerBind,
		"Mihomo external-controller bind endpoint",
	); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if err := ValidateConnectEndpoint(
		state.MihomoControllerConnect,
		"m-ui Mihomo controller connect endpoint",
	); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if err := ValidateControllerEndpointPair(
		state.MihomoExternalControllerBind,
		state.MihomoControllerConnect,
	); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if err := ValidateCORSOrigins(state.ExternalControllerCORSOrigins); err != nil {
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

func ValidateBindEndpoint(endpoint Endpoint, field string) error {
	if strings.TrimSpace(endpoint.Host) == "" || endpoint.Host != strings.TrimSpace(endpoint.Host) {
		return fmt.Errorf("%s host is required and must not have surrounding whitespace", field)
	}
	if net.ParseIP(endpoint.Host) == nil {
		return fmt.Errorf("%s host must be an IPv4 or IPv6 address", field)
	}
	if endpoint.Port == 0 {
		return fmt.Errorf("%s port must be between 1 and 65535", field)
	}
	return nil
}

func ValidateConnectEndpoint(endpoint Endpoint, field string) error {
	if err := ValidateBindEndpoint(endpoint, field); err != nil {
		return err
	}
	ip := net.ParseIP(endpoint.Host)
	if ip == nil || !ip.IsLoopback() || ip.IsUnspecified() ||
		(endpoint.Host != "127.0.0.1" && endpoint.Host != "::1") {
		return fmt.Errorf("%s host must be exactly 127.0.0.1 or ::1", field)
	}
	return nil
}

// ValidateControllerEndpointPair prevents an m-ui loopback client from being
// configured for a different address family or port than Mihomo's listener.
// The accepted wildcard mappings are explicit so a saved configuration cannot
// look valid while making the internal Controller unreachable.
func ValidateControllerEndpointPair(bind, connect Endpoint) error {
	if bind.Port != connect.Port {
		return fmt.Errorf(
			"Mihomo external-controller bind and m-ui Controller connect ports must match",
		)
	}
	if connect.Host != "127.0.0.1" && connect.Host != "::1" {
		return errors.New(
			"m-ui Mihomo Controller connect host must be exactly 127.0.0.1 or ::1",
		)
	}
	switch bind.Host {
	case "127.0.0.1", "0.0.0.0":
		if connect.Host != "127.0.0.1" {
			return fmt.Errorf(
				"Mihomo IPv4 bind %s requires m-ui Controller connect host 127.0.0.1",
				bind.Host,
			)
		}
	case "::1", "::":
		if connect.Host != "::1" {
			return fmt.Errorf(
				"Mihomo IPv6 bind %s requires m-ui Controller connect host ::1",
				bind.Host,
			)
		}
	default:
		return fmt.Errorf(
			"Mihomo external-controller bind host %s is not one of the supported loopback or wildcard addresses",
			bind.Host,
		)
	}
	return nil
}

func ValidateCORSOrigins(origins []string) error {
	for index, origin := range origins {
		if origin == "" || origin == "*" || strings.TrimSpace(origin) != origin {
			return fmt.Errorf("CORS origin %d must be an exact HTTP(S) origin", index+1)
		}
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Host == "" || parsed.User != nil || parsed.Path != "" ||
			parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("CORS origin %d must be an exact HTTP(S) origin", index+1)
		}
		if strings.HasSuffix(parsed.Host, ":") {
			return fmt.Errorf("CORS origin %d must be an exact HTTP(S) origin", index+1)
		}
		if port := parsed.Port(); port != "" {
			parsedPort, err := strconv.Atoi(port)
			if err != nil || parsedPort < 1 || parsedPort > 65535 {
				return fmt.Errorf("CORS origin %d must be an exact HTTP(S) origin", index+1)
			}
		}
		if err := validateHost("CORS origin host", parsed.Hostname(), true); err != nil {
			return err
		}
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
