package kustomizeprovider

import (
	"io/fs"
	"os"
	"testing"
	"testing/fstest"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/kustomize/api/resmap"
)

const (
	fixtureRoot    = "apps/my-app"
	fixtureOverlay = "staging"
)

// fixtureWith returns the fixture repo with some files replaced or added.
func fixtureWith(t *testing.T, files map[string]string) fs.FS {
	t.Helper()
	repo := fstest.MapFS{}
	err := fs.WalkDir(os.DirFS("testdata"), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := fs.ReadFile(os.DirFS("testdata"), p)
		repo[p] = &fstest.MapFile{Data: content}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for p, content := range files {
		repo[p] = &fstest.MapFile{Data: []byte(content)}
	}
	return repo
}

func deploymentImage(t *testing.T, b overlayBuild, m resmap.ResMap) string {
	t.Helper()
	index, err := b.index(m)
	if err != nil {
		t.Fatal(err)
	}
	built, ok := index.match(liveObject("apps/v1", "Deployment", "default", "staging-my-app"))
	if !ok {
		t.Fatal("no Deployment in the build")
	}
	containers, _, _ := unstructured.NestedSlice(built.Object.Object, "spec", "template", "spec", "containers")
	return containers[0].(map[string]any)["image"].(string)
}

func TestBuildOverlay(t *testing.T) {
	b, err := buildOverlay(os.DirFS("testdata"), fixtureRoot, fixtureOverlay)
	if err != nil {
		t.Fatal(err)
	}

	if got := deploymentImage(t, b, b.full); got != "nginx:1.27-debug" {
		t.Errorf("full image = %q, want the overlay patch applied", got)
	}
	if got := deploymentImage(t, b, b.baseline); got != "nginx:1.27" {
		t.Errorf("baseline image = %q, want the overlay patch left out", got)
	}
	for _, res := range b.full.Resources() {
		if origin, err := res.GetOrigin(); err != nil || origin == nil {
			t.Errorf("%s has no origin annotation (err: %v)", res.CurId(), err)
		}
	}
}

func TestBuildOverlayKeepsLegacyPatchesInBaseline(t *testing.T) {
	repo := fixtureWith(t, map[string]string{
		"apps/my-app/overlays/staging/kustomization.yaml": `
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namePrefix: staging-
resources:
  - ../../base
patchesStrategicMerge:
  - deployment-my-app.patch.yaml
`,
	})
	b, err := buildOverlay(repo, fixtureRoot, fixtureOverlay)
	if err != nil {
		t.Fatal(err)
	}
	if got := deploymentImage(t, b, b.baseline); got != "nginx:1.27-debug" {
		t.Errorf("baseline image = %q, want the patchesStrategicMerge entry applied", got)
	}
}

func TestBuildOverlayErrors(t *testing.T) {
	tests := []struct {
		name    string
		root    string
		overlay string
	}{
		{name: "unknown root", root: "apps/other", overlay: fixtureOverlay},
		{name: "unknown overlay", root: fixtureRoot, overlay: "prod"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := buildOverlay(os.DirFS("testdata"), tt.root, tt.overlay); err == nil {
				t.Error("expected an error")
			}
		})
	}
}
