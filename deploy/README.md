# smtx-lab-operator Deploy Manifests

This directory contains ready-to-apply YAML files for a quick cluster test.

## 1. Install Operator

```bash
kubectl apply -f deploy/install.yaml
kubectl -n smtx-lab-system rollout status deployment/smtx-lab-operator --timeout=180s
kubectl -n smtx-lab-system rollout status daemonset/smtx-lab-agent --timeout=180s
```

`install.yaml` includes:

- `NetworkProbeLab` and `ResourceAnalyzerLab` CRDs
- `smtx-lab-system` namespace
- manager and node-agent service accounts
- RBAC
- operator Deployment
- node-agent DaemonSet

## 2. Create Test Workload

```bash
kubectl apply -f deploy/samples/nginx-test-workload.yaml
kubectl -n test get pods -o wide
kubectl -n test get svc nginx
```

The workload creates two nginx Pods and one ClusterIP Service.

## 3. Run Network Probe

```bash
kubectl apply -f deploy/cr/networkprobelab-cross-node-calico.yaml
kubectl get npl cross-node-network-lab -o yaml
```

Expected result:

- `status.phase: Succeeded`
- `summary.totalTests: 12`
- `summary.succeeded: 12`
- `summary.cniDetected: ["calico"]`
- `summary.calicoOverlayModes` shows `ipip`, `vxlan`, or another detected Calico mode
- Excel and HTML reports are written to ConfigMaps

## 4. Run Resource Analyzer

```bash
kubectl apply -f deploy/cr/resourceanalyzerlab-14d-all-namespaces.yaml
kubectl get ral resource-14d-analysis -o yaml
```

Expected result:

- operator auto-deploys Prometheus, kube-state-metrics, and Grafana
- `status.phase: Succeeded`
- `status.summary.analyzedContainers` is greater than zero after Prometheus has samples
- `status.recommendations` contains current, observed, usage, and recommended resources
- Excel and HTML reports are written to ConfigMaps

## 5. Export Reports

```bash
kubectl -n smtx-lab-system get cm network-probe-lab-html-report \
  -o jsonpath='{.binaryData.network-probe-results\.html}' | base64 -d > network-probe-results.html

kubectl -n smtx-lab-system get cm resource-analyzer-html-report \
  -o jsonpath='{.binaryData.resource-recommendations\.html}' | base64 -d > resource-recommendations.html
```

