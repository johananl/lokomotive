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
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"

	"github.com/kinvolk/lokomotive/pkg/backend"
	"github.com/kinvolk/lokomotive/pkg/config"
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
func getKubeconfig(assetDir string) (string, error) {
	assetKubeconfig, err := assetsKubeconfigPath(assetDir)
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
func assetsKubeconfigPath(assetDir string) (string, error) {
	if assetDir != "" {
		return assetsKubeconfig(assetDir), nil
	}

	return "", nil
}

func assetsKubeconfig(assetDir string) string {
	return filepath.Join(assetDir, "cluster-assets", "auth", "kubeconfig")
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
