package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type NetworkProbeLabSpec struct {
	Target        NetworkProbeTargetSpec   `json:"target,omitempty"`
	Traffic       NetworkProbeTrafficSpec  `json:"traffic,omitempty"`
	Observability NetworkObservabilitySpec `json:"observability,omitempty"`
	Agent         NodeAgentSpec            `json:"agent,omitempty"`
	Output        NetworkProbeOutputSpec   `json:"output,omitempty"`
}

type NetworkProbeTargetSpec struct {
	Namespaces      []string          `json:"namespaces,omitempty"`
	PodSelector     LabelSelectorSpec `json:"podSelector,omitempty"`
	ServiceSelector LabelSelectorSpec `json:"serviceSelector,omitempty"`
	NodeSelector    map[string]string `json:"nodeSelector,omitempty"`
}

type NetworkProbeTrafficSpec struct {
	Protocols         []string `json:"protocols,omitempty"`
	Ports             []int32  `json:"ports,omitempty"`
	Count             int32    `json:"count,omitempty"`
	TimeoutSeconds    int32    `json:"timeoutSeconds,omitempty"`
	Concurrency       int32    `json:"concurrency,omitempty"`
	CrossNodeOnly     bool     `json:"crossNodeOnly,omitempty"`
	IncludeServiceVIP bool     `json:"includeServiceVIP,omitempty"`
	IncludePodIP      bool     `json:"includePodIP,omitempty"`
	IncludeDNS        bool     `json:"includeDNS,omitempty"`
}

type NetworkObservabilitySpec struct {
	CollectCNI       bool                `json:"collectCNI,omitempty"`
	CollectIptables  bool                `json:"collectIptables,omitempty"`
	CollectIPVS      bool                `json:"collectIPVS,omitempty"`
	CollectConntrack bool                `json:"collectConntrack,omitempty"`
	CollectRoutes    bool                `json:"collectRoutes,omitempty"`
	ChainAllowlist   []string            `json:"chainAllowlist,omitempty"`
	ConntrackFilter  ConntrackFilterSpec `json:"conntrackFilter,omitempty"`
}

type ConntrackFilterSpec struct {
	Protocols  []string `json:"protocols,omitempty"`
	MaxEntries int32    `json:"maxEntries,omitempty"`
}

type NodeAgentSpec struct {
	ServiceAccountName string            `json:"serviceAccountName,omitempty"`
	Image              string            `json:"image,omitempty"`
	NodeSelector       map[string]string `json:"nodeSelector,omitempty"`
	Tolerations        []TolerationSpec  `json:"tolerations,omitempty"`
}

type TolerationSpec struct {
	Key               string `json:"key,omitempty"`
	Operator          string `json:"operator,omitempty"`
	Value             string `json:"value,omitempty"`
	Effect            string `json:"effect,omitempty"`
	TolerationSeconds *int64 `json:"tolerationSeconds,omitempty"`
}

type NetworkProbeOutputSpec struct {
	Excel              ExcelOutputSpec `json:"excel,omitempty"`
	HTML               HTMLOutputSpec  `json:"html,omitempty"`
	RetainRawSnapshots bool            `json:"retainRawSnapshots,omitempty"`
}

type NetworkProbeLabStatus struct {
	ObservedGeneration int64                `json:"observedGeneration,omitempty"`
	Phase              LabPhase             `json:"phase,omitempty"`
	Conditions         []metav1.Condition   `json:"conditions,omitempty"`
	Summary            NetworkProbeSummary  `json:"summary,omitempty"`
	NodeResults        []NetworkNodeResult  `json:"nodeResults,omitempty"`
	ProbeResults       []NetworkProbeResult `json:"probeResults,omitempty"`
	Artifacts          ArtifactStatus       `json:"artifacts,omitempty"`
}

type NetworkProbeSummary struct {
	TotalTests         int32    `json:"totalTests,omitempty"`
	Succeeded          int32    `json:"succeeded,omitempty"`
	Failed             int32    `json:"failed,omitempty"`
	CNIDetected        []string `json:"cniDetected,omitempty"`
	DatapathModes      []string `json:"datapathModes,omitempty"`
	CalicoOverlayModes []string `json:"calicoOverlayModes,omitempty"`
}

type NetworkNodeResult struct {
	NodeName  string          `json:"nodeName,omitempty"`
	CNI       CNIStatus       `json:"cni,omitempty"`
	Iptables  IptablesStatus  `json:"iptables,omitempty"`
	IPVS      IPVSStatus      `json:"ipvs,omitempty"`
	Conntrack ConntrackStatus `json:"conntrack,omitempty"`
}

type CNIStatus struct {
	Type        string       `json:"type,omitempty"`
	Version     string       `json:"version,omitempty"`
	Mode        string       `json:"mode,omitempty"`
	OverlayMode string       `json:"overlayMode,omitempty"`
	Calico      CalicoStatus `json:"calico,omitempty"`
}

type CalicoStatus struct {
	OverlayMode       string   `json:"overlayMode,omitempty"`
	IPIPInterface     string   `json:"ipipInterface,omitempty"`
	VXLANInterface    string   `json:"vxlanInterface,omitempty"`
	WorkloadInterface []string `json:"workloadInterfaces,omitempty"`
	ConfigHints       []string `json:"configHints,omitempty"`
}

type SnapshotReference struct {
	Kind string `json:"kind,omitempty"`
	Name string `json:"name,omitempty"`
}

type IptablesStatus struct {
	Captured      bool              `json:"captured,omitempty"`
	ChainCount    int32             `json:"chainCount,omitempty"`
	SnapshotRef   SnapshotReference `json:"snapshotRef,omitempty"`
	PodChains     []IptablesChain   `json:"podChains,omitempty"`
	ServiceChains []IptablesChain   `json:"serviceChains,omitempty"`
}

type IptablesChain struct {
	Name      string `json:"name,omitempty"`
	Category  string `json:"category,omitempty"`
	RuleCount int32  `json:"ruleCount,omitempty"`
	Purpose   string `json:"purpose,omitempty"`
}

type IPVSStatus struct {
	Enabled         bool  `json:"enabled,omitempty"`
	ServiceCount    int32 `json:"serviceCount,omitempty"`
	RealServerCount int32 `json:"realServerCount,omitempty"`
}

type ConntrackStatus struct {
	Captured       bool  `json:"captured,omitempty"`
	EntriesMatched int32 `json:"entriesMatched,omitempty"`
}

type NetworkProbeResult struct {
	SourcePod     string          `json:"sourcePod,omitempty"`
	SourcePodIP   string          `json:"sourcePodIP,omitempty"`
	SourceNode    string          `json:"sourceNode,omitempty"`
	SourceNodeIP  string          `json:"sourceNodeIP,omitempty"`
	TargetPod     string          `json:"targetPod,omitempty"`
	TargetPodIP   string          `json:"targetPodIP,omitempty"`
	TargetNode    string          `json:"targetNode,omitempty"`
	TargetNodeIP  string          `json:"targetNodeIP,omitempty"`
	TargetService string          `json:"targetService,omitempty"`
	ServiceIP     string          `json:"serviceIP,omitempty"`
	Protocol      string          `json:"protocol,omitempty"`
	Port          int32           `json:"port,omitempty"`
	Path          string          `json:"path,omitempty"`
	Success       bool            `json:"success,omitempty"`
	Error         string          `json:"error,omitempty"`
	LatencyMsP50  float64         `json:"latencyMsP50,omitempty"`
	LatencyMsP95  float64         `json:"latencyMsP95,omitempty"`
	Datapath      DatapathSummary `json:"datapath,omitempty"`
}

type DatapathSummary struct {
	CNI                   string              `json:"cni,omitempty"`
	CalicoOverlayMode     string              `json:"calicoOverlayMode,omitempty"`
	KubeProxyMode         string              `json:"kubeProxyMode,omitempty"`
	RelevantChains        []string            `json:"relevantChains,omitempty"`
	PodForwardChains      []string            `json:"podForwardChains,omitempty"`
	ServiceChains         []string            `json:"serviceChains,omitempty"`
	ChainPath             []IptablesTraceStep `json:"chainPath,omitempty"`
	ServiceEndpointSource string              `json:"serviceEndpointSource,omitempty"`
	ConntrackMatched      bool                `json:"conntrackMatched,omitempty"`
}

type IptablesTraceStep struct {
	Order   int32  `json:"order,omitempty"`
	Node    string `json:"node,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Table   string `json:"table,omitempty"`
	Chain   string `json:"chain,omitempty"`
	Action  string `json:"action,omitempty"`
	Purpose string `json:"purpose,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=npl
type NetworkProbeLab struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkProbeLabSpec   `json:"spec,omitempty"`
	Status NetworkProbeLabStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type NetworkProbeLabList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkProbeLab `json:"items"`
}
