package kustomizeprovider

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/kustomize/api/resource"
	kyaml "sigs.k8s.io/kustomize/kyaml/yaml"
)

func fixtureIndex(t *testing.T) resourceIndex {
	t.Helper()
	return buildIndex(t, os.DirFS("testdata"))
}

func buildIndex(t *testing.T, repo fs.FS) resourceIndex {
	t.Helper()
	b, err := buildOverlay(repo, fixtureRoot, fixtureOverlay)
	if err != nil {
		t.Fatal(err)
	}
	index, err := b.index(b.full)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func liveObject(apiVersion, kind, namespace, name string) *unstructured.Unstructured {
	live := &unstructured.Unstructured{}
	live.SetAPIVersion(apiVersion)
	live.SetKind(kind)
	live.SetNamespace(namespace)
	live.SetName(name)
	return live
}

func TestResourceIndexOwners(t *testing.T) {
	index := fixtureIndex(t)
	tests := []struct {
		apiVersion   string
		kind         string
		name         string
		owner        Owner
		originPath   string
		originalName string
	}{
		{"apps/v1", "Deployment", "staging-my-app", OwnerBase, "apps/my-app/base/deployment.yaml", "my-app"},
		{"v1", "Service", "staging-my-app", OwnerBase, "apps/my-app/base/service.yaml", "my-app"},
		{"monitoring.coreos.com/v1", "ServiceMonitor", "staging-my-app", OwnerComponent, "apps/my-app/components/monitoring/servicemonitor.yaml", "my-app"},
		{"policy/v1", "PodDisruptionBudget", "staging-my-app", OwnerComponent, "apps/my-app/components/ha/poddisruptionbudget.yaml", "my-app"},
		{"v1", "Service", "staging-debug", OwnerOverlay, "apps/my-app/overlays/staging/debug-service.yaml", "debug"},
		{"v1", "ConfigMap", "staging-app-config-47668c6k28", OwnerGenerator, "", "app-config"},
	}
	for _, tt := range tests {
		t.Run(tt.kind+"/"+tt.name, func(t *testing.T) {
			built, ok := index.match(liveObject(tt.apiVersion, tt.kind, "default", tt.name))
			if !ok {
				t.Fatal("no match")
			}
			if built.Owner != tt.owner || built.OriginPath != tt.originPath || built.OriginalName != tt.originalName {
				t.Errorf("got (%s, %q, %q), want (%s, %q, %q)",
					built.Owner, built.OriginPath, built.OriginalName, tt.owner, tt.originPath, tt.originalName)
			}
		})
	}
}

func TestResourceIndexOriginalNames(t *testing.T) {
	tests := []struct {
		name          string
		kustomization string
		builtName     string
		originalName  string
	}{
		{
			name: "prefix and suffix",
			kustomization: `
namePrefix: staging-
nameSuffix: -v2
resources: [../../base]
configMapGenerator: [{name: app-config, literals: [A=b]}]
`,
			builtName:    "staging-my-app-v2",
			originalName: "my-app",
		},
		{
			name: "generator with suffix",
			kustomization: `
nameSuffix: -v2
resources: [../../base]
configMapGenerator: [{name: app-config, literals: [A=b]}]
`,
			builtName:    "app-config-v2-",
			originalName: "app-config",
		},
		{
			name: "generator without hash",
			kustomization: `
namePrefix: staging-
resources: [../../base]
configMapGenerator: [{name: app-config, literals: [A=b]}]
generatorOptions: {disableNameSuffixHash: true}
`,
			builtName:    "staging-app-config",
			originalName: "app-config",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := buildIndex(t, fixtureWith(t, map[string]string{
				"apps/my-app/overlays/staging/kustomization.yaml": tt.kustomization,
			}))
			for _, built := range index {
				if strings.HasPrefix(built.Object.GetName(), tt.builtName) {
					if built.OriginalName != tt.originalName {
						t.Errorf("OriginalName of %s = %q, want %q", built.Object.GetName(), built.OriginalName, tt.originalName)
					}
					return
				}
			}
			t.Errorf("no built resource named %s*", tt.builtName)
		})
	}
}

func TestResourceIndexMatch(t *testing.T) {
	index := resourceIndex{
		{Object: liveObject("v1", "Service", "", "no-namespace")},
		{Object: liveObject("v1", "Service", "team", "namespaced")},
	}
	tests := []struct {
		name string
		live *unstructured.Unstructured
		want bool
	}{
		{"no namespace matches default", liveObject("v1", "Service", "default", "no-namespace"), true},
		{"no namespace matches -n", liveObject("v1", "Service", "other", "no-namespace"), true},
		{"same namespace", liveObject("v1", "Service", "team", "namespaced"), true},
		{"other namespace", liveObject("v1", "Service", "other", "namespaced"), false},
		{"unprefixed name", liveObject("v1", "Service", "default", "my-app"), false},
		{"other kind", liveObject("v1", "ConfigMap", "default", "no-namespace"), false},
		{"other version", liveObject("v2", "Service", "default", "no-namespace"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, got := index.match(tt.live); got != tt.want {
				t.Errorf("match = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassifyOrigin(t *testing.T) {
	tests := []struct {
		name       string
		origin     *resource.Origin
		owner      Owner
		originPath string
	}{
		{"no origin", nil, OwnerNone, ""},
		{"base", &resource.Origin{Path: "../../base/deployment.yaml"}, OwnerBase, "apps/my-app/base/deployment.yaml"},
		{"component", &resource.Origin{Path: "../../components/ha/pdb.yaml"}, OwnerComponent, "apps/my-app/components/ha/pdb.yaml"},
		{"overlay", &resource.Origin{Path: "debug-service.yaml"}, OwnerOverlay, "apps/my-app/overlays/staging/debug-service.yaml"},
		{"other overlay", &resource.Origin{Path: "../prod/service.yaml"}, OwnerNone, ""},
		{"outside the bundle", &resource.Origin{Path: "../../../shared/service.yaml"}, OwnerNone, ""},
		{"remote", &resource.Origin{Repo: "https://github.com/org/repo", Path: "base/service.yaml"}, OwnerNone, ""},
		{"configmap generator", generatorOrigin("ConfigMapGenerator"), OwnerGenerator, ""},
		{"secret generator", generatorOrigin("SecretGenerator"), OwnerGenerator, ""},
		{"other generator", generatorOrigin("HelmChartInflationGenerator"), OwnerNone, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, originPath := classifyOrigin(tt.origin, fixtureRoot, fixtureOverlay)
			if owner != tt.owner || originPath != tt.originPath {
				t.Errorf("got (%s, %q), want (%s, %q)", owner, originPath, tt.owner, tt.originPath)
			}
		})
	}
}

func generatorOrigin(kind string) *resource.Origin {
	return &resource.Origin{
		ConfiguredIn: "kustomization.yaml",
		ConfiguredBy: kyaml.ResourceIdentifier{TypeMeta: kyaml.TypeMeta{APIVersion: "builtin", Kind: kind}},
	}
}

func TestGeneratorNames(t *testing.T) {
	names := generatorNames(fixtureIndex(t))
	if len(names) != 1 {
		t.Fatalf("got %v, want one generator", names)
	}
	for generated, name := range names {
		if name != "app-config" || !strings.HasPrefix(generated, "staging-app-config-") {
			t.Errorf("got %q -> %q", generated, name)
		}
	}
}
