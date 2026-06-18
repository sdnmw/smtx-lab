package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type LabPhase string

const (
	LabPhasePending   LabPhase = "Pending"
	LabPhaseRunning   LabPhase = "Running"
	LabPhaseSucceeded LabPhase = "Succeeded"
	LabPhaseFailed    LabPhase = "Failed"
)

type ExcelOutputSpec struct {
	Enabled       bool   `json:"enabled,omitempty"`
	ConfigMapName string `json:"configMapName,omitempty"`
}

type HTMLOutputSpec struct {
	Enabled       bool   `json:"enabled,omitempty"`
	ConfigMapName string `json:"configMapName,omitempty"`
}

type ArtifactStatus struct {
	ExcelConfigMapName        string   `json:"excelConfigMapName,omitempty"`
	HTMLConfigMapName         string   `json:"htmlConfigMapName,omitempty"`
	RawSnapshotConfigMapNames []string `json:"rawSnapshotConfigMapNames,omitempty"`
}

type LabelSelectorSpec struct {
	MatchLabels      map[string]string                 `json:"matchLabels,omitempty"`
	MatchExpressions []metav1.LabelSelectorRequirement `json:"matchExpressions,omitempty"`
}
