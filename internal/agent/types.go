package agent

import "time"

type SnapshotRequest struct {
	CollectCNI       bool            `json:"collectCNI,omitempty"`
	CollectIptables  bool            `json:"collectIptables,omitempty"`
	CollectIPVS      bool            `json:"collectIPVS,omitempty"`
	CollectConntrack bool            `json:"collectConntrack,omitempty"`
	CollectRoutes    bool            `json:"collectRoutes,omitempty"`
	ChainAllowlist   []string        `json:"chainAllowlist,omitempty"`
	ConntrackFilter  ConntrackFilter `json:"conntrackFilter,omitempty"`
}

type ConntrackFilter struct {
	Protocols  []string `json:"protocols,omitempty"`
	MaxEntries int      `json:"maxEntries,omitempty"`
}

type Snapshot struct {
	NodeName  string          `json:"nodeName"`
	Time      time.Time       `json:"time"`
	CNI       CNISnapshot     `json:"cni,omitempty"`
	Iptables  CommandSnapshot `json:"iptables,omitempty"`
	IPVS      IPVSSnapshot    `json:"ipvs,omitempty"`
	Conntrack CommandSnapshot `json:"conntrack,omitempty"`
	Routes    CommandSnapshot `json:"routes,omitempty"`
	Errors    []string        `json:"errors,omitempty"`
}

type CNISnapshot struct {
	Type         string         `json:"type,omitempty"`
	Mode         string         `json:"mode,omitempty"`
	OverlayMode  string         `json:"overlayMode,omitempty"`
	ConfigFiles  []string       `json:"configFiles,omitempty"`
	Interfaces   []string       `json:"interfaces,omitempty"`
	DetectionLog []string       `json:"detectionLog,omitempty"`
	Calico       CalicoSnapshot `json:"calico,omitempty"`
}

type CalicoSnapshot struct {
	OverlayMode        string   `json:"overlayMode,omitempty"`
	IPIPInterface      string   `json:"ipipInterface,omitempty"`
	VXLANInterface     string   `json:"vxlanInterface,omitempty"`
	WireGuardInterface string   `json:"wireGuardInterface,omitempty"`
	WorkloadInterfaces []string `json:"workloadInterfaces,omitempty"`
	ConfigHints        []string `json:"configHints,omitempty"`
}

type CommandSnapshot struct {
	Available bool     `json:"available"`
	Captured  bool     `json:"captured"`
	Truncated bool     `json:"truncated,omitempty"`
	Lines     []string `json:"lines,omitempty"`
	LineCount int      `json:"lineCount,omitempty"`
}

type IPVSSnapshot struct {
	Available       bool     `json:"available"`
	Enabled         bool     `json:"enabled"`
	ServiceCount    int      `json:"serviceCount,omitempty"`
	RealServerCount int      `json:"realServerCount,omitempty"`
	Lines           []string `json:"lines,omitempty"`
}
