# smtx-lab-operator Design

## Architecture Diagram

```text
NetworkProbeLab / ResourceAnalyzerLab
        |
        v
Kubernetes API Server
        |
        v
Controller manager
        |
        +--> NetworkProbeLab reconciler
        |       +--> probe pod/job orchestration
        |       +--> node-agent snapshot requests
        |       +--> datapath correlation
        |
        +--> ResourceAnalyzerLab reconciler
                +--> Prometheus query_range
                +--> percentile/resource recommendation engine
                +--> Excel export artifact

Node agent DaemonSet
        |
        +--> CNI detection from /etc/cni/net.d and host interfaces
        +--> bounded iptables-save snapshots
        +--> /proc/net/ip_vs statistics
        +--> filtered conntrack snapshots
```

## CRD Layer

`NetworkProbeLab` declares cross-node network tests, target pods/services,
datapath collection switches, node-agent settings, and Excel output settings.
Status carries `observedGeneration`, `phase`, Kubernetes-style conditions,
summary counts, per-node datapath facts, per-probe results, and artifact refs.

`ResourceAnalyzerLab` declares the workload selection, Prometheus source,
14-day lookback settings, recommendation policy, language hints, optional
monitoring-stack deployment, and Excel output. Status carries summarized
savings, per-container recommendations, conditions, and artifact refs.

## Controller Layer

The manager is kubebuilder-style and registers both APIs in the runtime scheme.
`ResourceAnalyzerLab` reconciliation now executes the first end-to-end path:
Prometheus `query_range`, percentile analysis, status updates, and optional
Excel artifact writes to a ConfigMap. When
`spec.monitoringStack.autoDeploy=true`, the controller creates lightweight
Prometheus, kube-state-metrics, and optional Grafana Deployments/Services in the
report namespace, waits for Prometheus availability, and defaults
`prometheusURL` to the internal service DNS name. `NetworkProbeLab` now executes
an asynchronous probe path: it selects target pods/services, creates one probe
Job per source node, parses probe reports from termination logs, collects
node-agent snapshots, correlates datapath evidence, updates status, and
optionally writes a network Excel artifact to a ConfigMap. Probe Jobs are
annotated with the CR generation and a hash of the source-node check list; if a
later reconcile sees stale metadata, it deletes the old Job and recreates it on
the next pass. Jobs also set an active deadline derived from check count, probe
count, and timeout to prevent stuck probe runs. When raw snapshot retention is
enabled, it also writes one gzipped snapshot ConfigMap per node and records the
names in `status.artifacts.rawSnapshotConfigMapNames`.

Both lab controllers use finalizers to clean generated artifacts before CR
deletion completes. `NetworkProbeLab` cleanup removes matching probe Jobs,
network reports, and raw snapshot ConfigMaps. `ResourceAnalyzerLab` cleanup
removes its generated Excel report ConfigMaps. Shared auto-deployed monitoring
components are intentionally retained because multiple analyzers can reuse them.

## Node Agent Layer

The node agent is a privileged, read-only DaemonSet. It exposes:

- `GET /healthz`
- `GET /snapshot`
- `POST /snapshot` with collection filters

The agent detects CNI by inspecting CNI config files and host network interface
names. It captures iptables with `iptables-save`, filters chain/rule output by
allowlist, captures IPVS from `/proc/net/ip_vs`, and filters conntrack output
by protocol and maximum entry count.

## Network Observability

CNI detection rules:

- Calico: `calico` CNI config, `cali*` interfaces, `CALI-*` chains.
- Cilium: `cilium` CNI config, `cilium_host`/`cilium_net`, `CILIUM_*` chains.
- Everoute: `everoute` config or interfaces, `EVEROUTE-*` chains.
- Flannel: `flannel` config, `flannel*` interfaces, `FLANNEL-*` chains.

Traffic correlation should use source pod IP, target pod IP, service VIP, port,
protocol, timestamp, and per-node before/after snapshots. The result explains
whether traffic used iptables, IPVS, CNI eBPF/Open vSwitch paths, and whether a
conntrack tuple matched.

The first probe implementation schedules `/probe` Jobs directly onto source
nodes via `spec.nodeName`. Each Job receives a JSON check list and performs TCP,
UDP, HTTP, or HTTPS probes to pod IPs, service ClusterIPs, and service DNS
names. The controller reads the Job pod termination message as structured JSON
and uses the source-node snapshot to populate datapath fields in status. Job
names are stable per lab and source node, while annotations carry the mutable
check hash and generation so changed targets or traffic settings trigger a fresh
run instead of reusing an old successful Job.
If `spec.output.retainRawSnapshots` is true, the full node-agent snapshot is
stored as `binaryData["snapshot.json.gz"]` in a per-node ConfigMap. If the
compressed payload exceeds the ConfigMap safety budget, the stored artifact is
truncated and annotated with `lab.smtx.io/truncated=true`.

## Resource Optimization

The Prometheus client uses `query_range` over the requested 14-day window.
Initial PromQL:

```promql
sum by (namespace,pod,container) (rate(container_cpu_usage_seconds_total{container!="",image!=""}[5m]))
container_memory_working_set_bytes{container!="",image!=""}
kube_pod_container_resource_requests{resource="cpu"}
kube_pod_container_resource_requests{resource="memory"}
kube_pod_container_resource_limits{resource="cpu"}
kube_pod_container_resource_limits{resource="memory"}
```

The analyzer computes p50, p95, and p99 from range samples. CPU recommendations
use p95 for requests and p99 for limits by default. Memory recommendations use
working-set p95 for requests and p99 for limits. Language hints adjust headroom:
Java keeps larger memory and CPU-limit headroom, Python keeps moderate extra
headroom, and Go uses tighter defaults.

The first implementation joins series by `namespace`, `pod`, and `container`.
It derives workload identity from Prometheus labels such as `workload_kind` and
`workload` when present, otherwise falls back to pod-level recommendations.
Current request and limit values are taken from the latest samples of the
`kube_pod_container_resource_*` series.

The auto-deployed Prometheus uses a 15-day retention window and scrapes
kubelet/cAdvisor plus the operator-managed `kube-state-metrics` service.
Prometheus availability gates analysis; kube-state-metrics is applied alongside
Prometheus so current request/limit series can be collected as soon as the
scrape target is ready.

## Excel Output

Resource reports include:

- `Summary`
- `Resource Recommendations`

When enabled, the controller stores the generated workbook in `binaryData` key
`resource-recommendations.xlsx` of the configured report ConfigMap. The report
namespace defaults to `POD_NAMESPACE`, then `smtx-lab-system`.

Network reports include:

- `Network Tests`
- `Datapath`

When enabled, the controller stores the generated workbook in `binaryData` key
`network-probe-results.xlsx` of the configured report ConfigMap.
Raw network snapshots are separate artifacts from the Excel workbook so large
iptables, IPVS, and conntrack evidence can be inspected independently.

Rows are shaped as namespace, workload, pod, and container for resource data,
and source pod/node to target pod/service/node for network data. Headers are
filterable and frozen for review.

## Component Breakdown

- CRDs and samples under `config/crd/bases` and `config/samples`.
- Controllers under `internal/controller`.
- Node agent under `cmd/agent` and `internal/agent`.
- Probe workload under `cmd/probe` and shared probe payload types under
  `internal/probe`.
- Metrics collector under `internal/metrics`.
- Resource and network analyzers under `internal/analyzer`.
- Excel exporter under `internal/exporter`.
- Deployment and RBAC manifests under `config`.
