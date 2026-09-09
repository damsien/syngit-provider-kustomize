# Basic example

A `web` deployment with a `production` overlay, and `intercepted.yaml`: the same deployment as the cluster holds it, `kustomize build` applied, plus two changes made there. `replicas: 3 -> 5` and `nginx:1.25.0 -> nginx:1.27.0`.

```sh
go run ./examples/basic
```

`DetectOverlay` finds `overlays/production`, and `Convert` strips the name prefix, the namespace, the labels and the  annotations the overlay injected, then merges the two changes into the patch already there:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 5
  template:
    spec:
      containers:
      - image: nginx:1.27.0
        name: web
```

Written back to `overlays/production/deployment.yaml`, that patch makes `kustomize build` render the intercepted resource again.
