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
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/commands/cmdlib"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/wrapper"
)

// fakeReader returns a fixed BundleWrapper.
type fakeReader struct {
	bw  *wrapper.BundleWrapper
	err error
}

func (f *fakeReader) ReadBundleData(context.Context, *cmdlib.GlobalOptions) (*wrapper.BundleWrapper, error) {
	return f.bw, f.err
}

func (f *fakeReader) WriteBundleData(context.Context, *wrapper.BundleWrapper, *cmdlib.GlobalOptions) error {
	return nil
}

func (f *fakeReader) WriteStructuredContents(context.Context, interface{}, *cmdlib.GlobalOptions) error {
	return nil
}

func wrapperFromYAML(t *testing.T, s string) *wrapper.BundleWrapper {
	t.Helper()
	bw, err := wrapper.FromRaw("yaml", []byte(s))
	if err != nil {
		t.Fatalf("parsing test input: %v", err)
	}
	return bw
}

func TestRunValidate(t *testing.T) {
	tests := []struct {
		desc          string
		input         string
		wantErrSubstr string
		wantStdout    string
	}{
		{
			desc: "component passes",
			input: `
apiVersion: bundle.gke.io/v1alpha1
kind: Component
spec:
  componentName: foo
  version: 1.0.0
  objects:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: cfg
      namespace: ns`,
		},
		{
			desc: "component with error fails",
			input: `
apiVersion: bundle.gke.io/v1alpha1
kind: Component
spec:
  componentName: foo
  version: 1.0.0
  objects:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      namespace: ns`,
			wantErrSubstr: "metadata.name is required",
		},
		{
			desc: "bundle is now supported",
			input: `
apiVersion: bundle.gke.io/v1alpha1
kind: Bundle
setName: test
version: 1.0.0
components:
- apiVersion: bundle.gke.io/v1alpha1
  kind: Component
  spec:
    componentName: foo
    version: 1.0.0
    objects:
    - apiVersion: v1
      kind: ConfigMap
      metadata:
        name: cfg
        namespace: ns`,
		},
		{
			desc: "bundle with a cross-component duplicate warns but passes",
			input: `
apiVersion: bundle.gke.io/v1alpha1
kind: Bundle
setName: test
version: 1.0.0
components:
- apiVersion: bundle.gke.io/v1alpha1
  kind: Component
  spec:
    componentName: a
    version: 1.0.0
    objects:
    - apiVersion: v1
      kind: ConfigMap
      metadata:
        name: shared
        namespace: ns
        labels:
          source: a
- apiVersion: bundle.gke.io/v1alpha1
  kind: Component
  spec:
    componentName: b
    version: 1.0.0
    objects:
    - apiVersion: v1
      kind: ConfigMap
      metadata:
        name: shared
        namespace: ns
        labels:
          source: b`,
			wantStdout: "Warnings:",
		},
		{
			desc: "unsupported builder kind is rejected",
			input: `
apiVersion: bundle.gke.io/v1alpha1
kind: ComponentBuilder
componentName: test
version: 1.0.0`,
			wantErrSubstr: "Unsupported value",
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			var out bytes.Buffer
			brw := &fakeReader{bw: wrapperFromYAML(t, tc.input)}
			err := runValidate(context.Background(), brw, &out, &cmdlib.GlobalOptions{})
			if tc.wantErrSubstr == "" {
				if err != nil {
					t.Fatalf("got error %v, want none", err)
				}
				if tc.wantStdout != "" && !strings.Contains(out.String(), tc.wantStdout) {
					t.Errorf("stdout %q does not contain %q", out.String(), tc.wantStdout)
				}
				return
			}
			if err == nil {
				t.Fatalf("got no error, want one containing %q", tc.wantErrSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Fatalf("got error %q, want it to contain %q", err.Error(), tc.wantErrSubstr)
			}
		})
	}
}

func TestRunValidatePrintsWarningsWithoutFailing(t *testing.T) {
	// Two byte-identical objects in one component produce a warning, not an
	// error, and the warning must be printed.
	input := `
apiVersion: bundle.gke.io/v1alpha1
kind: Component
spec:
  componentName: foo
  version: 1.0.0
  objects:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: cfg
      namespace: ns
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: cfg
      namespace: ns`
	var out bytes.Buffer
	brw := &fakeReader{bw: wrapperFromYAML(t, input)}
	if err := runValidate(context.Background(), brw, &out, &cmdlib.GlobalOptions{}); err != nil {
		t.Fatalf("got error %v, want none: warnings must not fail validation", err)
	}
	if !strings.Contains(out.String(), "Warnings:") {
		t.Errorf("stdout %q does not print warnings", out.String())
	}
}

func TestValidateWrapperKinds(t *testing.T) {
	// Guard against accidentally dropping Bundle support.
	bw := wrapperFromYAML(t, `
apiVersion: bundle.gke.io/v1alpha1
kind: Bundle
setName: s
version: 1.0.0`)
	if report := validateWrapper(bw); report.HasErrors() {
		t.Errorf("empty bundle reported errors: %v", report.Errors)
	}
}
