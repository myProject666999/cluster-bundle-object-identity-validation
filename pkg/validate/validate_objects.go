// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package validate

import (
	"encoding/json"
	"fmt"
	"sort"

	bundle "github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/apis/bundle/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// objectIdentity is how this package determines whether two objects are the
// same resource. It deliberately does not use core.ObjectRef: that type has no
// namespace, so it cannot distinguish "same name in two namespaces" from a real
// collision. The API version is also excluded, because Deployment apps/v1 and
// apps/v1beta1 address the same resource.
//
// The version portion of apiVersion is not part of an identity. The group,
// kind, namespace, and name are compared as a unit.
type objectIdentity struct {
	// Group is the API group, or "" for the core group (e.g. "v1").
	Group string

	// Kind is the resource kind.
	Kind string

	// Namespace is the object's namespace, or "" when none was written. An
	// empty namespace is treated as empty for all objects; see the package
	// documentation for the limitation this implies.
	Namespace string

	// Name is metadata.name.
	Name string
}

func (id objectIdentity) String() string {
	group := id.Group
	if group == "" {
		group = "core"
	}
	return fmt.Sprintf("group=%q kind=%q namespace=%q name=%q", group, id.Kind, id.Namespace, id.Name)
}

// objectOccurrence is a place where an identity appears in the validated input.
type objectOccurrence struct {
	id objectIdentity

	// componentOrdinal distinguishes two components that happen to share a
	// reference. Objects are only "in the same component" when both the ordinal
	// and the reference match.
	componentOrdinal int
	ref              bundle.ComponentReference
	index            int
	content          string

	// allowDuplicate is true when the object explicitly opts out of
	// cross-component duplicate checks.
	allowDuplicate bool
}

// isTemplateKind reports whether an object is one of the Bundle's own pre-render
// template types. A missing apiVersion is tolerated for the purpose of this
// check so that a malformed template is reported once for its missing
// apiVersion instead of also cascading into missing-name and namespace
// findings. When an apiVersion is present, it must be in the bundle group.
func isTemplateKind(apiVersion, kind string) bool {
	switch kind {
	case "ObjectTemplate", "ObjectTemplateBuilder", "PatchTemplate", "PatchTemplateBuilder":
	default:
		return false
	}
	if apiVersion == "" {
		return true
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	return err == nil && gv.Group == "bundle.gke.io"
}

// clusterScopedKinds is a best-effort list of well-known cluster-scoped kinds.
// The Bundle library has no API discovery and cannot know whether an arbitrary
// kind is namespaced, so this list is only used to avoid emitting a
// missing-namespace warning for obvious cluster-scoped resources. It does not
// affect identity or collision checks.
var clusterScopedKinds = map[string]bool{
	"APIService":                     true,
	"CSIDriver":                      true,
	"CSINode":                        true,
	"CertificateSigningRequest":      true,
	"ClusterRole":                    true,
	"ClusterRoleBinding":             true,
	"ComponentStatus":                true,
	"CustomResourceDefinition":       true,
	"IngressClass":                   true,
	"MutatingWebhookConfiguration":   true,
	"Namespace":                      true,
	"Node":                           true,
	"PersistentVolume":               true,
	"PodSecurityPolicy":              true,
	"PriorityClass":                  true,
	"RuntimeClass":                   true,
	"StorageClass":                   true,
	"ValidatingWebhookConfiguration": true,
	"VolumeAttachment":               true,
}

// validateAPIVersion checks that an apiVersion is present and shaped like a
// Kubernetes group/version (or a bare version for the core group).
func validateAPIVersion(path *field.Path, apiVersion string) *field.Error {
	if apiVersion == "" {
		return field.Required(path, "apiVersion is required")
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return field.Invalid(path, apiVersion, "must have the form \"group/version\" or \"version\"")
	}
	if gv.Version == "" {
		return field.Invalid(path, apiVersion, "the version portion of apiVersion must not be empty")
	}
	if errs := validation.IsDNS1123Label(gv.Version); len(errs) > 0 {
		return field.Invalid(path, apiVersion, fmt.Sprintf("the version portion of apiVersion is invalid: %v", errs))
	}
	if gv.Group != "" {
		if errs := validation.IsDNS1123Subdomain(gv.Group); len(errs) > 0 {
			return field.Invalid(path, apiVersion, fmt.Sprintf("the group portion of apiVersion is invalid: %v", errs))
		}
	}
	return nil
}

// identityFor builds the identity for a final object. It returns false when the
// object lacks a usable apiVersion, kind, or metadata.name, in which case the
// identity checks are skipped because the field rules already report the
// problem.
func identityFor(obj *unstructured.Unstructured) (objectIdentity, bool) {
	apiVersion := obj.GetAPIVersion()
	kind := obj.GetKind()
	name := obj.GetName()
	if apiVersion == "" || kind == "" || name == "" {
		return objectIdentity{}, false
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil || gv.Version == "" {
		return objectIdentity{}, false
	}
	return objectIdentity{
		Group:     gv.Group,
		Kind:      kind,
		Namespace: obj.GetNamespace(),
		Name:      name,
	}, true
}

// normalizedContent returns a canonical serialization of an object. Comparing
// these bytes, rather than pointers or the original text, is how this package
// decides whether two colliding objects are "the same".
func normalizedContent(obj *unstructured.Unstructured) string {
	b, err := json.Marshal(obj.Object)
	if err != nil {
		// Unstructured content is JSON-compatible by construction, so this is
		// only reachable for a hand-built object with unsupported values.
		return fmt.Sprintf("<unmarshalable:%v>", err)
	}
	return string(b)
}

// objectValidator collects per-object findings for one component.
type objectValidator struct {
	component *bundle.Component
	ref       bundle.ComponentReference
	ordinal   int
	phase     Phase
	collect   *collector

	occurrences []objectOccurrence
}

func componentObjectPath(ref bundle.ComponentReference, index int) *field.Path {
	return cPath(ref).Child("Spec", "Objects").Index(index)
}

func (v *objectValidator) path(index int) *field.Path {
	return componentObjectPath(v.ref, index)
}

// validate runs the object-level rules and returns the occurrences that
// participate in identity checks.
func (v *objectValidator) validate() []objectOccurrence {
	for i, obj := range v.component.Spec.Objects {
		v.validateObject(i, obj)
	}
	return v.occurrences
}

func (v *objectValidator) validateObject(index int, obj *unstructured.Unstructured) {
	if obj == nil {
		v.collect.addError(v.ref, index, field.Required(v.path(index), "objects must not be null"))
		return
	}

	apiVersion := obj.GetAPIVersion()
	kind := obj.GetKind()

	if err := validateAPIVersion(v.path(index).Child("APIVersion"), apiVersion); err != nil {
		v.collect.addError(v.ref, index, err)
	}
	if kind == "" {
		v.collect.addError(v.ref, index, field.Required(v.path(index).Child("Kind"), "kind is required"))
	}

	// Template objects exist only before rendering. In the pre-render phase
	// they are checked as templates; in the rendered phase they are treated as
	// final objects, because a template that survives rendering is a mistake.
	if v.phase == PhasePreRender && isTemplateKind(apiVersion, kind) {
		v.validateTemplateObject(index, obj)
		return
	}

	v.validateFinalObject(index, obj)
}

// validateTemplateObject checks an ObjectTemplate or PatchTemplate as it exists
// before rendering. Templates are not final Kubernetes objects: metadata.name is
// optional, and generateName is not meaningful only for final objects.
func (v *objectValidator) validateTemplateObject(index int, obj *unstructured.Unstructured) {
	if name := obj.GetName(); name != "" {
		for _, msg := range validation.IsDNS1123Subdomain(name) {
			v.collect.addError(v.ref, index, field.Invalid(v.path(index).Child("Metadata", "Name"), name, msg))
		}
	}
}

func (v *objectValidator) validateFinalObject(index int, obj *unstructured.Unstructured) {
	namePath := v.path(index).Child("Metadata", "Name")
	name := obj.GetName()
	generateName := obj.GetGenerateName()

	switch {
	case name == "" && generateName != "":
		// Bundle objects are exported as a fixed set; nothing will ever
		// generate a name for them.
		v.collect.addError(v.ref, index, field.Forbidden(
			v.path(index).Child("Metadata", "GenerateName"),
			"generateName has no meaning in a Bundle; set metadata.name instead"))
	case name == "":
		v.collect.addError(v.ref, index, field.Required(namePath, "metadata.name is required"))
	default:
		for _, msg := range validation.IsDNS1123Subdomain(name) {
			v.collect.addError(v.ref, index, field.Invalid(namePath, name, msg))
		}
	}

	v.warnOnMissingNamespace(index, obj)

	if id, ok := identityFor(obj); ok {
		v.occurrences = append(v.occurrences, objectOccurrence{
			id:               id,
			componentOrdinal: v.ordinal,
			ref:              v.ref,
			index:            index,
			content:          normalizedContent(obj),
			allowDuplicate:   allowDuplicate(obj),
		})
	}
}

func (v *objectValidator) warnOnMissingNamespace(index int, obj *unstructured.Unstructured) {
	if obj.GetNamespace() != "" || clusterScopedKinds[obj.GetKind()] {
		return
	}
	v.collect.addWarning(v.ref, index, field.Invalid(
		v.path(index).Child("Metadata", "Namespace"),
		"",
		"object has no explicit namespace; the Bundle library does not know whether this kind is namespaced, so it is treated as namespace \"\""))
}

// validateIdentities performs the duplicate-identity checks over already
// collected occurrences. It runs in one pass over a hash table keyed by
// identity; there are no pairwise comparisons across all objects.
func validateIdentities(occurrences []objectOccurrence, opts Options) []validationIssue {
	byIdentity := make(map[objectIdentity][]objectOccurrence)
	var order []objectIdentity
	for _, o := range occurrences {
		if _, ok := byIdentity[o.id]; !ok {
			order = append(order, o.id)
		}
		byIdentity[o.id] = append(byIdentity[o.id], o)
	}

	// Sorting identities makes the set of checks independent of input order,
	// even though the findings themselves are re-sorted by the caller.
	sort.Slice(order, func(i, j int) bool {
		if order[i].Group != order[j].Group {
			return order[i].Group < order[j].Group
		}
		if order[i].Kind != order[j].Kind {
			return order[i].Kind < order[j].Kind
		}
		if order[i].Namespace != order[j].Namespace {
			return order[i].Namespace < order[j].Namespace
		}
		return order[i].Name < order[j].Name
	})

	var issues []validationIssue
	for _, id := range order {
		group := byIdentity[id]
		if len(group) < 2 {
			continue
		}
		sort.SliceStable(group, func(i, j int) bool {
			return lessOccurrence(group[i], group[j])
		})

		// The earliest occurrence in the group is the baseline. Every other
		// occurrence is reported once against the baseline, so a colliding pair
		// never appears as both "A vs B" and "B vs A".
		baseline := group[0]
		for _, other := range group[1:] {
			if issue, ok := duplicateIssue(baseline, other, opts); ok {
				issues = append(issues, issue)
			}
		}
	}
	return issues
}

// lessOccurrence orders occurrences by component reference and object index.
// Content and the component ordinal only break ties, which can happen if the
// input contains two component entries with the same reference; ordering by
// content keeps the baseline stable regardless of the order those entries were
// supplied in.
func lessOccurrence(a, b objectOccurrence) bool {
	if a.ref.ComponentName != b.ref.ComponentName {
		return a.ref.ComponentName < b.ref.ComponentName
	}
	if a.ref.Version != b.ref.Version {
		return a.ref.Version < b.ref.Version
	}
	if a.index != b.index {
		return a.index < b.index
	}
	if a.content != b.content {
		return a.content < b.content
	}
	return a.componentOrdinal < b.componentOrdinal
}

// duplicateIssue builds the finding for one occurrence of an identity that was
// already seen at the baseline. The bool result is false when the duplication is
// explicitly allowed or otherwise exempt.
func duplicateIssue(baseline, other objectOccurrence, opts Options) (validationIssue, bool) {
	sameComponent := baseline.componentOrdinal == other.componentOrdinal && baseline.ref == other.ref
	path := componentObjectPath(other.ref, other.index).Child("Metadata", "Name")

	if sameComponent {
		// This cannot be an intentional override: a component has no ordering
		// semantics between its own objects. Exact repetition is almost always a
		// copy-paste artifact from concatenating documents.
		if baseline.content == other.content {
			return validationIssue{
				ref:         other.ref,
				objectIndex: other.index,
				severity:    SeverityWarning,
				err: field.Duplicate(path, fmt.Sprintf(
					"object identity %s; it is byte-identical to Spec.Objects[%d] in the same component and should be removed",
					other.id, baseline.index)),
			}, true
		}
		return validationIssue{
			ref:         other.ref,
			objectIndex: other.index,
			severity:    SeverityError,
			err: field.Duplicate(path, fmt.Sprintf(
				"object identity %s already appears at Spec.Objects[%d] in the same component with different content", other.id, baseline.index)),
		}, true
	}

	// Between components, the Bundle has no override semantics: a later
	// component does not replace an earlier one. An explicit annotation on both
	// objects is the only way to say the duplication is intended.
	if other.allowDuplicate && baseline.allowDuplicate {
		return validationIssue{}, false
	}

	if opts.Scope == ScopeUnselectedVersions &&
		baseline.ref.ComponentName == other.ref.ComponentName &&
		baseline.ref.Version != other.ref.Version {
		// Different versions of the same component name are alternative
		// versions. Their objects naturally share names and are not compared.
		// The same version appearing twice is not an alternative; it is a real
		// duplicate.
		return validationIssue{}, false
	}

	severity := opts.CrossComponentDuplicateSeverity
	if severity == "" {
		severity = SeverityWarning
	}

	detail := fmt.Sprintf("object identity %s also appears at %s.Spec.Objects[%d]",
		other.id, cPath(baseline.ref).String(), baseline.index)
	if !(other.allowDuplicate && baseline.allowDuplicate) {
		detail += fmt.Sprintf("; if the duplication is intentional, add the annotation %q on both objects",
			bundle.AllowDuplicateObjectIdentifier)
	}
	return validationIssue{
		ref:         other.ref,
		objectIndex: other.index,
		severity:    severity,
		err:         field.Duplicate(path, detail),
	}, true
}

func allowDuplicate(obj *unstructured.Unstructured) bool {
	return obj.GetAnnotations()[string(bundle.AllowDuplicateObjectIdentifier)] == "true"
}
