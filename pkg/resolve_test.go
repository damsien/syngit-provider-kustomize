package kustomizeprovider

import (
	"io/fs"
	"os"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const overlayDir = "apps/my-app/overlays/staging"

// fixtureObject returns a copy of a staging object as the cluster holds it.
func fixtureObject(t *testing.T, kind, name string) *unstructured.Unstructured {
	t.Helper()
	for _, built := range fixtureIndex(t) {
		if built.Object.GetKind() == kind && built.Object.GetName() == name {
			obj := built.Object.DeepCopy()
			obj.SetNamespace("default")
			return obj
		}
	}
	t.Fatalf("no %s %s in the fixture build", kind, name)
	return nil
}

func newService(name string, annotations map[string]string) *unstructured.Unstructured {
	obj := liveObject("v1", "Service", "default", name)
	obj.SetLabels(map[string]string{BundleLabel: "my-app", OverlayLabel: "staging"})
	annotations[RootAnnotation] = "apps/my-app"
	obj.SetAnnotations(annotations)
	return obj
}

func setAnnotation(obj *unstructured.Unstructured, key, value string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[key] = value
	obj.SetAnnotations(annotations)
}

type wantDecision struct {
	action        Action
	owner         Owner
	originalName  string
	targetPath    string
	patchStrategy PatchStrategy
}

func checkDecision(t *testing.T, got Decision, want wantDecision) {
	t.Helper()
	if got.Action == ActionRefuse && got.RefuseMessage == "" {
		t.Error("refused without message")
	}
	gotSummary := wantDecision{got.Action, got.Owner, got.OriginalName, got.TargetPath, got.PatchStrategy}
	if gotSummary != want {
		t.Errorf("got %+v (refusal: %q)\nwant %+v", gotSummary, got.RefuseMessage, want)
	}
}

func TestIsHandled(t *testing.T) {
	obj := liveObject("v1", "Service", "default", "my-app")
	if IsHandled(obj) {
		t.Error("object without bundle label is handled")
	}
	obj.SetLabels(map[string]string{BundleLabel: "my-app"})
	if !IsHandled(obj) {
		t.Error("object with bundle label is not handled")
	}
}

func TestResolveFixtureObjects(t *testing.T) {
	scale := func(obj *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(obj.Object, int64(5), "spec", "replicas")
	}
	addSelector := func(obj *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(obj.Object, "web", "spec", "selector", "team")
	}
	removeScrape := func(obj *unstructured.Unstructured) {
		unstructured.RemoveNestedField(obj.Object, "spec", "template", "metadata", "annotations", "prometheus.io/scrape")
	}
	changeLogLevel := func(obj *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(obj.Object, "info", "data", "LOG_LEVEL")
	}
	patchByTarget := fixtureWith(t, map[string]string{
		overlayDir + "/kustomization.yaml": `
namePrefix: staging-
resources: [../../base]
patches:
  - path: service.patch.yaml
    target: {kind: Service, name: my-app}
`,
		overlayDir + "/service.patch.yaml": `[{"op": "replace", "path": "/spec/ports/0/port", "value": 80}]`,
	})

	tests := []struct {
		name        string
		kind        string
		objName     string
		change      func(obj *unstructured.Unstructured)
		annotations map[string]string
		op          admissionv1.Operation
		noFinder    bool
		repo        fs.FS
		want        wantDecision
	}{
		{
			name: "no overlay label",
			kind: "Deployment", objName: "staging-my-app",
			change: func(obj *unstructured.Unstructured) {
				unstructured.RemoveNestedField(obj.Object, "metadata", "labels", OverlayLabel)
			},
			op:   admissionv1.Update,
			want: wantDecision{action: ActionRefuse, owner: OwnerNone},
		},
		{
			name: "no root annotation",
			kind: "Deployment", objName: "staging-my-app",
			change: func(obj *unstructured.Unstructured) {
				unstructured.RemoveNestedField(obj.Object, "metadata", "annotations", RootAnnotation)
			},
			op:   admissionv1.Update,
			want: wantDecision{action: ActionRefuse, owner: OwnerNone},
		},
		{
			name: "unchanged object",
			kind: "Deployment", objName: "staging-my-app",
			op:   admissionv1.Create,
			want: wantDecision{action: ActionNoOp, owner: OwnerBase, originalName: "my-app"},
		},
		{
			name: "base object with an existing patch",
			kind: "Deployment", objName: "staging-my-app",
			change: scale,
			op:     admissionv1.Update,
			want: wantDecision{
				action: ActionPatch, owner: OwnerBase, originalName: "my-app",
				targetPath: overlayDir + "/deployment-my-app.patch.yaml", patchStrategy: PatchStrategyStrategicMerge,
			},
		},
		{
			name: "base object without patch",
			kind: "Service", objName: "staging-my-app",
			change: addSelector,
			op:     admissionv1.Update,
			want: wantDecision{
				action: ActionPatch, owner: OwnerBase, originalName: "my-app",
				targetPath: overlayDir + "/service-my-app.patch.yaml", patchStrategy: PatchStrategyStrategicMerge,
			},
		},
		{
			name: "patch entry with a target",
			kind: "Service", objName: "staging-my-app",
			change: addSelector,
			op:     admissionv1.Update,
			repo:   patchByTarget,
			want: wantDecision{
				action: ActionPatch, owner: OwnerBase, originalName: "my-app",
				targetPath: overlayDir + "/service.patch.yaml", patchStrategy: PatchStrategyStrategicMerge,
			},
		},
		{
			name: "component object",
			kind: "PodDisruptionBudget", objName: "staging-my-app",
			change: func(obj *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(obj.Object, int64(2), "spec", "minAvailable")
			},
			op: admissionv1.Update,
			want: wantDecision{
				action: ActionPatch, owner: OwnerComponent, originalName: "my-app",
				targetPath: overlayDir + "/poddisruptionbudget-my-app.patch.yaml", patchStrategy: PatchStrategyStrategicMerge,
			},
		},
		{
			name: "removal of a component-added field",
			kind: "Deployment", objName: "staging-my-app",
			change: removeScrape,
			op:     admissionv1.Update,
			want: wantDecision{
				action: ActionPatch, owner: OwnerBase, originalName: "my-app",
				targetPath: overlayDir + "/deployment-my-app.patch.yaml", patchStrategy: PatchStrategyJSON6902,
			},
		},
		{
			name: "invalid patch strategy",
			kind: "Deployment", objName: "staging-my-app",
			change:      scale,
			annotations: map[string]string{PatchStrategyAnnotation: "merge"},
			op:          admissionv1.Update,
			want:        wantDecision{action: ActionRefuse, owner: OwnerBase, originalName: "my-app"},
		},
		{
			name: "overlay object",
			kind: "Service", objName: "staging-debug",
			change: addSelector,
			op:     admissionv1.Update,
			want: wantDecision{
				action: ActionWriteResource, owner: OwnerOverlay, originalName: "debug",
				targetPath: overlayDir + "/debug-service.yaml",
			},
		},
		{
			name: "changed generated object",
			kind: "ConfigMap", objName: "staging-app-config-47668c6k28",
			change: changeLogLevel,
			op:     admissionv1.Update,
			want:   wantDecision{action: ActionRefuse, owner: OwnerGenerator, originalName: "app-config"},
		},
		{
			name: "unchanged generated object",
			kind: "ConfigMap", objName: "staging-app-config-47668c6k28",
			op:   admissionv1.Create,
			want: wantDecision{action: ActionNoOp, owner: OwnerGenerator, originalName: "app-config"},
		},
		{
			name: "generated object with a new hash",
			kind: "ConfigMap", objName: "staging-app-config-47668c6k28",
			change: func(obj *unstructured.Unstructured) {
				obj.SetName("staging-app-config-9m2t8c7f4d")
				changeLogLevel(obj)
			},
			op:   admissionv1.Create,
			want: wantDecision{action: ActionRefuse, owner: OwnerNone},
		},
		{
			name: "resource finder disabled without path",
			kind: "Deployment", objName: "staging-my-app",
			change:   scale,
			op:       admissionv1.Update,
			noFinder: true,
			want: wantDecision{
				action: ActionRefuse, owner: OwnerBase, originalName: "my-app", patchStrategy: PatchStrategyStrategicMerge,
			},
		},
		{
			name: "resource finder disabled with path",
			kind: "Deployment", objName: "staging-my-app",
			change:      scale,
			annotations: map[string]string{PathAnnotation: overlayDir + "/replicas.yaml"},
			op:          admissionv1.Update,
			noFinder:    true,
			want: wantDecision{
				action: ActionPatch, owner: OwnerBase, originalName: "my-app",
				targetPath: overlayDir + "/replicas.yaml", patchStrategy: PatchStrategyStrategicMerge,
			},
		},
		{
			name: "path wins over the resource finder",
			kind: "Service", objName: "staging-debug",
			change:      addSelector,
			annotations: map[string]string{PathAnnotation: overlayDir + "/debug/service.yaml"},
			op:          admissionv1.Update,
			want: wantDecision{
				action: ActionWriteResource, owner: OwnerOverlay, originalName: "debug",
				targetPath: overlayDir + "/debug/service.yaml",
			},
		},
		{
			name: "path outside the overlay",
			kind: "Deployment", objName: "staging-my-app",
			change:      scale,
			annotations: map[string]string{PathAnnotation: "apps/my-app/base/deployment.yaml"},
			op:          admissionv1.Update,
			want: wantDecision{
				action: ActionRefuse, owner: OwnerBase, originalName: "my-app", patchStrategy: PatchStrategyStrategicMerge,
			},
		},
		{
			name: "path escaping the overlay",
			kind: "Deployment", objName: "staging-my-app",
			change:      scale,
			annotations: map[string]string{PathAnnotation: overlayDir + "/../../base/deployment.yaml"},
			op:          admissionv1.Update,
			want: wantDecision{
				action: ActionRefuse, owner: OwnerBase, originalName: "my-app", patchStrategy: PatchStrategyStrategicMerge,
			},
		},
		{
			name: "delete overlay object",
			kind: "Service", objName: "staging-debug",
			op: admissionv1.Delete,
			want: wantDecision{
				action: ActionDeleteResource, owner: OwnerOverlay, originalName: "debug",
				targetPath: overlayDir + "/debug-service.yaml",
			},
		},
		{
			name: "delete base object",
			kind: "Deployment", objName: "staging-my-app",
			op:   admissionv1.Delete,
			want: wantDecision{action: ActionRefuse, owner: OwnerBase, originalName: "my-app"},
		},
		{
			name: "delete base object with allow-delete",
			kind: "Deployment", objName: "staging-my-app",
			annotations: map[string]string{AllowDeleteAnnotation: "true"},
			op:          admissionv1.Delete,
			want:        wantDecision{action: ActionNoOp, owner: OwnerBase, originalName: "my-app"},
		},
		{
			name: "delete generated object with allow-delete",
			kind: "ConfigMap", objName: "staging-app-config-47668c6k28",
			annotations: map[string]string{AllowDeleteAnnotation: "true"},
			op:          admissionv1.Delete,
			want:        wantDecision{action: ActionNoOp, owner: OwnerGenerator, originalName: "app-config"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := fixtureObject(t, tt.kind, tt.objName)
			if tt.change != nil {
				tt.change(obj)
			}
			for key, value := range tt.annotations {
				setAnnotation(obj, key, value)
			}
			repo := tt.repo
			if repo == nil {
				repo = os.DirFS("testdata")
			}

			got, err := Resolve(obj, tt.op, repo, !tt.noFinder)
			if err != nil {
				t.Fatal(err)
			}
			checkDecision(t, got, tt.want)
		})
	}
}

func TestResolveObjectsAbsentFromGit(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		op          admissionv1.Operation
		want        wantDecision
	}{
		{
			name:        "new overlay-only object",
			annotations: map[string]string{OverlayOnlyAnnotation: "true"},
			op:          admissionv1.Create,
			want: wantDecision{
				action: ActionWriteResource, owner: OwnerNone, originalName: "extra",
				targetPath: overlayDir + "/service-extra.yaml",
			},
		},
		{
			name:        "new object without overlay-only",
			annotations: map[string]string{},
			op:          admissionv1.Create,
			want:        wantDecision{action: ActionRefuse, owner: OwnerNone},
		},
		{
			name:        "delete",
			annotations: map[string]string{},
			op:          admissionv1.Delete,
			want:        wantDecision{action: ActionNoOp, owner: OwnerNone},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(newService("staging-extra", tt.annotations), tt.op, os.DirFS("testdata"), true)
			if err != nil {
				t.Fatal(err)
			}
			checkDecision(t, got, tt.want)
		})
	}
}

func TestResolveContent(t *testing.T) {
	repo := os.DirFS("testdata")

	t.Run("strategic-merge patch", func(t *testing.T) {
		obj := fixtureObject(t, "Deployment", "staging-my-app")
		_ = unstructured.SetNestedField(obj.Object, int64(5), "spec", "replicas")
		d, err := Resolve(obj, admissionv1.Update, repo, true)
		if err != nil {
			t.Fatal(err)
		}
		if replicas, _, _ := unstructured.NestedInt64(d.StrategicMergePatch, "spec", "replicas"); replicas != 5 {
			t.Errorf("patch replicas = %d, want 5", replicas)
		}
		if d.JSON6902Patch != nil {
			t.Errorf("JSON6902Patch = %v, want nil", d.JSON6902Patch)
		}
	})

	t.Run("json6902 patch", func(t *testing.T) {
		obj := fixtureObject(t, "Deployment", "staging-my-app")
		unstructured.RemoveNestedField(obj.Object, "spec", "template", "metadata", "annotations", "prometheus.io/scrape")
		d, err := Resolve(obj, admissionv1.Update, repo, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.JSON6902Patch) == 0 || d.StrategicMergePatch != nil {
			t.Errorf("got JSON6902Patch %v and StrategicMergePatch %v", d.JSON6902Patch, d.StrategicMergePatch)
		}
	})

	t.Run("new overlay resource", func(t *testing.T) {
		obj := newService("staging-extra", map[string]string{OverlayOnlyAnnotation: "true"})
		d, err := Resolve(obj, admissionv1.Create, repo, true)
		if err != nil {
			t.Fatal(err)
		}
		if d.Resource.GetName() != "extra" || d.Resource.GetNamespace() != "" || len(d.Resource.GetAnnotations()) != 0 {
			t.Errorf("got resource metadata %v", d.Resource.Object["metadata"])
		}
	})
}

func TestResolveUnsupportedOperation(t *testing.T) {
	obj := fixtureObject(t, "Deployment", "staging-my-app")
	if _, err := Resolve(obj, admissionv1.Connect, os.DirFS("testdata"), true); err == nil {
		t.Error("expected an error")
	}
}
