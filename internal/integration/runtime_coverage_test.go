package integration_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/Aethersailor/m-ui/internal/protocol"
)

type runtimeEvidenceKind string

const (
	evidenceRealTransfer     runtimeEvidenceKind = "real-transfer"
	evidenceServerClientTest runtimeEvidenceKind = "server-client-mihomo-t"
	evidenceEquivalence      runtimeEvidenceKind = "equivalence"
	evidenceValidationReject runtimeEvidenceKind = "validation-rejection"
)

type runtimeCoverageReport struct {
	CapabilitySchemaVersion int                    `json:"capability_schema_version"`
	Entries                 []runtimeCoverageEntry `json:"entries"`
}

type runtimeCoverageEntry struct {
	Target       string              `json:"target"`
	Evidence     runtimeEvidenceKind `json:"evidence"`
	ScenarioID   string              `json:"scenario_id,omitempty"`
	EquivalentTo string              `json:"equivalent_to,omitempty"`
	Rationale    string              `json:"rationale,omitempty"`
}

var runtimeScenarioCatalog = map[string]runtimeEvidenceKind{
	"TestGeneratedServerAndClientConfigurationsWithRealMihomo/loopback-ipv4":             evidenceServerClientTest,
	"TestGeneratedHysteria2ConfigurationsWithRealMihomo":                                 evidenceServerClientTest,
	"TestGeneratedShadowsocksSecurityPluginsWithRealMihomo/shadow-tls-v3":                evidenceServerClientTest,
	"TestGeneratedShadowsocksSecurityPluginsWithRealMihomo/restls":                       evidenceServerClientTest,
	"TestGeneratedShadowsocksSecurityPluginsWithRealMihomo/jls":                          evidenceServerClientTest,
	"TestGeneratedAdvancedVLESSVariantsWithRealMihomo/xhttp-none":                        evidenceServerClientTest,
	"TestGeneratedAdvancedVLESSVariantsWithRealMihomo/grpc-tls":                          evidenceServerClientTest,
	"TestGeneratedAdvancedVLESSVariantsWithRealMihomo/raw-tls-vision":                    evidenceServerClientTest,
	"TestGeneratedAdvancedVLESSVariantsWithRealMihomo/raw-shadow-tls-v3":                 evidenceServerClientTest,
	"TestGeneratedAdvancedVLESSVariantsWithRealMihomo/websocket-res-tls":                 evidenceServerClientTest,
	"TestGeneratedAdvancedVLESSVariantsWithRealMihomo/raw-jls":                           evidenceServerClientTest,
	"TestR3ProtocolsTransferDataWithRealMihomo/vmess-raw":                                evidenceRealTransfer,
	"TestR3ProtocolsTransferDataWithRealMihomo/vmess-mkcp":                               evidenceRealTransfer,
	"TestR3ProtocolsTransferDataWithRealMihomo/vmess-mkcp-tls":                           evidenceRealTransfer,
	"TestR3ProtocolsTransferDataWithRealMihomo/vmess-websocket-tls":                      evidenceRealTransfer,
	"TestR3ProtocolsTransferDataWithRealMihomo/vmess-grpc-tls":                           evidenceRealTransfer,
	"TestR3ProtocolsTransferDataWithRealMihomo/trojan-tls":                               evidenceRealTransfer,
	"TestR3ProtocolsTransferDataWithRealMihomo/trojan-websocket-tls":                     evidenceRealTransfer,
	"TestR3ProtocolsTransferDataWithRealMihomo/trojan-grpc-tls":                          evidenceRealTransfer,
	"TestR3ProtocolsTransferDataWithRealMihomo/shadowsocks-2022":                         evidenceRealTransfer,
	"TestR3ProtocolsTransferDataWithRealMihomo/shadowsocks-simple-obfs":                  evidenceRealTransfer,
	"internal/domain/TestValidateClassicWebSocketRejectsRealityClientMismatch":           evidenceValidationReject,
	"internal/domain/TestValidateVMessMKCPRejectsUnsupportedSecurityWrappers/jls":        evidenceValidationReject,
	"internal/domain/TestValidateVMessMKCPRejectsUnsupportedSecurityWrappers/res-tls":    evidenceValidationReject,
	"internal/domain/TestValidateVMessMKCPRejectsUnsupportedSecurityWrappers/shadow-tls": evidenceValidationReject,
	"internal/domain/TestValidateShadowsocksSimpleObfsRejectsSecurityPlugins/jls":        evidenceValidationReject,
	"internal/domain/TestValidateShadowsocksSimpleObfsRejectsSecurityPlugins/res-tls":    evidenceValidationReject,
	"internal/domain/TestValidateShadowsocksSimpleObfsRejectsSecurityPlugins/shadow-tls": evidenceValidationReject,
}

func init() {
	for protocolKind, options := range map[string][]string{
		"vmess":       {"auto", "aes-128-gcm", "chacha20-poly1305", "none", "zero"},
		"shadowsocks": {"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305", "aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "none", "xchacha20-ietf-poly1305"},
		"trojan":      {"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"},
	} {
		for _, option := range options {
			runtimeScenarioCatalog["TestGeneratedCoreCipherOptionsWithRealMihomo/"+protocolKind+"/"+option] = evidenceServerClientTest
		}
	}
}

func TestCapabilityManifestV4HasRuntimeCoverageClosure(t *testing.T) {
	t.Parallel()
	report := readRuntimeCoverageReport(t)
	if err := validateRuntimeCoverageClosure(protocol.DefaultRegistry().Capabilities(), report); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeCoverageClosureRejectsUnmappedManifestComponent(t *testing.T) {
	t.Parallel()
	report := readRuntimeCoverageReport(t)
	manifest := protocol.DefaultRegistry().Capabilities()
	manifest.Protocols[0].Components = append(manifest.Protocols[0].Components, protocol.ComponentCapability{
		Group: protocol.ComponentTransport, Kind: "future-unmapped-transport", Label: "Future transport",
		DefaultConfig: json.RawMessage(`{}`),
	})
	err := validateRuntimeCoverageClosure(manifest, report)
	wantTarget := "component/" + string(manifest.Protocols[0].Kind) + "/transport/future-unmapped-transport"
	if err == nil || !strings.Contains(err.Error(), wantTarget) {
		t.Fatalf("unmapped manifest component error = %v", err)
	}
}

func readRuntimeCoverageReport(t *testing.T) runtimeCoverageReport {
	t.Helper()
	file, err := os.Open("testdata/runtime_coverage_v4.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var report runtimeCoverageReport
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		t.Fatalf("decode runtime coverage report: %v", err)
	}
	return report
}

func validateRuntimeCoverageClosure(manifest protocol.CapabilityManifest, report runtimeCoverageReport) error {
	if report.CapabilitySchemaVersion != manifest.SchemaVersion {
		return fmt.Errorf("runtime coverage schema = %d, manifest schema = %d", report.CapabilitySchemaVersion, manifest.SchemaVersion)
	}
	required := requiredRuntimeCoverageTargets(manifest)
	entries := make(map[string]runtimeCoverageEntry, len(report.Entries))
	previous := ""
	for _, entry := range report.Entries {
		if entry.Target == "" {
			return errors.New("runtime coverage entry has an empty target")
		}
		if previous != "" && entry.Target <= previous {
			return fmt.Errorf("runtime coverage entries are not uniquely sorted: %q follows %q", entry.Target, previous)
		}
		previous = entry.Target
		if _, exists := entries[entry.Target]; exists {
			return fmt.Errorf("runtime coverage target %q is duplicated", entry.Target)
		}
		entries[entry.Target] = entry
	}
	var missing, stale []string
	for target := range required {
		if _, exists := entries[target]; !exists {
			missing = append(missing, target)
		}
	}
	for target := range entries {
		if _, exists := required[target]; !exists {
			stale = append(stale, target)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) != 0 || len(stale) != 0 {
		return fmt.Errorf("runtime coverage closure mismatch: missing=%v stale=%v", missing, stale)
	}
	for target := range entries {
		if err := validateRuntimeEvidence(target, entries, map[string]bool{}); err != nil {
			return err
		}
	}
	return nil
}

func validateRuntimeEvidence(target string, entries map[string]runtimeCoverageEntry, visiting map[string]bool) error {
	if visiting[target] {
		return fmt.Errorf("runtime coverage equivalence cycle includes %q", target)
	}
	entry := entries[target]
	switch entry.Evidence {
	case evidenceRealTransfer, evidenceServerClientTest, evidenceValidationReject:
		kind, exists := runtimeScenarioCatalog[entry.ScenarioID]
		if !exists {
			return fmt.Errorf("runtime coverage target %q references unknown scenario %q", target, entry.ScenarioID)
		}
		if kind != entry.Evidence {
			return fmt.Errorf("runtime coverage target %q labels scenario %q as %q, catalog says %q", target, entry.ScenarioID, entry.Evidence, kind)
		}
		if entry.EquivalentTo != "" {
			return fmt.Errorf("terminal runtime coverage target %q also has equivalent_to", target)
		}
		if entry.Evidence == evidenceValidationReject && strings.TrimSpace(entry.Rationale) == "" {
			return fmt.Errorf("validation rejection %q requires a rationale", target)
		}
	case evidenceEquivalence:
		if entry.ScenarioID != "" {
			return fmt.Errorf("equivalent runtime coverage target %q must not name a scenario", target)
		}
		if strings.TrimSpace(entry.Rationale) == "" {
			return fmt.Errorf("equivalent runtime coverage target %q requires a rationale", target)
		}
		if _, exists := entries[entry.EquivalentTo]; !exists {
			return fmt.Errorf("equivalent runtime coverage target %q references missing target %q", target, entry.EquivalentTo)
		}
		visiting[target] = true
		err := validateRuntimeEvidence(entry.EquivalentTo, entries, visiting)
		delete(visiting, target)
		return err
	default:
		return fmt.Errorf("runtime coverage target %q has unsupported evidence %q", target, entry.Evidence)
	}
	return nil
}

func requiredRuntimeCoverageTargets(manifest protocol.CapabilityManifest) map[string]struct{} {
	required := map[string]struct{}{}
	for _, capability := range manifest.Protocols {
		kind := string(capability.Kind)
		required["protocol/"+kind] = struct{}{}
		for _, component := range capability.Components {
			if component.Group == protocol.ComponentTransport || component.Group == protocol.ComponentSecurity {
				required["component/"+kind+"/"+string(component.Group)+"/"+component.Kind] = struct{}{}
			}
			base := "interaction/" + kind + "/" + string(component.Group) + "/" + component.Kind
			for _, reference := range component.Conflicts {
				required[base+"/conflicts/"+reference] = struct{}{}
			}
			for _, reference := range component.Requires {
				required[base+"/requires/"+reference] = struct{}{}
			}
		}
		for _, field := range capability.UserFields {
			addAuthenticationTargets(required, kind, field)
		}
		for _, field := range allCapabilityFields(capability) {
			if !isCoreCipherField(field) {
				continue
			}
			addOptionTargets(required, "cipher-option/"+kind+"/"+field.Path, field.Options)
		}
	}
	return required
}

func addAuthenticationTargets(required map[string]struct{}, protocolKind string, field protocol.FieldCapability) {
	if isCoreCipherField(field) {
		return
	}
	base := "authentication/" + protocolKind + "/" + field.Path
	addOptionTargets(required, base, field.Options)
}

func addOptionTargets(required map[string]struct{}, base string, options []string) {
	if len(options) == 0 {
		required[base] = struct{}{}
		return
	}
	for _, option := range options {
		if option == "" {
			option = "<empty>"
		}
		required[base+"/"+option] = struct{}{}
	}
}

func allCapabilityFields(capability protocol.ProtocolCapability) []protocol.FieldCapability {
	fields := append([]protocol.FieldCapability(nil), capability.Fields...)
	fields = append(fields, capability.UserFields...)
	for _, component := range capability.Components {
		fields = append(fields, component.Fields...)
	}
	return fields
}

func isCoreCipherField(field protocol.FieldCapability) bool {
	identity := strings.ToLower(field.Path + " " + field.SourceKey)
	return strings.Contains(identity, "cipher") ||
		strings.Contains(identity, "shadowsocks.method") ||
		strings.Contains(identity, "ss-option.method")
}
