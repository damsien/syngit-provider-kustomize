package kustomizeprovider

import (
	"strings"

	"sigs.k8s.io/kustomize/api/types"
)

var (
	labelPaths = [][]string{
		{"metadata", "labels"},
		{"spec", "selector", "matchLabels"},
		{"spec", "selector"},
		{"spec", "template", "metadata", "labels"},
		{"spec", "jobTemplate", "metadata", "labels"},
		{"spec", "jobTemplate", "spec", "template", "metadata", "labels"},
	}
	annotationPaths = [][]string{
		{"metadata", "annotations"},
		{"spec", "template", "metadata", "annotations"},
		{"spec", "jobTemplate", "metadata", "annotations"},
		{"spec", "jobTemplate", "spec", "template", "metadata", "annotations"},
	}
)

func restoreName(object map[string]any, k *types.Kustomization, config KustomizeProviderConfig) {
	if config.BaseName != "" {
		setNested(object, config.BaseName, "metadata", "name")
		return
	}
	if name, ok := nestedString(object, "metadata", "name"); ok {
		setNested(object, strings.TrimSuffix(strings.TrimPrefix(name, k.NamePrefix), k.NameSuffix), "metadata", "name")
	}
}

func removeInjectedNamespace(object map[string]any, k *types.Kustomization) {
	if k.Namespace == "" {
		return
	}
	if namespace, ok := nestedString(object, "metadata", "namespace"); ok && namespace == k.Namespace {
		deleteNested(object, "metadata", "namespace")
	}
}

func removeInjectedLabels(object map[string]any, k *types.Kustomization, config KustomizeProviderConfig) {
	injected := injectedLabels(k)
	removePairs(object, labelPaths, func(key, value string) bool {
		declared, ok := injected[key]
		return (ok && declared == value) || (config.Bundle != "" && value == config.Bundle)
	})
}

func injectedLabels(k *types.Kustomization) map[string]string {
	injected := map[string]string{}
	// commonLabels is deprecated but legacy kustomizations still inject through it.
	for key, value := range k.CommonLabels { //nolint:staticcheck
		injected[key] = value
	}
	for _, label := range k.Labels {
		for key, value := range label.Pairs {
			injected[key] = value
		}
	}
	return injected
}

func removeInjectedAnnotations(object map[string]any, k *types.Kustomization) {
	removePairs(object, annotationPaths, func(key, value string) bool {
		declared, ok := k.CommonAnnotations[key]
		return ok && declared == value
	})
}

func removePairs(object map[string]any, paths [][]string, injected func(key, value string) bool) {
	for _, path := range paths {
		pairs, ok := nestedMap(object, path...)
		if !ok {
			continue
		}
		for key, value := range pairs {
			if text, ok := value.(string); ok && injected(key, text) {
				delete(pairs, key)
			}
		}
	}
}

func pruneEmptyMaps(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			pruneEmptyMaps(child)
			if nested, ok := child.(map[string]any); ok && len(nested) == 0 {
				delete(typed, key)
			}
		}
	case []any:
		for _, item := range typed {
			pruneEmptyMaps(item)
		}
	}
}
