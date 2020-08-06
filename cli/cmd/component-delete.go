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
	"context"
	"fmt"
	"io/ioutil"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"helm.sh/helm/v3/pkg/action"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kinvolk/lokomotive/pkg/components"
	"github.com/kinvolk/lokomotive/pkg/components/util"
	"github.com/kinvolk/lokomotive/pkg/config"
	"github.com/kinvolk/lokomotive/pkg/k8sutil"
	"github.com/kinvolk/lokomotive/pkg/platform"
)

var componentDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete an installed component",
	Long: `Delete a component.
When run with no arguments, all components listed in the configuration are deleted.`,
	Run: runDelete,
}

var deleteNamespace bool

// nolint:gochecknoinits
func init() {
	componentCmd.AddCommand(componentDeleteCmd)
	pf := componentDeleteCmd.PersistentFlags()
	pf.BoolVarP(&deleteNamespace, "delete-namespace", "", false, "Delete namespace with component")
	pf.BoolVarP(&confirm, "confirm", "", false, "Delete component without asking for confirmation")
}

func runDelete(cmd *cobra.Command, args []string) {
	contextLogger := log.WithFields(log.Fields{
		"command": "lokoctl component delete",
		"args":    args,
	})

	// Read cluster config from HCL files.
	cp := viper.GetString("lokocfg")
	vp := viper.GetString("lokocfg-vars")
	cc, diags := config.Read(cp, vp)
	if len(diags) > 0 {
		contextLogger.Fatal(diags)
	}
	if cc.RootConfig.Cluster == nil {
		// No `cluster` block specified in the configuration.
		contextLogger.Fatal("No cluster configured")
	}

	// Construct a Cluster.
	c, diags := platform.NewCluster(cc.RootConfig.Cluster.Platform, cc)
	if diags.HasErrors() {
		for _, diagnostic := range diags {
			contextLogger.Error(diagnostic.Error())
		}
		contextLogger.Fatal("Errors found while loading cluster configuration")
	}

	componentsToDelete := make([]string, len(args))
	copy(componentsToDelete, args)

	if len(args) == 0 {
		componentsToDelete = make([]string, len(cc.RootConfig.Components))

		for i, component := range cc.RootConfig.Components {
			componentsToDelete[i] = component.Name
		}
	}

	componentsObjects := make([]components.Component, len(componentsToDelete))

	for i, componentName := range componentsToDelete {
		compObj, err := components.Get(componentName)
		if err != nil {
			contextLogger.Fatal(err)
		}

		componentsObjects[i] = compObj
	}

	if !confirm && !askForConfirmation(
		fmt.Sprintf(
			"The following components will be deleted:\n\t%s\n\nAre you sure you want to proceed?",
			strings.Join(componentsToDelete, "\n\t"),
		),
	) {
		contextLogger.Info("Components deletion cancelled.")
		return
	}

	kubeconfig, err := getKubeconfig(c.AssetDir())
	if err != nil {
		contextLogger.Fatalf("Error in finding kubeconfig file: %s", err)
	}

	if err := deleteComponents(kubeconfig, componentsObjects...); err != nil {
		contextLogger.Fatal(err)
	}
}

func deleteComponents(kubeconfig string, componentObjects ...components.Component) error {
	for _, compObj := range componentObjects {
		fmt.Printf("Deleting component '%s'...\n", compObj.Metadata().Name)

		if err := deleteHelmRelease(compObj, kubeconfig, deleteNamespace); err != nil {
			return err
		}

		fmt.Printf("Successfully deleted component %q!\n", compObj.Metadata().Name)
	}

	// Add a line to distinguish between info logs and errors, if any.
	fmt.Println()

	return nil
}

// deleteComponent deletes a component.
func deleteHelmRelease(c components.Component, kubeconfig string, deleteNSBool bool) error {
	name := c.Metadata().Name
	if name == "" {
		// This should never fail in real user usage, if this does that means the component was not
		// created with all the needed information.
		panic(fmt.Errorf("component name is empty"))
	}

	ns := c.Metadata().Namespace
	if ns == "" {
		// This should never fail in real user usage, if this does that means the component was not
		// created with all the needed information.
		panic(fmt.Errorf("component %s namespace is empty", name))
	}

	cfg, err := util.HelmActionConfig(ns, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed preparing helm client: %w", err)
	}

	history := action.NewHistory(cfg)
	// Check if the component's release exists. If it does only then proceed to delete.
	//
	// Note: It is assumed that this call will return error only when the release does not exist.
	// The error check is ignored to make `lokoctl component delete ..` idempotent.
	// We rely on the fact that the 'component name' == 'release name'. Since component's name is
	// hardcoded and unlikely to change release name won't change as well. And they will be
	// consistent if installed by lokoctl. So it is highly unlikely that following call will return
	// any other error than "release not found".
	if _, err := history.Run(name); err == nil {
		uninstall := action.NewUninstall(cfg)

		// Ignore the err when we have deleted the release already or it does not exist for some reason.
		if _, err := uninstall.Run(name); err != nil {
			return err
		}
	}

	if deleteNSBool {
		if err := deleteNS(ns, kubeconfig); err != nil {
			return err
		}
	}

	return nil
}

func deleteNS(ns string, kubeconfig string) error {
	kubeconfigContent, err := ioutil.ReadFile(kubeconfig) // #nosec G304
	if err != nil {
		return fmt.Errorf("failed to read kubeconfig file: %v", err)
	}

	cs, err := k8sutil.NewClientset(kubeconfigContent)
	if err != nil {
		return err
	}

	// Delete the manually created namespace which was not created by helm.
	if err = cs.CoreV1().Namespaces().Delete(context.TODO(), ns, metav1.DeleteOptions{}); err != nil {
		// Ignore error when the namespace does not exist.
		if errors.IsNotFound(err) {
			return nil
		}

		return err
	}

	return nil
}
