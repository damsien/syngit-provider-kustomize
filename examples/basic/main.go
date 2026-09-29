package main

import (
	"fmt"
	"log"
	"testing/fstest"

	kustomizeprovider "github.com/syngit-org/syngit-provider-kustomize/pkg"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// A bundle as it sits in git. In syngit, the repo is the worktree of the
// target repository.
var repo = fstest.MapFS{
	"apps/my-app/base/kustomization.yaml": {Data: []byte(`
resources:
  - deployment.yaml
`)},
	"apps/my-app/base/deployment.yaml": {Data: []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  replicas: 1
  selector:
    matchLabels: {app: my-app}
  template:
    metadata:
      labels: {app: my-app}
    spec:
      containers:
        - name: app
          image: nginx:1.27
`)},
	"apps/my-app/overlays/staging/kustomization.yaml": {Data: []byte(`
namePrefix: staging-
resources:
  - ../../base
labels:
  - pairs:
      kustomize.syngit.io/bundle: my-app
      kustomize.syngit.io/overlay: staging
commonAnnotations:
  kustomize.syngit.io/root: apps/my-app
`)},
}

// The Deployment of `kubectl apply -k overlays/staging` after the user set
// replicas to 3 in their local base.
const applied = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: staging-my-app
  namespace: default
  labels:
    kustomize.syngit.io/bundle: my-app
    kustomize.syngit.io/overlay: staging
  annotations:
    kustomize.syngit.io/root: apps/my-app
spec:
  replicas: 3
  selector:
    matchLabels: {app: my-app}
  template:
    metadata:
      labels: {app: my-app}
      annotations:
        kustomize.syngit.io/root: apps/my-app
    spec:
      containers:
        - name: app
          image: nginx:1.27
`

func main() {
	// Admission requests carry JSON, which unstructured decodes with int64 numbers.
	content, err := yaml.YAMLToJSON([]byte(applied))
	if err != nil {
		log.Fatal(err)
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(content); err != nil {
		log.Fatal(err)
	}

	result, err := kustomizeprovider.Process(obj, admissionv1.Update, repo, true)
	if err != nil {
		log.Fatal(err)
	}
	if !result.Handled {
		log.Fatal("not a kustomize object")
	}

	d := result.Decision
	if d.Action == kustomizeprovider.ActionRefuse {
		log.Fatalf("refused: %s", d.RefuseMessage)
	}
	fmt.Printf("owner: %s, action: %s\n", d.Owner, d.Action)
	for file, content := range result.Files {
		fmt.Printf("\n--- write %s\n%s", file, content)
	}
	for _, file := range result.Deletes {
		fmt.Printf("\n--- delete %s\n", file)
	}
	for _, edit := range result.Edits {
		fmt.Printf("\n--- %s %s entry in %s: %v\n", edit.Op, edit.Field, edit.Kustomization, edit.Entry)
	}
}
