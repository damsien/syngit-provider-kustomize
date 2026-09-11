package kustomizeprovider

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"sigs.k8s.io/kustomize/api/types"
)

var (
	ErrOverlayNotFound  = errors.New("no overlay found")
	ErrAmbiguousOverlay = errors.New("several overlays match")
	ErrBaseNotFound     = errors.New("no base found")
	ErrAmbiguousBase    = errors.New("several bases match")
)

var KustomizationFileNames = []string{"kustomization.yaml", "kustomization.yml", "Kustomization"}

type OverlayTarget struct {
	Path              string
	KustomizationPath string
	Raw               []byte
	Kustomization     *types.Kustomization
}

// DetectOverlay walks fsys and returns the overlay to patch into. An overlay is
// a kustomization building on another one; bases are never candidates.
// config.Overlay names one by path or directory name, config.Bundle by the label
// value its kustomization applies.
func DetectOverlay(fsys fs.FS, config KustomizeProviderConfig) (*OverlayTarget, error) {
	candidates, err := findOverlays(fsys)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: no kustomization patches another one", ErrOverlayNotFound)
	}

	switch {
	case config.Overlay != "":
		return selectOverlay(candidates, fmt.Sprintf("the name %q", config.Overlay), func(o *OverlayTarget) bool {
			return o.Path == path.Clean(config.Overlay) || path.Base(o.Path) == config.Overlay
		})
	case config.Bundle != "":
		return selectOverlay(candidates, fmt.Sprintf("the bundle %q", config.Bundle), func(o *OverlayTarget) bool {
			return labelsBundle(o.Kustomization, config.Bundle)
		})
	default:
		return selectOverlay(candidates, "an empty config", func(*OverlayTarget) bool { return true })
	}
}

// DetectBase returns the directory of the kustomization the overlay builds on:
// the single resource entry of the overlay that is itself a kustomization.
func DetectBase(fsys fs.FS, overlay *OverlayTarget) (string, error) {
	var bases []string
	for _, resource := range overlay.Kustomization.Resources {
		dir := path.Join(overlay.Path, resource)
		if isKustomization(fsys, dir) {
			bases = append(bases, dir)
		}
	}

	switch len(bases) {
	case 1:
		return bases[0], nil
	case 0:
		return "", fmt.Errorf("%w: %s builds on no kustomization", ErrBaseNotFound, overlay.KustomizationPath)
	default:
		return "", fmt.Errorf("%w: %s builds on %s", ErrAmbiguousBase, overlay.KustomizationPath, strings.Join(bases, ", "))
	}
}

func selectOverlay(candidates []*OverlayTarget, selector string, keep func(*OverlayTarget) bool) (*OverlayTarget, error) {
	var matches []*OverlayTarget
	for _, candidate := range candidates {
		if keep(candidate) {
			matches = append(matches, candidate)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, fmt.Errorf("%w: %s matches none of %s", ErrOverlayNotFound, selector, overlayPaths(candidates))
	default:
		return nil, fmt.Errorf("%w: %s selects %s", ErrAmbiguousOverlay, selector, overlayPaths(matches))
	}
}

func findOverlays(fsys fs.FS) ([]*OverlayTarget, error) {
	var overlays []*OverlayTarget

	err := fs.WalkDir(fsys, ".", func(entryPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if entryPath != "." && strings.HasPrefix(entry.Name(), ".") {
			return fs.SkipDir
		}

		kustomizationPath, raw, ok := readKustomization(fsys, entryPath)
		if !ok {
			return nil
		}
		kustomization, err := ParseKustomization(raw)
		if err != nil || !isOverlay(fsys, entryPath, kustomization) {
			return nil //nolint:nilerr
		}

		overlays = append(overlays, &OverlayTarget{
			Path:              entryPath,
			KustomizationPath: kustomizationPath,
			Raw:               raw,
			Kustomization:     kustomization,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk the repository: %w", err)
	}

	sort.Slice(overlays, func(i, j int) bool { return overlays[i].Path < overlays[j].Path })
	return overlays, nil
}

func readKustomization(fsys fs.FS, dir string) (string, []byte, bool) {
	for _, name := range KustomizationFileNames {
		candidate := path.Join(dir, name)
		if raw, err := fs.ReadFile(fsys, candidate); err == nil {
			return candidate, raw, true
		}
	}
	return "", nil, false
}

func isOverlay(fsys fs.FS, dir string, k *types.Kustomization) bool {
	// The deprecated patch fields still identify older overlays.
	//nolint:staticcheck
	if len(k.Patches) > 0 || len(k.PatchesStrategicMerge) > 0 ||
		len(k.PatchesJson6902) > 0 || len(k.Components) > 0 {
		return true
	}
	for _, resource := range k.Resources {
		if isRemote(resource) || climbsOutOfDirectory(resource) || isKustomization(fsys, path.Join(dir, resource)) {
			return true
		}
	}
	return false
}

func isRemote(resource string) bool {
	return strings.Contains(resource, "://") || strings.HasPrefix(resource, "/")
}

// Checked before joining to the directory, which would collapse the leading "..".
func climbsOutOfDirectory(resource string) bool {
	return strings.HasPrefix(path.Clean(resource), "..")
}

func isKustomization(fsys fs.FS, dir string) bool {
	_, _, ok := readKustomization(fsys, dir)
	return ok
}

func labelsBundle(k *types.Kustomization, bundle string) bool {
	// The deprecated commonLabels field still labels older overlays.
	//nolint:staticcheck
	for _, value := range k.CommonLabels {
		if value == bundle {
			return true
		}
	}
	for _, label := range k.Labels {
		for _, value := range label.Pairs {
			if value == bundle {
				return true
			}
		}
	}
	return false
}

func overlayPaths(overlays []*OverlayTarget) string {
	paths := make([]string, 0, len(overlays))
	for _, overlay := range overlays {
		paths = append(paths, overlay.Path)
	}
	return strings.Join(paths, ", ")
}
