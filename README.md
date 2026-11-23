# Cluster Bundle

[![GoDoc](https://godoc.org/github.com/GoogleCloudPlatform/k8s-cluster-bundle?status.svg)](https://godoc.org/github.com/GoogleCloudPlatform/k8s-cluster-bundle)

**Note: This is not an officially supported Google product**

The Cluster Bundle is a Kubernetes-related project developed to improve the way
we package and release Kubernetes software. It was created by the Google
Kubernetes team, and was developed based on our experience managing Kubernetes
clusters and applications at scale in both GKE and GKE On-Prem.

It is currently experimental (Pre-Alpha) and will have frequent, breaking
changes until the API settles down.

## Installation

Most users will interact with Cluster Bundle objects via the command line
interface `bundlectl`.

To install, run:

```shell
go install github.com/GoogleCloudPlatform/k8s-cluster-bundle/cmd/bundlectl
```

## Packaging in the Cluster Bundle

Packaging in Cluster Bundle revolves around a new type, called the `Component`:

* **Component**: A versioned collection of Kubernetes objects. A component
  should correspond to a logical application. For example, we might imagine
  components for each of Etcd, Istio, KubeProxy, and CoreDNS.
* **ComponentBuilder**: An intermediate type that allows for easier creation of
  component objects.

The Cluster Bundle APIs are minimal and focused on the problem of packaging
Kubernetes objects into a single unit. It is assumed that some external
deployment mechanisms like a command line interface or an in-cluster controller
will consume the components and apply the Kubernetes objects in the component to
a cluster.

## Components

Here's how you might use the Component type to represent
[Etcd](https://github.com/etcd-io/etcd):

```yaml
apiVersion: bundle.gke.io/v1alpha1
kind: Component
spec:

  # A human readable name for the component.
  componentName: etcd-component

  # SemVer version for the component representing any and all changes to the
  # component or included object manifests. This should not be tied to the
  # application version.
  version: 30.0.2

  # A list of included Kubernetes objects.
  objects:
  - apiVersion: v1
    kind: Pod
    metadata:
      name: etcd-server
      namespace: kube-system
    spec:
      containers:
      - command:
        - /bin/sh
        - -c
        - |
          exec /usr/local/bin/etcd
    # ... and so on
```

Some notes about components:

- Components have a name and version. The pair of these should be a unique
  identifier for the component.
- Objects must be Kubernetes objects -- they must have `apiVersion`, `kind`, and
  `metadata`. Other than that, there are no restrictions on what objects can be
  contained in a component.

You can see more about examples of components in the [examples directory](https://github.com/GoogleCloudPlatform/k8s-cluster-bundle/tree/master/examples).

### Building

To make it easier to create components, you can use the `ComponentBuilder` type:

```yaml
apiVersion: bundle.gke.io/v1alpha1
kind: ComponentBuilder

componentName: etcd-component

version: 30.0.2

# File-references to kubernetes objects that make up this component.
objectFiles:
- url: file:///etcd-server.yaml
```

ComponentBuilders can be built into Components with:

```shell
bundlectl build --input-file=my-component.yaml
```

During `build`, objects are inlined, which means they are imported directly
into the component.

Raw-text can also be imported into a component. When inlined, this text is
converted into a ConfigMap and then added to the objects list:

```yaml
apiVersion: bundle.gke.io/v1alpha1
kind: Component
spec:
  canonicalName: data-blob
  version: 0.1.0
  rawTextFiles:
  - name: data-blob
    files:
    - url: file:///some-data.txt
```

### Patching

The Cluster Bundle library provides a new type called the `PatchTemplate` to
perform [Kustomize-style](https://github.com/kubernetes-sigs/kustomize)
patching to objects in component. These are Go-templates that are applied to
the objects in a component via
[StrategicMergePatch](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-api-machinery/strategic-merge-patch.md)
or, if the object is not a Kubernetes Object, via [JSONPatch](http://jsonpatch.com/).

For example, if you have the following PatchTemplate specified as an object in
a component:

```yaml
apiVersion: bundle.gke.io/v1alpha1
kind: PatchTemplate
template: |
  apiVersion: v1
  kind: Pod
  metadata:
    namespace: {{.namespace}}
```

and the following Kubernetes object in the same component:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: etcd-server
spec:
  containers:
  - command:
    - /bin/sh
    - -c
    - |
      exec /usr/local/bin/etcd
  # etc...
```

and you specified following options YAML file:

```yaml
namespace: foo-namespace
```

Then, running the `bundlectl` command like so:

```shell
bundlectl patch --input-file=etcd-component.yaml --options-file=options.yaml
```

produces:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: etcd-server
  namespace: foo-namespace
spec:
  containers:
  - command:
    - /bin/sh
    - -c
    - |
      exec /usr/local/bin/etcd
  # etc...
```

For more examples of Patching and Patch-Building, see the
[examples directory](https://github.com/GoogleCloudPlatform/k8s-cluster-bundle/tree/master/examples).

#### Options Schema for PatchTemplates

Additionally, you can provide an OpenAPIv3 schema to validate and provide
defaults for options applied to patch templates. Extending the example above,
we can provide defaulting and validation for our `namespace` parameter.

```yaml
apiVersion: bundle.gke.io/v1alpha1
kind: PatchTemplate
template: |
  apiVersion: v1
  kind: Pod
  metadata:
    namespace: {{.namespace}}
optionsSchema:
  properties:
    namespace:
      type: string
      pattern: '^[a-z0-9-]+$'
      default: dev-ns
```

Here, we require the namespace parameter to be a string that must match the
regex pattern. Additionally, if the namespace parameter is not supplied, then
we default namespace to `dev-ns`.

### Filtering

`bundlectl` can filter objects from a component, which allows for powerful
chaining of the bundlectl command.

```shell
# Filter all config map objects
bundlectl filter -f my-component.yaml --filterType=objects --kind=ConfigMap

# Keep only config map objects.
bundlectl filter -f my-component.yaml --filterType=objects --kind=ConfigMap --invert-match
```

### Validation

`bundlectl validate` checks a Component or a Bundle. For a Component it checks
both the component metadata (name, SemVer version, `apiVersion`, `kind`) and
every object in `spec.objects`. For a Bundle it validates every component and
checks object identities across the whole set, because a bundle exports all of
its components together.

```shell
bundlectl validate --input-file=my-bundle.yaml
```

The command prints warnings to stdout and exits non-zero only when there are
errors.

Object-level rules:

| Rule | Severity |
| --- | --- |
| `apiVersion` is present and has the form `group/version` (or `version`) | error |
| `kind` is present | error |
| `metadata.name` is present, a DNS-1123 subdomain, and not empty | error |
| `metadata.generateName` is used without `metadata.name` | error |
| Object has no explicit `metadata.namespace` (except well-known cluster-scoped kinds) | warning |
| Two objects in the same component with the same identity and identical content | warning |
| Two objects in the same component with the same identity and different content | error |
| Two objects in different components with the same identity | warning for this release; see below |

An object's identity is `(group, kind, namespace, name)`. The version part of
`apiVersion` is not part of the identity, so `apps/v1` and `apps/v1beta1`
Deployment objects with the same name collide. The Bundle library does not use
API discovery, so it cannot tell whether an arbitrary kind is namespaced; a
namespace participates in the identity only when it is written explicitly in
`metadata.namespace`. Missing namespaces are treated as the empty namespace.

Cross-component duplicates have no override semantics in a Bundle: a later
component does not replace an earlier one. To keep existing packages passing
while they are inspected, cross-component duplicates are **warnings** in this
release and will become **errors** in a future release. Callers using the Go API
can opt in to the stricter behavior now with
`validate.Options{CrossComponentDuplicateSeverity: validate.SeverityError}`.

Duplication across components can be declared intentional with the annotation
`bundle.gke.io/allow-duplicate-object: "true"`. The annotation must be on both
objects.

Several versions of the same component name (for example `etcd` 1.0.0 and
`etcd` 2.0.0 in one ComponentSet) are treated as alternative versions when the
set is validated without selecting one: collisions between different versions
of the same name are ignored, while collisions between different names are still
reported. Validation of components that will actually be exported together does
not grant this exemption, because version differences do not make a real
collision safe.

Validation is phase-aware. Before rendering, `ObjectTemplate` and `PatchTemplate`
objects are checked as templates (their name is optional and they do not
participate in identity checks). After rendering, every object is a final
object, so a surviving template is an error. Use `validate.ValidateComponent`
with `validate.Options{Phase: ...}`, and call it once in each phase.

Findings are capped (100 errors and 100 warnings by default). When the cap is
exceeded, the findings that sort last are omitted and a summary with the total
count is appended. Findings are ordered by component reference and object index,
so repeated runs over the same input produce identical output. Field paths have
the form `Component[{name version}].Spec.Objects[3].Metadata.Name`.

A `ComponentSet` contains only references. `validate.ValidateComponentSet`
accepts an optional pool of components, expands each reference to the exact
component with the same name and version, and validates the objects it finds.
References that cannot be expanded are reported in `Report.MissingReferences`,
and `Report.ObjectsValidated` is only true when every reference was expanded;
without a pool, only the references themselves are checked.

### Component Testing

The Cluster Bundle library provides *experimental* support for testing
building, patching, and validating components, by writing YAML test-suites that
are run via `go test`.

To write a component test suite, create a YAML test file like so:

```go
componentFile: etcd-component-builder.yaml
rootDirectory: './'

testCases:
- description: Success
  apply:
    options:
      namespace: default-ns
      buildLabel: test-env
  expect:
    objects:

    # The kind + the name references a unique object in the component.
    - kind: Pod
      name: etcd-server

      # ensure that the generated 'etcd-server' object YAML has the following
      substrings
      findSubstrs:
      - 'build-label: test-env'
      - 'image: k8s.gcr.io/etcd:3.1.11'
      - 'namespace: default-ns'

      # ensure that the generated 'etcd-server' object YAML does not have the
      # following substrings
      notFindSubstrs:
      - 'build-label: dev-env'

- description: 'Fail: parameter missing'
  expect:
    # Check for an error condition during the apply-process.
    applyErrSubstr: 'namespace in body is required'
```

Then, you need only write the following `go` boilerplate:

```go
package component

import (
	"testing"

	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/testutil/componentsuite"
)

func TestComponentSuite(t *testing.T) {
	componentsuite.Run(t, "test-suite.yaml")
}
```

Running the tests can be performed by running `go test`.

Examples:

* A [component test-suite with patching](https://github.com/GoogleCloudPlatform/k8s-cluster-bundle/tree/master/examples/component/test-suite.yaml)
* A [component test-suite with patching-building](https://github.com/GoogleCloudPlatform/k8s-cluster-bundle/tree/master/examples/patchbuilder/test-suite.yaml)

## Public APIs and Compatibility.

During pre-Alpha and Alpha, breaking changes may happen in any of the types in
`pkg/apis`, the `bundlectl` CLI, or the Go APIs (methods, structs, interfaces
not in `pkg/apis`).

During Beta, backwards incompatible breaking changes in the `bundlectl` and the
`pkg/apis` directory will only happy during Major version boundaries.

The Go APIs are not covered by any compatibility guarantee and can break in
backwards incompatible ways during any release

## Development

### Directory Structure

This directory follows a layout similar to other Kubernetes projects.

*   `pkg/`: Library code.
*   `pkg/apis`: APIs and schema for the Cluster Bundle.
*   `pkg/client`: Generated client for the Cluster Bundle types.
*   `config/crds`: Generated CRDs for the ClusterBundle types.
*   `examples/`: Examples of components
*   `cmd/`: Binaries. This contains the `bundlectl` CLI tool.

### Building and Testing

The Cluster Bundle relies on [Bazel](https://bazel.build/) for building and
testing, but the library is go-gettable and it works equally well to use `go
build` and `go test` for building and testing.

### Testing

To run the unit tests, run

```shell
bazel test //...
```

It should also work to use the `go` command:

```shell
go test ./...
```

### Regenerate BUILD files

To make using Bazel easier, we use Gazelle to automatically write Build targets.
To automatically write the Build targets, run:

```shell
bazel run //:gazelle -- update-repos -from_file=go.mod -to_macro 'repositories.bzl%gomod_repositories'
```

### Regenerate Generated Code

We rely heavily on Kubernetes code generation. To regenerate the code,
run:

```shell
hack/update-codegen.sh
```

If new files are added, you will need to re-run Gazelle (see above).

### Prow

We use [prow](https://github.com/kubernetes/test-infra/tree/master/prow) to
test the Cluster Bundle. If the prow-jobs need to be updated, see the Cluster
Bundle Prow Jobs:

* [historical, pinned](https://github.com/kubernetes/test-infra/blob/1f003d3e1d7aecad6e16fd08e9d0253df9033f20/config/jobs/GoogleCloudPlatform/k8s-cluster-bundle/k8s-cluster-bundle.yaml)
* [master](https://github.com/kubernetes/test-infra/blob/master/config/jobs/GoogleCloudPlatform/k8s-cluster-bundle/k8s-cluster-bundle.yaml)
