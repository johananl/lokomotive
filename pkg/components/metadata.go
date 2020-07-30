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

package components

import (
	"github.com/kinvolk/lokomotive/pkg/k8sutil"
)

// Metadata is a struct which represents basic information about the component.
// It may contain information like name, version, dependencies, namespace, source etc.
type Metadata struct {
	Name             string
	ReleaseNamespace k8sutil.Namespace
	Helm             HelmMetadata
}

// HelmMetadata stores Helm-related information about a component that is needed when managing component using Helm.
type HelmMetadata struct {
	Wait bool
}
