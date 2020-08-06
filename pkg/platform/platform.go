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

package platform

import (
	"github.com/kinvolk/lokomotive/pkg/terraform"
	"github.com/kinvolk/lokomotive/pkg/version"
)

const (
	// Packet represents a Packet cluster.
	Packet = "packet"
)

// CommonControlPlaneCharts defines a list of control plane Helm charts to be deployed for all
// platforms.
var CommonControlPlaneCharts = []string{
	"calico",
	"kube-apiserver",
	"kubernetes",
	"pod-checkpointer",
}

// TerraformExecutionStep represents a step in a Terraform execution plan.
type TerraformExecutionStep struct {
	// A short string describing the step in a way that is meaningful to the user. When a step
	// fails to execute, the description can be included in an error to let the user know exactly
	// what failed. Examples: "Create DNS resources", "Deploy virtual machines".
	Description string
	// A list of arguments to be passed to the `terraform` command. Note that for "apply" commands
	// the "-auto-approve" argument should always be included to avoid halting the Terraform
	// execution with interactive prompts.
	// Examples:
	// - []string{"apply", "-target=module.foo", "-auto-approve"}
	// - []string{"refresh"}
	// - []string{"apply", "-auto-approve"}
	Args []string
	// A function which should be run prior to executing the Terraform command. If specified and
	// the function returns an error, execution is halted.
	PreExecutionHook func(*terraform.Executor) error
}

// Cluster describes a Lokomotive cluster.
type Cluster interface {
	// AssetDir returns the path to the Lokomotive assets directory.
	AssetDir() string
	// ControlPlaneCharts returns a list of Helm charts which compose the k8s control plane.
	ControlPlaneCharts() []string
	// Managed returns true if the cluster uses a managed platform (e.g. AKS).
	Managed() bool
	// Nodes returns the total number of nodes for the cluster. This is the total number of nodes
	// including all controller nodes and all worker nodes from all worker pools.
	Nodes() int
	// TerraformExecutionPlan returns a list of TerraformExecutionSteps representing steps which
	// should be executed to get a working cluster on a platform. The execution plan is used during
	// cluster creation only - when destroying a cluster, a simple `terraform destroy` is always
	// executed.
	//
	// The commands specified in the Args field of each TerraformExecutionStep are passed as
	// arguments to the `terraform` binary and are executed in order.
	// `apply` operations should be followed by `-auto-approve` to skip interactive prompts.
	TerraformExecutionPlan() []TerraformExecutionStep
	// TerraformRootModule returns a string representing the contens of the root Terraform module
	// which should be used for cluster operations.
	TerraformRootModule() string
	// Validate ensures any runtime conditions required for managing a cluster are met. An example
	// is checking that an environment variable is set and/or has a valid value.
	//
	// Cluster configuration validation should NOT be done here but rather when initializing the
	// concrete Cluster struct.
	Validate() error
}

// AppendVersionTag appends the lokoctl-version tag to a given tags map.
func AppendVersionTag(tags *map[string]string) {
	if tags == nil {
		return
	}

	if *tags == nil {
		*tags = make(map[string]string)
	}

	if version.Version != "" {
		(*tags)["lokoctl-version"] = version.Version
	}
}
