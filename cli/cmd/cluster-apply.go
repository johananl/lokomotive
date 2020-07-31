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
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/kinvolk/lokomotive/pkg/assets"
	"github.com/kinvolk/lokomotive/pkg/backend/local"
	"github.com/kinvolk/lokomotive/pkg/config"
	"github.com/kinvolk/lokomotive/pkg/install"
	"github.com/kinvolk/lokomotive/pkg/k8sutil"
	"github.com/kinvolk/lokomotive/pkg/lokomotive"
	"github.com/kinvolk/lokomotive/pkg/terraform"
)

var (
	verbose         bool
	skipComponents  bool
	upgradeKubelets bool
)

var clusterApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Deploy or update a cluster",
	Long: `Deploy or update a cluster.
Deploys a cluster if it isn't deployed, otherwise updates it.
Unless explicitly skipped, components listed in the configuration are applied as well.`,
	Run: runClusterApply,
}

func init() {
	clusterCmd.AddCommand(clusterApplyCmd)
	pf := clusterApplyCmd.PersistentFlags()
	pf.BoolVarP(&confirm, "confirm", "", false, "Upgrade cluster without asking for confirmation")
	pf.BoolVarP(&verbose, "verbose", "v", false, "Show output from Terraform")
	pf.BoolVarP(&skipComponents, "skip-components", "", false, "Skip applying component configuration")
	pf.BoolVarP(&upgradeKubelets, "upgrade-kubelets", "", false, "Experimentally upgrade self-hosted kubelets")
}

//nolint:funlen
func runClusterApply(cmd *cobra.Command, args []string) {
	ctxLogger := log.WithFields(log.Fields{
		"command": "lokoctl cluster apply",
		"args":    args,
	})

	// Read cluster config from HCL files.
	cp := viper.GetString("lokocfg")
	vp := viper.GetString("lokocfg-vars")
	cc, diags := config.Read(cp, vp)
	if len(diags) > 0 {
		ctxLogger.Fatal(diags)
	}
	if cc.RootConfig.Cluster == nil {
		// No `cluster` block specified in the configuration.
		ctxLogger.Fatal("No cluster configured")
	}

	// Construct a Cluster.
	c, diags := createCluster(cc)
	if diags.HasErrors() {
		for _, diagnostic := range diags {
			ctxLogger.Error(diagnostic.Error())
		}
		ctxLogger.Fatal("Errors found while loading cluster configuration")
	}

	// TODO: Refactor backend generation. We can probably render the backend config as part of the
	// Terraform root module and get rid of the specialized backend-related functions.
	// Get the configured backend for the cluster.
	b, diags := getConfiguredBackend(cc)
	// TODO: Deduplicate error checking.
	if diags.HasErrors() {
		for _, diagnostic := range diags {
			ctxLogger.Error(diagnostic.Error())
		}
		ctxLogger.Fatal("Errors found while loading cluster configuration")
	}

	// Use a local backend if no backend is configured.
	if b == nil {
		b = local.NewLocalBackend()
	}

	// Validate backend configuration.
	if err := b.Validate(); err != nil {
		ctxLogger.Fatalf("Failed to validate backend configuration: %v", err)
	}

	// Render backend configuration.
	renderedBackend, err := b.Render()
	if err != nil {
		ctxLogger.Fatalf("Failed to render backend configuration file: %v", err)
	}

	assetDir, err := homedir.Expand(c.AssetDir())
	if err != nil {
		ctxLogger.Fatalf("Error expanding path: %v", err)
	}

	terraformModuleDir := filepath.Join(assetDir, "lokomotive-kubernetes")
	if err := assets.Extract(assets.TerraformModulesSource, terraformModuleDir); err != nil {
		ctxLogger.Fatalf("Writing Terraform files to disk: %v", err)
	}

	// Ensure Terraform root directory exists.
	terraformRootDir := filepath.Join(assetDir, "terraform")
	if err := os.MkdirAll(terraformRootDir, 0755); err != nil {
		ctxLogger.Fatalf("Creating Terraform root directory at %q: %v", terraformRootDir, err)
	}

	// Create backend file only if the backend rendered string isn't empty.
	if len(strings.TrimSpace(renderedBackend)) > 0 {
		path := filepath.Join(terraformRootDir, "backend.tf")
		content := fmt.Sprintf("terraform {%s}\n", renderedBackend)

		if err := writeToFile(path, content); err != nil {
			ctxLogger.Fatalf("Failed to write backend file %q to disk: %v", path, err)
		}
	}

	if err := c.Validate(); err != nil {
		ctxLogger.Fatalf("Cluster config validation failed: %v", err)
	}

	// Extract control plane chart files to cluster assets directory.
	for _, chart := range c.ControlPlaneCharts() {
		src := filepath.Join(assets.ControlPlaneSource, chart)
		dst := filepath.Join(assetDir, "cluster-assets", "charts", "kube-system", chart)
		if err := assets.Extract(src, dst); err != nil {
			ctxLogger.Fatalf("Failed to extract charts: %v", err)
		}
	}

	path := filepath.Join(terraformRootDir, "cluster.tf")
	content, err := c.TerraformRootModule()
	if err != nil {
		ctxLogger.Fatalf("Failed to render Terraform root module: %v", err)
	}

	if err := writeToFile(path, content); err != nil {
		ctxLogger.Fatalf("Failed to write Terraform root module %q to disk: %v", path, err)
	}

	// Construct Terraform executor.
	ex, err := terraform.NewExecutor(terraform.Config{
		WorkingDir: filepath.Join(assetDir, "terraform"),
		Verbose:    verbose,
	})
	if err != nil {
		ctxLogger.Fatalf("Failed to create Terraform executor: %v", err)
	}

	if err := ex.Init(); err != nil {
		ctxLogger.Fatalf("Failed to initialize Terraform: %v", err)
	}

	exists := clusterExists(ctxLogger, ex)
	if exists && !confirm {
		// TODO: We could plan to a file and use it when installing.
		// TODO: How does this play with a complex execution plan? Does a single "global" plan
		// operation represent what's going to be applied?
		if err := ex.Plan(); err != nil {
			ctxLogger.Fatalf("Failed to reconcile cluster state: %v", err)
		}

		if !askForConfirmation("Do you want to proceed with cluster apply?") {
			ctxLogger.Println("Cluster apply cancelled")

			return
		}
	}

	for _, step := range c.TerraformExecutionPlan() {
		if step.PreExecutionHook != nil {
			ctxLogger.Printf("Running pre-execution hook for step %q", step.Description)
			if err := step.PreExecutionHook(ex); err != nil {
				ctxLogger.Fatalf("Pre-execution hook for step %q failed: %v", step.Description, err)
			}
		}

		ctxLogger.Printf("Executing step %q", step.Description)
		err := ex.Execute(step.Args...)
		if err != nil {
			ctxLogger.Fatalf("Execution of step %q failed: %v", step.Description, err)
		}
	}

	fmt.Printf("\nYour configurations are stored in %s\n", assetDir)

	kubeconfigPath := assetsKubeconfig(assetDir)
	if err := verifyCluster(kubeconfigPath, c.Nodes()); err != nil {
		ctxLogger.Fatalf("Verify cluster: %v", err)
	}

	// Do controlplane upgrades only if cluster already exists and it is not a managed platform.
	if exists && !c.Managed() {
		fmt.Printf("\nEnsuring that cluster controlplane is up to date.\n")

		cu := controlplaneUpdater{
			kubeconfigPath: kubeconfigPath,
			assetDir:       assetDir,
			ctxLogger:      *ctxLogger,
			ex:             *ex,
		}

		// releases := []string{"pod-checkpointer", "kube-apiserver", "kubernetes", "calico"}
		var releases []string
		for _, r := range c.ControlPlaneCharts() {
			// Don't upgrade self-hosted kubelets unless requested by user.
			if r == "kubelet" && !upgradeKubelets {
				continue
			}
			releases = append(releases, r)
		}

		for _, c := range releases {
			cu.upgradeComponent(c)
		}
	}

	if skipComponents {
		return
	}

	componentsToApply := []string{}
	for _, component := range cc.RootConfig.Components {
		componentsToApply = append(componentsToApply, component.Name)
	}

	ctxLogger.Println("Applying component configuration")

	if len(componentsToApply) > 0 {
		if err := applyComponents(cc, kubeconfigPath, componentsToApply...); err != nil {
			ctxLogger.Fatalf("Applying component configuration failed: %v", err)
		}
	}
}

func verifyCluster(kubeconfigPath string, expectedNodes int) error {
	kubeconfig, err := ioutil.ReadFile(kubeconfigPath) // #nosec G304
	if err != nil {
		return errors.Wrapf(err, "failed to read kubeconfig file")
	}

	cs, err := k8sutil.NewClientset(kubeconfig)
	if err != nil {
		return errors.Wrapf(err, "failed to set up clientset")
	}

	cluster, err := lokomotive.NewCluster(cs, expectedNodes)
	if err != nil {
		return errors.Wrapf(err, "failed to set up cluster client")
	}

	return install.Verify(cluster)
}

// writeToFile creates a file at the provided path and writes the provided content to it.
func writeToFile(path string, content string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating file %q: %v", path, err)
	}
	defer f.Close()

	if _, err = f.WriteString(content); err != nil {
		return fmt.Errorf("writing to file %q: %v", path, err)
	}

	if err = f.Sync(); err != nil {
		return fmt.Errorf("flushing data to file %q: %v", path, err)
	}

	return nil
}
