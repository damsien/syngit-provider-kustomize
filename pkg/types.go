package kustomizeprovider

import (
	"gomodules.xyz/jsonpatch/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ProviderAnnotation set to "enabled" on a RemoteSyncer turns the provider on for it.
const ProviderAnnotation = "kustomize.syngit.io/provider"

const (
	BundleLabel  = "kustomize.syngit.io/bundle"
	OverlayLabel = "kustomize.syngit.io/overlay"
)

const (
	RootAnnotation          = "kustomize.syngit.io/root"
	GeneratedAnnotation     = "kustomize.syngit.io/generated"
	OverlayOnlyAnnotation   = "kustomize.syngit.io/overlay-only"
	AllowDeleteAnnotation   = "kustomize.syngit.io/allow-delete"
	PathAnnotation          = "kustomize.syngit.io/path"
	PatchStrategyAnnotation = "kustomize.syngit.io/patch-strategy"
)

// Owner is the layer that produces an object in the git build of the overlay.
type Owner string

const (
	OwnerBase      Owner = "base"
	OwnerComponent Owner = "component"
	OwnerGenerator Owner = "generator"
	OwnerOverlay   Owner = "overlay"
	OwnerNone      Owner = "none"
)

type Action string

const (
	ActionPatch          Action = "patch"
	ActionWriteResource  Action = "write-resource"
	ActionDeleteResource Action = "delete-resource"
	ActionNoOp           Action = "no-op"
	ActionRefuse         Action = "refuse"
)

// PatchStrategy values are the accepted values of PatchStrategyAnnotation.
type PatchStrategy string

const (
	PatchStrategyStrategicMerge PatchStrategy = "strategic-merge"
	PatchStrategyJSON6902       PatchStrategy = "json6902"
)

type Decision struct {
	// BundleRoot is the repo path of the bundle, from RootAnnotation.
	BundleRoot string
	Overlay    string
	Owner      Owner
	Action     Action
	// TargetPath is the repo path of the file to write or delete.
	TargetPath    string
	RefuseMessage string
	// OriginalName is the name without the overlay's namePrefix/nameSuffix,
	// which patches target and overlay resource files hold.
	OriginalName  string
	PatchStrategy PatchStrategy
	// Only one of StrategicMergePatch and JSON6902Patch is set for ActionPatch,
	// following PatchStrategy; both are nil when the object is back to its baseline.
	StrategicMergePatch map[string]any
	JSON6902Patch       []jsonpatch.Operation
	// Resource is the object to write for ActionWriteResource.
	Resource *unstructured.Unstructured
}

type EditOp string

const (
	EditOpAdd    EditOp = "add"
	EditOpRemove EditOp = "remove"
)

type EditField string

const (
	EditFieldResources EditField = "resources"
	EditFieldPatches   EditField = "patches"
)

type Edit struct {
	// Kustomization is the repo path of the kustomization.yaml to edit.
	Kustomization string
	Op            EditOp
	Field         EditField
	// Entry is a resources: path, or a patches: item (path, and target for JSON6902).
	Entry any
}

// Changes are the repo changes for one object, with paths relative to the repo root.
type Changes struct {
	Files   map[string][]byte
	Deletes []string
	Edits   []Edit
}

type Result struct {
	// Handled is false when the object has no BundleLabel.
	Handled  bool
	Decision Decision
	Changes
}
