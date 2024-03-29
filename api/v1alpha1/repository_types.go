/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"time"

	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RepoType string

const (
	GitHub RepoType = "github"
	GitLab RepoType = "gitlab"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// RepositorySpec defines the desired state of Repository
type RepositorySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of Repository. Edit repository_types.go to remove/update
	URL       string           `json:"url"`
	Ref       string           `json:"ref,omitempty"`
	Auth      *AuthSecret      `json:"auth,omitempty"`
	Type      RepoType         `json:"type,omitempty"`
	Frequency *metav1.Duration `json:"frequency,omitempty"`
	Pipeline  PipelineRef      `json:"pipelineRef"`
}

// RepositoryStatus defines the observed state of Repository
type RepositoryStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	LastError          string `json:"lastError,omitempty"`
	PollStatus         `json:"pollStatus,omitempty"`
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Repository is the Schema for the repositories API
type Repository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RepositorySpec   `json:"spec,omitempty"`
	Status RepositoryStatus `json:"status,omitempty"`
}

type PollStatus struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	ETag string `json:"etag"`
}

//+kubebuilder:object:root=true

// RepositoryList contains a list of Repository
type RepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Repository `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Repository{}, &RepositoryList{})
}

type AuthSecret struct {
	v1.SecretReference `json:"secretRef,omitempty"`
	Key                string `json:"key,omitempty"`
}

type PipelineRef struct {
	Name               string                               `json:"name"`
	Namespace          string                               `json:"namespace,omitempty"`
	ServiceAccountName string                               `json:"serviceAccountName,omitempty"`
	Params             []Param                              `json:"params,omitempty"`
	Resources          []pipelinev1.PipelineResourceBinding `json:"resources,omitempty"`
	Workspaces         []pipelinev1.WorkspaceBinding        `json:"workspaces,omitempty"`
}

type Param struct {
	Name       string `json:"name"`
	Expression string `json:"expression"`
}

func (r *Repository) GetFrequency() time.Duration {
	if r.Spec.Frequency != nil {
		return r.Spec.Frequency.Duration
	}
	return time.Second * 30
}

func (p PollStatus) Equal(o PollStatus) bool {
	return (p.Ref == o.Ref) && (p.SHA == o.SHA) && (p.ETag == o.ETag)
}
