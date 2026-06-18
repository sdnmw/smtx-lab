package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/smtx-lab/smtx-lab-operator/internal/agent"
)

func main() {
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		nodeName, _ = os.Hostname()
	}

	collector := agent.Collector{
		NodeName:       nodeName,
		CNIConfigDirs:  []string{"/host/etc/cni/net.d", "/etc/cni/net.d"},
		ProcRoot:       firstNonEmpty(os.Getenv("HOST_PROC"), "/host/proc"),
		CommandTimeout: 5 * time.Second,
		MaxOutputBytes: 512 * 1024,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/snapshot", func(w http.ResponseWriter, r *http.Request) {
		req := agent.SnapshotRequest{
			CollectCNI:       true,
			CollectIptables:  true,
			CollectIPVS:      true,
			CollectConntrack: true,
			ChainAllowlist:   []string{"KUBE-*", "CALI-*", "CILIUM_*", "EVEROUTE-*", "FLANNEL-*"},
			ConntrackFilter: agent.ConntrackFilter{
				Protocols:  []string{"tcp", "udp"},
				MaxEntries: 2000,
			},
		}
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		} else if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		snapshot := collector.Collect(ctx, req)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(snapshot); err != nil && !errors.Is(err, context.Canceled) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	addr := firstNonEmpty(os.Getenv("BIND_ADDRESS"), ":8080")
	if err := http.ListenAndServe(addr, mux); err != nil {
		panic(err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
