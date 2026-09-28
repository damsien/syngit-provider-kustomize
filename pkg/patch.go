package kustomizeprovider

import (
	"encoding/json"
	"fmt"

	"gomodules.xyz/jsonpatch/v2"
	jsonmergepatch "gopkg.in/evanphx/json-patch.v4"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/kubernetes/scheme"
)

// strategicMergePatch diffs two normalized objects into a patch document
// targeting originalName. It returns nil when they are identical.
func strategicMergePatch(live, baseline *unstructured.Unstructured, originalName string) (map[string]any, error) {
	liveJSON, baselineJSON, err := marshalPair(live, baseline)
	if err != nil {
		return nil, err
	}

	var diff []byte
	if typed, err := scheme.Scheme.New(live.GroupVersionKind()); err == nil {
		diff, err = strategicpatch.CreateTwoWayMergePatch(baselineJSON, liveJSON, typed)
		if err != nil {
			return nil, err
		}
	} else {
		// No Go type, so no patch merge keys: lists are replaced whole.
		diff, err = jsonmergepatch.CreateMergePatch(baselineJSON, liveJSON)
		if err != nil {
			return nil, err
		}
	}

	patch := map[string]any{}
	if err := json.Unmarshal(diff, &patch); err != nil {
		return nil, err
	}
	if len(patch) == 0 {
		return nil, nil
	}
	patch["apiVersion"] = live.GetAPIVersion()
	patch["kind"] = live.GetKind()
	if err := unstructured.SetNestedField(patch, originalName, "metadata", "name"); err != nil {
		return nil, err
	}
	return patch, nil
}

// json6902Patch diffs two normalized objects into JSON6902 operations. It
// returns nil when they are identical.
func json6902Patch(live, baseline *unstructured.Unstructured) ([]jsonpatch.Operation, error) {
	liveJSON, baselineJSON, err := marshalPair(live, baseline)
	if err != nil {
		return nil, err
	}
	operations, err := jsonpatch.CreatePatch(baselineJSON, liveJSON)
	if err != nil || len(operations) == 0 {
		return nil, err
	}
	return operations, nil
}

// patchStrategy picks the patch format: the annotation of obj if set,
// else JSON6902 when operations remove something, which a strategic-merge
// patch cannot express without directives.
func patchStrategy(obj *unstructured.Unstructured, operations []jsonpatch.Operation) (PatchStrategy, error) {
	if value, ok := obj.GetAnnotations()[PatchStrategyAnnotation]; ok {
		switch strategy := PatchStrategy(value); strategy {
		case PatchStrategyStrategicMerge, PatchStrategyJSON6902:
			return strategy, nil
		}
		return "", fmt.Errorf("invalid %s annotation %q: must be %q or %q",
			PatchStrategyAnnotation, value, PatchStrategyStrategicMerge, PatchStrategyJSON6902)
	}
	for _, operation := range operations {
		if operation.Operation == "remove" {
			return PatchStrategyJSON6902, nil
		}
	}
	return PatchStrategyStrategicMerge, nil
}

func marshalPair(live, baseline *unstructured.Unstructured) ([]byte, []byte, error) {
	liveJSON, err := live.MarshalJSON()
	if err != nil {
		return nil, nil, err
	}
	baselineJSON, err := baseline.MarshalJSON()
	if err != nil {
		return nil, nil, err
	}
	return liveJSON, baselineJSON, nil
}
