package kustomizeprovider

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// mergeKeys are the patchMergeKey values Kubernetes declares on its list fields.
var mergeKeys = []string{"name", "mountPath", "devicePath", "containerPort", "port", "topologyKey", "ip", "key"}

const deleteDirective = "$patch"

// mergePatch returns the strategic merge patch turning base into target.
func mergePatch(base, target map[string]any) map[string]any {
	patch := map[string]any{}

	for key, targetValue := range target {
		baseValue, inBase := base[key]
		if !inBase {
			patch[key] = targetValue
			continue
		}

		switch typedTarget := targetValue.(type) {
		case map[string]any:
			typedBase, ok := baseValue.(map[string]any)
			if !ok {
				patch[key] = targetValue
			} else if nested := mergePatch(typedBase, typedTarget); len(nested) > 0 {
				patch[key] = nested
			}
		case []any:
			typedBase, ok := baseValue.([]any)
			if !ok {
				patch[key] = targetValue
			} else if nested, changed := mergeListPatch(typedBase, typedTarget); changed {
				patch[key] = nested
			}
		default:
			if !reflect.DeepEqual(baseValue, targetValue) {
				patch[key] = targetValue
			}
		}
	}

	for key := range base {
		if _, ok := target[key]; !ok {
			patch[key] = nil
		}
	}

	return patch
}

func mergeListPatch(base, target []any) (any, bool) {
	mergeKey, ok := listMergeKey(base, target)
	if !ok {
		if reflect.DeepEqual(base, target) {
			return nil, false
		}
		return target, true
	}

	baseByKey := make(map[any]map[string]any, len(base))
	for _, entry := range base {
		typed := entry.(map[string]any)
		baseByKey[typed[mergeKey]] = typed
	}

	patch := []any{}
	patched := make(map[any]bool, len(target))

	for _, entry := range target {
		typed := entry.(map[string]any)
		identity := typed[mergeKey]
		patched[identity] = true

		baseEntry, inBase := baseByKey[identity]
		if !inBase {
			patch = append(patch, typed)
		} else if nested := mergePatch(baseEntry, typed); len(nested) > 0 {
			nested[mergeKey] = identity
			patch = append(patch, nested)
		}
	}

	for _, entry := range base {
		typed := entry.(map[string]any)
		if identity := typed[mergeKey]; !patched[identity] {
			patch = append(patch, map[string]any{mergeKey: identity, deleteDirective: "delete"})
		}
	}

	if len(patch) == 0 {
		return nil, false
	}
	return patch, true
}

func listMergeKey(base, target []any) (string, bool) {
	if len(base) == 0 || len(target) == 0 {
		return "", false
	}
	for _, key := range mergeKeys {
		if identifies(base, key) && identifies(target, key) {
			return key, true
		}
	}
	return "", false
}

func identifies(list []any, key string) bool {
	seen := make(map[any]bool, len(list))
	for _, entry := range list {
		object, ok := entry.(map[string]any)
		if !ok {
			return false
		}
		value, ok := object[key]
		if !ok || !isScalar(value) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func overlayMaps(existing, patch map[string]any) map[string]any {
	merged := deepCopy(existing).(map[string]any)
	for key, patchValue := range patch {
		existingMap, existingIsMap := merged[key].(map[string]any)
		patchMap, patchIsMap := patchValue.(map[string]any)
		if existingIsMap && patchIsMap {
			merged[key] = overlayMaps(existingMap, patchMap)
		} else {
			merged[key] = deepCopy(patchValue)
		}
	}
	return merged
}

type JSONPatchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

func (o JSONPatchOperation) MarshalJSON() ([]byte, error) {
	if o.Op == "remove" {
		return json.Marshal(struct {
			Op   string `json:"op"`
			Path string `json:"path"`
		}{Op: o.Op, Path: o.Path})
	}
	type operation JSONPatchOperation
	return json.Marshal(operation(o))
}

// jsonPatchOperations returns the RFC 6902 operations turning base into target.
func jsonPatchOperations(base, target map[string]any) []JSONPatchOperation {
	return appendDiff(nil, "", base, target)
}

func appendDiff(ops []JSONPatchOperation, pointer string, base, target any) []JSONPatchOperation {
	replace := func() []JSONPatchOperation {
		return append(ops, JSONPatchOperation{Op: "replace", Path: pointer, Value: target})
	}

	switch typedBase := base.(type) {
	case map[string]any:
		typedTarget, ok := target.(map[string]any)
		if !ok {
			return replace()
		}
		return appendMapDiff(ops, pointer, typedBase, typedTarget)

	case []any:
		typedTarget, ok := target.([]any)
		if !ok {
			return replace()
		}
		// Entries only line up while the list keeps its length; adding or removing
		// one shifts every later index.
		if len(typedBase) != len(typedTarget) {
			if reflect.DeepEqual(typedBase, typedTarget) {
				return ops
			}
			return replace()
		}
		for index := range typedBase {
			ops = appendDiff(ops, pointer+"/"+strconv.Itoa(index), typedBase[index], typedTarget[index])
		}
		return ops

	default:
		if reflect.DeepEqual(base, target) {
			return ops
		}
		return replace()
	}
}

func appendMapDiff(ops []JSONPatchOperation, pointer string, base, target map[string]any) []JSONPatchOperation {
	for _, key := range sortedKeys(target) {
		childPointer := pointer + "/" + escapePointerToken(key)
		if baseValue, inBase := base[key]; inBase {
			ops = appendDiff(ops, childPointer, baseValue, target[key])
		} else {
			ops = append(ops, JSONPatchOperation{Op: "add", Path: childPointer, Value: target[key]})
		}
	}
	for _, key := range sortedKeys(base) {
		if _, ok := target[key]; !ok {
			ops = append(ops, JSONPatchOperation{Op: "remove", Path: pointer + "/" + escapePointerToken(key)})
		}
	}
	return ops
}

func escapePointerToken(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
