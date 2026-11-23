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
	"reflect"
	"strings"
	"testing"

	bundle "github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/apis/bundle/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func testObj(apiVersion, kind, namespace, name string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetAPIVersion(apiVersion)
	o.SetKind(kind)
	o.SetNamespace(namespace)
	o.SetName(name)
	return o
}

func testComponent(name, version string, objs ...*unstructured.Unstructured) *bundle.Component {
	c := &bundle.Component{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "bundle.gke.io/v1alpha1",
			Kind:       "Component",
		},
	}
	c.Spec.ComponentName = name
	c.Spec.Version = version
	c.Spec.Objects = objs
	return c
}

func errorStrings(errs field.ErrorList) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Error()
	}
	return out
}

func assertFields(t *testing.T, errs field.ErrorList, wantPaths []string) {
	t.Helper()
	var got []string
	for _, e := range errs {
		got = append(got, e.Field)
	}
	if len(wantPaths) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(got, wantPaths) {
		t.Errorf("error fields = %v, want %v\nall errors: %v", got, wantPaths, errorStrings(errs))
	}
}

func hasErrorContaining(errs field.ErrorList, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Error(), substr) {
			return true
		}
	}
	return false
}

func TestSameComponentDuplicate(t *testing.T) {
	tests := []struct {
		name           string
		objects        []*unstructured.Unstructured
		wantErrors     int
		wantWarnings   int
		wantErrSubstr  string
		wantErrPath    string
		wantWarningSub string
	}{
		{
			name: "different content is an error",
			objects: []*unstructured.Unstructured{
				testObj("v1", "ConfigMap", "ns", "shared"),
				withData(testObj("v1", "ConfigMap", "ns", "shared"), "k", "v"),
			},
			wantErrors:    1,
			wantErrPath:   "Component[{foo 1.0.0}].Spec.Objects[1].Metadata.Name",
			wantErrSubstr: "already appears at Spec.Objects[0] in the same component with different content",
		},
		{
			name: "byte-identical content is a warning",
			objects: []*unstructured.Unstructured{
				testObj("v1", "ConfigMap", "ns", "shared"),
				testObj("v1", "ConfigMap", "ns", "shared"),
			},
			wantErrors:     0,
			wantWarnings:   1,
			wantErrPath:    "Component[{foo 1.0.0}].Spec.Objects[1].Metadata.Name",
			wantWarningSub: "byte-identical to Spec.Objects[0]",
		},
		{
			name: "three occurrences report against the baseline only",
			objects: []*unstructured.Unstructured{
				testObj("v1", "ConfigMap", "ns", "shared"),
				withData(testObj("v1", "ConfigMap", "ns", "shared"), "k", "a"),
				withData(testObj("v1", "ConfigMap", "ns", "shared"), "k", "b"),
			},
			wantErrors:  2,
			wantErrPath: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := testComponent("foo", "1.0.0", tc.objects...)
			r := ValidateComponent(c, Options{})
			if len(r.Errors) != tc.wantErrors {
				t.Fatalf("got %d errors, want %d: %v", len(r.Errors), tc.wantErrors, errorStrings(r.Errors))
			}
			if len(r.Warnings) != tc.wantWarnings {
				t.Fatalf("got %d warnings, want %d: %v", len(r.Warnings), tc.wantWarnings, errorStrings(r.Warnings))
			}
			if tc.wantErrors == 1 {
				assertFields(t, r.Errors, []string{tc.wantErrPath})
			}
			if tc.wantErrSubstr != "" && !hasErrorContaining(r.Errors, tc.wantErrSubstr) {
				t.Errorf("errors %v do not contain %q", errorStrings(r.Errors), tc.wantErrSubstr)
			}
			if tc.wantWarningSub != "" && !hasErrorContaining(r.Warnings, tc.wantWarningSub) {
				t.Errorf("warnings %v do not contain %q", errorStrings(r.Warnings), tc.wantWarningSub)
			}
		})
	}
}

func withData(o *unstructured.Unstructured, key, value string) *unstructured.Unstructured {
	o.Object["data"] = map[string]interface{}{key: value}
	return o
}

func TestCrossComponentDuplicates(t *testing.T) {
	tests := []struct {
		name         string
		a            *bundle.Component
		b            *bundle.Component
		opts         Options
		wantErrors   int
		wantWarnings int
	}{
		{
			name: "same name in different namespaces is fine",
			a:    testComponent("a", "1.0.0", testObj("v1", "ConfigMap", "ns-a", "shared")),
			b:    testComponent("b", "1.0.0", testObj("v1", "ConfigMap", "ns-b", "shared")),
		},
		{
			name:         "same name in same namespace collides",
			a:            testComponent("a", "1.0.0", testObj("v1", "ConfigMap", "ns", "shared")),
			b:            testComponent("b", "1.0.0", testObj("v1", "ConfigMap", "ns", "shared")),
			wantWarnings: 1,
		},
		{
			name:         "api version differences do not distinguish identity",
			a:            testComponent("a", "1.0.0", testObj("apps/v1", "Deployment", "ns", "shared")),
			b:            testComponent("b", "1.0.0", testObj("apps/v1beta1", "Deployment", "ns", "shared")),
			wantWarnings: 1,
		},
		{
			name:         "different kinds do not collide",
			a:            testComponent("a", "1.0.0", testObj("v1", "ConfigMap", "ns", "shared")),
			b:            testComponent("b", "1.0.0", testObj("v1", "Secret", "ns", "shared")),
			wantWarnings: 0,
		},
		{
			name: "annotation on both sides allows duplication",
			a:    testComponent("a", "1.0.0", annotated(testObj("v1", "ConfigMap", "ns", "shared"))),
			b:    testComponent("b", "1.0.0", annotated(testObj("v1", "ConfigMap", "ns", "shared"))),
		},
		{
			name:         "annotation on one side only does not",
			a:            testComponent("a", "1.0.0", annotated(testObj("v1", "ConfigMap", "ns", "shared"))),
			b:            testComponent("b", "1.0.0", testObj("v1", "ConfigMap", "ns", "shared")),
			wantWarnings: 1,
		},
		{
			name:       "cross-component duplicates can be promoted to errors",
			a:          testComponent("a", "1.0.0", testObj("v1", "ConfigMap", "ns", "shared")),
			b:          testComponent("b", "1.0.0", testObj("v1", "ConfigMap", "ns", "shared")),
			opts:       Options{CrossComponentDuplicateSeverity: SeverityError},
			wantErrors: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := ValidateComponents([]*bundle.Component{tc.a, tc.b}, tc.opts)
			if len(r.Errors) != tc.wantErrors {
				t.Errorf("got %d errors, want %d: %v", len(r.Errors), tc.wantErrors, errorStrings(r.Errors))
			}
			if len(r.Warnings) != tc.wantWarnings {
				t.Errorf("got %d warnings, want %d: %v", len(r.Warnings), tc.wantWarnings, errorStrings(r.Warnings))
			}
		})
	}
}

func annotated(o *unstructured.Unstructured) *unstructured.Unstructured {
	o.SetAnnotations(map[string]string{
		string(bundle.AllowDuplicateObjectIdentifier): "true",
	})
	return o
}

func TestVersionScoping(t *testing.T) {
	// etcd 1.0.0 and 2.0.0 each carry a Pod named etcd-server.
	makeComps := func() []*bundle.Component {
		return []*bundle.Component{
			testComponent("etcd", "1.0.0", testObj("v1", "Pod", "ns", "etcd-server")),
			testComponent("etcd", "2.0.0", testObj("v1", "Pod", "ns", "etcd-server")),
		}
	}

	t.Run("alternative versions do not collide", func(t *testing.T) {
		r := ValidateComponents(makeComps(), Options{Scope: ScopeUnselectedVersions})
		if len(r.Errors) != 0 {
			t.Errorf("got errors, want none: %v", errorStrings(r.Errors))
		}
	})

	t.Run("exported versions do collide", func(t *testing.T) {
		r := ValidateComponents(makeComps(), Options{Scope: ScopeExported})
		if len(r.Warnings) != 1 {
			t.Errorf("got %d warnings, want 1: %v", len(r.Warnings), errorStrings(r.Warnings))
		}
	})

	t.Run("different component names always collide", func(t *testing.T) {
		comps := []*bundle.Component{
			testComponent("etcd", "1.0.0", testObj("v1", "Pod", "ns", "etcd-server")),
			testComponent("etcd-other", "2.0.0", testObj("v1", "Pod", "ns", "etcd-server")),
		}
		r := ValidateComponents(comps, Options{Scope: ScopeUnselectedVersions})
		if len(r.Warnings) != 1 {
			t.Errorf("got %d warnings, want 1: %v", len(r.Warnings), errorStrings(r.Warnings))
		}
	})

	t.Run("the same version listed twice still collides", func(t *testing.T) {
		comps := []*bundle.Component{
			testComponent("etcd", "1.0.0", testObj("v1", "Pod", "ns", "etcd-server")),
			testComponent("etcd", "1.0.0", testObj("v1", "Pod", "ns", "etcd-server")),
		}
		r := ValidateComponents(comps, Options{Scope: ScopeUnselectedVersions})
		if len(r.Warnings) != 1 {
			t.Errorf("got %d warnings, want 1: %v", len(r.Warnings), errorStrings(r.Warnings))
		}
	})
}

func TestObjectRequiredFields(t *testing.T) {
	tests := []struct {
		name       string
		obj        *unstructured.Unstructured
		wantErrors int
		wantPath   string
		wantSubstr string
	}{
		{
			name:       "missing kind",
			obj:        testObj("v1", "", "ns", "x"),
			wantErrors: 1,
			wantPath:   "Component[{foo 1.0.0}].Spec.Objects[0].Kind",
			wantSubstr: "kind is required",
		},
		{
			name:       "missing apiVersion",
			obj:        testObj("", "Pod", "ns", "x"),
			wantErrors: 1,
			wantPath:   "Component[{foo 1.0.0}].Spec.Objects[0].APIVersion",
			wantSubstr: "apiVersion is required",
		},
		{
			name:       "missing name",
			obj:        testObj("v1", "Pod", "ns", ""),
			wantErrors: 1,
			wantPath:   "Component[{foo 1.0.0}].Spec.Objects[0].Metadata.Name",
			wantSubstr: "metadata.name is required",
		},
		{
			name: "only generateName",
			obj: func() *unstructured.Unstructured {
				o := testObj("v1", "Pod", "ns", "")
				o.SetGenerateName("generated-")
				return o
			}(),
			wantErrors: 1,
			wantPath:   "Component[{foo 1.0.0}].Spec.Objects[0].Metadata.GenerateName",
			wantSubstr: "generateName has no meaning in a Bundle",
		},
		{
			name:       "invalid name",
			obj:        testObj("v1", "Pod", "ns", "Not_A_DNS_Name"),
			wantErrors: 1,
			wantPath:   "Component[{foo 1.0.0}].Spec.Objects[0].Metadata.Name",
			wantSubstr: "RFC 1123 subdomain",
		},
		{
			name:       "invalid apiVersion",
			obj:        testObj("a/b/c", "Pod", "ns", "x"),
			wantErrors: 1,
			wantPath:   "Component[{foo 1.0.0}].Spec.Objects[0].APIVersion",
			wantSubstr: "must have the form",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := ValidateComponent(testComponent("foo", "1.0.0", tc.obj), Options{})
			if len(r.Errors) != tc.wantErrors {
				t.Fatalf("got %d errors, want %d: %v", len(r.Errors), tc.wantErrors, errorStrings(r.Errors))
			}
			assertFields(t, r.Errors, []string{tc.wantPath})
			if !hasErrorContaining(r.Errors, tc.wantSubstr) {
				t.Errorf("errors %v do not contain %q", errorStrings(r.Errors), tc.wantSubstr)
			}
		})
	}
}

func TestTemplatePhase(t *testing.T) {
	tmpl := func(apiVersion, kind, name string) *unstructured.Unstructured {
		return testObj(apiVersion, kind, "", name)
	}

	t.Run("pre-render accepts a template without a name", func(t *testing.T) {
		c := testComponent("foo", "1.0.0",
			tmpl("bundle.gke.io/v1alpha1", "ObjectTemplate", ""),
			tmpl("bundle.gke.io/v1alpha1", "PatchTemplate", ""),
		)
		r := ValidateComponent(c, Options{Phase: PhasePreRender})
		if len(r.Errors) != 0 {
			t.Errorf("got errors, want none: %v", errorStrings(r.Errors))
		}
	})

	t.Run("pre-render still validates a template name when present", func(t *testing.T) {
		c := testComponent("foo", "1.0.0",
			tmpl("bundle.gke.io/v1alpha1", "ObjectTemplate", "Not_Valid"),
		)
		r := ValidateComponent(c, Options{Phase: PhasePreRender})
		if len(r.Errors) != 1 {
			t.Errorf("got %d errors, want 1: %v", len(r.Errors), errorStrings(r.Errors))
		}
	})

	t.Run("rendered treats a surviving template as a final object", func(t *testing.T) {
		c := testComponent("foo", "1.0.0",
			tmpl("bundle.gke.io/v1alpha1", "ObjectTemplate", ""),
		)
		r := ValidateComponent(c, Options{Phase: PhaseRendered})
		if len(r.Errors) != 1 {
			t.Fatalf("got %d errors, want 1: %v", len(r.Errors), errorStrings(r.Errors))
		}
		if !hasErrorContaining(r.Errors, "metadata.name is required") {
			t.Errorf("errors %v do not mention metadata.name", errorStrings(r.Errors))
		}
	})

	t.Run("pre-render does not apply identity rules to templates", func(t *testing.T) {
		c := testComponent("foo", "1.0.0",
			tmpl("bundle.gke.io/v1alpha1", "ObjectTemplate", "same"),
			tmpl("bundle.gke.io/v1alpha1", "ObjectTemplate", "same"),
		)
		r := ValidateComponent(c, Options{Phase: PhasePreRender})
		if len(r.Errors) != 0 {
			t.Errorf("got errors, want none: %v", errorStrings(r.Errors))
		}
	})
}

func TestTruncation(t *testing.T) {
	var objs []*unstructured.Unstructured
	for i := 0; i < 10; i++ {
		o := testObj("v1", "ConfigMap", "ns", "shared")
		o.Object["index"] = i
		objs = append(objs, o)
	}
	c := testComponent("foo", "1.0.0", objs...)
	r := ValidateComponent(c, Options{MaxErrors: 3})
	if !r.TruncatedErrors {
		t.Fatal("expected errors to be truncated")
	}
	if r.TotalErrors != 9 {
		t.Fatalf("TotalErrors = %d, want 9", r.TotalErrors)
	}
	if len(r.Errors) != 4 {
		t.Fatalf("len(Errors) = %d, want 4 (3 kept + 1 summary)", len(r.Errors))
	}
	if !hasErrorContaining(field.ErrorList{r.Errors[3]}, "too many errors") {
		t.Errorf("last error is not the summary: %v", r.Errors[3])
	}
	// The kept errors must be the ones that sort first.
	wantPaths := []string{
		"Component[{foo 1.0.0}].Spec.Objects[1].Metadata.Name",
		"Component[{foo 1.0.0}].Spec.Objects[2].Metadata.Name",
		"Component[{foo 1.0.0}].Spec.Objects[3].Metadata.Name",
	}
	assertFields(t, r.Errors[:3], wantPaths)

	t.Run("no limit when negative", func(t *testing.T) {
		r := ValidateComponent(c, Options{MaxErrors: -1})
		if r.TruncatedErrors || len(r.Errors) != 9 {
			t.Errorf("got %d errors, truncated=%v; want 9, false", len(r.Errors), r.TruncatedErrors)
		}
	})
}

func TestDeterminism(t *testing.T) {
	comps := []*bundle.Component{
		testComponent("b", "1.0.0", testObj("v1", "ConfigMap", "ns", "shared")),
		testComponent("a", "2.0.0", testObj("v1", "ConfigMap", "ns", "shared")),
		testComponent("a", "1.0.0",
			testObj("v1", "Pod", "ns", "p"),
			testObj("v1", "Pod", "ns", "p"),
		),
	}
	first := errorStrings(ValidateComponents(comps, Options{}).Errors)
	second := errorStrings(ValidateComponents(comps, Options{}).Errors)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated validation differs:\n%v\n%v", first, second)
	}

	// Shuffling the input must not change the output, because findings are keyed
	// by component reference and object index, not input position.
	shuffled := []*bundle.Component{comps[2], comps[0], comps[1]}
	got := errorStrings(ValidateComponents(shuffled, Options{}).Errors)
	if !reflect.DeepEqual(first, got) {
		t.Fatalf("shuffled input changed output:\n%v\n%v", first, got)
	}
}

func TestReportNoCrossPair(t *testing.T) {
	// Two colliding objects must produce exactly one finding, anchored at the
	// later occurrence and naming the baseline, not both "A vs B" and "B vs A".
	comps := []*bundle.Component{
		testComponent("a", "1.0.0", testObj("v1", "ConfigMap", "ns", "shared")),
		testComponent("b", "1.0.0", testObj("v1", "ConfigMap", "ns", "shared")),
	}
	r := ValidateComponents(comps, Options{CrossComponentDuplicateSeverity: SeverityError})
	if len(r.Errors) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(r.Errors), errorStrings(r.Errors))
	}
	assertFields(t, r.Errors, []string{"Component[{b 1.0.0}].Spec.Objects[0].Metadata.Name"})
	if !hasErrorContaining(r.Errors, "Component[{a 1.0.0}].Spec.Objects[0]") {
		t.Errorf("error does not name the baseline: %v", r.Errors)
	}
}

func TestClusterScopedNoNamespaceWarning(t *testing.T) {
	c := testComponent("foo", "1.0.0",
		testObj("rbac.authorization.k8s.io/v1", "ClusterRoleBinding", "", "rb"),
	)
	r := ValidateComponent(c, Options{})
	if len(r.Errors) != 0 {
		t.Errorf("got errors: %v", errorStrings(r.Errors))
	}
	if len(r.Warnings) != 0 {
		t.Errorf("cluster-scoped object produced namespace warnings: %v", errorStrings(r.Warnings))
	}

	c2 := testComponent("foo", "1.0.0", testObj("v1", "ConfigMap", "", "cm"))
	r2 := ValidateComponent(c2, Options{})
	if len(r2.Warnings) != 1 {
		t.Errorf("got %d warnings, want 1: %v", len(r2.Warnings), errorStrings(r2.Warnings))
	}
}

func TestValidateComponentSetWithPool(t *testing.T) {
	objs := func(name string) []*unstructured.Unstructured {
		return []*unstructured.Unstructured{testObj("v1", "ConfigMap", "ns", name)}
	}
	c := testComponent("etcd", "1.0.0", objs("cfg")...)

	cs := &bundle.ComponentSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "bundle.gke.io/v1alpha1",
			Kind:       "ComponentSet",
		},
	}
	cs.Spec.SetName = "set"
	cs.Spec.Version = "1.0.0"
	cs.Spec.Components = []bundle.ComponentReference{
		{ComponentName: "etcd", Version: "1.0.0"},
		{ComponentName: "missing", Version: "2.0.0"},
	}

	t.Run("references only without a pool", func(t *testing.T) {
		r := ValidateComponentSet(cs, nil, Options{})
		if len(r.Errors) != 0 {
			t.Errorf("got errors: %v", errorStrings(r.Errors))
		}
		if r.ObjectsValidated {
			t.Error("ObjectsValidated = true without a pool")
		}
	})

	t.Run("expands references against the pool", func(t *testing.T) {
		r := ValidateComponentSet(cs, []*bundle.Component{c}, Options{})
		if r.ObjectsValidated {
			t.Error("ObjectsValidated = true even though a reference is missing")
		}
		if len(r.MissingReferences) != 1 || r.MissingReferences[0].ComponentName != "missing" {
			t.Errorf("MissingReferences = %v, want the missing one", r.MissingReferences)
		}
	})

	t.Run("all references found", func(t *testing.T) {
		cs2 := cs.DeepCopy()
		cs2.Spec.Components = cs2.Spec.Components[:1]
		r := ValidateComponentSet(cs2, []*bundle.Component{c}, Options{})
		if !r.ObjectsValidated {
			t.Error("ObjectsValidated = false with a complete pool")
		}
	})

	t.Run("objects from the pool are validated", func(t *testing.T) {
		bad := testComponent("etcd", "1.0.0", testObj("v1", "Pod", "ns", ""))
		cs2 := cs.DeepCopy()
		cs2.Spec.Components = cs2.Spec.Components[:1]
		r := ValidateComponentSet(cs2, []*bundle.Component{bad}, Options{})
		if len(r.Errors) != 1 {
			t.Errorf("got %d errors, want 1: %v", len(r.Errors), errorStrings(r.Errors))
		}
	})
}

func TestValidateBundleExportedScope(t *testing.T) {
	b := &bundle.Bundle{}
	b.Components = []*bundle.Component{
		testComponent("a", "1.0.0", testObj("v1", "ConfigMap", "ns", "shared")),
		testComponent("b", "1.0.0", testObj("v1", "ConfigMap", "ns", "shared")),
	}
	r := ValidateBundle(b, Options{CrossComponentDuplicateSeverity: SeverityError})
	if len(r.Errors) != 1 {
		t.Errorf("got %d errors, want 1: %v", len(r.Errors), errorStrings(r.Errors))
	}
}
