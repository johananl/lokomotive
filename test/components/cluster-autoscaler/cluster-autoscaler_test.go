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

// +build packet
// +build e2e

package clusterautoscaler

import (
	"fmt"
	"testing"
	"time"

	testutil "github.com/kinvolk/lokomotive/test/components/util"
)

const (
	defaultDeploymentTimeout       = 5 * time.Minute
	defaultDeploymentProbeInterval = 5 * time.Second
)

func TestClusterAutoscalerDeployments(t *testing.T) {
	client, err := testutil.CreateKubeClient(t)
	if err != nil {
		t.Errorf("could not create Kubernetes client: %v", err)
	}

	t.Log("got kubernetes client")

	t.Run(fmt.Sprintf("deployment"), func(t *testing.T) {
		t.Parallel()

		replicas := 1

		testutil.WaitForDeployment(t, client, "kube-system", "cluster-autoscaler-packet-cluster-autoscaler", replicas, defaultDeploymentProbeInterval, defaultDeploymentTimeout)

		t.Logf("Found required replicas: %d", replicas)
	})
}
