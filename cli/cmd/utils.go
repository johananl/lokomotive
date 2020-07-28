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
	"os"
	"path/filepath"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/hashicorp/hcl/v2"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"github.com/kinvolk/lokomotive/pkg/backend"
	"github.com/kinvolk/lokomotive/pkg/config"
	"github.com/kinvolk/lokomotive/pkg/platform"
)

const (
	kubeconfigEnvVariable = "KUBECONFIG"
	defaultKubeconfigPath = "~/.kube/config"
)

// getConfiguredBackend loads a backend from the given configuration file.
func getConfiguredBackend(lokoConfig *config.Config) (backend.Backend, hcl.Diagnostics) {
	if lokoConfig.RootConfig.Backend == nil {
		// No backend defined and no configuration error
		return nil, hcl.Diagnostics{}
	}

	backend, err := backend.GetBackend(lokoConfig.RootConfig.Backend.Name)
	if err != nil {
		diag := &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  err.Error(),
		}
		return nil, hcl.Diagnostics{diag}
	}

	return backend, backend.LoadConfig(&lokoConfig.RootConfig.Backend.Config, lokoConfig.EvalContext)
}

// getConfiguredPlatform loads a platform from the given configuration file.
func getConfiguredPlatform() (platform.Platform, hcl.Diagnostics) {
	lokoConfig, diags := getLokoConfig()
	if diags.HasErrors() {
		return nil, diags
	}

	if lokoConfig.RootConfig.Cluster == nil {
		// No cluster defined and no configuration error
		return nil, hcl.Diagnostics{}
	}

	platform, err := platform.GetPlatform(lokoConfig.RootConfig.Cluster.Name)
	if err != nil {
		diag := &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  err.Error(),
		}
		return nil, hcl.Diagnostics{diag}
	}

	return platform, platform.LoadConfig(&lokoConfig.RootConfig.Cluster.Config, lokoConfig.EvalContext)
}

// getAssetDir extracts the asset path from the cluster configuration.
// It is empty if there is no cluster defined. An error is returned if the
// cluster configuration has problems.
func getAssetDir() (string, error) {
	cfg, diags := getConfiguredPlatform()
	if diags.HasErrors() {
		return "", fmt.Errorf("cannot load config: %s", diags)
	}
	if cfg == nil {
		// No cluster defined and no configuration error
		return "", nil
	}

	return cfg.Meta().AssetDir, nil
}

// expandKubeconfigPath tries to expand ~ in the given kubeconfig path.
// However, if that fails, it just returns original path as the best effort.
func expandKubeconfigPath(path string) string {
	if expandedPath, err := homedir.Expand(path); err == nil {
		return expandedPath
	}

	// homedir.Expand is too restrictive for the ~ prefix,
	// i.e., it errors on "~somepath" which is a valid path,
	// so just return the original path.
	return path
}

// getKubeconfig finds the kubeconfig to be used. The precedence is the following:
// - --kubeconfig-file flag OR KUBECONFIG_FILE environment variable (the latter
// is a side-effect of cobra/viper and should NOT be documented because it's
// confusing).
// - Asset directory from cluster configuration.
// - KUBECONFIG environment variable.
// - ~/.kube/config path, which is the default for kubectl.
func getKubeconfig() (string, error) {
	assetKubeconfig, err := assetsKubeconfigPath()
	if err != nil {
		return "", fmt.Errorf("reading kubeconfig path from configuration failed: %w", err)
	}

	paths := []string{
		viper.GetString(kubeconfigFlag),
		assetKubeconfig,
		os.Getenv(kubeconfigEnvVariable),
		defaultKubeconfigPath,
	}

	return expandKubeconfigPath(pickString(paths...)), nil
}

// pickString returns first non-empty string.
func pickString(options ...string) string {
	for _, option := range options {
		if option != "" {
			return option
		}
	}

	return ""
}

// assetsKubeconfigPath reads the lokocfg configuration and returns
// the kubeconfig path defined in it.
//
// If no configuration is defined, empty string is returned.
func assetsKubeconfigPath() (string, error) {
	assetDir, err := getAssetDir()
	if err != nil {
		return "", err
	}

	if assetDir != "" {
		return assetsKubeconfig(assetDir), nil
	}

	return "", nil
}

func assetsKubeconfig(assetDir string) string {
	return filepath.Join(assetDir, "cluster-assets", "auth", "kubeconfig")
}

// doesKubeconfigExist checks if the kubeconfig provided by user exists
func doesKubeconfigExist(*cobra.Command, []string) error {
	var err error
	kubeconfig, err := getKubeconfig()
	if err != nil {
		return err
	}
	if _, err = os.Stat(kubeconfig); os.IsNotExist(err) {
		return fmt.Errorf("Kubeconfig %q not found", kubeconfig)
	}
	return err
}

func getLokoConfig() (*config.Config, hcl.Diagnostics) {
	return config.LoadConfig(viper.GetString("lokocfg"), viper.GetString("lokocfg-vars"))
}

// askForConfirmation asks the user to confirm an action.
// It prints the message and then asks the user to type "yes" or "no".
// If the user types "yes" the function returns true, otherwise it returns
// false.
func askForConfirmation(message string) bool {
	var input string
	fmt.Printf("%s [type \"yes\" to continue]: ", message)
	fmt.Scanln(&input)
	return input == "yes"
}

func waitForDeployment(cs *kubernetes.Clientset, ns, name string, retryInterval, timeout time.Duration) {
	var err error

	var deploy *appsv1.Deployment

	// Check the readiness of the Deployment
	if err = wait.PollImmediate(retryInterval, timeout, func() (done bool, err error) {
		deploy, err = cs.AppsV1().Deployments(ns).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				fmt.Printf("waiting for deployment %s to be available", name)
				return false, nil
			}
			return false, err
		}

		replicas := int(deploy.Status.Replicas)

		if replicas == 0 {
			fmt.Printf("\nno replicas scheduled for deployment %s\n", name)
			return false, nil
		}

		if int(deploy.Status.AvailableReplicas) == replicas {
			fmt.Println("Admission Webhook applied successfully")
			return true, nil
		}

		return false, nil
	}); err != nil {
		fmt.Printf("error while waiting for the deployment: %v", err)
		return
	}
}
