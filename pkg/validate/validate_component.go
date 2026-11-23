// Copyright 2018 Google LLC
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
	"fmt"
	"regexp"

	bundle "github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/apis/bundle/v1alpha1"
	"github.com/blang/semver"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var (
	apiVersionPattern = regexp.MustCompile(`^bundle.gke.io/\w+$`)
)

// Components validates a list of components. It is equivalent to
// ValidateComponents with default options and returns only the errors.
func Components(components []*bundle.Component) field.ErrorList {
	return ValidateComponents(components, Options{}).Errors
}

// Component validates a single component. It is equivalent to
// ValidateComponent with default options and returns only the errors.
func Component(c *bundle.Component) field.ErrorList {
	return ValidateComponent(c, Options{}).Errors
}

// ValidateComponent validates a single component and returns the full report,
// including object-level identity checks and warnings.
func ValidateComponent(c *bundle.Component, opts Options) *Report {
	opts = opts.withDefaults()
	col := &collector{}
	if c != nil {
		ref := c.ComponentReference()
		for _, err := range componentMetadataErrors(c) {
			col.addError(ref, -1, err)
		}
		if c.Spec.ComponentName != "" && c.Spec.Version != "" {
			ov := &objectValidator{component: c, ref: ref, ordinal: 0, phase: opts.Phase, collect: col}
			occurrences := ov.validate()
			col.objectsValidated = true
			for _, issue := range validateIdentities(occurrences, opts) {
				col.add(issue.ref, issue.objectIndex, issue.severity, issue.err)
			}
		}
	}
	return col.report(opts)
}

// ValidateComponents validates a list of components as a single exported set.
// Object identities are checked across all components; by default, different
// versions of the same component name are still compared, because all of them
// are presumed to be exported together. Use Options.Scope to relax that.
func ValidateComponents(components []*bundle.Component, opts Options) *Report {
	opts = opts.withDefaults()
	col := &collector{}
	var occurrences []objectOccurrence
	for i, c := range components {
		if c == nil {
			continue
		}
		ref := c.ComponentReference()
		for _, err := range componentMetadataErrors(c) {
			col.addError(ref, -1, err)
		}
		if c.Spec.ComponentName == "" || c.Spec.Version == "" {
			// Object identity checks depend on a usable component reference.
			continue
		}
		ov := &objectValidator{component: c, ref: ref, ordinal: i, phase: opts.Phase, collect: col}
		occurrences = append(occurrences, ov.validate()...)
		col.objectsValidated = true
	}
	for _, issue := range validateIdentities(occurrences, opts) {
		col.add(issue.ref, issue.objectIndex, issue.severity, issue.err)
	}
	return col.report(opts)
}

// ComponentSet validates the metadata and references of a component set. The
// set contains only references, so no object content is available and no claim
// is made about objects. Use ValidateComponentSet to expand references against
// a component pool.
func ComponentSet(cs *bundle.ComponentSet) field.ErrorList {
	return ValidateComponentSet(cs, nil, Options{}).Errors
}

// ValidateComponentSet validates a component set and, when pool is non-nil,
// expands each reference to the exact component with the same name and version
// and validates that component's objects. References that cannot be expanded
// are recorded in Report.MissingReferences, and Report.ObjectsValidated is only
// set when every reference was found.
//
// Components in the pool that the set does not reference are ignored. The
// default scope for a set is ScopeUnselectedVersions: a set may list several
// versions of a component as alternatives, so objects from different versions
// of the same component name are not treated as colliding. Callers validating
// objects that will be exported together should pass ScopeExported.
func ValidateComponentSet(cs *bundle.ComponentSet, pool []*bundle.Component, opts Options) *Report {
	if opts.Scope == "" {
		opts.Scope = ScopeUnselectedVersions
	}
	opts = opts.withDefaults()

	col := &collector{}
	if cs == nil {
		return col.report(opts)
	}
	ref := bundle.ComponentReference{ComponentName: cs.Spec.SetName, Version: cs.Spec.Version}
	for _, err := range componentSetMetadataErrors(cs) {
		col.addError(ref, -1, err)
	}
	if cs.Spec.SetName == "" || cs.Spec.Version == "" {
		return col.report(opts)
	}

	for _, err := range componentReferenceErrors(cs) {
		col.addError(ref, -1, err)
	}

	if pool == nil {
		return col.report(opts)
	}

	index := make(map[bundle.ComponentReference]*bundle.Component, len(pool))
	for _, c := range pool {
		if c == nil {
			continue
		}
		cref := c.ComponentReference()
		if _, ok := index[cref]; ok {
			// Duplicate references in the pool are the pool owner's problem; keep
			// the first so expansion is deterministic.
			continue
		}
		index[cref] = c
	}

	var occurrences []objectOccurrence
	allFound := true
	for i, cRef := range cs.Spec.Components {
		c, ok := index[cRef]
		if !ok {
			allFound = false
			col.missing = append(col.missing, cRef)
			continue
		}
		for _, err := range componentMetadataErrors(c) {
			col.addError(cRef, -1, err)
		}
		ov := &objectValidator{component: c, ref: cRef, ordinal: i, phase: opts.Phase, collect: col}
		occurrences = append(occurrences, ov.validate()...)
	}
	for _, issue := range validateIdentities(occurrences, opts) {
		col.add(issue.ref, issue.objectIndex, issue.severity, issue.err)
	}
	col.objectsValidated = allFound
	return col.report(opts)
}

// ValidateBundle validates every component in a bundle as one exported set,
// because a bundle's components are all exported together.
func ValidateBundle(b *bundle.Bundle, opts Options) *Report {
	if b == nil {
		return (&collector{}).report(opts)
	}
	if opts.Scope == "" {
		opts.Scope = ScopeExported
	}
	return ValidateComponents(b.Components, opts)
}

// componentReferenceErrors checks that each component reference in a set is
// well-formed and unique.
func componentReferenceErrors(cs *bundle.ComponentSet) field.ErrorList {
	errs := field.ErrorList{}
	p := field.NewPath("ComponentSet").Child("Spec", "Components")
	seen := make(map[bundle.ComponentReference]int)
	for i, ref := range cs.Spec.Components {
		rp := p.Index(i)
		if ref.ComponentName == "" {
			errs = append(errs, field.Required(rp.Child("ComponentName"), "componentName is required"))
		} else {
			errs = append(errs, validateName(rp.Child("ComponentName"), ref.ComponentName)...)
		}
		if ref.Version == "" {
			errs = append(errs, field.Required(rp.Child("Version"), "version is required"))
		} else if _, err := semver.Parse(ref.Version); err != nil {
			errs = append(errs, field.Invalid(rp.Child("Version"), ref.Version,
				fmt.Sprintf("must be a SemVer version: %v", err)))
		}
		if first, ok := seen[ref]; ok {
			errs = append(errs, field.Duplicate(rp, fmt.Sprintf(
				"component reference %v already appears at Spec.Components[%d]", ref, first)))
		} else {
			seen[ref] = i
		}
	}
	return errs
}

// componentMetadataErrors validates the component's own metadata. It does not
// look at objects.
func componentMetadataErrors(c *bundle.Component) field.ErrorList {
	errs := field.ErrorList{}
	pi := field.NewPath("Component")

	n := c.Spec.ComponentName
	if n == "" {
		errs = append(errs, field.Required(pi.Child("Spec", "ComponentName"), "components must have ComponentName"))
	}
	ver := c.Spec.Version
	if ver == "" {
		errs = append(errs, field.Required(pi.Child("Spec", "Version"), "components must have a Version"))
	}
	if n == "" || ver == "" {
		// Subsequent validation relies on components having a unique name+version pair.
		return errs
	}

	p := cPath(c.ComponentReference())

	if nameErrs := validateName(p.Child("Spec", "ComponentName"), n); len(nameErrs) > 0 {
		errs = append(errs, nameErrs...)
	}

	api := c.APIVersion
	if !apiVersionPattern.MatchString(api) {
		errs = append(errs, field.Invalid(p.Child("APIVersion"), api, "must have the form \"bundle.gke.io/<version>\""))
	}

	expType := "Component"

	if k := c.Kind; k != expType {
		errs = append(errs, field.Invalid(p.Child("Kind"), k, "kind must be Component"))
	}

	if _, err := semver.Parse(ver); err != nil {
		errs = append(errs, field.Invalid(p.Child("Spec", "Version"), ver, fmt.Sprintf("must be a SemVer version: %v", err)))
	}

	return errs
}

// componentSetMetadataErrors validates the component set's own metadata. It
// does not look at component references or objects.
func componentSetMetadataErrors(cs *bundle.ComponentSet) field.ErrorList {
	p := field.NewPath("ComponentSet")

	errs := field.ErrorList{}
	if cs == nil {
		return errs
	}

	n := cs.Spec.SetName
	if n == "" {
		errs = append(errs, field.Required(p.Child("Spec", "SetName"), "setName is required"))
	}
	ver := cs.Spec.Version
	if ver == "" {
		errs = append(errs, field.Required(p.Child("Spec", "Version"), "version is required"))
	}

	if n == "" || ver == "" {
		// Other validation relies on components having a unique name+version pair.
		return errs
	}

	if nameErrs := validateName(p.Child("Spec", "SetName"), n); len(nameErrs) > 0 {
		errs = append(errs, nameErrs...)
	}

	api := cs.APIVersion
	if !apiVersionPattern.MatchString(api) {
		errs = append(errs, field.Invalid(p.Child("APIVersion"), api, "must have an apiVersion of the form \"bundle.gke.io/<version>\""))
	}

	expType := "ComponentSet"
	if k := cs.Kind; k != expType {
		errs = append(errs, field.Invalid(p.Child("Kind"), k, "must be ComponentSet"))
	}

	if _, err := semver.Parse(ver); err != nil {
		errs = append(errs, field.Invalid(p.Child("Spec", "Version"), ver, fmt.Sprintf("must be a SemVer version: %v", err)))
	}

	return errs
}

func cPath(ref bundle.ComponentReference) *field.Path {
	return field.NewPath("Component").Key(fmt.Sprintf("%v", ref))
}
