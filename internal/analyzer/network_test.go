package analyzer

import (
	"testing"

	"github.com/smtx-lab/smtx-lab-operator/internal/agent"
)

func TestTraceServiceAndCrossNodeCalicoPath(t *testing.T) {
	source := agent.Snapshot{
		Iptables: agent.CommandSnapshot{Captured: true, Lines: []string{
			"*raw",
			":cali-PREROUTING - [0:0]",
			"COMMIT",
			"*filter",
			":KUBE-FORWARD - [0:0]",
			":cali-FORWARD - [0:0]",
			":cali-from-wl-dispatch - [0:0]",
			":cali-fw-cali123 - [0:0]",
			"-A cali-FORWARD -i cali+ -j cali-from-wl-dispatch",
			"-A cali-from-wl-dispatch -i cali123 -g cali-fw-cali123",
			"COMMIT",
			"*nat",
			":KUBE-SERVICES - [0:0]",
			":KUBE-SVC-NGINX - [0:0]",
			":KUBE-SEP-REMOTE - [0:0]",
			":KUBE-SVC-OTHER - [0:0]",
			":KUBE-SEP-OTHER - [0:0]",
			":cali-POSTROUTING - [0:0]",
			"-A KUBE-SERVICES -d 10.96.0.20/32 -p tcp --dport 80 -j KUBE-SVC-NGINX",
			"-A KUBE-SERVICES -d 10.96.0.30/32 -p tcp --dport 80 -j KUBE-SVC-OTHER",
			"-A KUBE-SVC-NGINX -m comment --comment \"test/nginx -> 172.31.2.10:80\" -j KUBE-SEP-REMOTE",
			"-A KUBE-SVC-OTHER -j KUBE-SEP-OTHER",
			"-A KUBE-SEP-REMOTE -p tcp -j DNAT --to-destination 172.31.2.10:80",
			"-A KUBE-SEP-OTHER -p tcp -j DNAT --to-destination 172.31.9.10:80",
			"COMMIT",
		}},
		Routes: agent.CommandSnapshot{Captured: true, Lines: []string{
			"172.31.1.10 dev cali123 scope link",
		}},
	}
	target := agent.Snapshot{
		Iptables: agent.CommandSnapshot{Captured: true, Lines: []string{
			"*raw",
			":cali-PREROUTING - [0:0]",
			"COMMIT",
			"*filter",
			":KUBE-FORWARD - [0:0]",
			":cali-FORWARD - [0:0]",
			":cali-to-wl-dispatch - [0:0]",
			":cali-tw-cali456 - [0:0]",
			"-A cali-FORWARD -o cali+ -j cali-to-wl-dispatch",
			"-A cali-to-wl-dispatch -o cali456 -g cali-tw-cali456",
			"COMMIT",
		}},
		Routes: agent.CommandSnapshot{Captured: true, Lines: []string{
			"172.31.2.10 dev cali456 scope link",
		}},
	}
	traffic := TrafficObservation{Protocol: "HTTP", SourceIP: "172.31.1.10", TargetIP: "172.31.2.10", ServiceIP: "10.96.0.20", Port: 80}

	sourceSteps := TraceSourceIptables(source, traffic)
	targetSteps := TraceTargetIptables(target, traffic)
	assertTraceChains(t, sourceSteps, []string{"cali-PREROUTING", "KUBE-SERVICES", "KUBE-SVC-NGINX", "KUBE-SEP-REMOTE", "cali-FORWARD", "cali-from-wl-dispatch", "cali-fw-cali123", "KUBE-FORWARD", "cali-POSTROUTING"})
	assertTraceChains(t, targetSteps, []string{"cali-PREROUTING", "cali-FORWARD", "cali-to-wl-dispatch", "cali-tw-cali456", "KUBE-FORWARD"})
	for _, step := range sourceSteps {
		if step.Chain == "KUBE-SVC-OTHER" || step.Chain == "KUBE-SEP-OTHER" {
			t.Fatalf("unrelated service chain leaked into trace: %#v", sourceSteps)
		}
	}
	if got := sourceSteps[3].Action; got != "DNAT 172.31.2.10:80" {
		t.Fatalf("endpoint action = %q, want DNAT target", got)
	}
}

func assertTraceChains(t *testing.T, steps []IptablesTraceStep, want []string) {
	t.Helper()
	if len(steps) != len(want) {
		t.Fatalf("trace length = %d, want %d: %#v", len(steps), len(want), steps)
	}
	for idx := range want {
		if steps[idx].Chain != want[idx] {
			t.Fatalf("trace[%d] = %q, want %q: %#v", idx, steps[idx].Chain, want[idx], steps)
		}
	}
}
