package kustomizeprovider

import (
	"bytes"
	"errors"
	"io/fs"
	"path"
	"reflect"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// The caller denies the request with Decision.RefuseMessage when
// Decision.Action is ActionRefuse.
func Process(obj *unstructured.Unstructured, op admissionv1.Operation, repo fs.FS, resourceFinder bool) (Result, error) {
	if !IsHandled(obj) {
		return Result{}, nil
	}
	d, err := Resolve(obj, op, repo, resourceFinder)
	if err != nil {
		return Result{}, err
	}
	changes, err := Render(obj, d, repo)
	if err != nil {
		return Result{}, err
	}
	return Result{Handled: true, Decision: d, Changes: changes}, nil
}

// repo must be the tree Resolve read d from.
func Render(obj *unstructured.Unstructured, d Decision, repo fs.FS) (Changes, error) {
	switch d.Action {
	case ActionPatch:
		return renderPatch(obj, d, repo)
	case ActionWriteResource:
		return renderWriteResource(d, repo)
	case ActionDeleteResource:
		return renderDeleteResource(obj, d, repo)
	}
	return Changes{}, nil
}

func renderPatch(obj *unstructured.Unstructured, d Decision, repo fs.FS) (Changes, error) {
	k, err := readOverlayKustomization(d, repo)
	if err != nil {
		return Changes{}, err
	}
	file := k.relative(d.TargetPath)
	existing := k.patchEntry(file)

	if d.StrategicMergePatch == nil && d.JSON6902Patch == nil {
		// The object is back to its baseline: the patch is no longer needed.
		changes := Changes{}
		if exists(repo, d.TargetPath) {
			changes.Deletes = []string{d.TargetPath}
		}
		if existing != nil {
			changes.Edits = []Edit{k.edit(EditOpRemove, EditFieldPatches, existing)}
		}
		return changes, nil
	}

	entry := map[string]any{"path": file}
	var content []byte
	if d.PatchStrategy == PatchStrategyJSON6902 {
		gvk := obj.GroupVersionKind()
		target := map[string]any{"version": gvk.Version, "kind": gvk.Kind, "name": d.OriginalName}
		if gvk.Group != "" {
			target["group"] = gvk.Group
		}
		entry["target"] = target
		content, err = yaml.Marshal(d.JSON6902Patch)
	} else {
		content, err = yaml.Marshal(d.StrategicMergePatch)
	}
	if err != nil {
		return Changes{}, err
	}

	changes := Changes{Files: map[string][]byte{d.TargetPath: content}}
	switch {
	case existing == nil:
		changes.Edits = []Edit{k.edit(EditOpAdd, EditFieldPatches, entry)}
	case !reflect.DeepEqual(existing, entry):
		// Switching strategy adds or drops the entry's target.
		changes.Edits = []Edit{
			k.edit(EditOpRemove, EditFieldPatches, existing),
			k.edit(EditOpAdd, EditFieldPatches, entry),
		}
	}
	return changes, nil
}

func renderWriteResource(d Decision, repo fs.FS) (Changes, error) {
	k, err := readOverlayKustomization(d, repo)
	if err != nil {
		return Changes{}, err
	}
	document, err := yaml.Marshal(d.Resource.Object)
	if err != nil {
		return Changes{}, err
	}

	documents, err := otherDocuments(repo, d.TargetPath, d.Resource.GetKind(), d.OriginalName)
	if err != nil {
		return Changes{}, err
	}
	changes := Changes{Files: map[string][]byte{d.TargetPath: joinDocuments(append(documents, document))}}
	file := k.relative(d.TargetPath)
	if !k.hasResource(file) {
		changes.Edits = []Edit{k.edit(EditOpAdd, EditFieldResources, file)}
	}
	return changes, nil
}

func renderDeleteResource(obj *unstructured.Unstructured, d Decision, repo fs.FS) (Changes, error) {
	k, err := readOverlayKustomization(d, repo)
	if err != nil {
		return Changes{}, err
	}
	documents, err := otherDocuments(repo, d.TargetPath, obj.GetKind(), d.OriginalName)
	if err != nil {
		return Changes{}, err
	}
	if len(documents) > 0 {
		return Changes{Files: map[string][]byte{d.TargetPath: joinDocuments(documents)}}, nil
	}

	changes := Changes{Deletes: []string{d.TargetPath}}
	if file := k.relative(d.TargetPath); k.hasResource(file) {
		changes.Edits = []Edit{k.edit(EditOpRemove, EditFieldResources, file)}
	}
	return changes, nil
}

// A resource file can hold several documents: the ones of other objects are kept.
func otherDocuments(repo fs.FS, file, kind, name string) ([][]byte, error) {
	content, err := fs.ReadFile(repo, file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var others [][]byte
	for _, document := range bytes.Split(content, []byte("\n---\n")) {
		document = bytes.TrimPrefix(document, []byte("---\n"))
		if len(bytes.TrimSpace(document)) == 0 {
			continue
		}
		documentKind, documentName, err := documentID(document)
		if err != nil {
			return nil, err
		}
		if documentKind != kind || documentName != name {
			others = append(others, document)
		}
	}
	return others, nil
}

func joinDocuments(documents [][]byte) []byte {
	for i, document := range documents {
		documents[i] = bytes.TrimSuffix(document, []byte("\n"))
	}
	return append(bytes.Join(documents, []byte("\n---\n")), '\n')
}

func exists(repo fs.FS, name string) bool {
	_, err := fs.Stat(repo, name)
	return err == nil
}

type overlayKustomization struct {
	path    string
	content map[string]any
}

func readOverlayKustomization(d Decision, repo fs.FS) (overlayKustomization, error) {
	p, content, err := readKustomization(repo, path.Join(d.BundleRoot, "overlays", d.Overlay))
	return overlayKustomization{path: p, content: content}, err
}

// Kustomization entries are relative to the kustomization's directory.
func (k overlayKustomization) relative(repoPath string) string {
	return strings.TrimPrefix(repoPath, path.Dir(k.path)+"/")
}

func (k overlayKustomization) patchEntry(file string) map[string]any {
	entries, _ := k.content["patches"].([]any)
	for _, item := range entries {
		entry, _ := item.(map[string]any)
		if entryPath, _ := entry["path"].(string); path.Clean(entryPath) == file {
			return entry
		}
	}
	return nil
}

func (k overlayKustomization) hasResource(file string) bool {
	entries, _ := k.content["resources"].([]any)
	for _, entry := range entries {
		if entryPath, _ := entry.(string); path.Clean(entryPath) == file {
			return true
		}
	}
	return false
}

func (k overlayKustomization) edit(op EditOp, field EditField, entry any) Edit {
	return Edit{Kustomization: k.path, Op: op, Field: field, Entry: entry}
}
