package protocol

import "github.com/Aethersailor/m-ui/internal/domain"

// classicStreamComponents intentionally derives shared component descriptions
// from the already source-audited VLESS capability. Mihomo's Meta branch uses
// the same inbound structs for raw/ws/grpc and the listed security wrappers on
// VMess and Trojan listeners.
func classicStreamComponents() []ComponentCapability {
	components := vlessCapability().Components
	selected := make([]ComponentCapability, 0, len(components))
	for _, component := range components {
		if component.Group == ComponentTransport &&
			component.Kind != string(domain.VLESSHandlerRaw) &&
			component.Kind != string(domain.VLESSHandlerWebSocket) &&
			component.Kind != string(domain.VLESSHandlerGRPC) {
			continue
		}
		selected = append(selected, component)
	}
	return selected
}

func streamMuxFields(root string) []FieldCapability {
	return []FieldCapability{
		{Path: root + ".mux.padding", SourceKey: "mux-option.padding", Label: "Mux padding", Type: FieldBoolean, Advanced: true},
		{Path: root + ".mux.brutal.enabled", SourceKey: "mux-option.brutal.enabled", Label: "Brutal mux", Type: FieldBoolean, Advanced: true},
		{Path: root + ".mux.brutal.up", SourceKey: "mux-option.brutal.up", Label: "Brutal upload", Type: FieldString, Advanced: true, VisibleWhen: visibleWhen(root+".mux.brutal.enabled", true)},
		{Path: root + ".mux.brutal.down", SourceKey: "mux-option.brutal.down", Label: "Brutal download", Type: FieldString, Advanced: true, VisibleWhen: visibleWhen(root+".mux.brutal.enabled", true)},
	}
}

func vmessCapability() ProtocolCapability {
	zero, uint32Max := int64(0), int64(4294967295)
	components := classicStreamComponents()
	for index := range components {
		if components[index].Group == ComponentSecurity && components[index].Kind == string(domain.VLESSSecurityTLS) {
			fields := components[index].Fields[:0]
			for _, field := range components[index].Fields {
				if field.Path != "tls.allow_insecure" {
					fields = append(fields, field)
				}
			}
			components[index].Fields = fields
		}
		if components[index].Group == ComponentTransport && components[index].Kind == string(domain.VLESSHandlerWebSocket) {
			components[index].Conflicts = append(components[index].Conflicts, componentID(ComponentSecurity, string(domain.VLESSSecurityReality)))
		}
	}
	components = append(components, ComponentCapability{
		Group: ComponentTransport, Kind: string(domain.VMessHandlerMKCP), Label: "mKCP",
		DefaultConfig: rawDefault(domain.VLESSHandlerSpec{Type: domain.VMessHandlerMKCP, MKCP: &domain.MKCPConfig{MTU: 1350, TTI: 50, UplinkCapacity: 5, DownlinkCapacity: 20, WriteBuffer: 2097152, ReadBuffer: 2097152}}),
		Conflicts: []string{
			componentID(ComponentSecurity, string(domain.VLESSSecurityShadowTLS)),
			componentID(ComponentSecurity, string(domain.VLESSSecurityResTLS)),
			componentID(ComponentSecurity, string(domain.VLESSSecurityJLS)),
		},
		Fields: []FieldCapability{
			{Path: "mkcp.mtu", SourceKey: "mkcp-config.mtu", Label: "MTU", Type: FieldInteger, Advanced: true, Minimum: &zero, Maximum: &uint32Max},
			{Path: "mkcp.tti", SourceKey: "mkcp-config.tti", Label: "TTI (ms)", Type: FieldInteger, Advanced: true, Minimum: &zero, Maximum: &uint32Max},
			{Path: "mkcp.uplink_capacity", SourceKey: "mkcp-config.uplink-capacity", Label: "Uplink capacity (MB/s)", Type: FieldInteger, Advanced: true, Minimum: &zero, Maximum: &uint32Max},
			{Path: "mkcp.downlink_capacity", SourceKey: "mkcp-config.downlink-capacity", Label: "Downlink capacity (MB/s)", Type: FieldInteger, Advanced: true, Minimum: &zero, Maximum: &uint32Max},
			{Path: "mkcp.congestion", SourceKey: "mkcp-config.congestion", Label: "Congestion control", Type: FieldBoolean, Advanced: true},
			{Path: "mkcp.write_buffer", SourceKey: "mkcp-config.write-buffer", Label: "Write buffer", Type: FieldInteger, Advanced: true, Minimum: &zero, Maximum: &uint32Max},
			{Path: "mkcp.read_buffer", SourceKey: "mkcp-config.read-buffer", Label: "Read buffer", Type: FieldInteger, Advanced: true, Minimum: &zero, Maximum: &uint32Max},
			{Path: "mkcp.seed", SourceKey: "mkcp-config.seed", Label: "Seed", Type: FieldSecret, Secret: true, Advanced: true},
			{Path: "mkcp.header", SourceKey: "mkcp-config.header", Label: "Header", Type: FieldString, Advanced: true, Options: []string{"", "none", "srtp", "utp", "wechat-video", "dtls", "wireguard"}},
		},
	})
	return ProtocolCapability{
		Kind:  domain.ProtocolVMess,
		Label: "VMess",
		DefaultNode: rawDefault(map[string]any{"vmess": domain.VMessSpec{
			Handler:  domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
			Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
		}}),
		Layers: []LayerCapability{
			{Group: ComponentTransport, Required: true, DefaultComponent: string(domain.VLESSHandlerRaw)},
			{Group: ComponentSecurity, Required: true, DefaultComponent: string(domain.VLESSSecurityNone)},
		},
		Components: components,
		Fields:     streamMuxFields("vmess"),
		UserFields: []FieldCapability{
			{Path: "vmess.uuid", SourceKey: "users.uuid", Label: "UUID", Type: FieldString, Required: true, Secret: true},
			{Path: "vmess.alter_id", SourceKey: "users.alterId", Label: "Alter ID", Type: FieldInteger, Advanced: true},
			{Path: "vmess.cipher", SourceKey: "client.cipher", Label: "Client cipher", Type: FieldString, Required: true, Options: []string{"auto", "aes-128-gcm", "chacha20-poly1305", "none", "zero"}},
		},
		Features: []string{"multiple-users", "websocket", "grpc", "mkcp", "mux", "brutal", "share-uri", "client-yaml", "source-partial-mekya", "source-partial-tlsmirror"},
	}
}

func trojanCapability() ProtocolCapability {
	fields := streamMuxFields("trojan")
	components := classicStreamComponents()
	for index := range components {
		if components[index].Group == ComponentTransport && components[index].Kind == string(domain.VLESSHandlerWebSocket) {
			components[index].Conflicts = append(components[index].Conflicts, componentID(ComponentSecurity, string(domain.VLESSSecurityReality)))
		}
	}
	fields = append(fields,
		FieldCapability{Path: "trojan.shadowsocks.enabled", SourceKey: "ss-option.enabled", Label: "Trojan-Go Shadowsocks wrapper", Type: FieldBoolean, Advanced: true},
		FieldCapability{Path: "trojan.shadowsocks.method", SourceKey: "ss-option.method", Label: "Wrapper method", Type: FieldString, Advanced: true, Options: []string{"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"}, VisibleWhen: visibleWhen("trojan.shadowsocks.enabled", true)},
		FieldCapability{Path: "trojan.shadowsocks.password", SourceKey: "ss-option.password", Label: "Wrapper password", Type: FieldSecret, Secret: true, Advanced: true, VisibleWhen: visibleWhen("trojan.shadowsocks.enabled", true)},
	)
	return ProtocolCapability{
		Kind:  domain.ProtocolTrojan,
		Label: "Trojan",
		DefaultNode: rawDefault(map[string]any{"trojan": domain.TrojanSpec{
			Handler:  domain.VLESSHandlerSpec{Type: domain.VLESSHandlerRaw},
			Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityTLS, TLS: &domain.TLSConfig{}},
		}}),
		Layers: []LayerCapability{
			{Group: ComponentTransport, Required: true, DefaultComponent: string(domain.VLESSHandlerRaw)},
			{Group: ComponentSecurity, Required: true, DefaultComponent: string(domain.VLESSSecurityTLS)},
		},
		Components: components,
		Fields:     fields,
		UserFields: []FieldCapability{
			{Path: "trojan.password", SourceKey: "users.password", Label: "Password", Type: FieldSecret, Required: true, Secret: true},
		},
		Features: []string{"multiple-users", "websocket", "grpc", "mux", "brutal", "trojan-go-shadowsocks", "share-uri", "client-yaml"},
	}
}

func shadowsocksCapability() ProtocolCapability {
	securityComponents := make([]ComponentCapability, 0, 4)
	for _, component := range vlessCapability().Components {
		if component.Group != ComponentSecurity {
			continue
		}
		switch domain.VLESSSecurityKind(component.Kind) {
		case domain.VLESSSecurityNone, domain.VLESSSecurityShadowTLS,
			domain.VLESSSecurityResTLS, domain.VLESSSecurityJLS:
			securityComponents = append(securityComponents, component)
		}
	}
	securityComponents = append([]ComponentCapability{{
		Group: ComponentTransport, Kind: "tcp-udp", Label: "TCP and optional UDP",
		DefaultConfig: rawDefault(map[string]any{}),
	}}, securityComponents...)
	securityComponents = append(securityComponents, ComponentCapability{
		Group: ComponentExtension, Kind: "simple-obfs", Label: "Simple obfs",
		DefaultConfig: rawDefault(domain.SimpleObfsSpec{Mode: "http"}),
		Conflicts: []string{
			componentID(ComponentSecurity, string(domain.VLESSSecurityShadowTLS)),
			componentID(ComponentSecurity, string(domain.VLESSSecurityResTLS)),
			componentID(ComponentSecurity, string(domain.VLESSSecurityJLS)),
		},
		Fields: []FieldCapability{
			{Path: "enabled", SourceKey: "simple-obfs.enable", Label: "Enabled", Type: FieldBoolean},
			{Path: "mode", SourceKey: "simple-obfs.mode", Label: "Mode", Type: FieldString, Required: true, Options: []string{"http", "tls"}},
		},
	})
	return ProtocolCapability{
		Kind:  domain.ProtocolShadowsocks,
		Label: "Shadowsocks",
		DefaultNode: rawDefault(map[string]any{"shadowsocks": domain.ShadowsocksSpec{
			Cipher: "2022-blake3-aes-256-gcm", UDP: true,
			Security: domain.VLESSSecuritySpec{Type: domain.VLESSSecurityNone},
		}}),
		Layers: []LayerCapability{
			{Group: ComponentTransport, Required: true, Locked: true, DefaultComponent: "tcp-udp"},
			{Group: ComponentSecurity, Required: true, DefaultComponent: string(domain.VLESSSecurityNone)},
			{Group: ComponentExtension, Multiple: true},
		},
		Components: securityComponents,
		Fields: append([]FieldCapability{
			{Path: "shadowsocks.cipher", SourceKey: "cipher", Label: "Cipher", Type: FieldString, Required: true, Options: []string{
				"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305",
				"aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305", "none",
			}},
			{Path: "shadowsocks.udp", SourceKey: "udp", Label: "UDP", Type: FieldBoolean},
		}, streamMuxFields("shadowsocks")...),
		UserFields: []FieldCapability{
			{Path: "shadowsocks.password", SourceKey: "password", Label: "Password", Type: FieldSecret, Required: true, Secret: true},
		},
		Features: []string{"single-active-user", "udp", "mux", "simple-obfs", "share-uri", "client-yaml"},
	}
}
