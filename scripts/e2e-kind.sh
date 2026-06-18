#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER_NAME="${CLUSTER_NAME:-smtx-lab-e2e}"
IMG="${IMG:-smtx/smtx-lab-operator:e2e}"
AGENT_IMG="${AGENT_IMG:-smtx/smtx-lab-agent:e2e}"
ARTIFACT_DIR="${ARTIFACT_DIR:-${ROOT_DIR}/e2e-artifacts}"
KEEP_CLUSTER="${KEEP_CLUSTER:-0}"
RUN_UNIT_TESTS="${RUN_UNIT_TESTS:-0}"

log() {
  printf '[e2e] %s\n' "$*"
}

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$1" >&2
    exit 127
  fi
}

cleanup() {
  if [[ "${KEEP_CLUSTER}" != "1" ]]; then
    kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
  fi
}

wait_phase() {
  local kind="$1"
  local name="$2"
  local expected="$3"
  local timeout_seconds="$4"
  local start
  start="$(date +%s)"
  while true; do
    local phase
    phase="$(kubectl get "${kind}" "${name}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    if [[ "${phase}" == "${expected}" ]]; then
      return 0
    fi
    if [[ "${phase}" == "Failed" && "${expected}" != "Failed" ]]; then
      kubectl get "${kind}" "${name}" -o yaml >&2 || true
      return 1
    fi
    if (( "$(date +%s)" - start > timeout_seconds )); then
      printf 'timed out waiting for %s/%s phase %s, current phase: %s\n' "${kind}" "${name}" "${expected}" "${phase}" >&2
      kubectl get "${kind}" "${name}" -o yaml >&2 || true
      return 1
    fi
    sleep 5
  done
}

need kind
need docker
need kubectl

if [[ "${RUN_UNIT_TESTS}" == "1" ]]; then
  need go
fi

mkdir -p "${ARTIFACT_DIR}"

if [[ "${KEEP_CLUSTER}" != "1" ]]; then
  trap cleanup EXIT
fi

cd "${ROOT_DIR}"

if [[ "${RUN_UNIT_TESTS}" == "1" ]]; then
  log "running unit tests"
  CGO_ENABLED=0 go test ./...
fi

log "building images"
docker build -t "${IMG}" -f Dockerfile .
docker build -t "${AGENT_IMG}" -f Dockerfile.agent .

if kind get clusters | grep -qx "${CLUSTER_NAME}"; then
  log "deleting existing kind cluster ${CLUSTER_NAME}"
  kind delete cluster --name "${CLUSTER_NAME}"
fi

log "creating kind cluster ${CLUSTER_NAME}"
cat <<YAML | kind create cluster --name "${CLUSTER_NAME}" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
  - role: worker
YAML

log "loading images into kind"
kind load docker-image "${IMG}" --name "${CLUSTER_NAME}"
kind load docker-image "${AGENT_IMG}" --name "${CLUSTER_NAME}"

log "rendering deployment with e2e image tags"
kubectl kustomize config/default \
  | sed "s#smtx/smtx-lab-operator:dev#${IMG}#g; s#smtx/smtx-lab-agent:dev#${AGENT_IMG}#g" \
  | kubectl apply -f -

log "waiting for CRDs and controllers"
kubectl wait --for=condition=Established crd/networkprobelabs.lab.smtx.io --timeout=90s
kubectl wait --for=condition=Established crd/resourceanalyzerlabs.lab.smtx.io --timeout=90s
kubectl -n smtx-lab-system rollout status deployment/smtx-lab-operator --timeout=180s
kubectl -n smtx-lab-system rollout status daemonset/smtx-lab-agent --timeout=180s

mapfile -t WORKER_NODES < <(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')
if (( "${#WORKER_NODES[@]}" < 2 )); then
  printf 'expected at least two worker nodes, got %d\n' "${#WORKER_NODES[@]}" >&2
  exit 1
fi
NODE_A="${WORKER_NODES[0]}"
NODE_B="${WORKER_NODES[1]}"

log "deploying cross-node nginx targets on ${NODE_A} and ${NODE_B}"
cat <<YAML | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: smtx-e2e-nginx-a
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx
      smtx-lab-e2e-node: a
  template:
    metadata:
      labels:
        app: nginx
        team: platform
        smtx-lab-e2e-node: a
    spec:
      nodeName: ${NODE_A}
      containers:
        - name: nginx
          image: nginx:1.27-alpine
          ports:
            - containerPort: 80
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 200m
              memory: 128Mi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: smtx-e2e-nginx-b
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx
      smtx-lab-e2e-node: b
  template:
    metadata:
      labels:
        app: nginx
        team: platform
        smtx-lab-e2e-node: b
    spec:
      nodeName: ${NODE_B}
      containers:
        - name: nginx
          image: nginx:1.27-alpine
          ports:
            - containerPort: 80
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 200m
              memory: 128Mi
---
apiVersion: v1
kind: Service
metadata:
  name: smtx-e2e-nginx
  namespace: default
  labels:
    app: nginx
spec:
  selector:
    app: nginx
  ports:
    - name: http
      port: 80
      targetPort: 80
YAML

kubectl -n default rollout status deployment/smtx-e2e-nginx-a --timeout=120s
kubectl -n default rollout status deployment/smtx-e2e-nginx-b --timeout=120s

log "applying NetworkProbeLab"
cat <<YAML | kubectl apply -f -
apiVersion: lab.smtx.io/v1alpha1
kind: NetworkProbeLab
metadata:
  name: e2e-network
spec:
  target:
    namespaces: ["default"]
    podSelector:
      matchLabels:
        app: nginx
    serviceSelector:
      matchLabels:
        app: nginx
  traffic:
    protocols: ["TCP", "HTTP"]
    ports: [80]
    count: 1
    timeoutSeconds: 3
    crossNodeOnly: true
    includeServiceVIP: true
    includePodIP: true
    includeDNS: true
  observability:
    collectCNI: true
    collectIptables: true
    collectIPVS: true
    collectConntrack: true
    chainAllowlist: ["KUBE-*", "CNI-*"]
    conntrackFilter:
      protocols: ["tcp"]
      maxEntries: 500
  output:
    excel:
      enabled: true
      configMapName: e2e-network-report
    retainRawSnapshots: true
YAML

log "waiting for NetworkProbeLab success"
wait_phase networkprobelab e2e-network Succeeded 240

log "applying ResourceAnalyzerLab with autoDeploy monitoring"
cat <<YAML | kubectl apply -f -
apiVersion: lab.smtx.io/v1alpha1
kind: ResourceAnalyzerLab
metadata:
  name: e2e-resource
spec:
  target:
    namespaces: ["default"]
    workloadKinds: ["Deployment", "Pod"]
    labelSelector:
      matchLabels:
        team: platform
    excludeNamespaces: ["kube-system"]
  metrics:
    lookbackDays: 1
    step: 5m
    timeoutSeconds: 10
  recommendation:
    cpu:
      requestPercentile: p95
      limitPercentile: p99
      requestHeadroomRatio: 1.2
      limitHeadroomRatio: 1.5
      minRequestMillicores: 50
    memory:
      requestPercentile: p95
      limitPercentile: p99
      requestHeadroomRatio: 1.15
      limitHeadroomRatio: 1.3
      minRequestMiB: 64
    languageHints:
      default: Go
  monitoringStack:
    autoDeploy: true
    prometheus:
      enabled: true
    grafana:
      enabled: true
  output:
    excel:
      enabled: true
      configMapName: e2e-resource-report
YAML

log "waiting for ResourceAnalyzerLab success"
wait_phase resourceanalyzerlab e2e-resource Succeeded 420

log "collecting e2e artifacts into ${ARTIFACT_DIR}"
kubectl get networkprobelab e2e-network -o yaml >"${ARTIFACT_DIR}/networkprobelab.yaml"
kubectl get resourceanalyzerlab e2e-resource -o yaml >"${ARTIFACT_DIR}/resourceanalyzerlab.yaml"
kubectl -n smtx-lab-system get configmap e2e-network-report -o yaml >"${ARTIFACT_DIR}/network-report-configmap.yaml"
kubectl -n smtx-lab-system get configmap e2e-resource-report -o yaml >"${ARTIFACT_DIR}/resource-report-configmap.yaml"
kubectl -n smtx-lab-system get pods,jobs,deployments,daemonsets,services >"${ARTIFACT_DIR}/smtx-lab-system.txt"

log "e2e completed successfully"
if [[ "${KEEP_CLUSTER}" == "1" ]]; then
  log "cluster kept: ${CLUSTER_NAME}"
fi
