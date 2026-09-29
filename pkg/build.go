package kustomizeprovider

import (
	"fmt"
	"io/fs"
	"path"
	"slices"

	"sigs.k8s.io/kustomize/api/konfig"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"
)

type overlayBuild struct {
	root, overlay  string
	fSys           filesys.FileSystem
	full, baseline resmap.ResMap
}

func (b overlayBuild) dir() string {
	return path.Join("/", b.root, "overlays", b.overlay)
}

// buildOverlay builds overlays/<overlay> of the bundle at root twice: as in git
// (full), and without the overlay's patches: entries.
func buildOverlay(repo fs.FS, root, overlay string) (overlayBuild, error) {
	b := overlayBuild{root: root, overlay: overlay}
	var err error
	b.fSys, err = loadBundle(repo, root)
	if err != nil {
		return b, err
	}

	kustomizationPath, kustomization, err := readKustomization(b.fSys, b.dir())
	if err != nil {
		return b, err
	}
	original, err := b.fSys.ReadFile(kustomizationPath)
	if err != nil {
		return b, err
	}

	buildMetadata, _ := kustomization["buildMetadata"].([]any)
	if !slices.Contains(buildMetadata, any(types.OriginAnnotations)) {
		kustomization["buildMetadata"] = append(buildMetadata, types.OriginAnnotations)
	}
	b.full, err = runKustomize(b.fSys, b.dir(), kustomizationPath, kustomization)
	if err != nil {
		return b, fmt.Errorf("build overlay %s: %w", overlay, err)
	}

	delete(kustomization, "patches")
	b.baseline, err = runKustomize(b.fSys, b.dir(), kustomizationPath, kustomization)
	if err != nil {
		return b, fmt.Errorf("build baseline of overlay %s: %w", overlay, err)
	}
	return b, b.fSys.WriteFile(kustomizationPath, original)
}

// loadBundle copies the bundle root into memory, so the overlay's
// kustomization can be edited without touching the repo.
func loadBundle(repo fs.FS, root string) (filesys.FileSystem, error) {
	fSys := filesys.MakeFsInMemory()
	err := fs.WalkDir(repo, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := fs.ReadFile(repo, p)
		if err != nil {
			return err
		}
		return fSys.WriteFile(path.Join("/", p), content)
	})
	if err != nil {
		return nil, fmt.Errorf("load bundle %s: %w", root, err)
	}
	return fSys, nil
}

func readKustomization(fSys filesys.FileSystem, dir string) (string, map[string]any, error) {
	for _, name := range konfig.RecognizedKustomizationFileNames() {
		p := path.Join(dir, name)
		if !fSys.Exists(p) {
			continue
		}
		content, err := fSys.ReadFile(p)
		if err != nil {
			return "", nil, err
		}
		kustomization := map[string]any{}
		if err := yaml.Unmarshal(content, &kustomization); err != nil {
			return "", nil, fmt.Errorf("parse %s: %w", p, err)
		}
		return p, kustomization, nil
	}
	return "", nil, fmt.Errorf("no kustomization file in %s", dir)
}

func runKustomize(fSys filesys.FileSystem, dir, kustomizationPath string, kustomization map[string]any) (resmap.ResMap, error) {
	content, err := yaml.Marshal(kustomization)
	if err != nil {
		return nil, err
	}
	if err := fSys.WriteFile(kustomizationPath, content); err != nil {
		return nil, err
	}
	return krusty.MakeKustomizer(krusty.MakeDefaultOptions()).Run(fSys, dir)
}
