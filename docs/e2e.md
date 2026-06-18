# Kind E2E Verification

This repository includes a `kind` smoke test that exercises the main operator
paths on a two-worker local cluster:

- Builds manager/probe and node-agent images.
- Deploys CRDs, RBAC, manager, and node-agent.
- Schedules two nginx pods on different nodes.
- Runs `NetworkProbeLab` through per-node probe Jobs.
- Runs `ResourceAnalyzerLab` with auto-deployed Prometheus, kube-state-metrics,
  and Grafana.
- Saves CR status and report ConfigMaps under `e2e-artifacts/`.

## Prerequisites

Install:

- `kind`
- `docker`
- `kubectl`

`go` is only required when `RUN_UNIT_TESTS=1` is set. The image builds use the
Go toolchain from the Dockerfiles.

## Run

```bash
make e2e-kind
```

Useful environment overrides:

```bash
KEEP_CLUSTER=1 make e2e-kind
CLUSTER_NAME=my-lab IMG=local/smtx-lab-operator:e2e AGENT_IMG=local/smtx-lab-agent:e2e make e2e-kind
RUN_UNIT_TESTS=1 make e2e-kind
```

When `KEEP_CLUSTER=1`, inspect the cluster manually:

```bash
kubectl get networkprobelab e2e-network -o yaml
kubectl get resourceanalyzerlab e2e-resource -o yaml
kubectl -n smtx-lab-system get pods,jobs,configmaps
```

Without `KEEP_CLUSTER=1`, the script deletes the kind cluster on exit.
