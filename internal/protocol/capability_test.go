package protocol

import (
	"encoding/json"
	"testing"

	"github.com/Aethersailor/m-ui/internal/domain"
)

func TestCapabilityManifestExposesStructuredCompositionContract(t *testing.T) {
	t.Parallel()
	manifest := DefaultRegistry().Capabilities()
	if manifest.SchemaVersion != CapabilitySchemaVersion || manifest.NodeSchemaVersion != domain.NodeSchemaVersion {
		t.Fatalf("schema versions = %d/%d", manifest.SchemaVersion, manifest.NodeSchemaVersion)
	}
	if manifest.Source.Repository != domain.MihomoRepository || manifest.Source.Branch != "Meta" || manifest.Source.Commit == "" {
		t.Fatalf("source contract = %#v", manifest.Source)
	}
	if len(manifest.NodeFields) == 0 || len(manifest.AccessProfileFields) == 0 {
		t.Fatal("shared node or access-profile schema is empty")
	}

	vless := protocolByKind(t, manifest, domain.ProtocolVLESS)
	if vless.Label != "VLESS" || len(vless.Layers) != 2 {
		t.Fatalf("VLESS layers = %#v", vless.Layers)
	}
	transport := layerByGroup(t, vless, ComponentTransport)
	security := layerByGroup(t, vless, ComponentSecurity)
	if transport.DefaultComponent != "raw" || security.DefaultComponent != "reality" {
		t.Fatalf("VLESS defaults = %q/%q", transport.DefaultComponent, security.DefaultComponent)
	}
	if len(componentByKind(t, vless, ComponentTransport, "xhttp").Fields) != 21 {
		t.Fatal("XHTTP capability does not cover the Meta field set")
	}
	shadow := componentByKind(t, vless, ComponentSecurity, "shadow-tls")
	if fieldByPath(t, shadow.Fields, "shadow_tls.users").Type != FieldObjectList ||
		fieldByPath(t, shadow.Fields, "shadow_tls.handshake_for_server_name").Type != FieldRecord {
		t.Fatal("ShadowTLS repeatable fields are not described structurally")
	}
	users := fieldByPath(t, shadow.Fields, "shadow_tls.users")
	if users.VisibleWhen == nil || users.VisibleWhen.Path != "shadow_tls.version" || users.VisibleWhen.Equals != 3 {
		t.Fatalf("ShadowTLS user visibility = %#v", users.VisibleWhen)
	}
	handshakes := fieldByPath(t, shadow.Fields, "shadow_tls.handshake_for_server_name")
	if handshakes.ItemKeyLabel != "Server name" {
		t.Fatalf("ShadowTLS record key label = %q", handshakes.ItemKeyLabel)
	}
	var defaults struct {
		VLESS *domain.VLESSSpec `json:"vless"`
	}
	if err := json.Unmarshal(vless.DefaultNode, &defaults); err != nil {
		t.Fatalf("decode VLESS defaults: %v", err)
	}
	if defaults.VLESS == nil || defaults.VLESS.Handler.Type != domain.VLESSHandlerRaw || defaults.VLESS.Security.Type != domain.VLESSSecurityReality {
		t.Fatalf("VLESS default node = %#v", defaults.VLESS)
	}

	hysteria2 := protocolByKind(t, manifest, domain.ProtocolHysteria2)
	if !layerByGroup(t, hysteria2, ComponentTransport).Locked || !layerByGroup(t, hysteria2, ComponentSecurity).Locked {
		t.Fatal("Hysteria2 fixed QUIC/TLS layers are not locked")
	}
	tls := componentByKind(t, hysteria2, ComponentSecurity, "tls")
	fieldByPath(t, tls.Fields, "alpn")
	for _, field := range tls.Fields {
		if field.Path == "allow_insecure" {
			t.Fatal("Hysteria2 schema exposed unsupported allow-insecure field")
		}
	}
	realm := componentByKind(t, hysteria2, ComponentExtension, "realm")
	if len(realm.Requires) != 2 || !fieldByPath(t, realm.Fields, "private_key").Secret {
		t.Fatalf("Realm capability = %#v", realm)
	}
	realmURL := fieldByPath(t, realm.Fields, "server_url")
	if realmURL.VisibleWhen == nil || realmURL.VisibleWhen.Path != "enabled" || realmURL.VisibleWhen.Equals != true {
		t.Fatalf("Realm visibility = %#v", realmURL.VisibleWhen)
	}
}

func TestCapabilityComponentsHaveUniqueStableIDsAndValidDefaults(t *testing.T) {
	t.Parallel()
	manifest := DefaultRegistry().Capabilities()
	for _, protocol := range manifest.Protocols {
		seen := make(map[string]struct{}, len(protocol.Components))
		for _, component := range protocol.Components {
			id := string(component.Group) + ":" + component.Kind
			if _, exists := seen[id]; exists {
				t.Fatalf("protocol %s repeats component %s", protocol.Kind, id)
			}
			seen[id] = struct{}{}
			var decoded any
			if err := json.Unmarshal(component.DefaultConfig, &decoded); err != nil {
				t.Fatalf("component %s default is invalid JSON: %v", id, err)
			}
			assertSecretMetadata(t, component.Fields)
		}
		assertSecretMetadata(t, protocol.Fields)
		assertSecretMetadata(t, protocol.UserFields)
	}
}

func TestCapabilityManifestPassesGenericEditorValidation(t *testing.T) {
	t.Parallel()
	if err := ValidateCapabilityManifest(DefaultRegistry().Capabilities()); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityManifestRejectsInvalidCompositionReferences(t *testing.T) {
	t.Parallel()
	manifest := DefaultRegistry().Capabilities()
	manifest.Protocols[0].Components[0].Requires = []string{"security:missing"}
	if err := ValidateCapabilityManifest(manifest); err == nil {
		t.Fatal("invalid component reference was accepted")
	}
}

func TestCapabilityManifestRejectsInvalidComplexAndConditionalFields(t *testing.T) {
	t.Parallel()
	manifest := DefaultRegistry().Capabilities()
	manifest.Protocols[0].Components[0].Fields = []FieldCapability{{
		Path: "items", Label: "Items", Type: FieldObjectList,
	}}
	if err := ValidateCapabilityManifest(manifest); err == nil {
		t.Fatal("object-list without item fields was accepted")
	}

	manifest = DefaultRegistry().Capabilities()
	manifest.Protocols[0].Components[0].Fields = []FieldCapability{{
		Path: "value", Label: "Value", Type: FieldString,
		VisibleWhen: visibleWhen("missing", true),
	}}
	if err := ValidateCapabilityManifest(manifest); err == nil {
		t.Fatal("condition referencing a missing field was accepted")
	}
}

func assertSecretMetadata(t *testing.T, fields []FieldCapability) {
	t.Helper()
	for _, field := range fields {
		if field.Type == FieldSecret && !field.Secret {
			t.Errorf("secret field %q is not marked secret", field.Path)
		}
		assertSecretMetadata(t, field.ItemFields)
	}
}

func protocolByKind(t *testing.T, manifest CapabilityManifest, kind domain.ProtocolKind) ProtocolCapability {
	t.Helper()
	for _, capability := range manifest.Protocols {
		if capability.Kind == kind {
			return capability
		}
	}
	t.Fatalf("protocol %s not found", kind)
	return ProtocolCapability{}
}

func layerByGroup(t *testing.T, protocol ProtocolCapability, group ComponentGroup) LayerCapability {
	t.Helper()
	for _, layer := range protocol.Layers {
		if layer.Group == group {
			return layer
		}
	}
	t.Fatalf("protocol %s layer %s not found", protocol.Kind, group)
	return LayerCapability{}
}

func componentByKind(t *testing.T, protocol ProtocolCapability, group ComponentGroup, kind string) ComponentCapability {
	t.Helper()
	for _, component := range protocol.Components {
		if component.Group == group && component.Kind == kind {
			return component
		}
	}
	t.Fatalf("protocol %s component %s:%s not found", protocol.Kind, group, kind)
	return ComponentCapability{}
}

func fieldByPath(t *testing.T, fields []FieldCapability, path string) FieldCapability {
	t.Helper()
	for _, field := range fields {
		if field.Path == path {
			return field
		}
	}
	t.Fatalf("field %s not found", path)
	return FieldCapability{}
}
