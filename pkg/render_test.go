package kustomizeprovider

import (
	"os"
	"reflect"
	"slices"
	"testing"
	"testing/fstest"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// applyChanges applies changes to repo the way syngit does.
func applyChanges(t *testing.T, repo fstest.MapFS, changes Changes) {
	t.Helper()
	for file, content := range changes.Files {
		repo[file] = &fstest.MapFile{Data: content}
	}
	for _, file := range changes.Deletes {
		delete(repo, file)
	}
	for _, edit := range changes.Edits {
		kustomization := map[string]any{}
		if err := yaml.Unmarshal(repo[edit.Kustomization].Data, &kustomization); err != nil {
			t.Fatal(err)
		}
		entries, _ := kustomization[string(edit.Field)].([]any)
		if edit.Op == EditOpAdd {
			entries = append(entries, edit.Entry)
		} else {
			entries = slices.DeleteFunc(entries, func(entry any) bool { return reflect.DeepEqual(entry, edit.Entry) })
		}
		kustomization[string(edit.Field)] = entries
		content, err := yaml.Marshal(kustomization)
		if err != nil {
			t.Fatal(err)
		}
		repo[edit.Kustomization] = &fstest.MapFile{Data: content}
	}
}

func TestRenderRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		objName string
		change  func(obj *unstructured.Unstructured)
		op      admissionv1.Operation
		action  Action
	}{
		{
			name: "strategic-merge patch replacing an existing one",
			kind: "Deployment", objName: "staging-my-app",
			change: func(obj *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(obj.Object, int64(5), "spec", "replicas")
			},
			op:     admissionv1.Update,
			action: ActionPatch,
		},
		{
			name: "switch to json6902",
			kind: "Deployment", objName: "staging-my-app",
			change: func(obj *unstructured.Unstructured) {
				unstructured.RemoveNestedField(obj.Object, "spec", "template", "metadata", "annotations", "prometheus.io/scrape")
			},
			op:     admissionv1.Update,
			action: ActionPatch,
		},
		{
			name: "back to baseline",
			kind: "Deployment", objName: "staging-my-app",
			change: func(obj *unstructured.Unstructured) {
				containers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
				containers[0].(map[string]any)["image"] = "nginx:1.27"
				_ = unstructured.SetNestedSlice(obj.Object, containers, "spec", "template", "spec", "containers")
			},
			op:     admissionv1.Update,
			action: ActionPatch,
		},
		{
			name: "new patch",
			kind: "Service", objName: "staging-my-app",
			change: func(obj *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(obj.Object, "web", "spec", "selector", "team")
			},
			op:     admissionv1.Update,
			action: ActionPatch,
		},
		{
			name: "component object",
			kind: "ServiceMonitor", objName: "staging-my-app",
			change: func(obj *unstructured.Unstructured) {
				_ = unstructured.SetNestedSlice(obj.Object, []any{map[string]any{"port": "http", "path": "/stats"}}, "spec", "endpoints")
			},
			op:     admissionv1.Update,
			action: ActionPatch,
		},
		{
			name: "overlay resource",
			kind: "Service", objName: "staging-debug",
			change: func(obj *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(obj.Object, "debug", "spec", "selector", "tier")
			},
			op:     admissionv1.Update,
			action: ActionWriteResource,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := fixtureObject(t, tt.kind, tt.objName)
			tt.change(obj)
			repo := fixtureWith(t, nil).(fstest.MapFS)

			result, err := Process(obj, tt.op, repo, true)
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision.Action != tt.action {
				t.Fatalf("action = %s, want %s (refusal: %q)", result.Decision.Action, tt.action, result.Decision.RefuseMessage)
			}
			applyChanges(t, repo, result.Changes)

			again, err := Resolve(obj, tt.op, repo, true)
			if err != nil {
				t.Fatal(err)
			}
			if again.Action != ActionNoOp {
				t.Errorf("after applying the changes, action = %s, want %s", again.Action, ActionNoOp)
			}
		})
	}
}

func TestRenderRoundTripNewResource(t *testing.T) {
	obj := newService("staging-extra", map[string]string{OverlayOnlyAnnotation: "true"})
	_ = unstructured.SetNestedSlice(obj.Object, []any{map[string]any{"port": int64(80)}}, "spec", "ports")
	repo := fixtureWith(t, nil).(fstest.MapFS)

	result, err := Process(obj, admissionv1.Create, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	applyChanges(t, repo, result.Changes)

	again, err := Resolve(obj, admissionv1.Update, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.Action != ActionNoOp || again.Owner != OwnerOverlay {
		t.Errorf("after applying the changes, got (%s, %s), want (%s, %s)", again.Action, again.Owner, ActionNoOp, OwnerOverlay)
	}
}

func TestRenderRoundTripDelete(t *testing.T) {
	obj := fixtureObject(t, "Service", "staging-debug")
	repo := fixtureWith(t, nil).(fstest.MapFS)

	result, err := Process(obj, admissionv1.Delete, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	applyChanges(t, repo, result.Changes)

	again, err := Resolve(obj, admissionv1.Delete, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.Owner != OwnerNone {
		t.Errorf("after applying the changes, owner = %s, want %s", again.Owner, OwnerNone)
	}
}

func TestRenderMultiDocumentFile(t *testing.T) {
	const debugFile = overlayDir + "/debug-service.yaml"
	withConfigMap := func(t *testing.T) fstest.MapFS {
		t.Helper()
		content, err := os.ReadFile("testdata/" + debugFile)
		if err != nil {
			t.Fatal(err)
		}
		return fixtureWith(t, map[string]string{
			debugFile: string(content) + "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: debug\n",
		}).(fstest.MapFS)
	}
	keepsConfigMap := func(t *testing.T, changes Changes) {
		t.Helper()
		kind, name, err := documentID(changes.Files[debugFile])
		if err != nil {
			t.Fatal(err)
		}
		if kind != "ConfigMap" || name != "debug" {
			t.Errorf("file holds %s %s first, want the ConfigMap:\n%s", kind, name, changes.Files[debugFile])
		}
	}

	t.Run("write", func(t *testing.T) {
		obj := fixtureObject(t, "Service", "staging-debug")
		_ = unstructured.SetNestedField(obj.Object, "debug", "spec", "selector", "tier")
		result, err := Process(obj, admissionv1.Update, withConfigMap(t), true)
		if err != nil {
			t.Fatal(err)
		}
		keepsConfigMap(t, result.Changes)
	})

	t.Run("delete", func(t *testing.T) {
		obj := fixtureObject(t, "Service", "staging-debug")
		result, err := Process(obj, admissionv1.Delete, withConfigMap(t), true)
		if err != nil {
			t.Fatal(err)
		}
		keepsConfigMap(t, result.Changes)
		if len(result.Deletes) != 0 || len(result.Edits) != 0 {
			t.Errorf("got deletes %v and edits %v, want none", result.Deletes, result.Edits)
		}
	})
}

func TestProcessNotHandled(t *testing.T) {
	obj := liveObject("v1", "Service", "default", "my-app")
	result, err := Process(obj, admissionv1.Create, os.DirFS("testdata"), true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Handled {
		t.Error("object without bundle label is handled")
	}
}
