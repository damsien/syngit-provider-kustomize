package kustomizeprovider

import (
	"fmt"
	"path"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/api/resource"
)

const generatorHashLength = 10

type builtResource struct {
	Object *unstructured.Unstructured
	Owner  Owner
	// OriginPath is the repo path of the file declaring the resource; empty for
	// generator output and for resources outside the bundle.
	OriginPath string
	// OriginalName is the name as the overlay's patches see it: without the
	// overlay's namePrefix/nameSuffix, and the generator name for generated objects.
	OriginalName string
}

type resourceIndex []builtResource

// index indexes m, one of b's builds. krusty drops the previous names of the
// resources, so original names are rebuilt from the overlay's kustomization.
func (b overlayBuild) index(m resmap.ResMap) (resourceIndex, error) {
	_, kustomization, err := readKustomization(b.fSys, b.dir())
	if err != nil {
		return nil, err
	}
	prefix, _ := kustomization["namePrefix"].(string)
	suffix, _ := kustomization["nameSuffix"].(string)

	index := make(resourceIndex, 0, m.Size())
	for _, res := range m.Resources() {
		// Through JSON, since res.Map() holds int values that unstructured rejects.
		content, err := res.MarshalJSON()
		if err != nil {
			return nil, err
		}
		object := &unstructured.Unstructured{}
		if err := object.UnmarshalJSON(content); err != nil {
			return nil, err
		}
		origin, err := res.GetOrigin()
		if err != nil {
			return nil, fmt.Errorf("origin of %s: %w", res.CurId(), err)
		}

		owner, originPath := classifyOrigin(origin, b.root, b.overlay)
		name := strings.TrimPrefix(object.GetName(), prefix)
		if owner == OwnerGenerator {
			name, err = b.generatorName(origin, name, suffix)
			if err != nil {
				return nil, err
			}
		} else {
			name = strings.TrimSuffix(name, suffix)
		}
		index = append(index, builtResource{
			Object:       object,
			Owner:        owner,
			OriginPath:   originPath,
			OriginalName: name,
		})
	}
	return index, nil
}

// generatorName finds the generator declared in origin.ConfiguredIn that
// produced name, which is <generator><suffix>, followed by -<hash> unless
// the hash is disabled.
func (b overlayBuild) generatorName(origin *resource.Origin, name, suffix string) (string, error) {
	_, kustomization, err := readKustomization(b.fSys, path.Dir(path.Join(b.dir(), origin.ConfiguredIn)))
	if err != nil {
		return "", err
	}
	field := "configMapGenerator"
	if origin.ConfiguredBy.Kind == "SecretGenerator" {
		field = "secretGenerator"
	}
	generators, _ := kustomization[field].([]any)
	for _, generator := range generators {
		generatorName, _ := generator.(map[string]any)["name"].(string)
		unhashed := generatorName + suffix
		if name == unhashed ||
			strings.HasPrefix(name, unhashed+"-") && len(name) == len(unhashed)+1+generatorHashLength {
			return generatorName, nil
		}
	}
	return "", fmt.Errorf("no %s in %s produces %s", field, origin.ConfiguredIn, name)
}

// match finds the built resource for a live object. A built resource without
// namespace matches the live object whatever its namespace, since
// `kubectl apply -n` sets it at apply time.
func (index resourceIndex) match(live *unstructured.Unstructured) (builtResource, bool) {
	for _, built := range index {
		if built.Object.GroupVersionKind() != live.GroupVersionKind() ||
			built.Object.GetName() != live.GetName() {
			continue
		}
		if ns := built.Object.GetNamespace(); ns == "" || ns == live.GetNamespace() {
			return built, true
		}
	}
	return builtResource{}, false
}

// classifyOrigin maps an origin, whose path is relative to the overlay
// directory, to its owner and repo path.
func classifyOrigin(origin *resource.Origin, root, overlay string) (Owner, string) {
	if origin == nil || origin.Repo != "" {
		return OwnerNone, ""
	}
	if kind := origin.ConfiguredBy.Kind; kind == "ConfigMapGenerator" || kind == "SecretGenerator" {
		return OwnerGenerator, ""
	}
	if origin.Path == "" {
		return OwnerNone, ""
	}

	rel := path.Join("overlays", overlay, origin.Path)
	repoPath := path.Join(root, rel)
	switch {
	case strings.HasPrefix(rel, "base/"):
		return OwnerBase, repoPath
	case strings.HasPrefix(rel, "components/"):
		return OwnerComponent, repoPath
	case strings.HasPrefix(rel, path.Join("overlays", overlay)+"/"):
		return OwnerOverlay, repoPath
	}
	return OwnerNone, ""
}

// generatorNames maps each generated name (prefix, suffix and hash included)
// to the generator name it comes from.
func generatorNames(index resourceIndex) map[string]string {
	names := map[string]string{}
	for _, built := range index {
		if built.Owner == OwnerGenerator {
			names[built.Object.GetName()] = built.OriginalName
		}
	}
	return names
}
