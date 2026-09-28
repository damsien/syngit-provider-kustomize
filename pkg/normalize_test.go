package kustomizeprovider

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func objectFromYAML(t *testing.T, content string) *unstructured.Unstructured {
	t.Helper()
	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal([]byte(content), &obj.Object); err != nil {
		t.Fatal(err)
	}
	return obj
}

func TestNormalize(t *testing.T) {
	obj := objectFromYAML(t, `
apiVersion: v1
kind: Service
metadata:
  name: staging-my-app
  namespace: default
  labels: {app: my-app}
  annotations:
    config.kubernetes.io/origin: "path: service.yaml"
    alpha.config.kubernetes.io/transformations: "[]"
    kustomize.syngit.io/root: apps/my-app
    team: web
spec:
  selector: {app: staging-app-config-47668c6k28}
`)
	want := objectFromYAML(t, `
apiVersion: v1
kind: Service
metadata:
  name: staging-my-app
  namespace: default
  labels: {app: my-app}
  annotations: {team: web}
spec:
  selector: {app: app-config}
`)

	got := normalize(obj, map[string]string{"staging-app-config-47668c6k28": "app-config"})
	if !reflect.DeepEqual(got.Object, want.Object) {
		t.Errorf("got %v, want %v", got.Object, want.Object)
	}
	if len(obj.GetAnnotations()) != 4 {
		t.Error("normalize modified its argument")
	}
}

func TestNormalizeDropsEmptyAnnotations(t *testing.T) {
	obj := objectFromYAML(t, `
apiVersion: v1
kind: Service
metadata:
  name: my-app
  annotations: {kustomize.syngit.io/root: apps/my-app}
`)
	metadata := normalize(obj, nil).Object["metadata"].(map[string]any)
	if _, ok := metadata["annotations"]; ok {
		t.Error("empty annotations kept")
	}
}

func TestNormalizePairNamespace(t *testing.T) {
	tests := []struct {
		name              string
		builtNamespace    string
		wantLiveNamespace string
	}{
		{name: "built without namespace", builtNamespace: "", wantLiveNamespace: ""},
		{name: "built with namespace", builtNamespace: "team", wantLiveNamespace: "other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			live := liveObject("v1", "Service", "other", "my-app")
			built := liveObject("v1", "Service", tt.builtNamespace, "my-app")
			live, _ = normalizePair(live, built, nil)
			if got := live.GetNamespace(); got != tt.wantLiveNamespace {
				t.Errorf("live namespace = %q, want %q", got, tt.wantLiveNamespace)
			}
		})
	}
}
