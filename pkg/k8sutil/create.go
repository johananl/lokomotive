// Copyright 2020 The Lokomotive Authors
// Copyright 2019 The Kubernetes Authors
// Copyright 2015 CoreOS, Inc
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

package k8sutil

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/pkg/errors"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// Namespace struct for holding the Lokomotive specific metadata.
// when installing cluster or components.
type Namespace struct {
	Name        string
	Labels      map[string]string
	Annotations map[string]string
}

// Adapted from https://github.com/kubernetes-incubator/bootkube/blob/83d32756c6b02c26cab1de3f03b57f06ae4339a7/pkg/bootkube/create.go

type manifest struct {
	kind       string
	apiVersion string
	namespace  string
	name       string
	raw        []byte

	filepath string
}

func (m manifest) String() string {
	if m.namespace == "" {
		return fmt.Sprintf("%s %s %s", m.filepath, m.kind, m.name)
	}
	return fmt.Sprintf("%s %s %s/%s", m.filepath, m.kind, m.namespace, m.name)
}

func (m manifest) Kind() string {
	return m.kind
}

func (m manifest) Raw() []byte {
	return m.raw
}

// LoadManifests parses a map of Kubernetes manifests.
func LoadManifests(files map[string]string) ([]manifest, error) {
	var manifests []manifest
	for path, fileContent := range files {
		r := strings.NewReader(fileContent)
		ms, err := parseManifests(r)
		if err != nil {
			return nil, errors.Wrapf(err, "error parsing file %s:", path)
		}
		manifests = append(manifests, ms...)
	}
	return manifests, nil
}

// parseManifests parses a YAML or JSON document that may contain one or more
// kubernetes resources.
func parseManifests(r io.Reader) ([]manifest, error) {
	reader := yaml.NewYAMLReader(bufio.NewReader(r))
	var manifests []manifest
	for {
		yamlManifest, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				return manifests, nil
			}
			return nil, err
		}
		yamlManifest = bytes.TrimSpace(yamlManifest)
		if len(yamlManifest) == 0 {
			continue
		}

		jsonManifest, err := yaml.ToJSON(yamlManifest)
		if err != nil {
			return nil, fmt.Errorf("invalid manifest: %w", err)
		}
		m, err := parseJSONManifest(jsonManifest)
		if err != nil {
			return nil, fmt.Errorf("parse manifest: %w", err)
		}
		manifests = append(manifests, m...)
	}
}

// parseJSONManifest parses a single JSON Kubernetes resource.
func parseJSONManifest(data []byte) ([]manifest, error) {
	if string(data) == "null" {
		return nil, nil
	}
	var m struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, errors.Wrapf(err, "failed to parse manifest")
	}

	// We continue if the object we received was a *List kind. Otherwise if a
	// single object is received we just return from here.
	if !strings.HasSuffix(m.Kind, "List") {
		return []manifest{{
			kind:       m.Kind,
			apiVersion: m.APIVersion,
			namespace:  m.Metadata.Namespace,
			name:       m.Metadata.Name,
			raw:        data,
		}}, nil
	}

	// We parse the list of items and extract one object at a time
	var mList struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(data, &mList); err != nil {
		return nil, errors.Wrapf(err, "failed to parse manifest list")
	}
	var manifests []manifest
	for _, item := range mList.Items {
		// make a recursive call, since this is a single object it will be
		// parsed and returned to us
		mn, err := parseJSONManifest(item)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, mn...)
	}
	return manifests, nil
}

// UpdateNamespace updates the namespace.
func UpdateNamespace(namespace Namespace, kubeconfig []byte) error {
	cs, err := NewClientset(kubeconfig)
	if err != nil {
		return fmt.Errorf("creating clientset: %w", err)
	}

	if namespace.Name == "" {
		return fmt.Errorf("namespace name can't be empty")
	}

	ns, err := cs.CoreV1().Namespaces().Get(context.TODO(), namespace.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	existingLabels := ns.ObjectMeta.Labels
	existingAnnotations := ns.ObjectMeta.Annotations
	// Delete keys from the existing labels and annotations that are defined by Lokomotive
	// ie. keys with the prefix `lokomotive.kinvolk.io`.
	// This ensures that when the namespace is updated, labels added by the user
	// are retained in the update.
	for key := range existingLabels {
		if strings.Contains(key, "lokomotive.kinvolk.io") {
			delete(existingLabels, key)
		}
	}

	for key := range existingAnnotations {
		if strings.Contains(key, "lokomotive.kinvolk.io") {
			delete(existingAnnotations, key)
		}
	}

	labels := namespace.Labels
	for key, value := range existingLabels {
		labels[key] = value
	}

	annotations := namespace.Annotations
	for key, value := range existingAnnotations {
		annotations[key] = value
	}

	// Update the namespace.
	_, err = cs.CoreV1().Namespaces().Update(context.TODO(), &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        namespace.Name,
			Labels:      labels,
			Annotations: annotations,
		},
	}, metav1.UpdateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	return nil
}
