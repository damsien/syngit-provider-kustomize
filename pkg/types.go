package kustomizeprovider

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

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
	BundleRoot    string
	Overlay       string
	Owner         Owner
	Action        Action
	TargetPath    string
	PatchStrategy PatchStrategy
	// OriginalName is the name before namePrefix/nameSuffix, which a patch targets.
	OriginalName  string
	RefuseMessage string
	// Baseline is the object built without the overlay's patches; set only for ActionPatch.
	Baseline *unstructured.Unstructured
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
