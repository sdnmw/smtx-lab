package analyzer

import (
	"sort"
	"strings"

	"github.com/smtx-lab/smtx-lab-operator/internal/agent"
)

type TrafficObservation struct {
	Protocol  string
	SourceIP  string
	TargetIP  string
	ServiceIP string
	Port      int32
}

type DatapathCorrelation struct {
	CNI               string
	CalicoOverlayMode string
	KubeProxyMode     string
	RelevantChains    []string
	PodForwardChains  []string
	ServiceChains     []string
	ConntrackMatched  bool
	IPVSMatched       bool
}

type IptablesChainSummary struct {
	Name      string
	Category  string
	RuleCount int
	Purpose   string
}

func CorrelateDatapath(snapshot agent.Snapshot, traffic TrafficObservation) DatapathCorrelation {
	result := DatapathCorrelation{
		CNI:               snapshot.CNI.Type,
		CalicoOverlayMode: snapshot.CNI.OverlayMode,
		KubeProxyMode:     inferKubeProxyMode(snapshot),
	}
	podChains, serviceChains := SummarizeIptables(snapshot.Iptables.Lines)
	for _, line := range snapshot.Iptables.Lines {
		upper := strings.ToUpper(line)
		if strings.Contains(upper, "KUBE-") || strings.Contains(upper, "CALI-") || strings.Contains(upper, "CILIUM") || strings.Contains(upper, "EVEROUTE") || strings.Contains(upper, "FLANNEL") {
			chain := extractRuleChain(line)
			if chain != "" && !contains(result.RelevantChains, chain) {
				result.RelevantChains = append(result.RelevantChains, chain)
			}
		}
	}
	for _, chain := range podChains {
		if !contains(result.PodForwardChains, chain.Name) {
			result.PodForwardChains = append(result.PodForwardChains, chain.Name)
		}
	}
	for _, chain := range serviceChains {
		if !contains(result.ServiceChains, chain.Name) {
			result.ServiceChains = append(result.ServiceChains, chain.Name)
		}
	}
	for _, line := range snapshot.Conntrack.Lines {
		if tupleMatches(line, traffic) {
			result.ConntrackMatched = true
			break
		}
	}
	for _, line := range snapshot.IPVS.Lines {
		if traffic.ServiceIP != "" && strings.Contains(line, traffic.ServiceIP) {
			result.IPVSMatched = true
			break
		}
	}
	return result
}

func SummarizeIptables(lines []string) ([]IptablesChainSummary, []IptablesChainSummary) {
	counts := map[string]int{}
	for _, line := range lines {
		chain := extractRuleChain(line)
		if chain == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "-A ") {
			counts[chain]++
		} else if _, ok := counts[chain]; !ok {
			counts[chain] = 0
		}
	}
	podChains := make([]IptablesChainSummary, 0)
	serviceChains := make([]IptablesChainSummary, 0)
	for chain, count := range counts {
		category := chainCategory(chain)
		if category == "" {
			continue
		}
		summary := IptablesChainSummary{
			Name:      chain,
			Category:  category,
			RuleCount: count,
			Purpose:   chainPurpose(chain),
		}
		switch category {
		case "pod-forward":
			podChains = append(podChains, summary)
		case "service":
			serviceChains = append(serviceChains, summary)
		}
	}
	sortChains(podChains)
	sortChains(serviceChains)
	return podChains, serviceChains
}

func sortChains(chains []IptablesChainSummary) {
	sort.Slice(chains, func(i, j int) bool {
		if chains[i].Category != chains[j].Category {
			return chains[i].Category < chains[j].Category
		}
		return chains[i].Name < chains[j].Name
	})
}

func chainCategory(chain string) string {
	lower := strings.ToLower(chain)
	upper := strings.ToUpper(chain)
	switch {
	case strings.HasPrefix(lower, "cali-"):
		return "pod-forward"
	case upper == "KUBE-FORWARD":
		return "pod-forward"
	case strings.HasPrefix(upper, "KUBE-SVC-"), strings.HasPrefix(upper, "KUBE-SEP-"), strings.HasPrefix(upper, "KUBE-EXT-"):
		return "service"
	case upper == "KUBE-SERVICES" || upper == "KUBE-NODEPORTS" || upper == "KUBE-EXTERNAL-SERVICES" || upper == "KUBE-MARK-MASQ" || upper == "KUBE-POSTROUTING":
		return "service"
	default:
		return ""
	}
}

func chainPurpose(chain string) string {
	lower := strings.ToLower(chain)
	upper := strings.ToUpper(chain)
	switch {
	case lower == "cali-forward":
		return "Calico policy and routing hook for forwarded pod traffic."
	case lower == "cali-from-wl-dispatch":
		return "Dispatches packets leaving workload interfaces to per-workload policy chains."
	case lower == "cali-to-wl-dispatch":
		return "Dispatches packets entering workload interfaces to per-workload policy chains."
	case strings.HasPrefix(lower, "cali-fw-"):
		return "Calico from-workload policy chain for a workload endpoint."
	case strings.HasPrefix(lower, "cali-tw-"):
		return "Calico to-workload policy chain for a workload endpoint."
	case strings.HasPrefix(lower, "cali-pri-") || strings.HasPrefix(lower, "cali-pro-"):
		return "Calico profile policy chain."
	case upper == "KUBE-FORWARD":
		return "kube-proxy forwarding chain that permits service and pod forwarding paths."
	case upper == "KUBE-SERVICES":
		return "kube-proxy service VIP entry chain."
	case strings.HasPrefix(upper, "KUBE-SVC-"):
		return "kube-proxy per-Service load-balancing chain."
	case strings.HasPrefix(upper, "KUBE-SEP-"):
		return "kube-proxy per-endpoint DNAT chain."
	case strings.HasPrefix(upper, "KUBE-EXT-"):
		return "kube-proxy external traffic policy chain."
	case upper == "KUBE-NODEPORTS":
		return "kube-proxy NodePort dispatch chain."
	case upper == "KUBE-EXTERNAL-SERVICES":
		return "kube-proxy external service guard chain."
	case upper == "KUBE-MARK-MASQ":
		return "kube-proxy chain that marks packets requiring masquerade."
	case upper == "KUBE-POSTROUTING":
		return "kube-proxy postrouting masquerade chain."
	default:
		return "iptables datapath chain captured for correlation."
	}
}

func inferKubeProxyMode(snapshot agent.Snapshot) string {
	if snapshot.IPVS.Enabled {
		return "ipvs"
	}
	if snapshot.Iptables.Captured {
		return "iptables"
	}
	if snapshot.CNI.Mode == "ebpf" {
		return "ebpf"
	}
	return "unknown"
}

func extractRuleChain(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	if fields[0] == "-A" || fields[0] == "-N" {
		return fields[1]
	}
	if strings.HasPrefix(fields[0], ":") {
		return strings.TrimPrefix(fields[0], ":")
	}
	return ""
}

func tupleMatches(line string, traffic TrafficObservation) bool {
	line = strings.ToLower(line)
	if traffic.Protocol != "" && !strings.Contains(line, strings.ToLower(traffic.Protocol)) {
		return false
	}
	for _, value := range []string{traffic.SourceIP, traffic.TargetIP, traffic.ServiceIP} {
		if value != "" && strings.Contains(line, value) {
			return true
		}
	}
	return false
}

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
