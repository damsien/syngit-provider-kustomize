# syngit-provider-kustomize

An addon to use Kustomize functionalities into Syngit.

An intercepted resource has already been through `kustomize build`: it carries the `namePrefix`, the `nameSuffix`, and the injected labels and annotations. The provider undoes that and gives back the file the repository holds: the base resource, or the overlay patch.

```go
config := kustomizeprovider.KustomizeProviderConfig{
    Override:      kustomizeprovider.Overlay,
    PatchStrategy: kustomizeprovider.StrategicMerge,
    Bundle:        "myapp",
    Overlay:       "production",
    BaseName:      "web",
}

overlay, err := kustomizeprovider.DetectOverlay(fsys, config)
patch, err := kustomizeprovider.Convert(config, intercepted, baseResource, overlay.Raw, existingPatch)
```

`DetectOverlay` walks `fsys` for the kustomization building on another one, selected by `Overlay` (path or directory name) or by `Bundle` (the label value it applies).

`Convert` returns the resource stripped of every kustomize transformation (`Override: Base`), or its difference against `baseResource` as a `StrategicMerge` patch merged over `overlayPatch`, or a `JSON6902` one superseding it (`Override: Overlay`).

A runnable walkthrough lives in [examples/basic](examples/basic).
