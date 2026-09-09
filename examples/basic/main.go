// Turns a resource intercepted from the cluster back into the overlay patch
// the repository holds.
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"path"

	kustomizeprovider "github.com/syngit-org/syngit-provider-kustomize/pkg"
)

//go:embed repository
var repositoryFiles embed.FS

//go:embed intercepted.yaml
var intercepted []byte

func main() {
	repository, err := fs.Sub(repositoryFiles, "repository")
	if err != nil {
		log.Fatal(err)
	}

	config := kustomizeprovider.KustomizeProviderConfig{
		Override:      kustomizeprovider.Overlay,
		PatchStrategy: kustomizeprovider.StrategicMerge,
		Bundle:        "myapp",
		Overlay:       "production",
		BaseName:      "web",
	}

	overlay, err := kustomizeprovider.DetectOverlay(repository, config)
	if err != nil {
		log.Fatal(err)
	}

	base, err := fs.ReadFile(repository, "base/deployment.yaml")
	if err != nil {
		log.Fatal(err)
	}
	existingPatch, err := fs.ReadFile(repository, path.Join(overlay.Path, "deployment.yaml"))
	if err != nil {
		log.Fatal(err)
	}

	patch, err := kustomizeprovider.Convert(config, intercepted, base, overlay.Raw, existingPatch)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("detected overlay: %s\n\n%s", overlay.KustomizationPath, patch)
}
