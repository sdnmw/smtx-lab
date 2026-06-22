package exporter

import (
	"bytes"
	"strings"
	"testing"

	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
)

func TestWriteNetworkHTML(t *testing.T) {
	var buf bytes.Buffer
	err := WriteNetworkHTML(&buf, "cross-node", labv1alpha1.NetworkProbeSummary{
		TotalTests:         1,
		Succeeded:          1,
		CNIDetected:        []string{"calico"},
		CalicoOverlayModes: []string{"ipip"},
	}, []labv1alpha1.NetworkProbeResult{{
		SourcePod:    "smtx/probe-a",
		SourcePodIP:  "172.31.1.10",
		SourceNode:   "node-a",
		SourceNodeIP: "192.168.1.10",
		TargetPod:    "test/nginx-b",
		TargetPodIP:  "172.31.2.10",
		TargetNode:   "node-b",
		TargetNodeIP: "192.168.1.11",
		Protocol:     "TCP",
		Port:         80,
		Path:         "podIP",
		Success:      true,
		LatencyMsP95: 1.25,
		Datapath: labv1alpha1.DatapathSummary{
			CNI:               "calico",
			CalicoOverlayMode: "ipip",
			ChainPath: []labv1alpha1.IptablesTraceStep{{
				Order: 1, Node: "node-a", Stage: "source-egress", Table: "filter", Chain: "cali-FORWARD", Purpose: "Calico forwarding",
			}},
		},
	}, {
		SourcePod:     "smtx/probe-a",
		SourcePodIP:   "172.31.1.10",
		SourceNode:    "node-a",
		SourceNodeIP:  "192.168.1.10",
		TargetPod:     "test/nginx-b",
		TargetPodIP:   "172.31.2.10",
		TargetNode:    "node-b",
		TargetNodeIP:  "192.168.1.11",
		TargetService: "test/nginx",
		ServiceIP:     "10.96.0.20",
		Protocol:      "TCP",
		Port:          80,
		Path:          "serviceVIP",
		Success:       true,
		Datapath: labv1alpha1.DatapathSummary{
			KubeProxyMode:         "iptables",
			ServiceEndpointSource: "service-selector-correlated",
			ChainPath: []labv1alpha1.IptablesTraceStep{{
				Order: 1, Node: "node-a", Stage: "service-dnat", Table: "nat", Chain: "KUBE-SERVICES", Action: "jump KUBE-SVC-TEST",
			}, {
				Order: 2, Node: "node-a", Stage: "service-dnat", Table: "nat", Chain: "KUBE-SVC-TEST", Action: "select KUBE-SEP-TEST",
			}},
		},
	}}, []labv1alpha1.NetworkNodeResult{{
		NodeName: "node-a",
		CNI: labv1alpha1.CNIStatus{
			Type:        "calico",
			OverlayMode: "ipip",
			Calico: labv1alpha1.CalicoStatus{
				IPIPInterface: "tunl0",
			},
		},
		Iptables: labv1alpha1.IptablesStatus{
			ChainCount: 10,
			PodChains: []labv1alpha1.IptablesChain{{
				Name: "cali-FORWARD",
			}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{"Network Probe Report", "172.31.1.10", "cali-FORWARD", "ipip", "Pod-to-Pod iptables calls", "Pod-to-Service-to-Pod iptables calls", "KUBE-SVC-TEST", "Correlated endpoint"} {
		if !strings.Contains(html, want) {
			t.Fatalf("html missing %q: %s", want, html)
		}
	}
	if strings.Contains(html, "ZgotmplZ") {
		t.Fatalf("html contains blocked template content: %s", html)
	}
}

func TestWriteResourceHTML(t *testing.T) {
	var buf bytes.Buffer
	err := WriteResourceHTML(&buf, "resource-14d", labv1alpha1.ResourceAnalyzerSummary{
		AnalyzedNamespaces:                     1,
		AnalyzedWorkloads:                      1,
		AnalyzedContainers:                     1,
		RecommendedChanges:                     1,
		PotentialCPURequestReductionMillicores: 200,
		PotentialMemoryRequestReductionMiB:     256,
	}, []labv1alpha1.ResourceRecommendation{{
		Namespace: "prod",
		Pod:       "orders-abc",
		Container: "app",
		Current: labv1alpha1.ContainerResourceValues{
			CPURequestMillicores: 500,
			CPULimitMillicores:   1000,
			MemoryRequestMiB:     512,
			MemoryLimitMiB:       1024,
		},
		Usage: labv1alpha1.ResourceUsageWindows{
			Last14d: labv1alpha1.ResourceUsageStats{
				CPUMinMillicores: 100,
				CPUAvgMillicores: 200,
				CPUMaxMillicores: 300,
				MemoryMinMiB:     128,
				MemoryAvgMiB:     192,
				MemoryMaxMiB:     256,
			},
		},
		Recommended: labv1alpha1.ContainerResourceValues{
			CPURequestMillicores: 300,
			CPULimitMillicores:   600,
			MemoryRequestMiB:     256,
			MemoryLimitMiB:       384,
		},
		Reason: "test recommendation",
	}})
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{"Resource Recommendation Report", "orders-abc", "test recommendation", "prod", "Pod recommendations", "300m / 600m", "256Mi / 384Mi", `class="recommended"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("html missing %q: %s", want, html)
		}
	}
	if strings.Contains(html, "Namespace summary") {
		t.Fatalf("html still contains removed namespace summary: %s", html)
	}
	if strings.Contains(html, "ZgotmplZ") {
		t.Fatalf("html contains blocked template content: %s", html)
	}
}
