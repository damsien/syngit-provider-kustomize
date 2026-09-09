package kustomizeprovider

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

func file(content string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(content)} }

func repository() fstest.MapFS {
	return fstest.MapFS{
		"base/kustomization.yaml": file("resources: [deployment.yaml]\n"),
		"base/deployment.yaml":    file(baseDeployment),

		"overlays/production/kustomization.yaml": file(`namePrefix: prod-
nameSuffix: -v2
namespace: production
resources: [../../base]
labels:
  - pairs: {app.kubernetes.io/part-of: myapp}
    includeSelectors: true
commonAnnotations: {managed-by: syngit}
patches:
  - path: deployment-patch.yaml
`),
		"overlays/production/deployment-patch.yaml": file("kind: Deployment\n"),

		"overlays/staging/kustomization.yaml": file(`namePrefix: staging-
resources: [../../base]
commonLabels: {app.kubernetes.io/part-of: otherapp}
`),

		".git/kustomization.yaml": file("resources: [../base]\n"),
	}
}

func detect(t *testing.T, fsys fstest.MapFS, config KustomizeProviderConfig) *OverlayTarget {
	t.Helper()
	overlay, err := DetectOverlay(fsys, config)
	if err != nil {
		t.Fatalf("DetectOverlay() error = %v", err)
	}
	return overlay
}

func TestDetectOverlaySelection(t *testing.T) {
	tests := []struct {
		name   string
		config KustomizeProviderConfig
		want   string
	}{
		{"by directory name", KustomizeProviderConfig{Overlay: "production"}, "overlays/production"},
		{"by path", KustomizeProviderConfig{Overlay: "overlays/production"}, "overlays/production"},
		{"by bundle", KustomizeProviderConfig{Bundle: "myapp"}, "overlays/production"},
		{"by bundle, deprecated commonLabels", KustomizeProviderConfig{Bundle: "otherapp"}, "overlays/staging"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := detect(t, repository(), test.config); got.Path != test.want {
				t.Errorf("Path = %q, want %q", got.Path, test.want)
			}
		})
	}
}

func TestDetectOverlayReturnsWhatConvertNeeds(t *testing.T) {
	overlay := detect(t, repository(), KustomizeProviderConfig{Overlay: "production"})
	if overlay.KustomizationPath != "overlays/production/kustomization.yaml" {
		t.Errorf("KustomizationPath = %q", overlay.KustomizationPath)
	}
	if overlay.Kustomization.NamePrefix != "prod-" {
		t.Errorf("the kustomization was not parsed: %+v", overlay.Kustomization)
	}
	if !strings.Contains(string(overlay.Raw), "namePrefix: prod-") {
		t.Errorf("Raw = %q", overlay.Raw)
	}
}

func TestDetectOverlayNotFound(t *testing.T) {
	basesOnly := fstest.MapFS{
		"base/kustomization.yaml": file("resources: [deployment.yaml]\n"),
		"base/deployment.yaml":    file(baseDeployment),
	}

	tests := []struct {
		name   string
		fsys   fstest.MapFS
		config KustomizeProviderConfig
	}{
		{"no overlay by that name", repository(), KustomizeProviderConfig{Overlay: "canary"}},
		{"no overlay with that bundle", repository(), KustomizeProviderConfig{Bundle: "unknown"}},
		{"a base is never a candidate", repository(), KustomizeProviderConfig{Overlay: "base"}},
		{"bases only", basesOnly, KustomizeProviderConfig{Overlay: "production"}},
		{"empty repository", fstest.MapFS{}, KustomizeProviderConfig{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DetectOverlay(test.fsys, test.config); !errors.Is(err, ErrOverlayNotFound) {
				t.Errorf("DetectOverlay() error = %v, want ErrOverlayNotFound", err)
			}
		})
	}
}

func TestDetectOverlayAmbiguousErrorNamesTheCandidates(t *testing.T) {
	_, err := DetectOverlay(repository(), KustomizeProviderConfig{})
	if !errors.Is(err, ErrAmbiguousOverlay) {
		t.Fatalf("DetectOverlay() error = %v, want ErrAmbiguousOverlay", err)
	}
	for _, want := range []string{"overlays/production", "overlays/staging"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
}

func TestDetectOverlayRecognizesOlderLayouts(t *testing.T) {
	tests := []struct {
		name string
		fsys fstest.MapFS
	}{
		{"deprecated bases", fstest.MapFS{
			"base/kustomization.yaml":    file("resources: [deployment.yaml]\n"),
			"base/deployment.yaml":       file(baseDeployment),
			"overlay/kustomization.yaml": file("bases: [../base]\n"),
		}},
		{"deprecated patchesStrategicMerge", fstest.MapFS{
			"overlay/kustomization.yaml": file("patchesStrategicMerge: [patch.yaml]\n"),
			"overlay/patch.yaml":         file("kind: Deployment\n"),
		}},
		{"remote base", fstest.MapFS{
			"overlay/kustomization.yaml": file(`resources: ["https://github.com/example/repo//base?ref=v1"]`),
		}},
		{"kustomization.yml spelling", fstest.MapFS{
			"overlay/kustomization.yml": file("resources: [../base]\n"),
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := detect(t, test.fsys, KustomizeProviderConfig{Overlay: "overlay"}); got.Path != "overlay" {
				t.Errorf("Path = %q, want %q", got.Path, "overlay")
			}
		})
	}
}

func TestDetectOverlayWalksPastAnUnparseableKustomization(t *testing.T) {
	fsys := repository()
	fsys["overlays/broken/kustomization.yaml"] = file("resources: [oops\n")

	if got := detect(t, fsys, KustomizeProviderConfig{Overlay: "production"}); got.Path != "overlays/production" {
		t.Errorf("Path = %q", got.Path)
	}
}

func TestDetectOverlayOutputFeedsConvert(t *testing.T) {
	config := KustomizeProviderConfig{
		Override:      Overlay,
		PatchStrategy: StrategicMerge,
		Bundle:        "myapp",
		BaseName:      "web",
	}
	overlay := detect(t, repository(), config)

	equal(t, convert(t, config, liveDeployment, baseDeployment, string(overlay.Raw), ""),
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
