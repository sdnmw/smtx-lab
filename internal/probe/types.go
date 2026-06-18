package probe

type Check struct {
	ID            string `json:"id"`
	SourceNode    string `json:"sourceNode"`
	TargetPod     string `json:"targetPod,omitempty"`
	TargetNode    string `json:"targetNode,omitempty"`
	TargetService string `json:"targetService,omitempty"`
	Protocol      string `json:"protocol"`
	Address       string `json:"address"`
	Port          int32  `json:"port"`
	Path          string `json:"path"`
	TargetIP      string `json:"targetIP,omitempty"`
	ServiceIP     string `json:"serviceIP,omitempty"`
}

type Report struct {
	SourceNode string   `json:"sourceNode"`
	Results    []Result `json:"results"`
}

type Result struct {
	CheckID       string  `json:"checkID"`
	SourcePod     string  `json:"sourcePod,omitempty"`
	SourceIP      string  `json:"sourceIP,omitempty"`
	SourceNode    string  `json:"sourceNode"`
	TargetPod     string  `json:"targetPod,omitempty"`
	TargetNode    string  `json:"targetNode,omitempty"`
	TargetService string  `json:"targetService,omitempty"`
	Protocol      string  `json:"protocol"`
	Address       string  `json:"address"`
	Port          int32   `json:"port"`
	Path          string  `json:"path"`
	TargetIP      string  `json:"targetIP,omitempty"`
	ServiceIP     string  `json:"serviceIP,omitempty"`
	Success       bool    `json:"success"`
	Error         string  `json:"error,omitempty"`
	LatencyMsP50  float64 `json:"latencyMsP50,omitempty"`
	LatencyMsP95  float64 `json:"latencyMsP95,omitempty"`
}
