package v1alpha1

import (
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hashicorp/consul-k8s/api/common"
	"github.com/hashicorp/consul/api"
	capi "github.com/hashicorp/consul/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ServiceIntentionsSpec defines the desired state of ServiceIntentions
type ServiceIntentionsSpec struct {
	Name      string             `json:"name,omitempty"`
	Namespace string             `json:"namespace,omitempty"`
	Sources   []*SourceIntention `json:"sources,omitempty"`
}

type SourceIntention struct {
	Name        string              `json:"name,omitempty"`
	Namespace   string              `json:"namespace,omitempty"`
	Action      IntentionAction     `json:"action,omitempty"`
	Precedence  int                 `json:"precedence,omitempty"`
	Type        IntentionSourceType `json:"type,omitempty"`
	Description string              `json:"description,omitempty"`
}

// IntentionSourceType is the type of the source within an intention.
type IntentionSourceType string

// IntentionAction is the action that the intention represents. This
// can be "allow" or "deny" to allowlist or denylist intentions.
type IntentionAction string

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ServiceIntentions is the Schema for the serviceintentions API
type ServiceIntentions struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServiceIntentionsSpec `json:"spec,omitempty"`
	Status Status                `json:"status,omitempty"`
}

func (in *ServiceIntentions) GetObjectMeta() metav1.ObjectMeta {
	return in.ObjectMeta
}

func (in *ServiceIntentions) AddFinalizer(f string) {
	in.ObjectMeta.Finalizers = append(in.Finalizers(), f)
}

func (in *ServiceIntentions) RemoveFinalizer(f string) {
	var newFinalizers []string
	for _, oldF := range in.Finalizers() {
		if oldF != f {
			newFinalizers = append(newFinalizers, oldF)
		}
	}
	in.ObjectMeta.Finalizers = newFinalizers
}

func (in *ServiceIntentions) Finalizers() []string {
	return in.ObjectMeta.Finalizers
}

func (in *ServiceIntentions) ConsulKind() string {
	return capi.ServiceIntentions
}

func (in *ServiceIntentions) ConsulNamespaced() bool {
	return true
}

func (in *ServiceIntentions) KubeKind() string {
	return common.ServiceIntentions
}

func (in *ServiceIntentions) Name() string {
	return in.ObjectMeta.Name
}

func (in *ServiceIntentions) SetSyncedCondition(status corev1.ConditionStatus, reason, message string) {
	in.Status.Conditions = Conditions{
		{
			Type:               ConditionSynced,
			Status:             status,
			LastTransitionTime: metav1.Now(),
			Reason:             reason,
			Message:            message,
		},
	}
}

func (in *ServiceIntentions) SyncedCondition() (status corev1.ConditionStatus, reason, message string) {
	cond := in.Status.GetCondition(ConditionSynced)
	if cond == nil {
		return corev1.ConditionUnknown, "", ""
	}
	return cond.Status, cond.Reason, cond.Message
}

func (in *ServiceIntentions) SyncedConditionStatus() corev1.ConditionStatus {
	condition := in.Status.GetCondition(ConditionSynced)
	if condition == nil {
		return corev1.ConditionUnknown
	}
	return condition.Status
}

func (in *ServiceIntentions) ToConsul(datacenter string) api.ConfigEntry {
	panic("implement me")
}

func (in *ServiceIntentions) MatchesConsul(candidate api.ConfigEntry) bool {
	configEntry, ok := candidate.(*capi.ServiceIntentionsConfigEntry)
	if !ok {
		return false
	}
	// No datacenter is passed to ToConsul as we ignore the Meta field when checking for equality.
	return cmp.Equal(in.ToConsul(""), configEntry, cmpopts.IgnoreFields(capi.ServiceIntentionsConfigEntry{}, "Namespace", "Meta", "ModifyIndex", "CreateIndex"), cmpopts.IgnoreUnexported(), cmpopts.EquateEmpty())

}

func (in *ServiceIntentions) Validate() error {
	panic("implement me")
}

// +kubebuilder:object:root=true

// ServiceIntentionsList contains a list of ServiceIntentions
type ServiceIntentionsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceIntentions `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServiceIntentions{}, &ServiceIntentionsList{})
}
