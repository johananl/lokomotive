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

	"github.com/hashicorp/hcl/v2"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"

	"github.com/kinvolk/lokomotive/pkg/backend"
	"github.com/kinvolk/lokomotive/pkg/config"
	"github.com/kinvolk/lokomotive/pkg/platform"
	"github.com/kinvolk/lokomotive/pkg/terraform"
)

const (
	kubeconfigEnvVariable        = "KUBECONFIG"
	defaultKubeconfigPath        = "~/.kube/config"
	kubeconfigTerraformOutputKey = "kubeconfig"
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
func getConfiguredPlatform(lokoConfig *config.Config) (platform.Platform, hcl.Diagnostics) {
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

func readKubeconfigFromTerraformOutput(ex *terraform.Executor, key string) ([]byte, error) {
	kubeconfig := ""
	if err := ex.Output(key, &kubeconfig); err != nil {
		return nil, fmt.Errorf("reading kubeconfig file content from Terraform state: %w", err)
	}

	return []byte(kubeconfig), nil
}

func readKubeconfigFromFile(path string) ([]byte, error) {
	if expandedPath, err := homedir.Expand(path); err == nil {
		path = expandedPath
	}

	// homedir.Expand is too restrictive for the ~ prefix,
	// i.e., it errors on "~somepath" which is a valid path,
	// so just read from the original path.
	return ioutil.ReadFile(path) // #nosec G304
}

// getKubeconfig returns content of kubeconfig file, based on the cluster configuration, flags and
// environment variables set.
//
// The hierarchy of selecting kubeconfig file to use is the following:
//
// - --kubeconfig-file OR KUBECONFIG_FILE environent variable (the latter
//   is a side-effect of cobra/viper and should NOT be documented because it's
//   confusing). It always takes precendence if it's not empty.
//
// - If cluster configuration is found and contains platform configuration, kubeconfig from the
//   Terraform state will be used.
//
// - Path from KUBECONFIG environment variable.
//
// - Default KUBECONFIG path, which is ~/.kube/config.
func getKubeconfig(p platform.Platform, ex *terraform.Executor) ([]byte, error) {
	// TODO: This should probably be passed as an argument, so we don't do global lookups here,
	// but for now, it stays here, as it would duplicate the code and require all callers to import
	// viper package.
	flagPath := viper.GetString(kubeconfigFlag)

	// Path from the flag takes precedence over all other source of kubeconfig content.
	if flagPath == "" && p != nil {
		return readKubeconfigFromTerraformOutput(ex, kubeconfigTerraformOutputKey)
	}

	paths := []string{
		flagPath,
		os.Getenv(kubeconfigEnvVariable),
		defaultKubeconfigPath,
	}

	for _, path := range paths {
		if path != "" {
			return readKubeconfigFromFile(path)
		}
	}

	// As we use defaultKubeconfigPath, this should never be triggered.
	return nil, fmt.Errorf("no valid kubeconfig found")
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
