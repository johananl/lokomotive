// Copyright 2020 The Lokomotive Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"sigs.k8s.io/yaml"

	"github.com/kinvolk/lokomotive/pkg/components/util"
	"github.com/kinvolk/lokomotive/pkg/config"
	"github.com/kinvolk/lokomotive/pkg/platform"
	"github.com/kinvolk/lokomotive/pkg/platform/packet"
	"github.com/kinvolk/lokomotive/pkg/terraform"
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Manage a cluster",
}

func init() {
	RootCmd.AddCommand(clusterCmd)
}

// clusterExists determines if cluster has already been created by getting all
// outputs from the Terraform. If there is any output defined, it means 'terraform apply'
// run at least once.
func clusterExists(ctxLogger *logrus.Entry, ex *terraform.Executor) bool {
	o := map[string]interface{}{}

	if err := ex.Output("", &o); err != nil {
		ctxLogger.Fatalf("Failed to check if cluster exists: %v", err)
	}

	return len(o) != 0
}

// createCluster constructs a Cluster based on the provided cluster config and returns a pointer to
// it.
func createCluster(config *config.Config) (platform.Cluster, hcl.Diagnostics) {
	p := config.RootConfig.Cluster.Platform

	switch p {
	case platform.Packet:
		c, diag := packet.NewCluster(config)
		if len(diag) > 0 {
			return nil, diag
		}
		return c, nil
	}
	// TODO: Add all platforms.

	return nil, hcl.Diagnostics{&hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  fmt.Sprintf("unknown platform %q", p),
	}}
}

type controlplaneUpdater struct {
	kubeconfigPath string
	assetDir       string
	ctxLogger      logrus.Entry
	ex             terraform.Executor
}

func (c controlplaneUpdater) getControlplaneChart(name string) (*chart.Chart, error) {
	helmChart, err := loader.Load(filepath.Join(c.assetDir, "cluster-assets", "charts", "kube-system", name))
	if err != nil {
		return nil, fmt.Errorf("loading chart from assets failed: %w", err)
	}

	if err := helmChart.Validate(); err != nil {
		return nil, fmt.Errorf("chart is invalid: %w", err)
	}

	return helmChart, nil
}

func (c controlplaneUpdater) getControlplaneValues(name string) (map[string]interface{}, error) {
	p := filepath.Join(c.assetDir, "cluster-assets", "charts", "kube-system", name+".yaml")
	v, err := ioutil.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("failed to read Helm values file %q: %w", p, err)
	}

	values := map[string]interface{}{}
	if err := yaml.Unmarshal([]byte(v), &values); err != nil {
		return nil, fmt.Errorf("failed to parse values.yaml for controlplane component: %w", err)
	}

	return values, nil
}

func (c controlplaneUpdater) upgradeComponent(component string) {
	ctxLogger := c.ctxLogger.WithFields(logrus.Fields{
		"action":    "controlplane-upgrade",
		"component": component,
	})

	actionConfig, err := util.HelmActionConfig("kube-system", c.kubeconfigPath)
	if err != nil {
		ctxLogger.Fatalf("Failed initializing helm: %v", err)
	}

	helmChart, err := c.getControlplaneChart(component)
	if err != nil {
		ctxLogger.Fatalf("Loading chart from assets failed: %v", err)
	}

	values, err := c.getControlplaneValues(component)
	if err != nil {
		ctxLogger.Fatalf("Failed to get kubernetes values.yaml from Terraform: %v", err)
	}

	exists, err := util.ReleaseExists(*actionConfig, component)
	if err != nil {
		ctxLogger.Fatalf("Failed checking if controlplane component is installed: %v", err)
	}

	if !exists {
		fmt.Printf("Controlplane component '%s' is missing, reinstalling...", component)

		install := action.NewInstall(actionConfig)
		install.ReleaseName = component
		install.Namespace = "kube-system"
		install.Atomic = true

		if _, err := install.Run(helmChart, map[string]interface{}{}); err != nil {
			fmt.Println("Failed!")

			ctxLogger.Fatalf("Installing controlplane component failed: %v", err)
		}

		fmt.Println("Done.")
	}

	update := action.NewUpgrade(actionConfig)

	update.Atomic = true

	fmt.Printf("Ensuring controlplane component '%s' is up to date... ", component)

	if _, err := update.Run(component, helmChart, values); err != nil {
		fmt.Println("Failed!")

		ctxLogger.Fatalf("Updating chart failed: %v", err)
	}

	fmt.Println("Done.")
}
