# smtx-lab-operator

`smtx-lab-operator` is a Kubernetes operator for lab-grade network validation,
datapath observability, 14-day resource analysis, optimization recommendation,
and Excel reporting.

## Current Implementation Scope

This repository contains the first implementation slice:

- `NetworkProbeLab` and `ResourceAnalyzerLab` API types.
- CRD manifests and sample custom resources.
- Kubebuilder-style manager entrypoint.
- `ResourceAnalyzerLab` reconciliation from Prometheus range queries to
  recommendations, status, and optional Excel ConfigMap artifact.
- `NetworkProbeLab` reconciliation that creates per-source-node probe Jobs,
  parses probe reports, correlates node-agent snapshots, updates status, and
  optionally writes an Excel ConfigMap artifact.
- Probe Job lifecycle safety with CR generation/check-list hash annotations,
  stale Job recreation, and bounded active deadlines.
- Optional raw node-agent snapshot retention as gzipped per-node ConfigMaps
  referenced from `status.artifacts.rawSnapshotConfigMapNames`.
- Optional monitoring stack auto-deploy for Prometheus, kube-state-metrics, and
  Grafana when `ResourceAnalyzerLab.spec.monitoringStack.autoDeploy` is enabled.
- Finalizer-based cleanup for probe Jobs and generated report/snapshot
  ConfigMaps when lab CRs are deleted.
- `/probe` workload binary for TCP, UDP, HTTP, and HTTPS checks from the
  scheduled source node.
- Node agent HTTP service for read-only datapath snapshots.
- Prometheus metrics client, resource analyzer, and Excel exporter modules.
- RBAC, manager deployment, and node-agent DaemonSet manifests.

## Architecture

```text
NetworkProbeLab / ResourceAnalyzerLab
        |
        v
Kubernetes API Server
        |
        v
smtx-lab-operator manager
        |
        +--> NetworkProbeLab controller
        |       +--> per-node probe Jobs
        |       +--> node-agent snapshot correlation
        |
        +--> ResourceAnalyzerLab controller
                +--> Prometheus query_range
                +--> recommendation engine
                +--> Excel exporter

smtx-lab-agent DaemonSet
        |
        +--> CNI detection
        +--> iptables/ip6tables snapshot
        +--> IPVS statistics
        +--> conntrack filtering
```

## Local Commands

```bash
make fmt
make test
make build
make e2e-kind
```

The current container does not have Go installed on `PATH`; install or provide
Go 1.22+ before running the commands locally.

For environments where Go is not globally installed, the implementation was
verified with a temporary Go 1.22.12 toolchain and:

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build ./cmd/manager ./cmd/agent ./cmd/probe
kubectl kustomize config/default
```

See [docs/e2e.md](docs/e2e.md) for the local `kind` smoke test workflow.

For the detailed implementation flow, CR status fields, network/resource
calculation formulas, and Calico validation evidence, see
[docs/cr-implementation-flow-and-validation.md](docs/cr-implementation-flow-and-validation.md).
