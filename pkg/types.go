package kustomizeprovider

type OverrideType string

const (
	Overlay OverrideType = "Overlay"
	Base    OverrideType = "Base"
)

type PatchStrategy string

const (
	StrategicMerge PatchStrategy = "StrategicMerge"
	JSON6902       PatchStrategy = "JSON6902"
)

type KustomizeProviderConfig struct {
	Override      OverrideType
	PatchStrategy PatchStrategy
	BaseName      string // For namePrefix & nameSuffix.
	Bundle        string // The label value matching the kustomized resources.
	Overlay       string // "" if no overlay. The targeted overlay.
}

// Annotations carried by an intercepted object to configure how it is written
// back into its kustomize bundle.
const (
	// BundlePathAnnotation is the repository directory holding the base and the
	// overlays of the intercepted object. Its absence means the object is not
	// kustomize-managed.
	BundlePathAnnotation = "kustomize.syngit.io/bundle-path"
	// OverlayAnnotation selects the overlay inside the bundle, by path or by
	// directory name.
	OverlayAnnotation = "kustomize.syngit.io/overlay"
	// BaseNameAnnotation is the name the resource has in the repository, before
	// the namePrefix and the nameSuffix of the overlay.
	BaseNameAnnotation = "kustomize.syngit.io/base-name"
	// OverlayOnlyAnnotation marks a resource that only exists in the overlay: it
	// is written there whole rather than as a patch over a base.
	OverlayOnlyAnnotation = "kustomize.syngit.io/overlay-only"
	// PatchStrategyAnnotation selects the PatchStrategy used to build the overlay patch.
	PatchStrategyAnnotation = "kustomize.syngit.io/patch-strategy"
)
