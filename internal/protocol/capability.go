package protocol

import (
	"encoding/json"
	"fmt"

	"github.com/Aethersailor/m-ui/internal/domain"
)

const CapabilitySchemaVersion = 4

type SourceContract struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Commit     string `json:"commit"`
}

type FieldType string

const (
	FieldString     FieldType = "string"
	FieldText       FieldType = "text"
	FieldSecret     FieldType = "secret"
	FieldBoolean    FieldType = "boolean"
	FieldInteger    FieldType = "integer"
	FieldStringList FieldType = "string-list"
	FieldObjectList FieldType = "object-list"
	FieldRecord     FieldType = "record"
)

type FieldCapability struct {
	Path         string            `json:"path"`
	SourceKey    string            `json:"source_key,omitempty"`
	Label        string            `json:"label"`
	Type         FieldType         `json:"type"`
	Required     bool              `json:"required,omitempty"`
	Secret       bool              `json:"secret,omitempty"`
	Advanced     bool              `json:"advanced,omitempty"`
	Minimum      *int64            `json:"minimum,omitempty"`
	Maximum      *int64            `json:"maximum,omitempty"`
	Options      []string          `json:"options,omitempty"`
	ItemFields   []FieldCapability `json:"item_fields,omitempty"`
	ItemKeyLabel string            `json:"item_key_label,omitempty"`
	VisibleWhen  *FieldCondition   `json:"visible_when,omitempty"`
	Description  string            `json:"description,omitempty"`
}

type FieldCondition struct {
	Path   string `json:"path"`
	Equals any    `json:"equals"`
}

type ComponentGroup string

const (
	ComponentTransport ComponentGroup = "transport"
	ComponentSecurity  ComponentGroup = "security"
	ComponentExtension ComponentGroup = "extension"
)

type LayerCapability struct {
	Group            ComponentGroup `json:"group"`
	Required         bool           `json:"required"`
	Multiple         bool           `json:"multiple"`
	Locked           bool           `json:"locked,omitempty"`
	DefaultComponent string         `json:"default_component,omitempty"`
}

type ComponentCapability struct {
	Group         ComponentGroup    `json:"group"`
	Kind          string            `json:"kind"`
	Label         string            `json:"label"`
	DefaultConfig json.RawMessage   `json:"default_config"`
	ConfigPath    string            `json:"config_path"`
	SelectionPath string            `json:"selection_path,omitempty"`
	EnabledPath   string            `json:"enabled_path,omitempty"`
	Fields        []FieldCapability `json:"fields,omitempty"`
	Requires      []string          `json:"requires,omitempty"`
	Conflicts     []string          `json:"conflicts,omitempty"`
}

type ProtocolCapability struct {
	Kind        domain.ProtocolKind   `json:"kind"`
	Label       string                `json:"label"`
	DefaultNode json.RawMessage       `json:"default_node"`
	DefaultUser json.RawMessage       `json:"default_user"`
	Layers      []LayerCapability     `json:"layers"`
	Components  []ComponentCapability `json:"components"`
	Fields      []FieldCapability     `json:"fields,omitempty"`
	UserFields  []FieldCapability     `json:"user_fields"`
	Features    []string              `json:"features,omitempty"`
}

type CapabilityManifest struct {
	SchemaVersion       int                  `json:"schema_version"`
	NodeSchemaVersion   int                  `json:"node_schema_version"`
	Source              SourceContract       `json:"source"`
	NodeFields          []FieldCapability    `json:"node_fields"`
	AccessProfileFields []FieldCapability    `json:"access_profile_fields"`
	Protocols           []ProtocolCapability `json:"protocols"`
}

func rawDefault(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("encode protocol capability default: %v", err))
	}
	return encoded
}

func integerBounds(minimum, maximum int64) (*int64, *int64) {
	return &minimum, &maximum
}

func visibleWhen(path string, equals any) *FieldCondition {
	return &FieldCondition{Path: path, Equals: equals}
}

func commonNodeFields() []FieldCapability {
	return []FieldCapability{
		{Path: "name", Label: "Name", Type: FieldString, Required: true},
		{Path: "enabled", Label: "Enabled", Type: FieldBoolean},
		{Path: "listen", SourceKey: "listen", Label: "Listen address", Type: FieldString, Required: true},
		{Path: "port", SourceKey: "port", Label: "Port or port range", Type: FieldString, Required: true},
	}
}

func accessProfileFields() []FieldCapability {
	portMin, portMax := integerBounds(1, 65535)
	return []FieldCapability{
		{Path: "name", Label: "Profile name", Type: FieldString, Required: true},
		{Path: "default", Label: "Default profile", Type: FieldBoolean},
		{Path: "public_host", Label: "Public host", Type: FieldString},
		{Path: "public_port", Label: "Public port", Type: FieldInteger, Required: true, Minimum: portMin, Maximum: portMax},
		{Path: "server_name", SourceKey: "servername", Label: "Server name", Type: FieldString},
		{Path: "fingerprint", SourceKey: "client-fingerprint", Label: "Client fingerprint", Type: FieldString, Advanced: true},
		{Path: "packet_encoding", SourceKey: "packet-encoding", Label: "Packet encoding", Type: FieldString, Advanced: true},
		{Path: "allow_insecure", SourceKey: "skip-cert-verify", Label: "Allow insecure", Type: FieldBoolean, Advanced: true},
	}
}

func vlessCapability() ProtocolCapability {
	zero, one, three := int64(0), int64(1), int64(3)
	return ProtocolCapability{
		Kind:  domain.ProtocolVLESS,
		Label: "VLESS",
		DefaultNode: rawDefault(map[string]any{"vless": domain.VLESSSpec{
			Decryption: "none",
			Handler:    domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
			Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityReality, Reality: &domain.RealityConfig{
				ShortIDs: []string{}, ServerNames: []string{},
			}},
			Mux: domain.MuxSpec{},
		}}),
		Layers: []LayerCapability{
			{Group: ComponentTransport, Required: true, DefaultComponent: string(domain.VLESSHandlerRaw)},
			{Group: ComponentSecurity, Required: true, DefaultComponent: string(domain.VLESSSecurityReality)},
		},
		Components: []ComponentCapability{
			{Group: ComponentTransport, Kind: string(domain.VLESSHandlerRaw), Label: "Raw TCP", DefaultConfig: rawDefault(domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw})},
			{Group: ComponentTransport, Kind: string(domain.VLESSHandlerWebSocket), Label: "WebSocket", DefaultConfig: rawDefault(domain.VLESSHandlerSpec{Type: domain.VLESSHandlerWebSocket, WebSocket: &domain.WebSocketSpec{Path: "/"}}), Fields: []FieldCapability{
				{Path: "websocket.path", SourceKey: "ws-path", Label: "WebSocket path", Type: FieldString, Required: true},
			}},
			{Group: ComponentTransport, Kind: string(domain.VLESSHandlerGRPC), Label: "gRPC", DefaultConfig: rawDefault(domain.VLESSHandlerSpec{Type: domain.VLESSHandlerGRPC, GRPC: &domain.GRPCSpec{ServiceName: ""}}), Fields: []FieldCapability{
				{Path: "grpc.service_name", SourceKey: "grpc-service-name", Label: "gRPC service name", Type: FieldString, Required: true},
			}},
			{Group: ComponentTransport, Kind: string(domain.VLESSHandlerXHTTP), Label: "XHTTP", DefaultConfig: rawDefault(domain.VLESSHandlerSpec{Type: domain.VLESSHandlerXHTTP, XHTTP: &domain.XHTTPConfig{Path: "/", Mode: "auto"}}), Fields: xhttpFields()},
			{Group: ComponentSecurity, Kind: string(domain.VLESSSecurityNone), Label: "None", DefaultConfig: rawDefault(domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone})},
			{Group: ComponentSecurity, Kind: string(domain.VLESSSecurityTLS), Label: "TLS", DefaultConfig: rawDefault(domain.VLESSSecuritySpec{Type: domain.VLESSSecurityTLS, TLS: &domain.TLSConfig{}}), Fields: vlessTLSFields()},
			{Group: ComponentSecurity, Kind: string(domain.VLESSSecurityReality), Label: "REALITY", DefaultConfig: rawDefault(domain.VLESSSecuritySpec{Type: domain.VLESSSecurityReality, Reality: &domain.RealityConfig{ShortIDs: []string{}, ServerNames: []string{}}}), Fields: realityFields()},
			{Group: ComponentSecurity, Kind: string(domain.VLESSSecurityShadowTLS), Label: "ShadowTLS", DefaultConfig: rawDefault(domain.VLESSSecuritySpec{Type: domain.VLESSSecurityShadowTLS, ShadowTLS: &domain.ShadowTLSConfig{Version: 3, Users: []domain.ShadowTLSUser{{}}, Handshake: domain.ShadowTLSHandshake{}, HandshakeForServerName: map[string]domain.ShadowTLSHandshake{}, WildcardSNI: "off"}}), Fields: []FieldCapability{
				{Path: "shadow_tls.version", SourceKey: "shadow-tls.version", Label: "Version", Type: FieldInteger, Required: true, Minimum: &one, Maximum: &three, Options: []string{"1", "2", "3"}},
				{Path: "shadow_tls.password", SourceKey: "shadow-tls.password", Label: "Version 2 password", Type: FieldSecret, Secret: true, VisibleWhen: visibleWhen("shadow_tls.version", 2)},
				{Path: "shadow_tls.users", SourceKey: "shadow-tls.users", Label: "Version 3 users", Type: FieldObjectList, Required: true, VisibleWhen: visibleWhen("shadow_tls.version", 3), ItemFields: []FieldCapability{{Path: "name", Label: "Name", Type: FieldString, Required: true}, {Path: "password", Label: "Password", Type: FieldSecret, Required: true, Secret: true}}},
				{Path: "shadow_tls.handshake.destination", SourceKey: "shadow-tls.handshake.dest", Label: "Handshake destination", Type: FieldString},
				{Path: "shadow_tls.handshake.proxy", SourceKey: "shadow-tls.handshake.proxy", Label: "Handshake proxy", Type: FieldString, Advanced: true},
				{Path: "shadow_tls.handshake_for_server_name", SourceKey: "shadow-tls.handshake-for-server-name", Label: "SNI handshakes", Type: FieldRecord, Advanced: true, ItemKeyLabel: "Server name", ItemFields: []FieldCapability{{Path: "destination", Label: "Destination", Type: FieldString, Required: true}, {Path: "proxy", Label: "Proxy", Type: FieldString}}},
				{Path: "shadow_tls.strict_mode", SourceKey: "shadow-tls.strict-mode", Label: "Strict mode", Type: FieldBoolean, Advanced: true},
				{Path: "shadow_tls.wildcard_sni", SourceKey: "shadow-tls.wildcard-sni", Label: "Wildcard SNI", Type: FieldString, Options: []string{"off", "authed", "all"}},
			}},
			{Group: ComponentSecurity, Kind: string(domain.VLESSSecurityResTLS), Label: "ResTLS", DefaultConfig: rawDefault(domain.VLESSSecuritySpec{Type: domain.VLESSSecurityResTLS, ResTLS: &domain.ResTLSConfig{VersionHint: "tls13"}}), Fields: []FieldCapability{
				{Path: "res_tls.destination", SourceKey: "res-tls.dest", Label: "Destination", Type: FieldString, Required: true},
				{Path: "res_tls.password", SourceKey: "res-tls.password", Label: "Password", Type: FieldSecret, Required: true, Secret: true},
				{Path: "res_tls.version_hint", SourceKey: "client.restls-opts.version-hint", Label: "Client TLS version hint", Type: FieldString, Required: true, Options: []string{"tls13", "tls12"}},
				{Path: "res_tls.script", SourceKey: "res-tls.restls-script", Label: "Script", Type: FieldText, Advanced: true},
				{Path: "res_tls.min_record_length", SourceKey: "res-tls.min-record-len", Label: "Minimum record length", Type: FieldInteger, Advanced: true, Minimum: &zero},
				{Path: "res_tls.proxy", SourceKey: "res-tls.proxy", Label: "Proxy", Type: FieldString, Advanced: true},
			}},
			{Group: ComponentSecurity, Kind: string(domain.VLESSSecurityJLS), Label: "JLS", DefaultConfig: rawDefault(domain.VLESSSecuritySpec{Type: domain.VLESSSecurityJLS, JLS: &domain.JLSConfig{Users: []domain.JLSUser{{}}, ALPN: []string{}}}), Fields: []FieldCapability{
				{Path: "jls.destination", SourceKey: "jls-config.dest", Label: "Destination", Type: FieldString, Required: true},
				{Path: "jls.server_name", SourceKey: "jls-config.sni", Label: "Server name", Type: FieldString},
				{Path: "jls.users", SourceKey: "jls-config.users", Label: "Users", Type: FieldObjectList, Required: true, ItemFields: []FieldCapability{{Path: "username", Label: "Username", Type: FieldString, Required: true}, {Path: "password", Label: "Password", Type: FieldSecret, Required: true, Secret: true}}},
				{Path: "jls.alpn", SourceKey: "jls-config.alpn", Label: "ALPN", Type: FieldStringList, Advanced: true},
				{Path: "jls.proxy", SourceKey: "jls-config.proxy", Label: "Proxy", Type: FieldString, Advanced: true},
				{Path: "jls.rate_limit", SourceKey: "jls-config.rate-limit", Label: "Rate limit", Type: FieldInteger, Advanced: true, Minimum: &zero},
			}},
		},
		Fields: []FieldCapability{
			{Path: "vless.decryption", SourceKey: "decryption", Label: "Decryption", Type: FieldString, Required: true},
			{Path: "vless.mux.padding", SourceKey: "mux-option.padding", Label: "Mux padding", Type: FieldBoolean, Advanced: true},
			{Path: "vless.mux.brutal.enabled", SourceKey: "mux-option.brutal.enabled", Label: "Brutal mux", Type: FieldBoolean, Advanced: true},
			{Path: "vless.mux.brutal.up", SourceKey: "mux-option.brutal.up", Label: "Brutal upload", Type: FieldString, Advanced: true, VisibleWhen: visibleWhen("vless.mux.brutal.enabled", true)},
			{Path: "vless.mux.brutal.down", SourceKey: "mux-option.brutal.down", Label: "Brutal download", Type: FieldString, Advanced: true, VisibleWhen: visibleWhen("vless.mux.brutal.enabled", true)},
		},
		UserFields: []FieldCapability{
			{Path: "vless.uuid", SourceKey: "users.uuid", Label: "UUID", Type: FieldString, Required: true, Secret: true},
			{Path: "vless.flow", SourceKey: "users.flow", Label: "Flow", Type: FieldString, Options: []string{"", domain.VLESSFlowVision}},
		},
		Features: []string{"decryption", "mux", "brutal", "multiple-server-names", "multiple-short-ids"},
	}
}

func hysteria2Capability() ProtocolCapability {
	zero := int64(0)
	return ProtocolCapability{
		Kind:  domain.ProtocolHysteria2,
		Label: "Hysteria2",
		DefaultNode: rawDefault(map[string]any{"hysteria2": domain.Hysteria2Spec{
			ALPN: []string{"h3"}, Mux: domain.MuxSpec{},
			Realm: &domain.Hysteria2RealmConfig{STUNServers: []string{}, ALPN: []string{}},
		}}),
		Layers: []LayerCapability{
			{Group: ComponentTransport, Required: true, Locked: true, DefaultComponent: "quic"},
			{Group: ComponentSecurity, Required: true, Locked: true, DefaultComponent: "tls"},
			{Group: ComponentExtension, Multiple: true},
		},
		Components: []ComponentCapability{
			{Group: ComponentTransport, Kind: "quic", Label: "QUIC", DefaultConfig: rawDefault(map[string]any{}), Fields: []FieldCapability{
				{Path: "hysteria2.obfs", SourceKey: "obfs", Label: "Obfuscation", Type: FieldString},
				{Path: "hysteria2.obfs_password", SourceKey: "obfs-password", Label: "Obfuscation password", Type: FieldSecret, Secret: true},
				{Path: "hysteria2.obfs_min_packet_size", SourceKey: "obfs-min-packet-size", Label: "Minimum packet size", Type: FieldInteger, Advanced: true, Minimum: &zero},
				{Path: "hysteria2.obfs_max_packet_size", SourceKey: "obfs-max-packet-size", Label: "Maximum packet size", Type: FieldInteger, Advanced: true, Minimum: &zero},
				{Path: "hysteria2.max_idle_time", SourceKey: "max-idle-time", Label: "Maximum idle time", Type: FieldInteger, Advanced: true, Minimum: &zero},
				{Path: "hysteria2.up", SourceKey: "up", Label: "Upload bandwidth", Type: FieldString},
				{Path: "hysteria2.down", SourceKey: "down", Label: "Download bandwidth", Type: FieldString},
				{Path: "hysteria2.ignore_client_bandwidth", SourceKey: "ignore-client-bandwidth", Label: "Ignore client bandwidth", Type: FieldBoolean, Advanced: true},
				{Path: "hysteria2.masquerade", SourceKey: "masquerade", Label: "Masquerade", Type: FieldString},
				{Path: "hysteria2.cwnd", SourceKey: "cwnd", Label: "CWND", Type: FieldInteger, Advanced: true, Minimum: &zero},
				{Path: "hysteria2.bbr_profile", SourceKey: "bbr-profile", Label: "BBR profile", Type: FieldString, Advanced: true},
				{Path: "hysteria2.udp_mtu", SourceKey: "udp-mtu", Label: "UDP MTU", Type: FieldInteger, Advanced: true, Minimum: &zero},
				{Path: "hysteria2.mux.padding", SourceKey: "mux-option.padding", Label: "Mux padding", Type: FieldBoolean, Advanced: true},
				{Path: "hysteria2.mux.brutal.enabled", SourceKey: "mux-option.brutal.enabled", Label: "Brutal mux", Type: FieldBoolean, Advanced: true},
				{Path: "hysteria2.mux.brutal.up", SourceKey: "mux-option.brutal.up", Label: "Brutal upload", Type: FieldString, Advanced: true, VisibleWhen: visibleWhen("hysteria2.mux.brutal.enabled", true)},
				{Path: "hysteria2.mux.brutal.down", SourceKey: "mux-option.brutal.down", Label: "Brutal download", Type: FieldString, Advanced: true, VisibleWhen: visibleWhen("hysteria2.mux.brutal.enabled", true)},
				{Path: "hysteria2.initial_stream_receive_window", SourceKey: "initial-stream-receive-window", Label: "Initial stream receive window", Type: FieldInteger, Advanced: true, Minimum: &zero},
				{Path: "hysteria2.max_stream_receive_window", SourceKey: "max-stream-receive-window", Label: "Maximum stream receive window", Type: FieldInteger, Advanced: true, Minimum: &zero},
				{Path: "hysteria2.initial_connection_receive_window", SourceKey: "initial-connection-receive-window", Label: "Initial connection receive window", Type: FieldInteger, Advanced: true, Minimum: &zero},
				{Path: "hysteria2.max_connection_receive_window", SourceKey: "max-connection-receive-window", Label: "Maximum connection receive window", Type: FieldInteger, Advanced: true, Minimum: &zero},
			}},
			{Group: ComponentSecurity, Kind: "tls", Label: "TLS", DefaultConfig: rawDefault(map[string]any{}), Fields: hysteria2TLSFields()},
			{Group: ComponentExtension, Kind: "realm", Label: "Hysteria2 Realm", DefaultConfig: rawDefault(domain.Hysteria2RealmConfig{STUNServers: []string{}, ALPN: []string{}}), Requires: []string{"transport:quic", "security:tls"}, Fields: []FieldCapability{
				{Path: "hysteria2.realm.enabled", SourceKey: "realm-opts.enable", Label: "Enabled", Type: FieldBoolean},
				{Path: "hysteria2.realm.server_url", SourceKey: "realm-opts.server-url", Label: "Server URL", Type: FieldString, Required: true, VisibleWhen: visibleWhen("hysteria2.realm.enabled", true)},
				{Path: "hysteria2.realm.token", SourceKey: "realm-opts.token", Label: "Token", Type: FieldSecret, Secret: true, VisibleWhen: visibleWhen("hysteria2.realm.enabled", true)},
				{Path: "hysteria2.realm.realm_id", SourceKey: "realm-opts.realm-id", Label: "Realm ID", Type: FieldString, Required: true, VisibleWhen: visibleWhen("hysteria2.realm.enabled", true)},
				{Path: "hysteria2.realm.stun_servers", SourceKey: "realm-opts.stun-servers", Label: "STUN servers", Type: FieldStringList, Required: true, VisibleWhen: visibleWhen("hysteria2.realm.enabled", true)},
				{Path: "hysteria2.realm.server_name", SourceKey: "realm-opts.sni", Label: "Server name", Type: FieldString, VisibleWhen: visibleWhen("hysteria2.realm.enabled", true)},
				{Path: "hysteria2.realm.skip_cert_verify", SourceKey: "realm-opts.skip-cert-verify", Label: "Skip certificate verification", Type: FieldBoolean, Advanced: true, VisibleWhen: visibleWhen("hysteria2.realm.enabled", true)},
				{Path: "hysteria2.realm.name_cert_verify", SourceKey: "realm-opts.name-cert-verify", Label: "Certificate name verification", Type: FieldString, Advanced: true, VisibleWhen: visibleWhen("hysteria2.realm.enabled", true)},
				{Path: "hysteria2.realm.fingerprint", SourceKey: "realm-opts.fingerprint", Label: "Fingerprint", Type: FieldString, Advanced: true, VisibleWhen: visibleWhen("hysteria2.realm.enabled", true)},
				{Path: "hysteria2.realm.certificate", SourceKey: "realm-opts.certificate", Label: "Client certificate", Type: FieldText, Advanced: true, VisibleWhen: visibleWhen("hysteria2.realm.enabled", true)},
				{Path: "hysteria2.realm.private_key", SourceKey: "realm-opts.private-key", Label: "Client private key", Type: FieldSecret, Secret: true, Advanced: true, VisibleWhen: visibleWhen("hysteria2.realm.enabled", true)},
				{Path: "hysteria2.realm.alpn", SourceKey: "realm-opts.alpn", Label: "ALPN", Type: FieldStringList, Advanced: true, VisibleWhen: visibleWhen("hysteria2.realm.enabled", true)},
				{Path: "hysteria2.realm.proxy", SourceKey: "realm-opts.proxy", Label: "Proxy", Type: FieldString, Advanced: true, VisibleWhen: visibleWhen("hysteria2.realm.enabled", true)},
			}},
		},
		UserFields: []FieldCapability{{Path: "hysteria2.password", SourceKey: "users", Label: "Password", Type: FieldSecret, Required: true, Secret: true}},
		Features:   []string{"password-users", "obfs", "gecko-packet-size", "tls", "client-auth", "ech", "bandwidth", "masquerade", "cwnd", "bbr", "udp-mtu", "mux", "realm", "quic-windows"},
	}
}

func vlessTLSFields() []FieldCapability {
	return []FieldCapability{
		{Path: "tls.certificate", SourceKey: "certificate", Label: "Certificate", Type: FieldText, Required: true},
		{Path: "tls.private_key", SourceKey: "private-key", Label: "Private key", Type: FieldSecret, Required: true, Secret: true},
		{Path: "tls.client_auth_type", SourceKey: "client-auth-type", Label: "Client authentication type", Type: FieldString, Advanced: true},
		{Path: "tls.client_auth_cert", SourceKey: "client-auth-cert", Label: "Client authentication certificate", Type: FieldText, Advanced: true},
		{Path: "tls.ech_key", SourceKey: "ech-key", Label: "ECH key", Type: FieldSecret, Secret: true, Advanced: true},
		{Path: "tls.allow_insecure", SourceKey: "allow-insecure", Label: "Allow insecure", Type: FieldBoolean, Advanced: true},
	}
}

func hysteria2TLSFields() []FieldCapability {
	return []FieldCapability{
		{Path: "hysteria2.certificate", SourceKey: "certificate", Label: "Certificate", Type: FieldText, Required: true},
		{Path: "hysteria2.private_key", SourceKey: "private-key", Label: "Private key", Type: FieldSecret, Required: true, Secret: true},
		{Path: "hysteria2.client_auth_type", SourceKey: "client-auth-type", Label: "Client authentication type", Type: FieldString, Advanced: true},
		{Path: "hysteria2.client_auth_cert", SourceKey: "client-auth-cert", Label: "Client authentication certificate", Type: FieldText, Advanced: true},
		{Path: "hysteria2.ech_key", SourceKey: "ech-key", Label: "ECH key", Type: FieldSecret, Secret: true, Advanced: true},
		{Path: "hysteria2.alpn", SourceKey: "alpn", Label: "ALPN", Type: FieldStringList, Advanced: true},
	}
}

func realityFields() []FieldCapability {
	zero := int64(0)
	return []FieldCapability{
		{Path: "reality.destination", SourceKey: "reality-config.dest", Label: "Destination", Type: FieldString, Required: true},
		{Path: "reality.private_key", SourceKey: "reality-config.private-key", Label: "Private key", Type: FieldSecret, Secret: true},
		{Path: "reality.public_key", SourceKey: "client.reality-opts.public-key", Label: "Public key", Type: FieldString},
		{Path: "reality.short_ids", SourceKey: "reality-config.short-id", Label: "Short IDs", Type: FieldStringList, Required: true},
		{Path: "reality.server_names", SourceKey: "reality-config.server-names", Label: "Server names", Type: FieldStringList, Required: true},
		{Path: "reality.max_time_difference", SourceKey: "reality-config.max-time-difference", Label: "Maximum time difference", Type: FieldInteger, Advanced: true, Minimum: &zero},
		{Path: "reality.proxy", SourceKey: "reality-config.proxy", Label: "Fallback proxy", Type: FieldString, Advanced: true},
		{Path: "reality.limit_fallback_upload.after_bytes", SourceKey: "reality-config.limit-fallback-upload.after-bytes", Label: "Fallback upload after bytes", Type: FieldInteger, Advanced: true, Minimum: &zero},
		{Path: "reality.limit_fallback_upload.bytes_per_sec", SourceKey: "reality-config.limit-fallback-upload.bytes-per-sec", Label: "Fallback upload bytes per second", Type: FieldInteger, Advanced: true, Minimum: &zero},
		{Path: "reality.limit_fallback_upload.burst_bytes_per_sec", SourceKey: "reality-config.limit-fallback-upload.burst-bytes-per-sec", Label: "Fallback upload burst", Type: FieldInteger, Advanced: true, Minimum: &zero},
		{Path: "reality.limit_fallback_download.after_bytes", SourceKey: "reality-config.limit-fallback-download.after-bytes", Label: "Fallback download after bytes", Type: FieldInteger, Advanced: true, Minimum: &zero},
		{Path: "reality.limit_fallback_download.bytes_per_sec", SourceKey: "reality-config.limit-fallback-download.bytes-per-sec", Label: "Fallback download bytes per second", Type: FieldInteger, Advanced: true, Minimum: &zero},
		{Path: "reality.limit_fallback_download.burst_bytes_per_sec", SourceKey: "reality-config.limit-fallback-download.burst-bytes-per-sec", Label: "Fallback download burst", Type: FieldInteger, Advanced: true, Minimum: &zero},
	}
}

func xhttpFields() []FieldCapability {
	paths := []struct{ path, source, label string }{
		{"xhttp.path", "xhttp-config.path", "Path"}, {"xhttp.host", "xhttp-config.host", "Host"},
		{"xhttp.mode", "xhttp-config.mode", "Mode"}, {"xhttp.x_padding_bytes", "xhttp-config.x-padding-bytes", "Padding bytes"},
		{"xhttp.x_padding_key", "xhttp-config.x-padding-key", "Padding key"}, {"xhttp.x_padding_header", "xhttp-config.x-padding-header", "Padding header"},
		{"xhttp.x_padding_placement", "xhttp-config.x-padding-placement", "Padding placement"}, {"xhttp.x_padding_method", "xhttp-config.x-padding-method", "Padding method"},
		{"xhttp.uplink_http_method", "xhttp-config.uplink-http-method", "Uplink HTTP method"}, {"xhttp.session_placement", "xhttp-config.session-placement", "Session placement"},
		{"xhttp.session_key", "xhttp-config.session-key", "Session key"}, {"xhttp.seq_placement", "xhttp-config.seq-placement", "Sequence placement"},
		{"xhttp.seq_key", "xhttp-config.seq-key", "Sequence key"}, {"xhttp.uplink_data_placement", "xhttp-config.uplink-data-placement", "Uplink data placement"},
		{"xhttp.uplink_data_key", "xhttp-config.uplink-data-key", "Uplink data key"}, {"xhttp.uplink_chunk_size", "xhttp-config.uplink-chunk-size", "Uplink chunk size"},
		{"xhttp.sc_stream_up_server_secs", "xhttp-config.sc-stream-up-server-secs", "Stream-up server seconds"},
		{"xhttp.sc_max_buffered_posts", "xhttp-config.sc-max-buffered-posts", "Maximum buffered posts"},
		{"xhttp.sc_max_each_post_bytes", "xhttp-config.sc-max-each-post-bytes", "Maximum bytes per post"},
	}
	fields := make([]FieldCapability, 0, len(paths)+2)
	for _, current := range paths {
		fields = append(fields, FieldCapability{Path: current.path, SourceKey: current.source, Label: current.label, Type: FieldString, Advanced: current.path != "xhttp.path" && current.path != "xhttp.host" && current.path != "xhttp.mode"})
	}
	fields = append(fields,
		FieldCapability{Path: "xhttp.x_padding_obfs_mode", SourceKey: "xhttp-config.x-padding-obfs-mode", Label: "Padding obfuscation mode", Type: FieldBoolean, Advanced: true},
		FieldCapability{Path: "xhttp.no_sse_header", SourceKey: "xhttp-config.no-sse-header", Label: "Disable SSE header", Type: FieldBoolean, Advanced: true},
	)
	return fields
}
