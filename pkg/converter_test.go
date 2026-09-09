package kustomizeprovider

import (
	"errors"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const baseKustomization = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namePrefix: prod-
nameSuffix: -v2
namespace: production
labels:
  - pairs:
      app.kubernetes.io/part-of: myapp
    includeSelectors: true
commonAnnotations:
  managed-by: syngit
resources:
  - deployment.yaml
`

// The cluster object: built by kustomize, plus drift against the base.
const liveDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: prod-web-v2
  namespace: production
  labels: {app.kubernetes.io/part-of: myapp, tier: frontend}
  annotations: {managed-by: syngit}
spec:
  replicas: 5
  selector:
    matchLabels: {app.kubernetes.io/part-of: myapp, tier: frontend}
  template:
    metadata:
      labels: {app.kubernetes.io/part-of: myapp, tier: frontend}
    spec:
      containers:
        - {name: web, image: "nginx:1.27.0", ports: [{containerPort: 8080}]}
        - {name: sidecar, image: "envoy:1.31.0"}
`

const baseDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  labels: {tier: frontend}
spec:
  replicas: 2
  selector:
    matchLabels: {tier: frontend}
  template:
    metadata:
      labels: {tier: frontend}
    spec:
      containers:
        - {name: web, image: "nginx:1.25.0", ports: [{containerPort: 8080}]}
`

func convert(t *testing.T, config KustomizeProviderConfig, resource, base, kustomization, patch string) string {
	t.Helper()
	out, err := Convert(config, []byte(resource), []byte(base), []byte(kustomization), []byte(patch))
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	return string(out)
}

func equal(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestConvertToBaseStripsPrefixSuffixNamespaceLabelsAndAnnotations(t *testing.T) {
	equal(t, convert(t, KustomizeProviderConfig{Override: Base}, liveDeployment, "", baseKustomization, ""),
		`apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    tier: frontend
  name: web
spec:
  replicas: 5
  selector:
    matchLabels:
      tier: frontend
  template:
    metadata:
      labels:
        tier: frontend
    spec:
      containers:
      - image: nginx:1.27.0
        name: web
        ports:
        - containerPort: 8080
      - image: envoy:1.31.0
        name: sidecar
`)
}

func TestConvertUsesBaseNameWhenPrefixAndSuffixDoNotBracketTheName(t *testing.T) {
	live := strings.Replace(liveDeployment, "name: prod-web-v2", "name: prod-web-renamed-v2", 1)
	got := convert(t, KustomizeProviderConfig{Override: Base, BaseName: "web"}, live, "", baseKustomization, "")
	if name, _ := nestedString(parse(t, got), "metadata", "name"); name != "web" {
		t.Errorf("metadata.name = %q, want %q", name, "web")
	}
}

func TestConvertStripsBundleLabelUnderAnyKeyWithoutAKustomization(t *testing.T) {
	got := convert(t, KustomizeProviderConfig{Override: Base, Bundle: "myapp", BaseName: "web"}, liveDeployment, "", "", "")
	labels, _ := nestedMap(parse(t, got), "metadata", "labels")
	if _, ok := labels["app.kubernetes.io/part-of"]; ok || labels["tier"] != "frontend" {
		t.Errorf("labels = %v, want only tier", labels)
	}
}

func TestConvertStrategicMergePatchNamesTheBaseAndCarriesOnlyDrift(t *testing.T) {
	config := KustomizeProviderConfig{Override: Overlay, PatchStrategy: StrategicMerge}
	equal(t, convert(t, config, liveDeployment, baseDeployment, baseKustomization, ""),
		`apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 5
  template:
    spec:
      containers:
      - image: nginx:1.27.0
        name: web
      - image: envoy:1.31.0
        name: sidecar
`)
}

func TestConvertMergesOverTheExistingStrategicMergePatch(t *testing.T) {
	existing := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web}\nspec: {revisionHistoryLimit: 3, replicas: 2}\n"
	config := KustomizeProviderConfig{Override: Overlay, PatchStrategy: StrategicMerge}

	spec, _ := nestedMap(parse(t, convert(t, config, liveDeployment, baseDeployment, baseKustomization, existing)), "spec")
	if spec["revisionHistoryLimit"] != float64(3) {
		t.Errorf("the hand-written field was dropped: %v", spec)
	}
	if spec["replicas"] != float64(5) {
		t.Errorf("spec.replicas = %v, the cluster value must win over the stale one", spec["replicas"])
	}
}

func TestConvertJSON6902ReplacesAListThatChangedLength(t *testing.T) {
	config := KustomizeProviderConfig{Override: Overlay, PatchStrategy: JSON6902}
	equal(t, convert(t, config, liveDeployment, baseDeployment, baseKustomization, ""),
		`- op: replace
  path: /spec/replicas
  value: 5
- op: replace
  path: /spec/template/spec/containers
  value:
  - image: nginx:1.27.0
    name: web
    ports:
    - containerPort: 8080
  - image: envoy:1.31.0
    name: sidecar
`)
}

func TestConvertJSON6902IndexesIntoAListThatKeptItsLength(t *testing.T) {
	live := strings.Replace(liveDeployment, `        - {name: sidecar, image: "envoy:1.31.0"}
`, "", 1)
	config := KustomizeProviderConfig{Override: Overlay, PatchStrategy: JSON6902}
	equal(t, convert(t, config, live, baseDeployment, baseKustomization, ""),
		`- op: replace
  path: /spec/replicas
  value: 5
- op: replace
  path: /spec/template/spec/containers/0/image
  value: nginx:1.27.0
`)
}

func TestJSON6902RemoveHasNoValueAndReplaceKeepsFalse(t *testing.T) {
	base := parse(t, `{"kept": true, "dropped": "gone", "flag": true}`)
	target := parse(t, `{"kept": true, "flag": false}`)

	out, err := yaml.Marshal(jsonPatchOperations(base, target))
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	equal(t, string(out), `- op: replace
  path: /flag
  value: false
- op: remove
  path: /dropped
`)
}

func TestJSON6902IsDeterministic(t *testing.T) {
	config := KustomizeProviderConfig{Override: Overlay, PatchStrategy: JSON6902}
	first := convert(t, config, liveDeployment, baseDeployment, baseKustomization, "")
	for range 20 {
		if again := convert(t, config, liveDeployment, baseDeployment, baseKustomization, ""); again != first {
			t.Fatalf("unstable across runs:\n%s\nvs\n%s", first, again)
		}
	}
}

func TestMergePatchNullsRemovedFieldsAndMarksRemovedEntries(t *testing.T) {
	base := parse(t, `spec: {paused: true, containers: [{name: web, image: nginx}, {name: removed, image: old}]}`)
	target := parse(t, `spec: {containers: [{name: web, image: nginx}]}`)

	out, err := yaml.Marshal(mergePatch(base, target))
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	equal(t, string(out), `spec:
  containers:
  - $patch: delete
    name: removed
  paused: null
`)
}

func TestMergePatchReplacesListsWithoutMergeKey(t *testing.T) {
	patch := mergePatch(parse(t, `{"args": ["--a", "--b"]}`), parse(t, `{"args": ["--a", "--c"]}`))
	if args, _ := patch["args"].([]any); len(args) != 2 || args[1] != "--c" {
		t.Errorf("args = %v, want the target list verbatim", patch["args"])
	}
}

func TestConvertErrors(t *testing.T) {
	tests := []struct {
		name           string
		config         KustomizeProviderConfig
		resource, base string
		want           error
	}{
		{"no resource", KustomizeProviderConfig{Override: Base}, "   \n", "", ErrEmptyResource},
		{"overlay patch with no base to diff", KustomizeProviderConfig{Override: Overlay, PatchStrategy: StrategicMerge}, liveDeployment, "", ErrEmptyBaseResource},
		{"unknown override", KustomizeProviderConfig{Override: "Sidecar"}, liveDeployment, "", ErrUnknownOverride},
		{"unknown strategy", KustomizeProviderConfig{Override: Overlay, PatchStrategy: "Rebase"}, liveDeployment, baseDeployment, ErrUnknownPatchStrategy},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Convert(test.config, []byte(test.resource), []byte(test.base), []byte(baseKustomization), nil)
			if !errors.Is(err, test.want) {
				t.Errorf("Convert() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestConvertDoesNotMutateInput(t *testing.T) {
	live := parse(t, liveDeployment)
	before, err := yaml.Marshal(live)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	convert(t, KustomizeProviderConfig{Override: Base, Bundle: "myapp"}, string(before), "", baseKustomization, "")

	after, err := yaml.Marshal(live)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	equal(t, string(after), string(before))
}

func parse(t *testing.T, raw string) map[string]any {
	t.Helper()
	parsed := map[string]any{}
	if err := yaml.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("failed to parse %q: %v", raw, err)
	}
	return parsed
}
