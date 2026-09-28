package kustomizeprovider

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// These annotations come from the provider's own build.
var buildMetadataAnnotations = []string{
	"config.kubernetes.io/origin",
	"alpha.config.kubernetes.io/transformations",
}

// normalizePair normalizes a live object and its built counterpart so
// they can be compared or diffed.
func normalizePair(live, built *unstructured.Unstructured, generatorNames map[string]string) (*unstructured.Unstructured, *unstructured.Unstructured) {
	live, built = normalize(live, generatorNames), normalize(built, generatorNames)
	// The namespace then comes from `kubectl apply -n`, not from git.
	if built.GetNamespace() == "" {
		live.SetNamespace("")
	}
	return live, built
}

func normalize(obj *unstructured.Unstructured, generatorNames map[string]string) *unstructured.Unstructured {
	obj = obj.DeepCopy()
	annotations := obj.GetAnnotations()
	for key := range annotations {
		if strings.HasPrefix(key, "kustomize.syngit.io/") {
			delete(annotations, key)
		}
	}
	for _, key := range buildMetadataAnnotations {
		delete(annotations, key)
	}
	if len(annotations) == 0 {
		annotations = nil
	}
	obj.SetAnnotations(annotations)

	obj.Object = renameGenerated(obj.Object, generatorNames).(map[string]any)
	return obj
}

// renameGenerated replaces every string equal to a generated name with its
// generator name, so a reference that only differs by hash is not a change.
func renameGenerated(value any, generatorNames map[string]string) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			v[key] = renameGenerated(item, generatorNames)
		}
	case []any:
		for i, item := range v {
			v[i] = renameGenerated(item, generatorNames)
		}
	case string:
		if name, ok := generatorNames[v]; ok {
			return name
		}
	}
	return value
}
