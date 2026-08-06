package domain

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	shortIDPattern  = regexp.MustCompile(`^[0-9a-f]{2,16}$`)
	dnsLabelPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
)

type PortRange struct {
	Start uint16
	End   uint16
}

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
	if err := ValidateBindEndpoint(state.MihomoExternalControllerBind, "Mihomo external-controller bind endpoint"); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if err := ValidateConnectEndpoint(state.MihomoControllerConnect, "m-ui Mihomo controller connect endpoint"); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if err := ValidateControllerEndpointPair(state.MihomoExternalControllerBind, state.MihomoControllerConnect); err != nil {
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

	nodeIDs := make(map[string]struct{}, len(state.Nodes))
	nodeNames := make(map[string]struct{}, len(state.Nodes))
	type boundRange struct {
		address string
		range_  PortRange
		name    string
	}
	var bound []boundRange
	for index := range state.Nodes {
		node := state.Nodes[index]
		prefix := fmt.Sprintf("node %d", index+1)
		if _, exists := nodeIDs[node.ID]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("%s ID is duplicated", prefix))
		}
		nodeIDs[node.ID] = struct{}{}
		if _, exists := nodeNames[node.Name]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("%s name is duplicated", prefix))
		}
		nodeNames[node.Name] = struct{}{}
		ranges, rangeErr := ParsePortRanges(node.Port)
		if rangeErr == nil {
			for _, current := range ranges {
				for _, previous := range bound {
					if previous.address == node.ListenAddress && rangesOverlap(previous.range_, current) {
						validationErrors = append(validationErrors, fmt.Errorf(
							"%s port range conflicts with node %q", prefix, previous.name,
						))
					}
				}
				bound = append(bound, boundRange{address: node.ListenAddress, range_: current, name: node.Name})
			}
		}
		if err := validateNode(node, state.AsOf); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: %w", prefix, err))
		}
	}
	return errors.Join(validationErrors...)
}

func ParsePortRanges(value string) ([]PortRange, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return nil, errors.New("port is required and must not have surrounding whitespace")
	}
	var ranges []PortRange
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, errors.New("port contains an empty range")
		}
		startText, endText, hasRange := strings.Cut(part, "-")
		start, err := strconv.ParseUint(startText, 10, 16)
		if err != nil || start == 0 {
			return nil, fmt.Errorf("port %q must be between 1 and 65535", part)
		}
		end := start
		if hasRange {
			parsedEnd, err := strconv.ParseUint(endText, 10, 16)
			if err != nil || parsedEnd == 0 || parsedEnd < start {
				return nil, fmt.Errorf("port range %q is invalid", part)
			}
			end = parsedEnd
		}
		ranges = append(ranges, PortRange{Start: uint16(start), End: uint16(end)})
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	for i := 1; i < len(ranges); i++ {
		if rangesOverlap(ranges[i-1], ranges[i]) {
			return nil, errors.New("port ranges overlap")
		}
	}
	return ranges, nil
}

func SinglePort(value string) (uint16, bool) {
	ranges, err := ParsePortRanges(value)
	if err != nil || len(ranges) != 1 || ranges[0].Start != ranges[0].End {
		return 0, false
	}
	return ranges[0].Start, true
}

func rangesOverlap(left, right PortRange) bool {
	return left.Start <= right.End && right.Start <= left.End
}

func ValidateRealityKey(value string) error {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return errors.New("must be an unpadded URL-safe base64 value encoding 32 bytes")
	}
	return nil
}

func ValidateShortID(value string) error {
	if !shortIDPattern.MatchString(value) || len(value)%2 != 0 {
		return errors.New("must contain 2 to 16 lowercase hexadecimal characters in complete bytes")
	}
	return nil
}

func validateNode(node Node, asOf time.Time) error {
	var validationErrors []error
	if _, err := uuid.Parse(node.ID); err != nil {
		validationErrors = append(validationErrors, errors.New("ID must be a UUID"))
	}
	if err := validateName("name", node.Name); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if net.ParseIP(node.ListenAddress) == nil {
		validationErrors = append(validationErrors, errors.New("listen address must be an IP address"))
	}
	if _, err := ParsePortRanges(node.Port); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if node.SchemaVersion != NodeSchemaVersion {
		validationErrors = append(validationErrors, fmt.Errorf("schema version must be %d", NodeSchemaVersion))
	}
	protocolSpecCount := 0
	for _, configured := range []bool{
		node.VLESS != nil, node.Hysteria2 != nil, node.VMess != nil,
		node.Trojan != nil, node.Shadowsocks != nil,
	} {
		if configured {
			protocolSpecCount++
		}
	}
	switch node.Protocol {
	case ProtocolVLESS:
		if node.VLESS == nil || protocolSpecCount != 1 {
			validationErrors = append(validationErrors, errors.New("VLESS node must contain only a VLESS protocol specification"))
		} else if err := validateVLESS(*node.VLESS); err != nil {
			validationErrors = append(validationErrors, err)
		}
	case ProtocolHysteria2:
		if node.Hysteria2 == nil || protocolSpecCount != 1 {
			validationErrors = append(validationErrors, errors.New("Hysteria2 node must contain only a Hysteria2 protocol specification"))
		} else if err := validateHysteria2(*node.Hysteria2); err != nil {
			validationErrors = append(validationErrors, err)
		}
	case ProtocolVMess:
		if node.VMess == nil || protocolSpecCount != 1 {
			validationErrors = append(validationErrors, errors.New("VMess node must contain only a VMess protocol specification"))
		} else if err := validateVMess(*node.VMess); err != nil {
			validationErrors = append(validationErrors, err)
		}
	case ProtocolTrojan:
		if node.Trojan == nil || protocolSpecCount != 1 {
			validationErrors = append(validationErrors, errors.New("Trojan node must contain only a Trojan protocol specification"))
		} else if err := validateTrojan(*node.Trojan); err != nil {
			validationErrors = append(validationErrors, err)
		}
	case ProtocolShadowsocks:
		if node.Shadowsocks == nil || protocolSpecCount != 1 {
			validationErrors = append(validationErrors, errors.New("Shadowsocks node must contain only a Shadowsocks protocol specification"))
		} else if err := validateShadowsocks(*node.Shadowsocks); err != nil {
			validationErrors = append(validationErrors, err)
		}
	default:
		validationErrors = append(validationErrors, fmt.Errorf("unsupported protocol %q", node.Protocol))
	}

	userIDs := make(map[string]struct{}, len(node.Users))
	userNames := make(map[string]struct{}, len(node.Users))
	credentialIDs := make(map[string]struct{}, len(node.Users))
	for index := range node.Users {
		user := node.Users[index]
		prefix := fmt.Sprintf("user %d", index+1)
		if _, exists := userIDs[user.ID]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("%s ID is duplicated", prefix))
		}
		userIDs[user.ID] = struct{}{}
		if _, exists := userNames[user.Name]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("%s name is duplicated", prefix))
		}
		userNames[user.Name] = struct{}{}
		credentialID, err := validateNodeUser(user, node)
		if err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: %w", prefix, err))
		} else if _, exists := credentialIDs[credentialID]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("%s credential is duplicated", prefix))
		} else {
			credentialIDs[credentialID] = struct{}{}
		}
	}
	if node.Enabled && len(node.EffectiveUsers(asOf)) == 0 {
		validationErrors = append(validationErrors, errors.New("enabled node must have at least one enabled, unexpired user"))
	}
	if node.Protocol == ProtocolShadowsocks && len(node.EffectiveUsers(asOf)) > 1 {
		validationErrors = append(validationErrors, errors.New("Mihomo Shadowsocks listener supports exactly one effective user"))
	}
	if node.Protocol == ProtocolShadowsocks && node.Shadowsocks != nil && strings.HasPrefix(node.Shadowsocks.Cipher, "2022-") {
		expectedBytes := 32
		if node.Shadowsocks.Cipher == "2022-blake3-aes-128-gcm" {
			expectedBytes = 16
		}
		for _, user := range node.EffectiveUsers(asOf) {
			if user.Shadowsocks == nil {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(user.Shadowsocks.Password)
			if err != nil || len(decoded) != expectedBytes {
				validationErrors = append(validationErrors, fmt.Errorf("Shadowsocks 2022 password must be standard base64 encoding exactly %d bytes", expectedBytes))
			}
		}
	}

	profileIDs := make(map[string]struct{}, len(node.AccessProfiles))
	profileNames := make(map[string]struct{}, len(node.AccessProfiles))
	defaultCount := 0
	for index, profile := range node.AccessProfiles {
		if profile.Default {
			defaultCount++
		}
		if _, exists := profileIDs[profile.ID]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("access profile %d ID is duplicated", index+1))
		}
		profileIDs[profile.ID] = struct{}{}
		if _, exists := profileNames[profile.Name]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("access profile %d name is duplicated", index+1))
		}
		profileNames[profile.Name] = struct{}{}
		if err := validateAccessProfile(profile, node.ID); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("access profile %d: %w", index+1, err))
		}
	}
	if len(node.AccessProfiles) == 0 {
		validationErrors = append(validationErrors, errors.New("at least one access profile is required"))
	} else if defaultCount != 1 {
		validationErrors = append(validationErrors, errors.New("exactly one access profile must be the default"))
	}
	if node.Generation < 1 {
		validationErrors = append(validationErrors, errors.New("generation must be positive"))
	}
	return errors.Join(validationErrors...)
}

func validateNodeUser(user NodeUser, node Node) (string, error) {
	var validationErrors []error
	if _, err := uuid.Parse(user.ID); err != nil {
		validationErrors = append(validationErrors, errors.New("ID must be a UUID"))
	}
	if user.NodeID != node.ID {
		validationErrors = append(validationErrors, errors.New("node ID does not match the parent node"))
	}
	if err := validateName("name", user.Name); err != nil {
		validationErrors = append(validationErrors, err)
	}
	credentialID := ""
	switch node.Protocol {
	case ProtocolVLESS:
		if user.VLESS == nil || userCredentialCount(user) != 1 {
			validationErrors = append(validationErrors, errors.New("VLESS user must contain only VLESS credentials"))
			break
		}
		parsed, err := uuid.Parse(user.VLESS.UUID)
		if err != nil || parsed.Variant() != uuid.RFC4122 || parsed.Version() != 4 {
			validationErrors = append(validationErrors, errors.New("VLESS UUID must be an RFC 4122 version 4 UUID"))
		}
		if user.VLESS.Flow != "" && user.VLESS.Flow != VLESSFlowVision {
			validationErrors = append(validationErrors, fmt.Errorf("unsupported VLESS flow %q", user.VLESS.Flow))
		}
		credentialID = strings.ToLower(user.VLESS.UUID)
	case ProtocolHysteria2:
		if user.Hysteria2 == nil || userCredentialCount(user) != 1 {
			validationErrors = append(validationErrors, errors.New("Hysteria2 user must contain only Hysteria2 credentials"))
			break
		}
		if strings.TrimSpace(user.Hysteria2.Password) == "" {
			validationErrors = append(validationErrors, errors.New("Hysteria2 password is required"))
		}
		credentialID = user.Hysteria2.Password
	case ProtocolVMess:
		if user.VMess == nil || userCredentialCount(user) != 1 {
			validationErrors = append(validationErrors, errors.New("VMess user must contain only VMess credentials"))
			break
		}
		parsed, err := uuid.Parse(user.VMess.UUID)
		if err != nil || parsed.Variant() != uuid.RFC4122 {
			validationErrors = append(validationErrors, errors.New("VMess UUID must be an RFC 4122 UUID"))
		}
		if user.VMess.AlterID < 0 {
			validationErrors = append(validationErrors, errors.New("VMess alter ID must not be negative"))
		}
		if !oneOf(defaultString(user.VMess.Cipher, "auto"), "auto", "aes-128-gcm", "chacha20-poly1305", "none", "zero") {
			validationErrors = append(validationErrors, fmt.Errorf("unsupported VMess cipher %q", user.VMess.Cipher))
		}
		credentialID = strings.ToLower(user.VMess.UUID)
	case ProtocolTrojan:
		if user.Trojan == nil || userCredentialCount(user) != 1 {
			validationErrors = append(validationErrors, errors.New("Trojan user must contain only Trojan credentials"))
			break
		}
		if strings.TrimSpace(user.Trojan.Password) == "" {
			validationErrors = append(validationErrors, errors.New("Trojan password is required"))
		}
		credentialID = user.Trojan.Password
	case ProtocolShadowsocks:
		if user.Shadowsocks == nil || userCredentialCount(user) != 1 {
			validationErrors = append(validationErrors, errors.New("Shadowsocks user must contain only Shadowsocks credentials"))
			break
		}
		if strings.TrimSpace(user.Shadowsocks.Password) == "" {
			validationErrors = append(validationErrors, errors.New("Shadowsocks password is required"))
		}
		credentialID = user.Shadowsocks.Password
	}
	return credentialID, errors.Join(validationErrors...)
}

func userCredentialCount(user NodeUser) int {
	count := 0
	for _, configured := range []bool{
		user.VLESS != nil, user.Hysteria2 != nil, user.VMess != nil,
		user.Trojan != nil, user.Shadowsocks != nil,
	} {
		if configured {
			count++
		}
	}
	return count
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func validateAccessProfile(profile AccessProfile, nodeID string) error {
	var validationErrors []error
	if _, err := uuid.Parse(profile.ID); err != nil {
		validationErrors = append(validationErrors, errors.New("ID must be a UUID"))
	}
	if profile.NodeID != nodeID {
		validationErrors = append(validationErrors, errors.New("node ID does not match the parent node"))
	}
	if err := validateName("name", profile.Name); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if profile.PublicHost != "" {
		if err := validateHost("public host", profile.PublicHost, true); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}
	if profile.PublicPort == 0 {
		validationErrors = append(validationErrors, errors.New("public port must be between 1 and 65535"))
	}
	if profile.ServerName != "" {
		if err := validateHost("server name", profile.ServerName, false); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}
	return errors.Join(validationErrors...)
}

func validateVLESS(spec VLESSSpec) error {
	var validationErrors []error
	if spec.Decryption == "" {
		spec.Decryption = "none"
	}
	switch spec.Handler.Type {
	case VLESSHandlerRaw:
		if spec.Handler.WebSocket != nil || spec.Handler.GRPC != nil || spec.Handler.XHTTP != nil || spec.Handler.MKCP != nil {
			validationErrors = append(validationErrors, errors.New("raw VLESS handler must not include another handler configuration"))
		}
	case VLESSHandlerWebSocket:
		if spec.Handler.WebSocket == nil || spec.Handler.GRPC != nil || spec.Handler.XHTTP != nil || spec.Handler.MKCP != nil {
			validationErrors = append(validationErrors, errors.New("WebSocket handler configuration is invalid"))
		} else if !strings.HasPrefix(spec.Handler.WebSocket.Path, "/") {
			validationErrors = append(validationErrors, errors.New("WebSocket path must start with /"))
		}
	case VLESSHandlerGRPC:
		if spec.Handler.GRPC == nil || spec.Handler.WebSocket != nil || spec.Handler.XHTTP != nil || spec.Handler.MKCP != nil || strings.TrimSpace(spec.Handler.GRPC.ServiceName) == "" {
			validationErrors = append(validationErrors, errors.New("gRPC service name is required and no other handler may be configured"))
		}
	case VLESSHandlerXHTTP:
		if spec.Handler.XHTTP == nil || spec.Handler.WebSocket != nil || spec.Handler.GRPC != nil || spec.Handler.MKCP != nil {
			validationErrors = append(validationErrors, errors.New("XHTTP handler configuration is invalid"))
		} else if spec.Handler.XHTTP.Path != "" && !strings.HasPrefix(spec.Handler.XHTTP.Path, "/") {
			validationErrors = append(validationErrors, errors.New("XHTTP path must start with /"))
		}
	default:
		validationErrors = append(validationErrors, fmt.Errorf("unsupported VLESS handler %q", spec.Handler.Type))
	}

	securityCount := 0
	for _, configured := range []bool{spec.Security.TLS != nil, spec.Security.Reality != nil, spec.Security.ShadowTLS != nil, spec.Security.ResTLS != nil, spec.Security.JLS != nil} {
		if configured {
			securityCount++
		}
	}
	switch spec.Security.Type {
	case VLESSSecurityNone:
		if securityCount != 0 {
			validationErrors = append(validationErrors, errors.New("none security must not include a security configuration"))
		}
	case VLESSSecurityTLS:
		if securityCount != 1 || spec.Security.TLS == nil {
			validationErrors = append(validationErrors, errors.New("TLS security must contain only TLS configuration"))
		} else if strings.TrimSpace(spec.Security.TLS.Certificate) == "" || strings.TrimSpace(spec.Security.TLS.PrivateKey) == "" {
			validationErrors = append(validationErrors, errors.New("TLS certificate and private key are required"))
		}
	case VLESSSecurityReality:
		if securityCount != 1 || spec.Security.Reality == nil {
			validationErrors = append(validationErrors, errors.New("REALITY security must contain only REALITY configuration"))
		} else if err := validateReality(*spec.Security.Reality); err != nil {
			validationErrors = append(validationErrors, err)
		}
	case VLESSSecurityShadowTLS:
		if securityCount != 1 || spec.Security.ShadowTLS == nil {
			validationErrors = append(validationErrors, errors.New("ShadowTLS security must contain only ShadowTLS configuration"))
		} else if err := validateShadowTLS(*spec.Security.ShadowTLS); err != nil {
			validationErrors = append(validationErrors, err)
		}
	case VLESSSecurityResTLS:
		if securityCount != 1 || spec.Security.ResTLS == nil {
			validationErrors = append(validationErrors, errors.New("ResTLS security must contain only ResTLS configuration"))
		} else if strings.TrimSpace(spec.Security.ResTLS.Destination) == "" || strings.TrimSpace(spec.Security.ResTLS.Password) == "" {
			validationErrors = append(validationErrors, errors.New("ResTLS destination and password are required"))
		} else if spec.Security.ResTLS.VersionHint != "" && spec.Security.ResTLS.VersionHint != "tls12" && spec.Security.ResTLS.VersionHint != "tls13" {
			validationErrors = append(validationErrors, errors.New("ResTLS version hint must be tls12 or tls13"))
		}
	case VLESSSecurityJLS:
		if securityCount != 1 || spec.Security.JLS == nil {
			validationErrors = append(validationErrors, errors.New("JLS security must contain only JLS configuration"))
		} else if err := validateJLS(*spec.Security.JLS); err != nil {
			validationErrors = append(validationErrors, err)
		}
	default:
		validationErrors = append(validationErrors, fmt.Errorf("unsupported VLESS security %q", spec.Security.Type))
	}
	if spec.Mux.Brutal.Enabled && (strings.TrimSpace(spec.Mux.Brutal.Up) == "" || strings.TrimSpace(spec.Mux.Brutal.Down) == "") {
		validationErrors = append(validationErrors, errors.New("Brutal mux requires up and down bandwidth"))
	}
	return errors.Join(validationErrors...)
}

func validateVMess(spec VMessSpec) error {
	var validationErrors []error
	if err := validateClassicStreamHandler("VMess", spec.Handler); err != nil {
		validationErrors = append(validationErrors, err)
	}
	// The Meta listener exposes the same TLS/REALITY/ShadowTLS/ResTLS/JLS
	// security structures as VLESS. Reuse their strict validation contract.
	if err := validateVLESS(VLESSSpec{
		Handler:  VLESSHandlerSpec{Type: VLESSHandlerRaw},
		Security: spec.Security,
		Mux:      spec.Mux,
	}); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if spec.Handler.Type == VMessHandlerMKCP &&
		oneOf(string(spec.Security.Type), string(VLESSSecurityShadowTLS), string(VLESSSecurityResTLS), string(VLESSSecurityJLS)) {
		validationErrors = append(validationErrors, errors.New("VMess mKCP does not support ShadowTLS, ResTLS, or JLS security"))
	}
	return errors.Join(validationErrors...)
}

func validateTrojan(spec TrojanSpec) error {
	var validationErrors []error
	if err := validateClassicStreamHandler("Trojan", spec.Handler); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if err := validateVLESS(VLESSSpec{
		Handler:  VLESSHandlerSpec{Type: VLESSHandlerRaw},
		Security: spec.Security,
		Mux:      spec.Mux,
	}); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if spec.Shadowsocks.Enabled {
		if !oneOf(strings.ToLower(spec.Shadowsocks.Method), "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305") {
			validationErrors = append(validationErrors, errors.New("Trojan Shadowsocks method must be aes-128-gcm, aes-256-gcm, or chacha20-ietf-poly1305"))
		}
		if strings.TrimSpace(spec.Shadowsocks.Password) == "" {
			validationErrors = append(validationErrors, errors.New("Trojan Shadowsocks password is required when enabled"))
		}
	}
	return errors.Join(validationErrors...)
}

func validateClassicStreamHandler(protocol string, handler VLESSHandlerSpec) error {
	switch handler.Type {
	case VLESSHandlerRaw:
		if handler.WebSocket != nil || handler.GRPC != nil || handler.XHTTP != nil || handler.MKCP != nil {
			return fmt.Errorf("raw %s handler must not include another handler configuration", protocol)
		}
	case VLESSHandlerWebSocket:
		if handler.WebSocket == nil || handler.GRPC != nil || handler.XHTTP != nil || handler.MKCP != nil ||
			!strings.HasPrefix(handler.WebSocket.Path, "/") {
			return fmt.Errorf("%s WebSocket handler requires a path starting with / and no other handler configuration", protocol)
		}
	case VLESSHandlerGRPC:
		if handler.GRPC == nil || handler.WebSocket != nil || handler.XHTTP != nil || handler.MKCP != nil ||
			strings.TrimSpace(handler.GRPC.ServiceName) == "" {
			return fmt.Errorf("%s gRPC handler requires a service name and no other handler configuration", protocol)
		}
	case VMessHandlerMKCP:
		if protocol != "VMess" || handler.MKCP == nil || handler.WebSocket != nil || handler.GRPC != nil || handler.XHTTP != nil {
			return fmt.Errorf("%s mKCP handler configuration is invalid", protocol)
		}
		if !oneOf(handler.MKCP.Header, "", "none", "srtp", "utp", "wechat-video", "dtls", "wireguard") {
			return errors.New("VMess mKCP header is unsupported")
		}
	default:
		return fmt.Errorf("unsupported %s handler %q", protocol, handler.Type)
	}
	return nil
}

func validateShadowsocks(spec ShadowsocksSpec) error {
	var validationErrors []error
	if !oneOf(spec.Cipher,
		"none",
		"aes-128-gcm", "aes-192-gcm", "aes-256-gcm",
		"chacha20-ietf-poly1305", "xchacha20-ietf-poly1305",
		"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm",
		"2022-blake3-chacha20-poly1305",
	) {
		validationErrors = append(validationErrors, fmt.Errorf("unsupported Shadowsocks cipher %q", spec.Cipher))
	}
	if !oneOf(string(spec.Security.Type),
		string(VLESSSecurityNone), string(VLESSSecurityShadowTLS),
		string(VLESSSecurityResTLS), string(VLESSSecurityJLS),
	) {
		validationErrors = append(validationErrors, fmt.Errorf("unsupported Shadowsocks security %q", spec.Security.Type))
	} else if err := validateVLESS(VLESSSpec{
		Handler: VLESSHandlerSpec{Type: VLESSHandlerRaw}, Security: spec.Security, Mux: spec.Mux,
	}); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if spec.SimpleObfs.Enabled && !oneOf(spec.SimpleObfs.Mode, "http", "tls") {
		validationErrors = append(validationErrors, errors.New("Shadowsocks simple obfs mode must be http or tls"))
	}
	if spec.SimpleObfs.Enabled && spec.Security.Type != VLESSSecurityNone {
		validationErrors = append(validationErrors, errors.New("Shadowsocks simple obfs cannot be combined with another Mihomo client plugin security"))
	}
	return errors.Join(validationErrors...)
}

func validateShadowTLS(config ShadowTLSConfig) error {
	var validationErrors []error
	if config.Version < 1 || config.Version > 3 {
		validationErrors = append(validationErrors, errors.New("ShadowTLS version must be 1 to 3"))
	}
	if config.Version == 2 && strings.TrimSpace(config.Password) == "" {
		validationErrors = append(validationErrors, errors.New("ShadowTLS version 2 password is required"))
	}
	if config.Version == 3 && len(config.Users) == 0 {
		validationErrors = append(validationErrors, errors.New("ShadowTLS version 3 requires at least one user"))
	}
	if strings.TrimSpace(config.Handshake.Destination) == "" &&
		(config.Version < 3 || (config.WildcardSNI != "authed" && config.WildcardSNI != "all")) {
		validationErrors = append(validationErrors, errors.New("ShadowTLS handshake destination is required"))
	} else if config.Handshake.Destination != "" {
		if err := validateDestination(config.Handshake.Destination); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("ShadowTLS handshake %w", err))
		}
	}
	if config.WildcardSNI != "" && config.WildcardSNI != "off" && config.WildcardSNI != "authed" && config.WildcardSNI != "all" {
		validationErrors = append(validationErrors, errors.New("ShadowTLS wildcard SNI must be off, authed, or all"))
	}
	userNames := make(map[string]struct{}, len(config.Users))
	for index, user := range config.Users {
		if err := validateName("ShadowTLS user name", user.Name); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("ShadowTLS user %d: %w", index+1, err))
		}
		if strings.TrimSpace(user.Password) == "" {
			validationErrors = append(validationErrors, fmt.Errorf("ShadowTLS user %d password is required", index+1))
		}
		if _, exists := userNames[user.Name]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("ShadowTLS user %q is duplicated", user.Name))
		}
		userNames[user.Name] = struct{}{}
	}
	for serverName, handshake := range config.HandshakeForServerName {
		if strings.TrimSpace(serverName) == "" || serverName != strings.TrimSpace(serverName) {
			validationErrors = append(validationErrors, errors.New("ShadowTLS handshake server name is required and must not have surrounding whitespace"))
		}
		if err := validateDestination(handshake.Destination); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("ShadowTLS handshake for %q %w", serverName, err))
		}
	}
	return errors.Join(validationErrors...)
}

func validateJLS(config JLSConfig) error {
	var validationErrors []error
	if err := validateDestination(config.Destination); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("JLS %w", err))
	}
	if len(config.Users) == 0 {
		validationErrors = append(validationErrors, errors.New("JLS requires at least one user"))
	}
	userNames := make(map[string]struct{}, len(config.Users))
	for index, user := range config.Users {
		if err := validateName("JLS username", user.Username); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("JLS user %d: %w", index+1, err))
		}
		if strings.TrimSpace(user.Password) == "" {
			validationErrors = append(validationErrors, fmt.Errorf("JLS user %d password is required", index+1))
		}
		if _, exists := userNames[user.Username]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("JLS user %q is duplicated", user.Username))
		}
		userNames[user.Username] = struct{}{}
	}
	if err := validateStringList("JLS ALPN", config.ALPN); err != nil {
		validationErrors = append(validationErrors, err)
	}
	return errors.Join(validationErrors...)
}

func validateReality(config RealityConfig) error {
	var validationErrors []error
	if err := validateDestination(config.Destination); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if err := ValidateRealityKey(config.PrivateKey); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("REALITY private key %w", err))
	}
	if err := ValidateRealityKey(config.PublicKey); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("REALITY public key %w", err))
	}
	if len(config.ShortIDs) == 0 || len(config.ServerNames) == 0 {
		validationErrors = append(validationErrors, errors.New("REALITY requires at least one short ID and server name"))
	}
	for _, shortID := range config.ShortIDs {
		if err := ValidateShortID(shortID); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("short ID %q %w", shortID, err))
		}
	}
	for _, serverName := range config.ServerNames {
		if err := validateHost("REALITY server name", serverName, false); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}
	return errors.Join(validationErrors...)
}

func validateHysteria2(spec Hysteria2Spec) error {
	var validationErrors []error
	if strings.TrimSpace(spec.Certificate) == "" || strings.TrimSpace(spec.PrivateKey) == "" {
		validationErrors = append(validationErrors, errors.New("Hysteria2 certificate and private key are required"))
	}
	if spec.Obfs != "" && strings.TrimSpace(spec.ObfsPassword) == "" {
		validationErrors = append(validationErrors, errors.New("Hysteria2 obfuscation password is required when obfuscation is enabled"))
	}
	if spec.ObfsMinPacketSize < 0 || spec.ObfsMaxPacketSize < 0 ||
		(spec.ObfsMaxPacketSize > 0 && spec.ObfsMinPacketSize > spec.ObfsMaxPacketSize) {
		validationErrors = append(validationErrors, errors.New("Hysteria2 obfuscation packet size range is invalid"))
	}
	if spec.UDPMTU < 0 || spec.CWND < 0 || spec.MaxIdleTime < 0 {
		validationErrors = append(validationErrors, errors.New("Hysteria2 numeric tuning values must not be negative"))
	}
	if spec.Mux.Brutal.Enabled && (strings.TrimSpace(spec.Mux.Brutal.Up) == "" || strings.TrimSpace(spec.Mux.Brutal.Down) == "") {
		validationErrors = append(validationErrors, errors.New("Hysteria2 Brutal mux requires up and down bandwidth"))
	}
	if err := validateStringList("Hysteria2 ALPN", spec.ALPN); err != nil {
		validationErrors = append(validationErrors, err)
	}
	if spec.Realm != nil && spec.Realm.Enabled {
		if err := validateHysteria2Realm(*spec.Realm); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}
	return errors.Join(validationErrors...)
}

func validateHysteria2Realm(config Hysteria2RealmConfig) error {
	var validationErrors []error
	parsed, err := url.Parse(config.ServerURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		validationErrors = append(validationErrors, errors.New("Hysteria2 Realm server URL must be an absolute HTTP or HTTPS URL"))
	}
	if strings.TrimSpace(config.RealmID) == "" {
		validationErrors = append(validationErrors, errors.New("Hysteria2 Realm ID is required"))
	}
	if len(config.STUNServers) == 0 {
		validationErrors = append(validationErrors, errors.New("Hysteria2 Realm requires at least one STUN server"))
	}
	for _, server := range config.STUNServers {
		if err := validateSTUNServer(server); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("Hysteria2 Realm STUN server %q %w", server, err))
		}
	}
	if (config.Certificate == "") != (config.PrivateKey == "") {
		validationErrors = append(validationErrors, errors.New("Hysteria2 Realm client certificate and private key must be configured together"))
	}
	if err := validateStringList("Hysteria2 Realm ALPN", config.ALPN); err != nil {
		validationErrors = append(validationErrors, err)
	}
	return errors.Join(validationErrors...)
}

func validateSTUNServer(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		host = value
		port = "3478"
	}
	if err := validateHost("host", host, true); err != nil {
		return err
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort == 0 {
		return errors.New("port must be between 1 and 65535")
	}
	return nil
}

func validateStringList(field string, values []string) error {
	var validationErrors []error
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			validationErrors = append(validationErrors, fmt.Errorf("%s item %d is required and must not have surrounding whitespace", field, index+1))
		}
		if _, exists := seen[value]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("%s value %q is duplicated", field, value))
		}
		seen[value] = struct{}{}
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

func ValidateControllerEndpointPair(bind, connect Endpoint) error {
	if bind.Port != connect.Port {
		return errors.New("mihomo external-controller bind and m-ui Controller connect ports must match")
	}
	if connect.Host != "127.0.0.1" && connect.Host != "::1" {
		return errors.New("m-ui Mihomo Controller connect host must be exactly 127.0.0.1 or ::1")
	}
	switch bind.Host {
	case "127.0.0.1", "0.0.0.0":
		if connect.Host != "127.0.0.1" {
			return fmt.Errorf("mihomo IPv4 bind %s requires m-ui Controller connect host 127.0.0.1", bind.Host)
		}
	case "::1", "::":
		if connect.Host != "::1" {
			return fmt.Errorf("mihomo IPv6 bind %s requires m-ui Controller connect host ::1", bind.Host)
		}
	default:
		return fmt.Errorf("mihomo external-controller bind host %s is not supported", bind.Host)
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
		return errors.New("destination must use host:port syntax")
	}
	if err := validateHost("destination host", host, true); err != nil {
		return err
	}
	parsedPort, err := net.LookupPort("tcp", port)
	if err != nil || parsedPort == 0 {
		return errors.New("destination port must be between 1 and 65535")
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
