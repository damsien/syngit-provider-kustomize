# syngit-provider-kustomize

An addon to use Kustomize bundles into Syngit.

## Feature

`kubectl apply -k overlays/<overlay>` sends each object of the overlay to the
cluster on its own. For each object Syngit intercepts, this provider finds the
layer of the bundle that produces it in git and writes the change back into
**the overlay only**:

- an object of the base or of a component gets an overlay patch;
- an object declared by the overlay gets its file rewritten;
- a new object can be added to the overlay;
- anything else is refused with a message.

Base, components and generators are never written: they are shared by every
overlay and edited by hand.

## Setting up an overlay

The bundle must already exist in git, with this layout under its root:
`base/`, `components/<component>/`, `overlays/<overlay>/`. The overlay marks
its objects with two labels and the bundle root:

```yaml
# apps/my-app/overlays/staging/kustomization.yaml
namePrefix: staging-
resources:
  - ../../base
components:
  - ../../components/monitoring
configMapGenerator:
  - name: app-config
    literals: [LOG_LEVEL=debug]
labels:
  - pairs:
      kustomize.syngit.io/bundle: my-app
      kustomize.syngit.io/overlay: staging
commonAnnotations:
  kustomize.syngit.io/root: apps/my-app
generatorOptions:
  annotations:
    kustomize.syngit.io/generated: "true"
```

Objects without the `kustomize.syngit.io/bundle` label are not handled by the
provider. An object with the bundle label but without the overlay label or the
root annotation is refused.

### Annotations

| Annotation | Purpose |
|---|---|
| `kustomize.syngit.io/root` | Repo path of the bundle root. Required. |
| `kustomize.syngit.io/generated: "true"` | Marks generator output, so that a changed generated object is refused as such. |
| `kustomize.syngit.io/overlay-only: "true"` | Confirms that an object absent from git is a new overlay resource. |
| `kustomize.syngit.io/allow-delete: "true"` | Lets an object of the base, a component or a generator be deleted from the cluster. Git is left untouched. |
| `kustomize.syngit.io/path` | Repo path of the overlay file to write. Must be inside `overlays/<overlay>/`. Wins over the resource finder. |
| `kustomize.syngit.io/patch-strategy` | Forces `strategic-merge` or `json6902`. |

## What happens to each object

The provider builds the overlay from git in memory and matches the object by
kind, name and namespace.

| Operation | Object comes from | Result |
|---|---|---|
| CREATE / UPDATE | anywhere, identical to git | Nothing written. |
| CREATE / UPDATE | base or component | Overlay patch, full replace. |
| CREATE / UPDATE | overlay | Overlay file rewritten. |
| CREATE / UPDATE | generator | Refused. |
| CREATE / UPDATE | not in git, `overlay-only` | New overlay file, added to `resources:`. |
| CREATE / UPDATE | not in git | Refused. |
| DELETE | base, component or generator | Refused, or allowed without git change with `allow-delete`. |
| DELETE | overlay | File deleted and removed from `resources:`. |
| DELETE | not in git | Allowed, nothing written. |

A patch is the diff between the object and the overlay built without its own
`patches:`, so the name prefix, labels and other overlay settings never leak
into it. It is strategic-merge by default, and JSON6902 when the change
removes something. When an object goes back to what git builds without the
patch, the patch file and its entry are removed.

### File paths

With the resource finder, the provider picks the file itself: the patch
already targeting the object, the file the object comes from, or
`<kind>-<name>.patch.yaml` / `<kind>-<name>.yaml` in the overlay. Without it,
every object that changes git must carry `kustomize.syngit.io/path`.

## Usage

```go
result, err := kustomizeprovider.Process(obj, op, repo, resourceFinder)
```

- `obj` is the admission object, or the old object for a DELETE.
- `repo` is an `fs.FS` rooted at the repository root.
- When `result.Handled` is false, the object is not a kustomize object.
- When `result.Decision.Action` is `ActionRefuse`, deny the request with
  `result.Decision.RefuseMessage`.
- Otherwise apply `result.Files`, `result.Deletes` and `result.Edits`. An edit
  adds its entry to, or removes the equal entry from, the `resources:` or
  `patches:` list of the kustomization file it names.

`Process` chains `IsHandled`, `Resolve` and `Render`, which can also be called
on their own.

See [examples/basic](examples/basic/main.go):

```sh
go run ./examples/basic
```

## Requirements and limitations

- Server-side fields must be removed before the object reaches the provider,
  with the `RemoteSyncer` `excludedFields`: `status`, `metadata.managedFields`,
  `metadata.uid`, `metadata.resourceVersion`, `metadata.generation`,
  `metadata.creationTimestamp` and the
  `kubectl.kubernetes.io/last-applied-configuration` annotation.
- Fields defaulted by the API server are part of the object, so they end up in
  the patches.
- A strategic-merge patch forced on a change that removes a field can hold
  directives that Kustomize does not apply.
