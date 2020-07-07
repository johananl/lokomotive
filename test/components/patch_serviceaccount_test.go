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

// +build aks aws aws_edge packet
// +build e2e

//nolint
package components

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	_ "github.com/kinvolk/lokomotive/pkg/components/flatcar-linux-update-operator"
	testutil "github.com/kinvolk/lokomotive/test/components/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

const (
	retryInterval  = time.Second * 5
	timeout        = time.Minute * 5
	contextTimeout = 10
)

type componentTestCase struct {
	namespace string
}

func TestDisableAutomountServiceAccountToken(t *testing.T) {
	client := testutil.CreateKubeClient(t)
	ns, _ := client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})

	var componentTestCases []componentTestCase

	for _, val := range ns.Items {
		if !isSpecialNamespace(val.Name) {
			componentTestCases = append(componentTestCases, componentTestCase{
				namespace: val.Name,
			})
		}
	}

	for _, tc := range componentTestCases {
		tc := tc
		t.Run(tc.namespace, func(t *testing.T) {
			t.Parallel()

			if err := wait.PollImmediate(
				retryInterval, timeout, checkDefaultServiceAccountPatch(client, tc),
			); err != nil {
				t.Fatalf("%v", err)
			}
		})
	}
}

func checkDefaultServiceAccountPatch(client kubernetes.Interface, tc componentTestCase) wait.ConditionFunc {
	return func() (done bool, err error) {
		ctx, cancel := context.WithTimeout(context.Background(), contextTimeout*time.Second)
		defer cancel()

		sa, err := client.CoreV1().ServiceAccounts(tc.namespace).Get(ctx, "default", metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error getting service account: %v", err)
		}

		automountServiceAccountToken := *sa.AutomountServiceAccountToken

		if automountServiceAccountToken != false {
			//nolint:lll
			return false, fmt.Errorf("service account for namespace %q was not patched. Expected %v got %v", tc.namespace, false, automountServiceAccountToken)
		}

		return true, nil
	}
}

func isSpecialNamespace(ns string) bool {
	if ns == "kube-system" || ns == "kube-public" || ns == "kube-node-lease" || ns == "default" {
		return true
	}

	// check for metadata-access-test by calico
	matched, _ := regexp.Match(`metadata-access-test-.*`, []byte(ns))
	if matched {
		return true
	}

	return false
}
