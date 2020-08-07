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

// Package aks is a Platform implementation for creating a Kubernetes cluster using
// Azure AKS.
package aks

import (
	"bytes"
	"fmt"
	"os"
	"text/template"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/kinvolk/lokomotive/pkg/platform"
	"github.com/kinvolk/lokomotive/pkg/terraform"
)

const (
	// Environment variables used to load sensitive parts of the configuration.
	clientIDEnv       = "LOKOMOTIVE_AKS_CLIENT_ID"
	clientSecretEnv   = "LOKOMOTIVE_AKS_CLIENT_SECRET" // #nosec G101
	subscriptionIDEnv = "LOKOMOTIVE_AKS_SUBSCRIPTION_ID"
	tenantIDEnv       = "LOKOMOTIVE_AKS_TENANT_ID"

	kubernetesVersion = "1.16.10"
)

// workerPool defines "worker_pool" block.
type workerPool struct {
	// Label field.
	Name string `hcl:"name,label"`

	// Block properties.
	Count  int               `hcl:"count,optional"`
	VMSize string            `hcl:"vm_size,optional"`
	Labels map[string]string `hcl:"labels,optional"`
	Taints []string          `hcl:"taints,optional"`
}

type Config struct {
	AssetDir    string            `hcl:"asset_dir,optional"`
	ClusterName string            `hcl:"cluster_name,optional"`
	Tags        map[string]string `hcl:"tags,optional"`

	// Azure specific.
	TenantID       string `hcl:"tenant_id,optional"`
	SubscriptionID string `hcl:"subscription_id,optional"`
	ClientID       string `hcl:"client_id,optional"`
	ClientSecret   string `hcl:"client_secret,optional"`

	Location string `hcl:"location,optional"`

	// ApplicationName for created service principal.
	ApplicationName string `hcl:"application_name,optional"`

	ResourceGroupName   string `hcl:"resource_group_name,optional"`
	ManageResourceGroup bool   `hcl:"manage_resource_group,optional"`

	WorkerPools []workerPool `hcl:"worker_pool,block"`

	KubernetesVersion string
}

// NewConfig creates a new Config and returns a pointer to it as well as any HCL diagnostics.
func NewConfig(b *hcl.Body, ctx *hcl.EvalContext) (*Config, hcl.Diagnostics) {
	diags := hcl.Diagnostics{}

	// Create config with default values.
	c := &Config{
		Location:            "West Europe",
		ManageResourceGroup: true,
		KubernetesVersion:   kubernetesVersion,
	}

	if b == nil {
		return nil, hcl.Diagnostics{}
	}

	if d := gohcl.DecodeBody(*b, ctx, c); len(d) != 0 {
		diags = append(diags, d...)
		return nil, diags
	}

	if d := c.validate(); len(d) != 0 {
		diags = append(diags, d...)
		return nil, diags
	}

	if c.ClientSecret == "" {
		c.ClientSecret = os.Getenv(clientSecretEnv)
	}

	if c.SubscriptionID == "" {
		c.SubscriptionID = os.Getenv(subscriptionIDEnv)
	}

	if c.ClientID == "" {
		c.ClientID = os.Getenv(clientIDEnv)
	}

	if c.TenantID == "" {
		c.TenantID = os.Getenv(tenantIDEnv)
	}

	return c, diags
}

// Cluster implements the Cluster interface for AKS.
type Cluster struct {
	config *Config
	// A string containing the rendered Terraform code of the root module.
	rootModule string
}

func (c *Cluster) AssetDir() string {
	return c.config.AssetDir
}

func (c *Cluster) ControlPlaneCharts() []string {
	// AKS is a managed platform and therefore doesn't use the Lokomotive control plane.
	return []string{}
}

func (c *Cluster) Managed() bool {
	return true
}

func (c *Cluster) Nodes() int {
	nodes := 0
	for _, wp := range c.config.WorkerPools {
		nodes += wp.Count
	}

	return nodes
}

func (c *Cluster) TerraformExecutionPlan() []terraform.ExecutionStep {
	return []terraform.ExecutionStep{
		terraform.ExecutionStep{
			Description: "Create infrastructure",
			Args:        []string{"apply", "-auto-approve"},
		},
	}
}

func (c *Cluster) TerraformRootModule() string {
	return c.rootModule
}

// NewCluster constructs a Cluster based on the provided config and returns a pointer to it.
func NewCluster(c *Config) (*Cluster, error) {
	rendered, err := renderRootModule(c)
	if err != nil {
		return nil, fmt.Errorf("rendering root module: %v", err)
	}

	return &Cluster{config: c, rootModule: rendered}, nil
}

// validate validates the cluster configuration.
func (c *Config) validate() hcl.Diagnostics {
	var d hcl.Diagnostics

	d = append(d, c.checkNotEmptyWorkers()...)
	d = append(d, c.checkWorkerPoolNamesUnique()...)
	d = append(d, c.checkWorkerPools()...)
	d = append(d, c.checkCredentials()...)
	d = append(d, c.checkRequiredFields()...)

	return d
}

// checkWorkerPools validates all configured worker pool fields.
func (c *Config) checkWorkerPools() hcl.Diagnostics {
	var d hcl.Diagnostics

	for _, w := range c.WorkerPools {
		if w.VMSize == "" {
			d = append(d, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  fmt.Sprintf("pool %q: VMSize field can't be empty", w.Name),
			})
		}

		if w.Count <= 0 {
			d = append(d, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  fmt.Sprintf("pool %q: count must be bigger than 0", w.Name),
			})
		}
	}

	return d
}

// checkRequiredFields checks if that all required fields are populated in the top level configuration.
func (c *Config) checkRequiredFields() hcl.Diagnostics {
	var d hcl.Diagnostics

	if c.SubscriptionID == "" && os.Getenv(subscriptionIDEnv) == "" {
		d = append(d, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "cannot find the Azure subscription ID",
			Detail: fmt.Sprintf("%q field is empty and %q environment variable "+
				"is not defined. At least one of these should be defined",
				"SubscriptionID", subscriptionIDEnv),
		})
	}

	if c.TenantID == "" && os.Getenv(tenantIDEnv) == "" {
		d = append(d, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "cannot find the Azure client ID",
			Detail: fmt.Sprintf("%q field is empty and %q environment variable "+
				"is not defined. At least one of these should be defined", "TenantID", tenantIDEnv),
		})
	}

	f := map[string]string{
		"AssetDir":          c.AssetDir,
		"ClusterName":       c.ClusterName,
		"ResourceGroupName": c.ResourceGroupName,
	}

	for k, v := range f {
		if v == "" {
			d = append(d, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  fmt.Sprintf("field %q can't be empty", k),
			})
		}
	}

	return d
}

// checkCredentials checks if credentials are correctly defined.
func (c *Config) checkCredentials() hcl.Diagnostics {
	var d hcl.Diagnostics

	// If the application name is defined, we assume that we work as a highly privileged
	// account which has permissions to create new Azure AD application, so Client ID
	// and Client Secret fields are not needed.
	if c.ApplicationName != "" {
		if c.ClientID != "" {
			d = append(d, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "ClientID and ApplicationName are mutually exclusive",
			})
		}

		if c.ClientSecret != "" {
			d = append(d, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "ClientSecret and ApplicationName are mutually exclusive",
			})
		}

		return d
	}

	if c.ClientSecret == "" && os.Getenv(clientSecretEnv) == "" {
		d = append(d, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "cannot find the Azure client secret",
			Detail: fmt.Sprintf("%q field is empty and %q environment variable "+
				"is not defined. At least one of these should be defined", "ClientSecret", clientSecretEnv),
		})
	}

	if c.ClientID == "" && os.Getenv(clientIDEnv) == "" {
		d = append(d, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "cannot find the Azure client ID",
			Detail: fmt.Sprintf("%q field is empty and %q environment variable is "+
				"not defined. At least one of these should be defined", "ClientID", clientIDEnv),
		})
	}

	return d
}

// checkNotEmptyWorkers checks if the cluster has at least 1 node pool defined.
func (c *Config) checkNotEmptyWorkers() hcl.Diagnostics {
	var diagnostics hcl.Diagnostics

	if len(c.WorkerPools) == 0 {
		diagnostics = append(diagnostics, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "At least one worker pool must be defined",
			Detail:   "Make sure to define at least one worker pool block in your cluster block",
		})
	}

	return diagnostics
}

// checkWorkerPoolNamesUnique verifies that all worker pool names are unique.
func (c *Config) checkWorkerPoolNamesUnique() hcl.Diagnostics {
	var diagnostics hcl.Diagnostics

	dup := make(map[string]bool)

	for _, w := range c.WorkerPools {
		if !dup[w.Name] {
			dup[w.Name] = true
			continue
		}

		// It is duplicated.
		diagnostics = append(diagnostics, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Worker pool names should be unique",
			Detail:   fmt.Sprintf("Worker pool '%v' is duplicated", w.Name),
		})
	}

	return diagnostics
}

func renderRootModule(conf *Config) (string, error) {
	t, err := template.New("rootModule").Parse(terraformConfigTmpl)
	if err != nil {
		return "", fmt.Errorf("parsing template: %v", err)
	}

	platform.AppendVersionTag(&conf.Tags)

	var rendered bytes.Buffer
	if err := t.Execute(&rendered, conf); err != nil {
		return "", fmt.Errorf("rendering template: %v", err)
	}

	return rendered.String(), nil
}
