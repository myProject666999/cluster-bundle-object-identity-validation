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

// Package validate provides validation for types in the cluster bundle
// repository.
//
// # What is validated
//
// Component and ComponentSet metadata is checked for a valid name, a SemVer
// version, and the expected apiVersion and kind. In addition, every object in a
// Component's Spec.Objects is checked for the fields that Bundle consumers rely
// on:
//
//   - apiVersion must be present and have the form "group/version" or "version".
//   - kind must be present.
//   - metadata.name must be present, must be a DNS-1123 subdomain, and must not
//     be empty. An object that specifies only generateName is an error: a Bundle
//     exports a fixed set of objects, so nothing will ever generate the name.
//
// Rules specific to individual resources, such as ConfigMap data keys, are not
// checked here.
//
// # Object identity
//
// Two objects are the same resource when their (group, kind, namespace, name)
// agree. The version part of apiVersion is not part of the identity, because
// apps/v1 and apps/v1beta1 Deployment objects address the same resource. This
// package does not use core.ObjectRef for identity: that type has no namespace,
// so it cannot tell "same name in different namespaces" from a real collision.
//
// The Bundle library is deliberately independent of API discovery, so it cannot
// know whether a particular kind is namespaced. For identity purposes a
// namespace is used only when it is written explicitly in metadata.namespace; a
// missing namespace is treated as the empty namespace. This means that a
// namespaced object with no explicit namespace will be considered identical to
// another object of the same group, kind, and name that also omits its
// namespace, even if a cluster would place them in different namespaces.
// Objects without an explicit namespace produce a warning (except for a small
// list of well-known cluster-scoped kinds) so this limitation is visible.
//
// # Duplicate identities
//
// Within one component, two objects with the same identity are an error when
// their content differs, because a component has no ordering semantics that
// would make an override meaningful. When the two objects are byte-identical
// after canonical JSON serialization, the finding is downgraded to a warning:
// the usual cause is a duplicated document from concatenated YAML, and the
// suggestion is to remove one copy.
//
// Across components the answer is subtler. Two components may each carry an
// object with the same identity by accident, or on purpose so that the later one
// overrides the earlier one, but a Bundle has no override semantics. This
// package therefore reports cross-component duplicates as warnings by default
// for the current release, so that existing packages keep passing while they are
// inspected; a future release will make them errors. Callers can select the
// behavior now with Options.CrossComponentDuplicateSeverity.
//
// Duplication can be declared intentional with the annotation
// "bundle.gke.io/allow-duplicate-object": "true". The annotation must appear on
// both objects involved; one-sided annotations are ignored.
//
// # Multiple versions of one component
//
// A ComponentSet may reference several versions of the same component name as
// alternatives. Their objects naturally share names. Options.Scope selects how
// this is handled:
//
//   - ScopeUnselectedVersions ignores collisions between components that share a
//     ComponentName, and still compares components with different names. It is
//     the default for ComponentSet validation, where no final version has been
//     selected.
//   - ScopeExported compares every component, because all of them are presumed
//     to be exported at the same time. It is the default for Component, Bundle,
//     and plain component-list validation.
//
// A caller that validates objects that will actually be exported together must
// choose ScopeExported; differing versions are not a license to collide.
//
// # Pre-render and rendered phases
//
// A component contains ObjectTemplate and PatchTemplate objects before it is
// rendered, and those are not final Kubernetes objects. Options.Phase selects
// the rules:
//
//   - PhasePreRender (the default) checks template objects as templates. Their
//     metadata.name is optional, generateName is irrelevant, and they do not
//     participate in identity checks.
//   - PhaseRendered treats every object as a final object. A template that
//     survives rendering is reported as an error, because it should have been
//     consumed.
//
// Callers should validate once before rendering and once after, with the
// corresponding phase.
//
// # Reporting
//
// Validation returns a Report containing Errors and Warnings. Only errors make
// the input invalid; warnings are informational. Both lists are capped by
// Options.MaxErrors and Options.MaxWarnings. When a cap is exceeded, the
// findings that sort last are omitted, a summary finding with the total count is
// appended, and Report.TotalErrors / Report.TotalWarnings retain the true count.
//
// Findings are sorted by component reference and object index, so the same input
// always produces byte-identical output, and the output does not depend on the
// order in which components were supplied. Field paths have the form
// Component[{name version}].Spec.Objects[3].Metadata.Name, which editors and
// scripts can use to locate the problem.
//
// A duplicate identity is reported once. The earliest occurrence in sort order
// is the baseline, and every later occurrence is reported against it; a pair is
// never reported both as "A vs B" and as "B vs A".
//
// # ComponentSet validation
//
// A ComponentSet contains only references. ValidateComponentSet accepts an
// optional pool of components and expands each reference to the exact component
// with the same name and version. References that cannot be expanded are listed
// in Report.MissingReferences, and Report.ObjectsValidated is true only when
// every reference was found and its objects were checked. Without a pool, only
// the references themselves are checked, and no claim is made about objects.
package validate
