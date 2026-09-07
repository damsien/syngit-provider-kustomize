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
