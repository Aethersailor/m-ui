package domain

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

const (
	NodeSchemaVersion  = 2
	MihomoRepository   = "MetaCubeX/mihomo"
	MihomoSourceBranch = "Meta"
	// MihomoSourceCommit is the reviewed Meta branch contract for this schema.
	MihomoSourceCommit = "e26714a181ac0e2fa803453c0a8e9a9ce94e31cb"

	VLESSFlowVision    = "xtls-rprx-vision"
	PacketEncodingXUDP = "xudp"
	ClientFingerprint  = "chrome"
)

type DesiredState struct {
	AsOf                          time.Time `json:"as_of"`
	PanelTitle                    string    `json:"panel_title"`
	UILanguage                    string    `json:"ui_language"`
	CookieSecure                  bool      `json:"cookie_secure"`
	PanelUIBind                   Endpoint  `json:"panel_ui_bind"`
	MihomoExternalControllerBind  Endpoint  `json:"mihomo_external_controller_bind"`
	MihomoControllerConnect       Endpoint  `json:"mihomo_controller_connect"`
	ExternalControllerCORSOrigins []string  `json:"external_controller_cors_origins"`
	EndpointGeneration            int64     `json:"endpoint_generation"`
	ControllerAddress             string    `json:"controller_address,omitempty"` // Legacy endpoint-setting migration only.
	ControllerSecret              string    `json:"controller_secret"`
	PublicHost                    string    `json:"public_host"`
	Nodes                         []Node    `json:"nodes"`
}

// Endpoint is a typed host/port pair. Hosts are stored without IPv6 brackets;
// Address formats them with net.JoinHostPort at the process boundary.
type Endpoint struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

func (endpoint Endpoint) Address() string {
	return net.JoinHostPort(endpoint.Host, strconv.Itoa(int(endpoint.Port)))
}

func (endpoint Endpoint) Equal(other Endpoint) bool {
	return endpoint.Host == other.Host && endpoint.Port == other.Port
}

// SplitLegacyControllerEndpoint is retained only for the independent panel
// endpoint migration. Node configuration itself intentionally has no V1
// compatibility path.
func SplitLegacyControllerEndpoint(endpoint Endpoint) (Endpoint, Endpoint, error) {
	if endpoint.Port == 0 {
		return Endpoint{}, Endpoint{}, errors.New("legacy controller endpoint port is invalid")
	}
	switch endpoint.Host {
	case "127.0.0.1":
		return endpoint, endpoint, nil
	case "0.0.0.0":
		return endpoint, Endpoint{Host: "127.0.0.1", Port: endpoint.Port}, nil
	case "::1":
		return endpoint, endpoint, nil
	case "::":
		return endpoint, Endpoint{Host: "::1", Port: endpoint.Port}, nil
	default:
		return Endpoint{}, Endpoint{}, fmt.Errorf(
			"legacy controller endpoint host %q cannot be safely migrated; use 127.0.0.1, 0.0.0.0, ::1, or the IPv6 wildcard address",
			endpoint.Host,
		)
	}
}

func (state DesiredState) NormalizeLegacy() (DesiredState, error) {
	if state.PanelUIBind.Port == 0 && state.PanelUIBind.Host == "" {
		state.PanelUIBind = Endpoint{Host: "0.0.0.0", Port: 2095}
	}
	var err error
	var legacyBind, legacyConnect Endpoint
	var hasLegacy bool
	if state.ControllerAddress != "" &&
		(state.MihomoExternalControllerBind.Port == 0 ||
			state.MihomoExternalControllerBind.Host == "" ||
			state.MihomoControllerConnect.Port == 0 ||
			state.MihomoControllerConnect.Host == "") {
		legacy, parseErr := ParseEndpoint(state.ControllerAddress)
		if parseErr != nil {
			return DesiredState{}, parseErr
		}
		legacyBind, legacyConnect, err = SplitLegacyControllerEndpoint(legacy)
		if err != nil {
			return DesiredState{}, err
		}
		hasLegacy = true
	}
	if state.MihomoExternalControllerBind.Port == 0 &&
		state.MihomoExternalControllerBind.Host == "" && hasLegacy {
		state.MihomoExternalControllerBind = legacyBind
	}
	if state.MihomoControllerConnect.Port == 0 &&
		state.MihomoControllerConnect.Host == "" {
		switch {
		case hasLegacy:
			state.MihomoControllerConnect = legacyConnect
		case state.MihomoExternalControllerBind.Port != 0 ||
			state.MihomoExternalControllerBind.Host != "":
			_, state.MihomoControllerConnect, err = SplitLegacyControllerEndpoint(
				state.MihomoExternalControllerBind,
			)
			if err != nil {
				return DesiredState{}, err
			}
		}
	}
	if state.EndpointGeneration == 0 {
		state.EndpointGeneration = 1
	}
	return state, nil
}

func ParseEndpoint(address string) (Endpoint, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return Endpoint{}, err
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort == 0 {
		return Endpoint{}, strconv.ErrRange
	}
	return Endpoint{Host: host, Port: uint16(parsedPort)}, nil
}

type ProtocolKind string

const (
	ProtocolVLESS       ProtocolKind = "vless"
	ProtocolHysteria2   ProtocolKind = "hysteria2"
	ProtocolVMess       ProtocolKind = "vmess"
	ProtocolTrojan      ProtocolKind = "trojan"
	ProtocolShadowsocks ProtocolKind = "shadowsocks"
)

type Node struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Enabled        bool             `json:"enabled"`
	ListenAddress  string           `json:"listen"`
	Port           string           `json:"port"`
	Protocol       ProtocolKind     `json:"protocol"`
	SchemaVersion  int              `json:"schema_version"`
	VLESS          *VLESSSpec       `json:"vless,omitempty"`
	Hysteria2      *Hysteria2Spec   `json:"hysteria2,omitempty"`
	VMess          *VMessSpec       `json:"vmess,omitempty"`
	Trojan         *TrojanSpec      `json:"trojan,omitempty"`
	Shadowsocks    *ShadowsocksSpec `json:"shadowsocks,omitempty"`
	Users          []NodeUser       `json:"users"`
	AccessProfiles []AccessProfile  `json:"access_profiles"`
	Generation     int64            `json:"generation"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

type NodeUser struct {
	ID          string                 `json:"id"`
	NodeID      string                 `json:"node_id"`
	Name        string                 `json:"name"`
	Enabled     bool                   `json:"enabled"`
	VLESS       *VLESSCredential       `json:"vless,omitempty"`
	Hysteria2   *Hysteria2Credential   `json:"hysteria2,omitempty"`
	VMess       *VMessCredential       `json:"vmess,omitempty"`
	Trojan      *TrojanCredential      `json:"trojan,omitempty"`
	Shadowsocks *ShadowsocksCredential `json:"shadowsocks,omitempty"`
	ExpiresAt   *time.Time             `json:"expires_at,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

type VLESSCredential struct {
	UUID string `json:"uuid"`
	Flow string `json:"flow,omitempty"`
}

type Hysteria2Credential struct {
	Password string `json:"password"`
}

// VMessCredential mirrors listener/inbound.VmessUser on Mihomo's Meta branch.
// Cipher is client-side VMess security and is retained with the credential so
// every exported profile has a deterministic value.
type VMessCredential struct {
	UUID    string `json:"uuid"`
	AlterID int    `json:"alter_id,omitempty"`
	Cipher  string `json:"cipher,omitempty"`
}

type TrojanCredential struct {
	Password string `json:"password"`
}

// Mihomo's Shadowsocks listener has one password rather than a users array.
// m-ui still models it as a credential for consistent lifecycle/share APIs,
// while validation permits only one effective Shadowsocks user at a time.
type ShadowsocksCredential struct {
	Password string `json:"password"`
}

type AccessProfile struct {
	ID             string    `json:"id"`
	NodeID         string    `json:"node_id"`
	Name           string    `json:"name"`
	Default        bool      `json:"default"`
	PublicHost     string    `json:"public_host"`
	PublicPort     uint16    `json:"public_port"`
	ServerName     string    `json:"server_name,omitempty"`
	Fingerprint    string    `json:"fingerprint,omitempty"`
	PacketEncoding string    `json:"packet_encoding,omitempty"`
	AllowInsecure  bool      `json:"allow_insecure,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type VLESSHandlerKind string

const (
	VLESSHandlerRaw       VLESSHandlerKind = "raw"
	VLESSHandlerWebSocket VLESSHandlerKind = "websocket"
	VLESSHandlerGRPC      VLESSHandlerKind = "grpc"
	VLESSHandlerXHTTP     VLESSHandlerKind = "xhttp"
	VMessHandlerMKCP      VLESSHandlerKind = "mkcp"
)

type VLESSSpec struct {
	Decryption string            `json:"decryption,omitempty"`
	Handler    VLESSHandlerSpec  `json:"handler"`
	Security   VLESSSecuritySpec `json:"security"`
	Mux        MuxSpec           `json:"mux,omitempty"`
}

// VMess and Trojan share the stream handler and security building blocks that
// Mihomo exposes for their listeners. Keeping the composition typed here lets
// later protocol modules opt into only the components their source supports.
type VMessSpec struct {
	Handler  VLESSHandlerSpec  `json:"handler"`
	Security VLESSSecuritySpec `json:"security"`
	Mux      MuxSpec           `json:"mux,omitempty"`
}

type TrojanSpec struct {
	Handler     VLESSHandlerSpec      `json:"handler"`
	Security    VLESSSecuritySpec     `json:"security"`
	Mux         MuxSpec               `json:"mux,omitempty"`
	Shadowsocks TrojanShadowsocksSpec `json:"shadowsocks,omitempty"`
}

type TrojanShadowsocksSpec struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Method   string `json:"method,omitempty"`
	Password string `json:"password,omitempty"`
}

type ShadowsocksSpec struct {
	Cipher     string            `json:"cipher"`
	UDP        bool              `json:"udp"`
	Security   VLESSSecuritySpec `json:"security"`
	Mux        MuxSpec           `json:"mux,omitempty"`
	SimpleObfs SimpleObfsSpec    `json:"simple_obfs,omitempty"`
}

type SimpleObfsSpec struct {
	Enabled bool   `json:"enabled,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type VLESSHandlerSpec struct {
	Type      VLESSHandlerKind `json:"type"`
	WebSocket *WebSocketSpec   `json:"websocket,omitempty"`
	GRPC      *GRPCSpec        `json:"grpc,omitempty"`
	XHTTP     *XHTTPConfig     `json:"xhttp,omitempty"`
	MKCP      *MKCPConfig      `json:"mkcp,omitempty"`
}

// MKCPConfig mirrors listener/inbound.MKCPConfig on Mihomo Meta. It is
// currently registered only by the VMess module.
type MKCPConfig struct {
	MTU              uint32 `json:"mtu,omitempty"`
	TTI              uint32 `json:"tti,omitempty"`
	UplinkCapacity   uint32 `json:"uplink_capacity,omitempty"`
	DownlinkCapacity uint32 `json:"downlink_capacity,omitempty"`
	Congestion       bool   `json:"congestion,omitempty"`
	WriteBuffer      uint32 `json:"write_buffer,omitempty"`
	ReadBuffer       uint32 `json:"read_buffer,omitempty"`
	Seed             string `json:"seed,omitempty"`
	Header           string `json:"header,omitempty"`
}

type WebSocketSpec struct {
	Path string `json:"path"`
}

type GRPCSpec struct {
	ServiceName string `json:"service_name"`
}

type XHTTPConfig struct {
	Path                 string `json:"path,omitempty"`
	Host                 string `json:"host,omitempty"`
	Mode                 string `json:"mode,omitempty"`
	XPaddingBytes        string `json:"x_padding_bytes,omitempty"`
	XPaddingObfsMode     bool   `json:"x_padding_obfs_mode,omitempty"`
	XPaddingKey          string `json:"x_padding_key,omitempty"`
	XPaddingHeader       string `json:"x_padding_header,omitempty"`
	XPaddingPlacement    string `json:"x_padding_placement,omitempty"`
	XPaddingMethod       string `json:"x_padding_method,omitempty"`
	UplinkHTTPMethod     string `json:"uplink_http_method,omitempty"`
	SessionPlacement     string `json:"session_placement,omitempty"`
	SessionKey           string `json:"session_key,omitempty"`
	SeqPlacement         string `json:"seq_placement,omitempty"`
	SeqKey               string `json:"seq_key,omitempty"`
	UplinkDataPlacement  string `json:"uplink_data_placement,omitempty"`
	UplinkDataKey        string `json:"uplink_data_key,omitempty"`
	UplinkChunkSize      string `json:"uplink_chunk_size,omitempty"`
	NoSSEHeader          bool   `json:"no_sse_header,omitempty"`
	SCStreamUpServerSecs string `json:"sc_stream_up_server_secs,omitempty"`
	SCMaxBufferedPosts   string `json:"sc_max_buffered_posts,omitempty"`
	SCMaxEachPostBytes   string `json:"sc_max_each_post_bytes,omitempty"`
}

type VLESSSecurityKind string

const (
	VLESSSecurityNone      VLESSSecurityKind = "none"
	VLESSSecurityTLS       VLESSSecurityKind = "tls"
	VLESSSecurityReality   VLESSSecurityKind = "reality"
	VLESSSecurityShadowTLS VLESSSecurityKind = "shadow-tls"
	VLESSSecurityResTLS    VLESSSecurityKind = "res-tls"
	VLESSSecurityJLS       VLESSSecurityKind = "jls"
)

type VLESSSecuritySpec struct {
	Type      VLESSSecurityKind `json:"type"`
	TLS       *TLSConfig        `json:"tls,omitempty"`
	Reality   *RealityConfig    `json:"reality,omitempty"`
	ShadowTLS *ShadowTLSConfig  `json:"shadow_tls,omitempty"`
	ResTLS    *ResTLSConfig     `json:"res_tls,omitempty"`
	JLS       *JLSConfig        `json:"jls,omitempty"`
}

type TLSConfig struct {
	Certificate    string `json:"certificate"`
	PrivateKey     string `json:"private_key"`
	ClientAuthType string `json:"client_auth_type,omitempty"`
	ClientAuthCert string `json:"client_auth_cert,omitempty"`
	ECHKey         string `json:"ech_key,omitempty"`
	AllowInsecure  bool   `json:"allow_insecure,omitempty"`
}

type RealityConfig struct {
	Destination           string               `json:"destination"`
	PrivateKey            string               `json:"private_key"`
	PublicKey             string               `json:"public_key"`
	ShortIDs              []string             `json:"short_ids"`
	ServerNames           []string             `json:"server_names"`
	MaxTimeDifference     int                  `json:"max_time_difference,omitempty"`
	Proxy                 string               `json:"proxy,omitempty"`
	LimitFallbackUpload   RealityFallbackLimit `json:"limit_fallback_upload,omitempty"`
	LimitFallbackDownload RealityFallbackLimit `json:"limit_fallback_download,omitempty"`
}

type RealityFallbackLimit struct {
	AfterBytes       uint64 `json:"after_bytes,omitempty"`
	BytesPerSec      uint64 `json:"bytes_per_sec,omitempty"`
	BurstBytesPerSec uint64 `json:"burst_bytes_per_sec,omitempty"`
}

type ShadowTLSConfig struct {
	Version                int                           `json:"version,omitempty"`
	Password               string                        `json:"password,omitempty"`
	Users                  []ShadowTLSUser               `json:"users,omitempty"`
	Handshake              ShadowTLSHandshake            `json:"handshake"`
	HandshakeForServerName map[string]ShadowTLSHandshake `json:"handshake_for_server_name,omitempty"`
	StrictMode             bool                          `json:"strict_mode,omitempty"`
	WildcardSNI            string                        `json:"wildcard_sni,omitempty"`
}

type ShadowTLSUser struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type ShadowTLSHandshake struct {
	Destination string `json:"destination"`
	Proxy       string `json:"proxy,omitempty"`
}

type ResTLSConfig struct {
	Destination     string `json:"destination"`
	Password        string `json:"password"`
	VersionHint     string `json:"version_hint,omitempty"`
	Script          string `json:"script,omitempty"`
	MinRecordLength int    `json:"min_record_length,omitempty"`
	Proxy           string `json:"proxy,omitempty"`
}

type JLSConfig struct {
	Users       []JLSUser `json:"users"`
	ServerName  string    `json:"server_name,omitempty"`
	Destination string    `json:"destination"`
	ALPN        []string  `json:"alpn,omitempty"`
	Proxy       string    `json:"proxy,omitempty"`
	RateLimit   uint64    `json:"rate_limit,omitempty"`
}

type JLSUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type MuxSpec struct {
	Padding bool       `json:"padding,omitempty"`
	Brutal  BrutalSpec `json:"brutal,omitempty"`
}

type BrutalSpec struct {
	Enabled bool   `json:"enabled,omitempty"`
	Up      string `json:"up,omitempty"`
	Down    string `json:"down,omitempty"`
}

type Hysteria2Spec struct {
	Obfs                           string                `json:"obfs,omitempty"`
	ObfsPassword                   string                `json:"obfs_password,omitempty"`
	ObfsMinPacketSize              int                   `json:"obfs_min_packet_size,omitempty"`
	ObfsMaxPacketSize              int                   `json:"obfs_max_packet_size,omitempty"`
	Certificate                    string                `json:"certificate"`
	PrivateKey                     string                `json:"private_key"`
	ClientAuthType                 string                `json:"client_auth_type,omitempty"`
	ClientAuthCert                 string                `json:"client_auth_cert,omitempty"`
	ECHKey                         string                `json:"ech_key,omitempty"`
	MaxIdleTime                    int                   `json:"max_idle_time,omitempty"`
	ALPN                           []string              `json:"alpn,omitempty"`
	Up                             string                `json:"up,omitempty"`
	Down                           string                `json:"down,omitempty"`
	IgnoreClientBandwidth          bool                  `json:"ignore_client_bandwidth,omitempty"`
	Masquerade                     string                `json:"masquerade,omitempty"`
	CWND                           int                   `json:"cwnd,omitempty"`
	BBRProfile                     string                `json:"bbr_profile,omitempty"`
	UDPMTU                         int                   `json:"udp_mtu,omitempty"`
	Mux                            MuxSpec               `json:"mux,omitempty"`
	Realm                          *Hysteria2RealmConfig `json:"realm,omitempty"`
	InitialStreamReceiveWindow     uint64                `json:"initial_stream_receive_window,omitempty"`
	MaxStreamReceiveWindow         uint64                `json:"max_stream_receive_window,omitempty"`
	InitialConnectionReceiveWindow uint64                `json:"initial_connection_receive_window,omitempty"`
	MaxConnectionReceiveWindow     uint64                `json:"max_connection_receive_window,omitempty"`
}

type Hysteria2RealmConfig struct {
	Enabled        bool     `json:"enabled"`
	ServerURL      string   `json:"server_url,omitempty"`
	Token          string   `json:"token,omitempty"`
	RealmID        string   `json:"realm_id,omitempty"`
	STUNServers    []string `json:"stun_servers,omitempty"`
	ServerName     string   `json:"server_name,omitempty"`
	SkipCertVerify bool     `json:"skip_cert_verify,omitempty"`
	NameCertVerify string   `json:"name_cert_verify,omitempty"`
	Fingerprint    string   `json:"fingerprint,omitempty"`
	Certificate    string   `json:"certificate,omitempty"`
	PrivateKey     string   `json:"private_key,omitempty"`
	ALPN           []string `json:"alpn,omitempty"`
	Proxy          string   `json:"proxy,omitempty"`
}

type Keypair struct {
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
}

func (node Node) EffectiveUsers(asOf time.Time) []NodeUser {
	users := make([]NodeUser, 0, len(node.Users))
	for _, user := range node.Users {
		if !user.Enabled {
			continue
		}
		if user.ExpiresAt != nil && !user.ExpiresAt.After(asOf) {
			continue
		}
		users = append(users, user)
	}
	return users
}

func (node Node) DefaultAccessProfile() (AccessProfile, bool) {
	for _, profile := range node.AccessProfiles {
		if profile.Default {
			return profile, true
		}
	}
	return AccessProfile{}, false
}
