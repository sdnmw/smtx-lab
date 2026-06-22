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
	ChainPath         []IptablesTraceStep
	ConntrackMatched  bool
	IPVSMatched       bool
}

type IptablesTraceStep struct {
	Stage   string
	Table   string
	Chain   string
	Action  string
	Purpose string
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
	result.ChainPath = TraceSourceIptables(snapshot, traffic)
	return result
}

func TraceSourceIptables(snapshot agent.Snapshot, traffic TrafficObservation) []IptablesTraceStep {
	rules := parseIptables(snapshot.Iptables.Lines)
	var out []IptablesTraceStep
	appendExistingChain(&out, rules, "source-egress", []string{"raw", "mangle", "nat"}, "cali-PREROUTING", "Calico pre-routing hook for traffic leaving the source workload.")
	if traffic.ServiceIP != "" {
		out = append(out, traceServiceDNAT(rules, traffic)...)
	}
	appendExistingChain(&out, rules, "source-egress", []string{"filter"}, "cali-FORWARD", chainPurpose("cali-FORWARD"))
	appendExistingChain(&out, rules, "source-egress", []string{"filter"}, "cali-from-wl-dispatch", chainPurpose("cali-from-wl-dispatch"))
	if iface := workloadInterface(snapshot.Routes.Lines, traffic.SourceIP); iface != "" {
		if endpointChain := dispatchTarget(rules, "filter", "cali-from-wl-dispatch", "-i", iface); endpointChain != "" {
			appendTraceStep(&out, IptablesTraceStep{
				Stage:   "source-egress",
				Table:   "filter",
				Chain:   endpointChain,
				Action:  "policy for " + iface,
				Purpose: chainPurpose(endpointChain),
			})
		}
	}
	appendExistingChain(&out, rules, "source-egress", []string{"filter"}, "KUBE-FORWARD", chainPurpose("KUBE-FORWARD"))
	appendExistingChain(&out, rules, "source-egress", []string{"mangle", "nat"}, "cali-POSTROUTING", "Calico post-routing hook before traffic leaves the source node.")
	return out
}

func TraceTargetIptables(snapshot agent.Snapshot, traffic TrafficObservation) []IptablesTraceStep {
	rules := parseIptables(snapshot.Iptables.Lines)
	var out []IptablesTraceStep
	appendExistingChain(&out, rules, "target-ingress", []string{"raw", "mangle", "nat"}, "cali-PREROUTING", "Calico pre-routing hook for traffic entering the target node.")
	appendExistingChain(&out, rules, "target-ingress", []string{"filter"}, "cali-FORWARD", chainPurpose("cali-FORWARD"))
	appendExistingChain(&out, rules, "target-ingress", []string{"filter"}, "cali-to-wl-dispatch", chainPurpose("cali-to-wl-dispatch"))
	if iface := workloadInterface(snapshot.Routes.Lines, traffic.TargetIP); iface != "" {
		if endpointChain := dispatchTarget(rules, "filter", "cali-to-wl-dispatch", "-o", iface); endpointChain != "" {
			appendTraceStep(&out, IptablesTraceStep{
				Stage:   "target-ingress",
				Table:   "filter",
				Chain:   endpointChain,
				Action:  "policy for " + iface,
				Purpose: chainPurpose(endpointChain),
			})
		}
	}
	appendExistingChain(&out, rules, "target-ingress", []string{"filter"}, "KUBE-FORWARD", chainPurpose("KUBE-FORWARD"))
	return out
}

type iptablesRules map[string]map[string][]string

func parseIptables(lines []string) iptablesRules {
	out := iptablesRules{}
	table := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "*") {
			table = strings.TrimPrefix(trimmed, "*")
			if out[table] == nil {
				out[table] = map[string][]string{}
			}
			continue
		}
		if trimmed == "COMMIT" {
			table = ""
			continue
		}
		if table == "" {
			continue
		}
		chain := extractRuleChain(trimmed)
		if chain == "" {
			continue
		}
		if _, ok := out[table][chain]; !ok {
			out[table][chain] = nil
		}
		if strings.HasPrefix(trimmed, "-A ") {
			out[table][chain] = append(out[table][chain], trimmed)
		}
	}
	return out
}

func traceServiceDNAT(rules iptablesRules, traffic TrafficObservation) []IptablesTraceStep {
	entryRule := findMatchingRule(rules["nat"]["KUBE-SERVICES"], func(line string) bool {
		if traffic.ServiceIP == "" || !strings.Contains(line, traffic.ServiceIP) {
			return false
		}
		if traffic.Port > 0 && !strings.Contains(line, "--dport "+int32String(traffic.Port)) {
			return false
		}
		protocol := iptablesProtocol(traffic.Protocol)
		return protocol == "" || strings.Contains(strings.ToLower(line), "-p "+protocol)
	})
	serviceChain := jumpTarget(entryRule)
	if serviceChain == "" {
		return nil
	}
	out := []IptablesTraceStep{{
		Stage:   "service-dnat",
		Table:   "nat",
		Chain:   "KUBE-SERVICES",
		Action:  "jump " + serviceChain,
		Purpose: chainPurpose("KUBE-SERVICES"),
	}}
	appendTraceStep(&out, IptablesTraceStep{
		Stage:   "service-dnat",
		Table:   "nat",
		Chain:   serviceChain,
		Purpose: chainPurpose(serviceChain),
	})

	endpointChain := ""
	for _, line := range rules["nat"][serviceChain] {
		candidate := jumpTarget(line)
		if !strings.HasPrefix(candidate, "KUBE-SEP-") {
			continue
		}
		if traffic.TargetIP != "" && strings.Contains(line, traffic.TargetIP) {
			endpointChain = candidate
			break
		}
		if endpointChain == "" && endpointDNATMatches(rules["nat"][candidate], traffic.TargetIP) {
			endpointChain = candidate
		}
	}
	if endpointChain == "" {
		return out
	}
	out[len(out)-1].Action = "select " + endpointChain
	appendTraceStep(&out, IptablesTraceStep{
		Stage:   "service-dnat",
		Table:   "nat",
		Chain:   endpointChain,
		Action:  dnatAction(rules["nat"][endpointChain]),
		Purpose: chainPurpose(endpointChain),
	})
	return out
}

func appendExistingChain(out *[]IptablesTraceStep, rules iptablesRules, stage string, tables []string, chain, purpose string) {
	for _, table := range tables {
		if _, ok := rules[table][chain]; ok {
			appendTraceStep(out, IptablesTraceStep{Stage: stage, Table: table, Chain: chain, Purpose: purpose})
			return
		}
	}
}

func appendTraceStep(out *[]IptablesTraceStep, step IptablesTraceStep) {
	if step.Chain == "" {
		return
	}
	for _, existing := range *out {
		if existing.Stage == step.Stage && existing.Table == step.Table && existing.Chain == step.Chain {
			return
		}
	}
	*out = append(*out, step)
}

func workloadInterface(routes []string, ip string) string {
	if ip == "" {
		return ""
	}
	for _, line := range routes {
		fields := strings.Fields(line)
		if len(fields) < 3 || strings.TrimSuffix(fields[0], "/32") != ip {
			continue
		}
		for idx := 1; idx+1 < len(fields); idx++ {
			if fields[idx] == "dev" {
				return fields[idx+1]
			}
		}
	}
	return ""
}

func dispatchTarget(rules iptablesRules, table, chain, direction, iface string) string {
	for _, line := range rules[table][chain] {
		fields := strings.Fields(line)
		for idx := 0; idx+1 < len(fields); idx++ {
			if fields[idx] == direction && fields[idx+1] == iface {
				return jumpTarget(line)
			}
		}
	}
	return ""
}

func jumpTarget(line string) string {
	fields := strings.Fields(line)
	for idx := 0; idx+1 < len(fields); idx++ {
		if fields[idx] == "-j" || fields[idx] == "-g" {
			return fields[idx+1]
		}
	}
	return ""
}

func endpointDNATMatches(lines []string, targetIP string) bool {
	if targetIP == "" {
		return false
	}
	for _, line := range lines {
		if strings.Contains(line, "--to-destination "+targetIP+":") || strings.Contains(line, "--to-destination "+targetIP+" ") {
			return true
		}
	}
	return false
}

func dnatAction(lines []string) string {
	for _, line := range lines {
		fields := strings.Fields(line)
		for idx := 0; idx+1 < len(fields); idx++ {
			if fields[idx] == "--to-destination" {
				return "DNAT " + fields[idx+1]
			}
		}
	}
	return "DNAT endpoint"
}

func findMatchingRule(lines []string, matches func(string) bool) string {
	for _, line := range lines {
		if matches(line) {
			return line
		}
	}
	return ""
}

func int32String(value int32) string {
	if value == 0 {
		return "0"
	}
	var digits [11]byte
	idx := len(digits)
	for value > 0 {
		idx--
		digits[idx] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[idx:])
}

func iptablesProtocol(protocol string) string {
	switch strings.ToLower(protocol) {
	case "http", "https":
		return "tcp"
	default:
		return strings.ToLower(protocol)
	}
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
