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

	"github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/kinvolk/lokomotive/pkg/backend/local"
	"github.com/kinvolk/lokomotive/pkg/config"
	"github.com/kinvolk/lokomotive/pkg/install"
	"github.com/kinvolk/lokomotive/pkg/k8sutil"
	"github.com/kinvolk/lokomotive/pkg/lokomotive"
	"github.com/kinvolk/lokomotive/pkg/platform"
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
	c, diags := platform.NewCluster(cc.RootConfig.Cluster.Platform, cc)
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
	// renderedBackend, err := b.Render()
	// if err != nil {
	// 	ctxLogger.Fatalf("Failed to render backend configuration file: %v", err)
	// }

	// TODO: Write Terraform files to disk.

	// exists := clusterExists(ctxLogger, ex)
	// if exists && !confirm {
	// 	// TODO: We could plan to a file and use it when installing.
	// 	if err := ex.Plan(); err != nil {
	// 		ctxLogger.Fatalf("Failed to reconcile cluster state: %v", err)
	// 	}

	// 	if !askForConfirmation("Do you want to proceed with cluster apply?") {
	// 		ctxLogger.Println("Cluster apply cancelled")

	// 		return
	// 	}
	// }

	// if err := p.Apply(ex); err != nil {
	// 	ctxLogger.Fatalf("error applying cluster: %v", err)
	// }

	assetDir, err := homedir.Expand(c.AssetDir())
	if err != nil {
		ctxLogger.Fatalf("Error expanding path: %v", err)
	}

	fmt.Printf("\nYour configurations are stored in %s\n", assetDir)

	// kubeconfigPath := assetsKubeconfig(assetDir)
	// if err := verifyCluster(kubeconfigPath, p.Meta().ExpectedNodes); err != nil {
	// 	ctxLogger.Fatalf("Verify cluster: %v", err)
	// }

	// // Do controlplane upgrades only if cluster already exists and it is not a managed platform.
	// if exists && !p.Meta().Managed {
	// 	fmt.Printf("\nEnsuring that cluster controlplane is up to date.\n")

	// 	cu := controlplaneUpdater{
	// 		kubeconfigPath: kubeconfigPath,
	// 		assetDir:       assetDir,
	// 		ctxLogger:      *ctxLogger,
	// 		ex:             *ex,
	// 	}

	// 	releases := []string{"pod-checkpointer", "kube-apiserver", "kubernetes", "calico"}

	// 	if upgradeKubelets {
	// 		releases = append(releases, "kubelet")
	// 	}

	// 	for _, c := range releases {
	// 		cu.upgradeComponent(c)
	// 	}
	// }

	// if skipComponents {
	// 	return
	// }

	// componentsToApply := []string{}
	// for _, component := range lokoConfig.RootConfig.Components {
	// 	componentsToApply = append(componentsToApply, component.Name)
	// }

	// ctxLogger.Println("Applying component configuration")

	// if len(componentsToApply) > 0 {
	// 	if err := applyComponents(lokoConfig, kubeconfigPath, componentsToApply...); err != nil {
	// 		ctxLogger.Fatalf("Applying component configuration failed: %v", err)
	// 	}
	// }
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
