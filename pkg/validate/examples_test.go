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

package validate_test

import (
	"context"
	"io/ioutil"
	"path/filepath"
	"testing"

	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/build"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/converter"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/validate"
)

// TestExamplesPassValidation runs the object-level rules over every example
// component and bundle in the repository. Examples are treated as regression
// input: if an example violates a rule, the example must be fixed rather than
// exempted.
func TestExamplesPassValidation(t *testing.T) {
	ctx := context.Background()

	componentBuilders := []string{
		"../../examples/component/etcd-component-builder.yaml",
		"../../examples/templatebuilder/builder.yaml",
		"../../examples/templatefiles/builder.yaml",
		"../../examples/patchbuilder/builder.yaml",
	}
	for _, p := range componentBuilders {
		p := p
		t.Run(filepath.Base(filepath.Dir(p))+"/"+filepath.Base(p), func(t *testing.T) {
			b, err := ioutil.ReadFile(p)
			if err != nil {
				t.Fatalf("reading %s: %v", p, err)
			}
			cb, err := converter.FromYAML(b).ToComponentBuilder()
			if err != nil {
				t.Fatalf("parsing %s: %v", p, err)
			}
			inliner := build.NewLocalInliner(p)
			comp, err := inliner.ComponentFiles(ctx, cb, p)
			if err != nil {
				t.Fatalf("inlining %s: %v", p, err)
			}
			report := validate.ValidateComponent(comp, validate.Options{})
			if report.HasErrors() {
				t.Errorf("example %s produced validation errors:\n%v", p, report.Errors.ToAggregate())
			}
		})
	}

	t.Run("component/etcd-component.yaml", func(t *testing.T) {
		p := "../../examples/component/etcd-component.yaml"
		b, err := ioutil.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		comp, err := converter.FromYAML(b).ToComponent()
		if err != nil {
			t.Fatalf("parsing %s: %v", p, err)
		}
		report := validate.ValidateComponent(comp, validate.Options{})
		if report.HasErrors() {
			t.Errorf("example %s produced validation errors:\n%v", p, report.Errors.ToAggregate())
		}
	})

	t.Run("cluster/bundle-builder-example.yaml", func(t *testing.T) {
		p := "../../examples/cluster/bundle-builder-example.yaml"
		b, err := ioutil.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		bb, err := converter.FromYAML(b).ToBundleBuilder()
		if err != nil {
			t.Fatalf("parsing %s: %v", p, err)
		}
		inliner := build.NewLocalInliner("../../examples/cluster/")
		bun, err := inliner.BundleFiles(ctx, bb, p)
		if err != nil {
			t.Fatalf("inlining %s: %v", p, err)
		}
		report := validate.ValidateBundle(bun, validate.Options{})
		if report.HasErrors() {
			t.Errorf("example %s produced validation errors:\n%v", p, report.Errors.ToAggregate())
		}
	})
}
