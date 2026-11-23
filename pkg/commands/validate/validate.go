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
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/validation/field"
	log "k8s.io/klog"

	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/commands/cmdlib"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/files"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/validate"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/wrapper"
)

// Action is the cobra command action for bundle validation.
func action(ctx context.Context, fio files.FileReaderWriter, sio cmdlib.StdioReaderWriter, cmd *cobra.Command, gopts *cmdlib.GlobalOptions) {
	brw := cmdlib.NewBundleReaderWriter(fio, sio)
	if err := runValidate(ctx, brw, sio, gopts); err != nil {
		log.Exit(err)
	}
}

func runValidate(ctx context.Context, brw cmdlib.BundleReaderWriter, sio io.Writer, gopt *cmdlib.GlobalOptions) error {
	bw, err := brw.ReadBundleData(ctx, gopt)
	if err != nil {
		return fmt.Errorf("error reading contents: %v", err)
	}

	report := validateWrapper(bw)

	// Warnings are advisory and are printed regardless of whether there are
	// errors, so that a partially-clean input still surfaces its suspicions.
	if len(report.Warnings) > 0 {
		fmt.Fprintf(sio, "Warnings:\n%v\n", report.Warnings.ToAggregate())
	}

	if report.HasErrors() {
		msg := fmt.Sprintf("there were one or more errors found while validating the bundle:\n%v", report.Errors.ToAggregate())
		if report.TruncatedErrors {
			msg += fmt.Sprintf("\n(%d errors total; only the first %d are shown)", report.TotalErrors, len(report.Errors)-1)
		}
		return fmt.Errorf("%s", msg)
	}

	log.Info("No errors found")
	return nil
}

// validateWrapper dispatches validation based on the wrapped kind. Bundle
// components are validated together, because a bundle exports all of them at
// once.
func validateWrapper(bw *wrapper.BundleWrapper) *validate.Report {
	switch bw.Kind() {
	case "Component":
		return validate.ValidateComponent(bw.Component(), validate.Options{})
	case "Bundle":
		// A bundle exports all of its components at once, so they are validated
		// as one exported set.
		return validate.ValidateComponents(bw.AllComponents(), validate.Options{Scope: validate.ScopeExported})
	default:
		// TODO(BradfordMedeiros): add validation for BundleBuilder, ComponentBuilder here.
		err := field.NotSupported(field.NewPath("Kind"), bw.Kind(),
			[]string{"Component", "Bundle"})
		return &validate.Report{
			Errors:      field.ErrorList{err},
			TotalErrors: 1,
		}
	}
}
