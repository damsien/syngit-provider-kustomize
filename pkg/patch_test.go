package kustomizeprovider

import (
	"os"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// fixtureBaseline returns the baseline of a staging object, and the generator
// name map of the full build.
func fixtureBaseline(t *testing.T, apiVersion, kind, name string) (*unstructured.Unstructured, map[string]string) {
	t.Helper()
	b, err := buildOverlay(os.DirFS("testdata"), fixtureRoot, fixtureOverlay)
	if err != nil {
		t.Fatal(err)
	}
	full, err := b.index(b.full)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := b.index(b.baseline)
	if err != nil {
		t.Fatal(err)
	}
	built, ok := baseline.match(liveObject(apiVersion, kind, "default", name))
	if !ok {
		t.Fatalf("no %s %s in the baseline", kind, name)
	}
	return built.Object, generatorNames(full)
}

// normalizedDiffInputs applies change to a copy of the baseline, as a live
// object in the default namespace, and normalizes both.
func normalizedDiffInputs(t *testing.T, apiVersion, kind string, change func(live map[string]any)) (live, baseline *unstructured.Unstructured) {
	t.Helper()
	baseline, names := fixtureBaseline(t, apiVersion, kind, "staging-my-app")
	live = baseline.DeepCopy()
	live.SetNamespace("default")
	change(live.Object)
	return normalizePair(live, baseline, names)
}

func setField(value any, fields ...string) func(map[string]any) {
	return func(obj map[string]any) {
		if err := unstructured.SetNestedField(obj, value, fields...); err != nil {
			panic(err)
		}
	}
}

func setImage(image string) func(map[string]any) {
	return func(obj map[string]any) {
		containers, _, _ := unstructured.NestedSlice(obj, "spec", "template", "spec", "containers")
		containers[0].(map[string]any)["image"] = image
		setField(containers, "spec", "template", "spec", "containers")(obj)
	}
}

func TestStrategicMergePatch(t *testing.T) {
	tests := []struct {
		name       string
		apiVersion string
		kind       string
		change     func(map[string]any)
		want       string
	}{
		{
			name:       "no change",
			apiVersion: "apps/v1", kind: "Deployment",
			change: func(map[string]any) {},
			want:   `null`,
		},
		{
			name:       "scalar change",
			apiVersion: "apps/v1", kind: "Deployment",
			change: setField(int64(5), "spec", "replicas"),
			want: `
apiVersion: apps/v1
kind: Deployment
metadata: {name: my-app}
spec: {replicas: 5}
`,
		},
		{
			name:       "map addition",
			apiVersion: "v1", kind: "Service",
			change: setField("web", "spec", "selector", "team"),
			want: `
apiVersion: v1
kind: Service
metadata: {name: my-app}
spec:
  selector: {team: web}
`,
		},
		{
			name:       "list item change",
			apiVersion: "apps/v1", kind: "Deployment",
			change: setImage("nginx:1.28"),
			want: `
apiVersion: apps/v1
kind: Deployment
metadata: {name: my-app}
spec:
  template:
    spec:
      $setElementOrder/containers: [{name: app}]
      containers: [{name: app, image: "nginx:1.28"}]
`,
		},
		{
			name:       "generated reference is written as the generator name",
			apiVersion: "apps/v1", kind: "Deployment",
			change: func(obj map[string]any) {
				containers, _, _ := unstructured.NestedSlice(obj, "spec", "template", "spec", "containers")
				container := containers[0].(map[string]any)
				container["envFrom"] = append(container["envFrom"].([]any), map[string]any{"secretRef": map[string]any{"name": "creds"}})
				setField(containers, "spec", "template", "spec", "containers")(obj)
			},
			want: `
apiVersion: apps/v1
kind: Deployment
metadata: {name: my-app}
spec:
  template:
    spec:
      $setElementOrder/containers: [{name: app}]
      containers:
        - name: app
          envFrom: [{configMapRef: {name: app-config}}, {secretRef: {name: creds}}]
`,
		},
		{
			name:       "kind without Go type replaces lists",
			apiVersion: "monitoring.coreos.com/v1", kind: "ServiceMonitor",
			change: setField([]any{map[string]any{"port": "http", "path": "/stats"}}, "spec", "endpoints"),
			want: `
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata: {name: my-app}
spec:
  endpoints: [{port: http, path: /stats}]
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			live, baseline := normalizedDiffInputs(t, tt.apiVersion, tt.kind, tt.change)
			got, err := strategicMergePatch(live, baseline, "my-app")
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := yaml.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				gotYAML, _ := yaml.Marshal(got)
				t.Errorf("got:\n%s\nwant:\n%s", gotYAML, tt.want)
			}
		})
	}
}

func TestJSON6902Patch(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any)
		want   string
	}{
		{
			name:   "no change",
			change: func(map[string]any) {},
			want:   `null`,
		},
		{
			name:   "scalar change",
			change: setField(int64(5), "spec", "replicas"),
			want:   `[{op: replace, path: /spec/replicas, value: 5}]`,
		},
		{
			name: "removal of a component-added field",
			change: func(obj map[string]any) {
				unstructured.RemoveNestedField(obj, "spec", "template", "metadata", "annotations", "prometheus.io/scrape")
			},
			want: `[{op: remove, path: /spec/template/metadata/annotations/prometheus.io~1scrape}]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			live, baseline := normalizedDiffInputs(t, "apps/v1", "Deployment", tt.change)
			operations, err := json6902Patch(live, baseline)
			if err != nil {
				t.Fatal(err)
			}
			got, err := yaml.Marshal(operations)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(yamlValue(t, string(got)), yamlValue(t, tt.want)) {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func yamlValue(t *testing.T, content string) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal([]byte(content), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPatchStrategy(t *testing.T) {
	removeScrape := func(obj map[string]any) {
		unstructured.RemoveNestedField(obj, "spec", "template", "metadata", "annotations", "prometheus.io/scrape")
	}
	tests := []struct {
		name       string
		annotation string
		change     func(map[string]any)
		want       PatchStrategy
		wantErr    bool
	}{
		{name: "change defaults to strategic-merge", change: setField(int64(5), "spec", "replicas"), want: PatchStrategyStrategicMerge},
		{name: "removal switches to json6902", change: removeScrape, want: PatchStrategyJSON6902},
		{name: "forced json6902", annotation: "json6902", change: setField(int64(5), "spec", "replicas"), want: PatchStrategyJSON6902},
		{name: "forced strategic-merge on a removal", annotation: "strategic-merge", change: removeScrape, want: PatchStrategyStrategicMerge},
		{name: "invalid annotation", annotation: "merge", change: removeScrape, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			live, baseline := normalizedDiffInputs(t, "apps/v1", "Deployment", tt.change)
			operations, err := json6902Patch(live, baseline)
			if err != nil {
				t.Fatal(err)
			}
			obj := live.DeepCopy()
			if tt.annotation != "" {
				obj.SetAnnotations(map[string]string{PatchStrategyAnnotation: tt.annotation})
			}

			got, err := patchStrategy(obj, operations)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
