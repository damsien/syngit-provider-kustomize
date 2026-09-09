package kustomizeprovider

import (
	"bytes"
	"errors"
	"fmt"

	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/yaml"
)

var (
	ErrEmptyResource        = errors.New("the intercepted resource is empty")
	ErrEmptyBaseResource    = errors.New("the base resource is required to build an overlay patch")
	ErrUnknownOverride      = errors.New("unknown override type")
	ErrUnknownPatchStrategy = errors.New("unknown patch strategy")
)

// Convert turns an intercepted resource, as `kustomize build` rendered it into
// the cluster, back into the file the repository holds: the base resource, or
// the patch carrying its difference against baseResource.
//
// kustomizationManifest declares the transformations to undo, and in a
// base/overlay layout is the overlay's, ie. DetectOverlay's OverlayTarget.Raw.
func Convert(
	config KustomizeProviderConfig,
	resource, baseResource, kustomizationManifest, overlayPatch []byte,
) ([]byte, error) {
	live, err := parseYAML(resource)
	if err != nil {
		return nil, fmt.Errorf("failed to parse the intercepted resource: %w", err)
	}
	if live == nil {
		return nil, ErrEmptyResource
	}

	kustomization, err := ParseKustomization(kustomizationManifest)
	if err != nil {
		return nil, err
	}
	stripped := deKustomize(live, kustomization, config)

	switch config.Override {
	case Base:
		return marshal(stripped)
	case Overlay:
		return overlayPatchFor(config, stripped, baseResource, overlayPatch)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownOverride, config.Override)
	}
}

func ParseKustomization(manifest []byte) (*types.Kustomization, error) {
	kustomization := &types.Kustomization{}
	if len(bytes.TrimSpace(manifest)) == 0 {
		return kustomization, nil
	}
	if err := kustomization.Unmarshal(manifest); err != nil {
		return nil, fmt.Errorf("failed to parse the kustomization: %w", err)
	}
	kustomization.FixKustomization()
	return kustomization, nil
}

func overlayPatchFor(config KustomizeProviderConfig, stripped map[string]any, baseResource, overlayPatch []byte) ([]byte, error) {
	base, err := parseYAML(baseResource)
	if err != nil {
		return nil, fmt.Errorf("failed to parse the base resource: %w", err)
	}
	if base == nil {
		return nil, ErrEmptyBaseResource
	}

	switch config.PatchStrategy {
	case StrategicMerge:
		existing, err := parseYAML(overlayPatch)
		if err != nil {
			return nil, fmt.Errorf("failed to parse the existing overlay patch: %w", err)
		}

		patch := mergePatch(base, stripped)
		if existing != nil {
			patch = overlayMaps(existing, patch)
		}
		setPatchIdentity(patch, base)
		return marshal(patch)

	case JSON6902:
		ops := jsonPatchOperations(base, stripped)
		if len(ops) == 0 {
			return []byte("[]\n"), nil
		}
		return marshal(ops)

	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownPatchStrategy, config.PatchStrategy)
	}
}

func setPatchIdentity(patch, base map[string]any) {
	for _, field := range []string{"apiVersion", "kind"} {
		if value, ok := base[field]; ok {
			patch[field] = value
		}
	}
	for _, field := range []string{"name", "namespace"} {
		if value, ok := nestedString(base, "metadata", field); ok {
			setNested(patch, value, "metadata", field)
		}
	}
}

// Strips from the live resource what `kustomize build`, leaving the copy the repository holds.
func deKustomize(live map[string]any, k *types.Kustomization, config KustomizeProviderConfig) map[string]any {
	object := deepCopy(live).(map[string]any)

	restoreName(object, k, config)
	removeInjectedNamespace(object, k)
	removeInjectedLabels(object, k, config)
	removeInjectedAnnotations(object, k)
	pruneEmptyMaps(object)

	return object
}

func parseYAML(raw []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	object := map[string]any{}
	if err := yaml.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	return object, nil
}

func marshal(value any) ([]byte, error) {
	out, err := yaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal the result: %w", err)
	}
	return out, nil
}

func nestedMap(object map[string]any, path ...string) (map[string]any, bool) {
	for _, field := range path {
		child, ok := object[field].(map[string]any)
		if !ok {
			return nil, false
		}
		object = child
	}
	return object, true
}

func nestedString(object map[string]any, path ...string) (string, bool) {
	parent, ok := nestedMap(object, path[:len(path)-1]...)
	if !ok {
		return "", false
	}
	value, ok := parent[path[len(path)-1]].(string)
	return value, ok
}

// Creates the missing maps along the path, replacing any non-map value on the way.
func setNested(object map[string]any, value any, path ...string) {
	for _, field := range path[:len(path)-1] {
		child, ok := object[field].(map[string]any)
		if !ok {
			child = map[string]any{}
			object[field] = child
		}
		object = child
	}
	object[path[len(path)-1]] = value
}

// No-op when any map along the path is missing.
func deleteNested(object map[string]any, path ...string) {
	if parent, ok := nestedMap(object, path[:len(path)-1]...); ok {
		delete(parent, path[len(path)-1])
	}
}

func deepCopy(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copied := make(map[string]any, len(typed))
		for key, item := range typed {
			copied[key] = deepCopy(item)
		}
		return copied
	case []any:
		copied := make([]any, len(typed))
		for index, item := range typed {
			copied[index] = deepCopy(item)
		}
		return copied
	default:
		return value
	}
}

func isScalar(value any) bool {
	switch value.(type) {
	case string, float64, bool:
		return true
	default:
		return false
	}
}
