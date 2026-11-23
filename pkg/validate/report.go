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
	"fmt"
	"sort"

	bundle "github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/apis/bundle/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	// DefaultMaxErrors is the number of errors reported before the report is
	// truncated. The omitted errors are always the ones that sort last.
	DefaultMaxErrors = 100

	// DefaultMaxWarnings is the number of warnings reported before the report
	// is truncated. The omitted warnings are always the ones that sort last.
	DefaultMaxWarnings = 100
)

// Severity classifies a validation finding.
type Severity string

const (
	// SeverityError means the finding makes the input invalid.
	SeverityError Severity = "error"

	// SeverityWarning means the finding is suspicious, but does not make the
	// input invalid.
	SeverityWarning Severity = "warning"
)

// Phase selects which rules apply to Bundle template objects. A component
// contains ObjectTemplate and PatchTemplate objects before it is rendered, and
// those are not final Kubernetes objects. See the package documentation for
// details.
type Phase string

const (
	// PhasePreRender validates a component as it exists before templates are
	// applied. Template objects are checked as templates and are not subject to
	// final-object rules such as requiring metadata.name.
	PhasePreRender Phase = "pre-render"

	// PhaseRendered validates a component after templates have been applied.
	// Every object is treated as a final Kubernetes object.
	PhaseRendered Phase = "rendered"
)

// IdentityScope selects how components that share a ComponentName but have
// different versions are treated during cross-component identity checks.
type IdentityScope string

const (
	// ScopeExported treats every component in the input as being exported at
	// the same time. Different versions of the same component therefore do not
	// exempt objects from collision checks.
	ScopeExported IdentityScope = "exported"

	// ScopeUnselectedVersions treats components with the same ComponentName as
	// alternative versions. Collisions between different versions of the same
	// component name are not reported, and only components with different names
	// are compared.
	ScopeUnselectedVersions IdentityScope = "unselected-versions"
)

// Options controls validation behavior. The zero value is valid and selects
// the documented defaults.
type Options struct {
	// Phase selects pre-render or rendered rules. Empty defaults to
	// PhasePreRender.
	Phase Phase

	// Scope selects how multiple versions of the same component are treated
	// during cross-component checks. Empty defaults to ScopeExported.
	Scope IdentityScope

	// CrossComponentDuplicateSeverity is the severity used for duplicate object
	// identities found across components. Empty defaults to SeverityWarning for
	// this release, so that existing packages can be inspected without failing
	// validation; a future release will default to SeverityError. Callers that
	// are ready can set SeverityError now. Objects with byte-identical normalized
	// content in the same component are always reported as warnings.
	CrossComponentDuplicateSeverity Severity

	// MaxErrors caps the number of errors in a Report. Zero selects
	// DefaultMaxErrors; a negative value means no limit. When the cap is
	// exceeded, the errors that sort last are omitted and a final summary error
	// is appended. The summary error is not included in Report.TotalErrors.
	MaxErrors int

	// MaxWarnings caps the number of warnings in a Report. Zero selects
	// DefaultMaxWarnings; a negative value means no limit. Truncation behaves
	// like MaxErrors.
	MaxWarnings int
}

func (o Options) withDefaults() Options {
	if o.Phase == "" {
		o.Phase = PhasePreRender
	}
	if o.Scope == "" {
		o.Scope = ScopeExported
	}
	if o.CrossComponentDuplicateSeverity == "" {
		o.CrossComponentDuplicateSeverity = SeverityWarning
	}
	if o.MaxErrors == 0 {
		o.MaxErrors = DefaultMaxErrors
	}
	if o.MaxWarnings == 0 {
		o.MaxWarnings = DefaultMaxWarnings
	}
	return o
}

// Report is the result of validating one or more components.
type Report struct {
	// Errors are the findings that make the input invalid. When Options.MaxErrors
	// is exceeded, this list is truncated and ends with one summary error.
	Errors field.ErrorList

	// Warnings are suspicious findings that do not make the input invalid.
	// Warnings are subject to Options.MaxWarnings in the same way as Errors.
	Warnings field.ErrorList

	// TotalErrors is the number of errors found before truncation.
	TotalErrors int

	// TotalWarnings is the number of warnings found before truncation.
	TotalWarnings int

	// TruncatedErrors is true when TotalErrors exceeds the error cap.
	TruncatedErrors bool

	// TruncatedWarnings is true when TotalWarnings exceeds the warning cap.
	TruncatedWarnings bool

	// ObjectsValidated is true when component objects were available and were
	// checked. It is primarily meaningful for ComponentSet validation: when the
	// caller does not supply a component pool, or when the pool does not contain
	// every referenced component, only the references themselves are checked.
	ObjectsValidated bool

	// MissingReferences lists ComponentSet references that could not be expanded
	// from the supplied component pool. Their objects were not validated.
	MissingReferences []bundle.ComponentReference
}

// HasErrors reports whether validation produced any error.
func (r *Report) HasErrors() bool {
	return r != nil && r.TotalErrors > 0
}

// validationIssue is an error or warning plus the sort key needed to make
// output deterministic.
type validationIssue struct {
	ref         bundle.ComponentReference
	objectIndex int // -1 for a component-level finding.
	severity    Severity
	err         *field.Error
}

type collector struct {
	issues           []validationIssue
	missing          []bundle.ComponentReference
	objectsValidated bool
}

func (c *collector) add(ref bundle.ComponentReference, objectIndex int, severity Severity, err *field.Error) {
	c.issues = append(c.issues, validationIssue{
		ref:         ref,
		objectIndex: objectIndex,
		severity:    severity,
		err:         err,
	})
}

func (c *collector) addError(ref bundle.ComponentReference, objectIndex int, err *field.Error) {
	c.add(ref, objectIndex, SeverityError, err)
}

func (c *collector) addWarning(ref bundle.ComponentReference, objectIndex int, err *field.Error) {
	c.add(ref, objectIndex, SeverityWarning, err)
}

// lessIssue orders findings by component reference and object index, which is
// the stable order promised to callers. The error text only breaks ties, so
// the same logical input always produces byte-identical output even when the
// input components are presented in a different order.
func lessIssue(a, b validationIssue) bool {
	if a.ref.ComponentName != b.ref.ComponentName {
		return a.ref.ComponentName < b.ref.ComponentName
	}
	if a.ref.Version != b.ref.Version {
		return a.ref.Version < b.ref.Version
	}
	if a.objectIndex != b.objectIndex {
		return a.objectIndex < b.objectIndex
	}
	return a.err.Error() < b.err.Error()
}

func (c *collector) report(opts Options) *Report {
	opts = opts.withDefaults()

	sort.SliceStable(c.issues, func(i, j int) bool {
		return lessIssue(c.issues[i], c.issues[j])
	})

	r := &Report{
		ObjectsValidated:  c.objectsValidated,
		MissingReferences: c.missing,
	}
	for _, issue := range c.issues {
		if issue.severity == SeverityWarning {
			r.Warnings = append(r.Warnings, issue.err)
		} else {
			r.Errors = append(r.Errors, issue.err)
		}
	}

	r.TotalErrors = len(r.Errors)
	r.TotalWarnings = len(r.Warnings)
	r.Errors, r.TruncatedErrors = truncate(r.Errors, opts.MaxErrors, "errors")
	r.Warnings, r.TruncatedWarnings = truncate(r.Warnings, opts.MaxWarnings, "warnings")
	return r
}

// truncate keeps at most max findings. When it has to omit findings, it appends
// a summary finding with the total count. Because the input is already sorted,
// the omitted findings are always the ones that sort last.
func truncate(list field.ErrorList, max int, kind string) (field.ErrorList, bool) {
	if max <= 0 || len(list) <= max {
		return list, false
	}
	total := len(list)
	out := make(field.ErrorList, 0, max+1)
	out = append(out, list[:max]...)
	out = append(out, field.Invalid(
		field.NewPath("Validation"),
		total,
		fmt.Sprintf("too many %s: %d total; only the first %d are shown", kind, total, max),
	))
	return out, true
}
