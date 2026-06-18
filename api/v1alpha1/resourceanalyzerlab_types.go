package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type ResourceAnalyzerLabSpec struct {
	Target          ResourceTargetSpec         `json:"target,omitempty"`
	Metrics         MetricsSourceSpec          `json:"metrics,omitempty"`
	Recommendation  RecommendationPolicySpec   `json:"recommendation,omitempty"`
	MonitoringStack MonitoringStackSpec        `json:"monitoringStack,omitempty"`
	Output          ResourceAnalyzerOutputSpec `json:"output,omitempty"`
}

type ResourceTargetSpec struct {
	Namespaces        []string          `json:"namespaces,omitempty"`
	WorkloadKinds     []string          `json:"workloadKinds,omitempty"`
	LabelSelector     LabelSelectorSpec `json:"labelSelector,omitempty"`
	ExcludeNamespaces []string          `json:"excludeNamespaces,omitempty"`
}

type MetricsSourceSpec struct {
	PrometheusURL  string `json:"prometheusURL,omitempty"`
	LookbackDays   int32  `json:"lookbackDays,omitempty"`
	Step           string `json:"step,omitempty"`
	TimeoutSeconds int32  `json:"timeoutSeconds,omitempty"`
}

type RecommendationPolicySpec struct {
	CPU           ResourceRecommendationRule `json:"cpu,omitempty"`
	Memory        ResourceRecommendationRule `json:"memory,omitempty"`
	LanguageHints LanguageHintsSpec          `json:"languageHints,omitempty"`
}

type ResourceRecommendationRule struct {
	RequestPercentile    string  `json:"requestPercentile,omitempty"`
	LimitPercentile      string  `json:"limitPercentile,omitempty"`
	RequestHeadroomRatio float64 `json:"requestHeadroomRatio,omitempty"`
	LimitHeadroomRatio   float64 `json:"limitHeadroomRatio,omitempty"`
	MinRequestMillicores int64   `json:"minRequestMillicores,omitempty"`
	MinRequestMiB        int64   `json:"minRequestMiB,omitempty"`
}

type LanguageHintsSpec struct {
	Default   string                 `json:"default,omitempty"`
	Overrides []LanguageOverrideSpec `json:"overrides,omitempty"`
}

type LanguageOverrideSpec struct {
	Namespace string `json:"namespace,omitempty"`
	Workload  string `json:"workload,omitempty"`
	Container string `json:"container,omitempty"`
	Language  string `json:"language,omitempty"`
}

type MonitoringStackSpec struct {
	AutoDeploy bool           `json:"autoDeploy,omitempty"`
	Prometheus MonitoringItem `json:"prometheus,omitempty"`
	Grafana    MonitoringItem `json:"grafana,omitempty"`
}

type MonitoringItem struct {
	Enabled bool `json:"enabled,omitempty"`
}

type ResourceAnalyzerOutputSpec struct {
	Excel             ExcelOutputSpec `json:"excel,omitempty"`
	HTML              HTMLOutputSpec  `json:"html,omitempty"`
	IncludeRawMetrics bool            `json:"includeRawMetrics,omitempty"`
}

type ResourceAnalyzerLabStatus struct {
	ObservedGeneration int64                    `json:"observedGeneration,omitempty"`
	Phase              LabPhase                 `json:"phase,omitempty"`
	Conditions         []metav1.Condition       `json:"conditions,omitempty"`
	Summary            ResourceAnalyzerSummary  `json:"summary,omitempty"`
	Recommendations    []ResourceRecommendation `json:"recommendations,omitempty"`
	Artifacts          ArtifactStatus           `json:"artifacts,omitempty"`
}

type ResourceAnalyzerSummary struct {
	AnalyzedNamespaces                     int32 `json:"analyzedNamespaces,omitempty"`
	AnalyzedWorkloads                      int32 `json:"analyzedWorkloads,omitempty"`
	AnalyzedContainers                     int32 `json:"analyzedContainers,omitempty"`
	RecommendedChanges                     int32 `json:"recommendedChanges,omitempty"`
	PotentialCPURequestReductionMillicores int64 `json:"potentialCpuRequestReductionMillicores,omitempty"`
	PotentialMemoryRequestReductionMiB     int64 `json:"potentialMemoryRequestReductionMiB,omitempty"`
}

type ResourceRecommendation struct {
	Namespace    string                  `json:"namespace,omitempty"`
	WorkloadKind string                  `json:"workloadKind,omitempty"`
	WorkloadName string                  `json:"workloadName,omitempty"`
	Pod          string                  `json:"pod,omitempty"`
	Container    string                  `json:"container,omitempty"`
	Language     string                  `json:"language,omitempty"`
	Current      ContainerResourceValues `json:"current,omitempty"`
	Observed     ObservedResourceValues  `json:"observed,omitempty"`
	Usage        ResourceUsageWindows    `json:"usage,omitempty"`
	Recommended  ContainerResourceValues `json:"recommended,omitempty"`
	Reason       string                  `json:"reason,omitempty"`
}

type ContainerResourceValues struct {
	CPURequestMillicores int64 `json:"cpuRequestMillicores,omitempty"`
	CPULimitMillicores   int64 `json:"cpuLimitMillicores,omitempty"`
	MemoryRequestMiB     int64 `json:"memoryRequestMiB,omitempty"`
	MemoryLimitMiB       int64 `json:"memoryLimitMiB,omitempty"`
}

type ObservedResourceValues struct {
	CPUP50Millicores int64 `json:"cpuP50Millicores,omitempty"`
	CPUP95Millicores int64 `json:"cpuP95Millicores,omitempty"`
	CPUP99Millicores int64 `json:"cpuP99Millicores,omitempty"`
	MemoryP50MiB     int64 `json:"memoryP50MiB,omitempty"`
	MemoryP95MiB     int64 `json:"memoryP95MiB,omitempty"`
	MemoryP99MiB     int64 `json:"memoryP99MiB,omitempty"`
}

type ResourceUsageWindows struct {
	Current ResourceUsageStats `json:"current,omitempty"`
	Last7d  ResourceUsageStats `json:"last7d,omitempty"`
	Last14d ResourceUsageStats `json:"last14d,omitempty"`
}

type ResourceUsageStats struct {
	SampleCount      int32 `json:"sampleCount,omitempty"`
	CPUMinMillicores int64 `json:"cpuMinMillicores,omitempty"`
	CPUAvgMillicores int64 `json:"cpuAvgMillicores,omitempty"`
	CPUMaxMillicores int64 `json:"cpuMaxMillicores,omitempty"`
	MemoryMinMiB     int64 `json:"memoryMinMiB,omitempty"`
	MemoryAvgMiB     int64 `json:"memoryAvgMiB,omitempty"`
	MemoryMaxMiB     int64 `json:"memoryMaxMiB,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=ral
type ResourceAnalyzerLab struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ResourceAnalyzerLabSpec   `json:"spec,omitempty"`
	Status ResourceAnalyzerLabStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ResourceAnalyzerLabList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ResourceAnalyzerLab `json:"items"`
}
