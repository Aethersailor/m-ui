package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

var supportedFieldTypes = map[FieldType]struct{}{
	FieldString: {}, FieldText: {}, FieldSecret: {}, FieldBoolean: {},
	FieldInteger: {}, FieldStringList: {}, FieldObjectList: {}, FieldRecord: {},
}

// ValidateCapabilityManifest rejects capability contracts that the generic
// editor could not render or enforce deterministically.
func ValidateCapabilityManifest(manifest CapabilityManifest) error {
	if manifest.SchemaVersion <= 0 || manifest.NodeSchemaVersion <= 0 {
		return fmt.Errorf("capability and node schema versions must be positive")
	}
	if manifest.Source.Repository == "" || manifest.Source.Branch == "" || manifest.Source.Commit == "" {
		return fmt.Errorf("mihomo source contract is incomplete")
	}
	if err := validateFields("node", manifest.NodeFields); err != nil {
		return err
	}
	if err := validateFields("access profile", manifest.AccessProfileFields); err != nil {
		return err
	}

	protocols := make(map[string]struct{}, len(manifest.Protocols))
	for _, capability := range manifest.Protocols {
		kind := string(capability.Kind)
		if kind == "" {
			return fmt.Errorf("protocol kind is empty")
		}
		if _, exists := protocols[kind]; exists {
			return fmt.Errorf("protocol %q is duplicated", kind)
		}
		protocols[kind] = struct{}{}
		if capability.Label == "" {
			return fmt.Errorf("protocol %q label is empty", kind)
		}
		if !json.Valid(capability.DefaultNode) {
			return fmt.Errorf("protocol %q default node is invalid JSON", kind)
		}
		if !json.Valid(capability.DefaultUser) {
			return fmt.Errorf("protocol %q default user is invalid JSON", kind)
		}
		if err := validateProtocolCapability(capability); err != nil {
			return fmt.Errorf("protocol %q: %w", kind, err)
		}
	}
	return nil
}

func validateProtocolCapability(capability ProtocolCapability) error {
	layers := make(map[ComponentGroup]LayerCapability, len(capability.Layers))
	for _, layer := range capability.Layers {
		if layer.Group == "" {
			return fmt.Errorf("layer group is empty")
		}
		if _, exists := layers[layer.Group]; exists {
			return fmt.Errorf("layer %q is duplicated", layer.Group)
		}
		if layer.Locked && layer.Multiple {
			return fmt.Errorf("locked layer %q cannot allow multiple components", layer.Group)
		}
		if layer.Required && layer.DefaultComponent == "" {
			return fmt.Errorf("required layer %q has no default component", layer.Group)
		}
		layers[layer.Group] = layer
	}

	components := make(map[string]ComponentCapability, len(capability.Components))
	for _, component := range capability.Components {
		if _, exists := layers[component.Group]; !exists {
			return fmt.Errorf("component %q uses undeclared layer %q", component.Kind, component.Group)
		}
		if component.Kind == "" || component.Label == "" {
			return fmt.Errorf("component in layer %q has an empty kind or label", component.Group)
		}
		id := componentID(component.Group, component.Kind)
		if _, exists := components[id]; exists {
			return fmt.Errorf("component %q is duplicated", id)
		}
		if !json.Valid(component.DefaultConfig) {
			return fmt.Errorf("component %q default is invalid JSON", id)
		}
		if component.ConfigPath == "" {
			return fmt.Errorf("component %q has no config path", id)
		}
		if component.EnabledPath != "" && component.SelectionPath != "" {
			return fmt.Errorf("component %q cannot use both selection and enabled paths", id)
		}
		if component.SelectionPath != "" && !strings.HasPrefix(component.SelectionPath, component.ConfigPath+".") {
			return fmt.Errorf("component %q selection path is outside config path", id)
		}
		if component.EnabledPath != "" && !strings.HasPrefix(component.EnabledPath, component.ConfigPath+".") {
			return fmt.Errorf("component %q enabled path is outside config path", id)
		}
		for _, field := range component.Fields {
			if field.Path == component.ConfigPath || strings.HasPrefix(field.Path, component.ConfigPath+".") {
				return fmt.Errorf("component %q field %q is not relative to config path", id, field.Path)
			}
		}
		if err := validateFields("component "+id, component.Fields); err != nil {
			return err
		}
		components[id] = component
	}

	for group, layer := range layers {
		if layer.DefaultComponent == "" {
			continue
		}
		if _, exists := components[componentID(group, layer.DefaultComponent)]; !exists {
			return fmt.Errorf("layer %q references missing default component %q", group, layer.DefaultComponent)
		}
	}
	for id, component := range components {
		for _, reference := range append(append([]string{}, component.Requires...), component.Conflicts...) {
			if _, exists := components[reference]; !exists {
				return fmt.Errorf("component %q references missing component %q", id, reference)
			}
			if reference == id {
				return fmt.Errorf("component %q references itself", id)
			}
			if _, _, ok := strings.Cut(reference, ":"); !ok {
				return fmt.Errorf("component reference %q is not group:kind", reference)
			}
		}
	}
	if err := validateFields("protocol "+string(capability.Kind), capability.Fields); err != nil {
		return err
	}
	return validateFields("protocol user "+string(capability.Kind), capability.UserFields)
}

func validateFields(scope string, fields []FieldCapability) error {
	paths := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field.Path == "" || field.Label == "" {
			return fmt.Errorf("%s has a field with an empty path or label", scope)
		}
		if _, exists := paths[field.Path]; exists {
			return fmt.Errorf("%s field %q is duplicated", scope, field.Path)
		}
		paths[field.Path] = struct{}{}
		if _, supported := supportedFieldTypes[field.Type]; !supported {
			return fmt.Errorf("%s field %q has unsupported type %q", scope, field.Path, field.Type)
		}
		if field.Type == FieldSecret && !field.Secret {
			return fmt.Errorf("%s secret field %q is not marked secret", scope, field.Path)
		}
		complex := field.Type == FieldObjectList || field.Type == FieldRecord
		if complex && len(field.ItemFields) == 0 {
			return fmt.Errorf("%s complex field %q has no item fields", scope, field.Path)
		}
		if !complex && len(field.ItemFields) != 0 {
			return fmt.Errorf("%s scalar field %q declares item fields", scope, field.Path)
		}
		if field.Type != FieldRecord && field.ItemKeyLabel != "" {
			return fmt.Errorf("%s non-record field %q declares an item key label", scope, field.Path)
		}
		if complex {
			if err := validateFields(scope+" field "+field.Path+" item", field.ItemFields); err != nil {
				return err
			}
		}
	}
	for _, field := range fields {
		if field.VisibleWhen == nil {
			continue
		}
		if _, exists := paths[field.VisibleWhen.Path]; !exists {
			return fmt.Errorf("%s field %q visibility references missing field %q", scope, field.Path, field.VisibleWhen.Path)
		}
	}
	return nil
}

func componentID(group ComponentGroup, kind string) string {
	return string(group) + ":" + kind
}
