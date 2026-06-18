package controller

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
	"github.com/smtx-lab/smtx-lab-operator/internal/agent"
	"github.com/smtx-lab/smtx-lab-operator/internal/analyzer"
	"github.com/smtx-lab/smtx-lab-operator/internal/exporter"
	"github.com/smtx-lab/smtx-lab-operator/internal/probe"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type AgentSnapshotFunc func(context.Context, corev1.Node, agent.SnapshotRequest) (agent.Snapshot, error)

// NetworkProbeLabReconciler reconciles NetworkProbeLab objects.
type NetworkProbeLabReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	HTTPClient      *http.Client
	ReportNamespace string
	ProbeImage      string
	AgentPort       int32
	SnapshotFunc    AgentSnapshotFunc
}

// +kubebuilder:rbac:groups=lab.smtx.io,resources=networkprobelabs,verbs=get;list;watch
// +kubebuilder:rbac:groups=lab.smtx.io,resources=networkprobelabs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=lab.smtx.io,resources=networkprobelabs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods;services;nodes;configmaps;events,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
func (r *NetworkProbeLabReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("networkProbeLab", req.NamespacedName)

	var lab labv1alpha1.NetworkProbeLab
	if err := r.Get(ctx, req.NamespacedName, &lab); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if lab.GetDeletionTimestamp() != nil {
		if hasFinalizer(&lab, networkProbeLabFinalizer) {
			if err := r.cleanupNetworkProbeArtifacts(ctx, &lab); err != nil {
				return ctrl.Result{}, err
			}
			removeFinalizer(&lab, networkProbeLabFinalizer)
			return ctrl.Result{}, r.Update(ctx, &lab)
		}
		return ctrl.Result{}, nil
	}
	if addFinalizer(&lab, networkProbeLabFinalizer) {
		return ctrl.Result{}, r.Update(ctx, &lab)
	}

	next := lab.Status
	next.ObservedGeneration = lab.Generation
	next.Artifacts.ExcelConfigMapName = excelName(lab.Spec.Output.Excel.ConfigMapName, lab.Name, "network")
	next.Artifacts.HTMLConfigMapName = htmlName(lab.Spec.Output.HTML.ConfigMapName, lab.Name, "network")

	setCondition(&next.Conditions, lab.Generation, "SpecAccepted", metav1.ConditionTrue, "SpecAccepted", "NetworkProbeLab spec is accepted.")
	run, err := r.runNetworkProbe(ctx, &lab)
	requeue := false
	if err != nil {
		next.Phase = labv1alpha1.LabPhaseFailed
		next.Summary = run.Summary
		next.NodeResults = run.NodeResults
		next.ProbeResults = run.ProbeResults
		next.Artifacts.RawSnapshotConfigMapNames = run.RawSnapshotConfigMapNames
		setCondition(&next.Conditions, lab.Generation, "AgentReady", metav1.ConditionUnknown, "Skipped", "Agent snapshot collection did not complete.")
		setCondition(&next.Conditions, lab.Generation, "ProbeCompleted", metav1.ConditionFalse, "Failed", err.Error())
	} else {
		next.Summary = run.Summary
		next.NodeResults = run.NodeResults
		next.ProbeResults = run.ProbeResults
		next.Artifacts.RawSnapshotConfigMapNames = run.RawSnapshotConfigMapNames
		if run.Pending {
			next.Phase = labv1alpha1.LabPhaseRunning
			requeue = true
			setCondition(&next.Conditions, lab.Generation, "AgentReady", metav1.ConditionUnknown, "PendingProbeJobs", "Agent snapshots will be collected after probe jobs complete.")
			setCondition(&next.Conditions, lab.Generation, "ProbeCompleted", metav1.ConditionFalse, "ProbeJobsRunning", "Probe jobs are running or waiting to be scheduled.")
		} else {
			if run.Summary.Failed > 0 {
				next.Phase = labv1alpha1.LabPhaseFailed
			} else {
				next.Phase = labv1alpha1.LabPhaseSucceeded
			}
			agentStatus := metav1.ConditionTrue
			agentReason := "SnapshotsCollected"
			agentMessage := "Node-agent snapshots were collected."
			if len(run.NodeResults) == 0 {
				agentStatus = metav1.ConditionFalse
				agentReason = "NoSnapshots"
				agentMessage = "No node-agent snapshots were collected."
			}
			setCondition(&next.Conditions, lab.Generation, "AgentReady", agentStatus, agentReason, agentMessage)
			setCondition(&next.Conditions, lab.Generation, "ProbeCompleted", metav1.ConditionTrue, "Completed", "Probe jobs completed and results were collected.")
			if lab.Spec.Output.Excel.Enabled {
				setCondition(&next.Conditions, lab.Generation, "ExcelExported", metav1.ConditionTrue, "ConfigMapWritten", "Excel report was written to ConfigMap "+next.Artifacts.ExcelConfigMapName+".")
			}
			if lab.Spec.Output.HTML.Enabled {
				setCondition(&next.Conditions, lab.Generation, "HTMLExported", metav1.ConditionTrue, "ConfigMapWritten", "HTML report was written to ConfigMap "+next.Artifacts.HTMLConfigMapName+".")
			}
			if lab.Spec.Output.RetainRawSnapshots {
				if len(run.RawSnapshotConfigMapNames) > 0 {
					setCondition(&next.Conditions, lab.Generation, "RawSnapshotsExported", metav1.ConditionTrue, "ConfigMapsWritten", "Raw node-agent snapshots were written to ConfigMaps.")
				} else {
					setCondition(&next.Conditions, lab.Generation, "RawSnapshotsExported", metav1.ConditionFalse, "NoSnapshots", "No raw node-agent snapshots were available to write.")
				}
			}
		}
	}

	if reflect.DeepEqual(lab.Status, next) {
		if requeue {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		return ctrl.Result{}, nil
	}

	lab.Status = next
	if err := r.Status().Update(ctx, &lab); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("updated NetworkProbeLab status")
	if requeue {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

func (r *NetworkProbeLabReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&labv1alpha1.NetworkProbeLab{}).
		Complete(r)
}

type networkRunResult struct {
	Pending                   bool
	Summary                   labv1alpha1.NetworkProbeSummary
	NodeResults               []labv1alpha1.NetworkNodeResult
	ProbeResults              []labv1alpha1.NetworkProbeResult
	RawSnapshotConfigMapNames []string
}

func (r *NetworkProbeLabReconciler) runNetworkProbe(ctx context.Context, lab *labv1alpha1.NetworkProbeLab) (networkRunResult, error) {
	pods, err := r.listProbePods(ctx, lab.Spec.Target)
	if err != nil {
		return networkRunResult{}, err
	}
	services, err := r.listProbeServices(ctx, lab.Spec.Target)
	if err != nil {
		return networkRunResult{}, err
	}
	if len(pods) == 0 {
		return networkRunResult{}, fmt.Errorf("no running target pods matched NetworkProbeLab target")
	}

	checks := buildProbeChecks(lab, pods, services)
	if len(checks) == 0 {
		return networkRunResult{}, fmt.Errorf("no probe checks could be built from matched pods/services")
	}

	result := networkRunResult{}
	result.Summary.TotalTests = int32(len(checks))
	reports, pending, err := r.ensureAndReadProbeJobs(ctx, lab, checks)
	if err != nil {
		result.Pending = pending
		return result, err
	}
	if pending {
		result.Pending = true
		return result, nil
	}

	nodes, err := r.listObservationNodes(ctx, lab.Spec.Target.NodeSelector, checks)
	if err != nil {
		return result, err
	}
	nodeIPs := nodeIPMap(nodes)
	snapshots := r.collectSnapshots(ctx, nodes, lab.Spec.Observability)
	snapshotRefs := map[string]string{}
	if lab.Spec.Output.RetainRawSnapshots {
		snapshotRefs, result.RawSnapshotConfigMapNames, err = r.writeRawSnapshotConfigMaps(ctx, lab, snapshots)
		if err != nil {
			return result, err
		}
	}
	for _, snapshot := range snapshots {
		result.NodeResults = append(result.NodeResults, snapshotToNodeResult(snapshot, snapshotRefs[snapshot.NodeName]))
	}
	sort.Slice(result.NodeResults, func(i, j int) bool {
		return result.NodeResults[i].NodeName < result.NodeResults[j].NodeName
	})

	snapshotByNode := map[string]agent.Snapshot{}
	for _, snapshot := range snapshots {
		snapshotByNode[snapshot.NodeName] = snapshot
	}
	for _, report := range reports {
		for _, item := range report.Results {
			probeResult := probeResultFromReport(lab.Name, item, nodeIPs)
			if snapshot, ok := snapshotByNode[item.SourceNode]; ok {
				correlation := analyzer.CorrelateDatapath(snapshot, analyzer.TrafficObservation{
					Protocol:  item.Protocol,
					SourceIP:  item.SourceIP,
					TargetIP:  item.TargetIP,
					ServiceIP: item.ServiceIP,
					Port:      item.Port,
				})
				probeResult.Datapath = labv1alpha1.DatapathSummary{
					CNI:               correlation.CNI,
					CalicoOverlayMode: correlation.CalicoOverlayMode,
					KubeProxyMode:     correlation.KubeProxyMode,
					RelevantChains:    correlation.RelevantChains,
					PodForwardChains:  correlation.PodForwardChains,
					ServiceChains:     correlation.ServiceChains,
					ConntrackMatched:  correlation.ConntrackMatched,
				}
			}
			result.ProbeResults = append(result.ProbeResults, probeResult)
			if probeResult.Success {
				result.Summary.Succeeded++
			} else {
				result.Summary.Failed++
			}
		}
	}
	result.Summary.CNIDetected = uniqueCNI(result.NodeResults)
	result.Summary.DatapathModes = uniqueDatapathModes(result.ProbeResults)
	result.Summary.CalicoOverlayModes = uniqueCalicoOverlayModes(result.NodeResults)
	if lab.Spec.Output.Excel.Enabled {
		var buf bytes.Buffer
		if err := exporter.WriteNetworkResults(&buf, result.ProbeResults, result.NodeResults); err != nil {
			return result, err
		}
		if err := r.writeNetworkExcelConfigMap(ctx, lab, buf.Bytes()); err != nil {
			return result, err
		}
	}
	if lab.Spec.Output.HTML.Enabled {
		var buf bytes.Buffer
		if err := exporter.WriteNetworkHTML(&buf, lab.Name, result.Summary, result.ProbeResults, result.NodeResults); err != nil {
			return result, err
		}
		if err := r.writeNetworkHTMLConfigMap(ctx, lab, buf.Bytes()); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (r *NetworkProbeLabReconciler) listProbePods(ctx context.Context, target labv1alpha1.NetworkProbeTargetSpec) ([]corev1.Pod, error) {
	selector, err := selectorFromSpec(target.PodSelector)
	if err != nil {
		return nil, err
	}
	allowedNodes, err := r.allowedNodeNames(ctx, target.NodeSelector)
	if err != nil {
		return nil, err
	}
	var list corev1.PodList
	opts := []client.ListOption{client.MatchingLabelsSelector{Selector: selector}}
	if len(target.Namespaces) == 0 {
		if err := r.List(ctx, &list, opts...); err != nil {
			return nil, err
		}
		return filterRunnablePods(list.Items, allowedNodes), nil
	}
	var out []corev1.Pod
	for _, namespace := range target.Namespaces {
		var namespaced corev1.PodList
		nsOpts := append([]client.ListOption{client.InNamespace(namespace)}, opts...)
		if err := r.List(ctx, &namespaced, nsOpts...); err != nil {
			return nil, err
		}
		out = append(out, filterRunnablePods(namespaced.Items, allowedNodes)...)
	}
	return out, nil
}

func (r *NetworkProbeLabReconciler) listProbeServices(ctx context.Context, target labv1alpha1.NetworkProbeTargetSpec) ([]corev1.Service, error) {
	selector, err := selectorFromSpec(target.ServiceSelector)
	if err != nil {
		return nil, err
	}
	var list corev1.ServiceList
	opts := []client.ListOption{client.MatchingLabelsSelector{Selector: selector}}
	if len(target.Namespaces) == 0 {
		if err := r.List(ctx, &list, opts...); err != nil {
			return nil, err
		}
		return list.Items, nil
	}
	var out []corev1.Service
	for _, namespace := range target.Namespaces {
		var namespaced corev1.ServiceList
		nsOpts := append([]client.ListOption{client.InNamespace(namespace)}, opts...)
		if err := r.List(ctx, &namespaced, nsOpts...); err != nil {
			return nil, err
		}
		out = append(out, namespaced.Items...)
	}
	return out, nil
}

func selectorFromSpec(spec labv1alpha1.LabelSelectorSpec) (labels.Selector, error) {
	return metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels:      spec.MatchLabels,
		MatchExpressions: spec.MatchExpressions,
	})
}

func (r *NetworkProbeLabReconciler) allowedNodeNames(ctx context.Context, nodeSelector map[string]string) (map[string]struct{}, error) {
	if len(nodeSelector) == 0 {
		return nil, nil
	}
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes, client.MatchingLabelsSelector{Selector: labels.SelectorFromSet(nodeSelector)}); err != nil {
		return nil, err
	}
	allowed := map[string]struct{}{}
	for _, node := range nodes.Items {
		allowed[node.Name] = struct{}{}
	}
	return allowed, nil
}

func filterRunnablePods(pods []corev1.Pod, allowedNodes map[string]struct{}) []corev1.Pod {
	out := make([]corev1.Pod, 0, len(pods))
	for _, pod := range pods {
		if pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" || pod.Spec.NodeName == "" {
			continue
		}
		if allowedNodes != nil {
			if _, ok := allowedNodes[pod.Spec.NodeName]; !ok {
				continue
			}
		}
		out = append(out, pod)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Namespace+"/"+out[i].Name < out[j].Namespace+"/"+out[j].Name
	})
	return out
}

func buildProbeChecks(lab *labv1alpha1.NetworkProbeLab, pods []corev1.Pod, services []corev1.Service) []probe.Check {
	protocols := defaultProtocols(lab.Spec.Traffic.Protocols)
	ports := defaultProbePorts(lab.Spec.Traffic.Ports, pods, services)
	sourceNodes := uniquePodNodes(pods)
	crossNodeOnly := lab.Spec.Traffic.CrossNodeOnly
	includePodIP := lab.Spec.Traffic.IncludePodIP
	includeServiceVIP := lab.Spec.Traffic.IncludeServiceVIP
	includeDNS := lab.Spec.Traffic.IncludeDNS
	if !includePodIP && !includeServiceVIP && !includeDNS {
		includePodIP = true
	}

	var checks []probe.Check
	for _, sourceNode := range sourceNodes {
		if includePodIP {
			for _, target := range pods {
				if crossNodeOnly && target.Spec.NodeName == sourceNode {
					continue
				}
				for _, protocol := range protocols {
					for _, port := range ports {
						checks = append(checks, probe.Check{
							ID:         checkID(sourceNode, target.Namespace, target.Name, protocol, port, "podIP"),
							SourceNode: sourceNode,
							TargetPod:  target.Namespace + "/" + target.Name,
							TargetNode: target.Spec.NodeName,
							Protocol:   protocol,
							Address:    target.Status.PodIP,
							Port:       port,
							Path:       "podIP",
							TargetIP:   target.Status.PodIP,
						})
					}
				}
			}
		}
		for _, svc := range services {
			if includeServiceVIP && svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != corev1.ClusterIPNone {
				for _, protocol := range protocols {
					for _, port := range serviceOrDefaultPorts(svc, ports) {
						checks = append(checks, probe.Check{
							ID:            checkID(sourceNode, svc.Namespace, svc.Name, protocol, port, "serviceVIP"),
							SourceNode:    sourceNode,
							TargetService: svc.Namespace + "/" + svc.Name,
							Protocol:      protocol,
							Address:       svc.Spec.ClusterIP,
							Port:          port,
							Path:          "serviceVIP",
							ServiceIP:     svc.Spec.ClusterIP,
						})
					}
				}
			}
			if includeDNS {
				dnsName := svc.Name + "." + svc.Namespace + ".svc"
				for _, protocol := range protocols {
					for _, port := range serviceOrDefaultPorts(svc, ports) {
						checks = append(checks, probe.Check{
							ID:            checkID(sourceNode, svc.Namespace, svc.Name, protocol, port, "dns"),
							SourceNode:    sourceNode,
							TargetService: svc.Namespace + "/" + svc.Name,
							Protocol:      protocol,
							Address:       dnsName,
							Port:          port,
							Path:          "dns",
							ServiceIP:     svc.Spec.ClusterIP,
						})
					}
				}
			}
		}
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].ID < checks[j].ID })
	return checks
}

func defaultProtocols(protocols []string) []string {
	if len(protocols) == 0 {
		return []string{"TCP"}
	}
	out := append([]string(nil), protocols...)
	for i := range out {
		out[i] = strings.ToUpper(out[i])
	}
	sort.Strings(out)
	return out
}

func defaultProbePorts(configured []int32, pods []corev1.Pod, services []corev1.Service) []int32 {
	ports := map[int32]struct{}{}
	for _, port := range configured {
		if port > 0 {
			ports[port] = struct{}{}
		}
	}
	if len(ports) == 0 {
		for _, svc := range services {
			for _, port := range svc.Spec.Ports {
				if port.Port > 0 {
					ports[port.Port] = struct{}{}
				}
			}
		}
	}
	if len(ports) == 0 {
		for _, pod := range pods {
			for _, container := range pod.Spec.Containers {
				for _, port := range container.Ports {
					if port.ContainerPort > 0 {
						ports[port.ContainerPort] = struct{}{}
					}
				}
			}
		}
	}
	if len(ports) == 0 {
		ports[80] = struct{}{}
	}
	out := make([]int32, 0, len(ports))
	for port := range ports {
		out = append(out, port)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func serviceOrDefaultPorts(service corev1.Service, fallback []int32) []int32 {
	if len(service.Spec.Ports) == 0 {
		return fallback
	}
	ports := make([]int32, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		if port.Port > 0 {
			ports = append(ports, port.Port)
		}
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	return ports
}

func uniquePodNodes(pods []corev1.Pod) []string {
	seen := map[string]struct{}{}
	for _, pod := range pods {
		if pod.Spec.NodeName != "" {
			seen[pod.Spec.NodeName] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for node := range seen {
		out = append(out, node)
	}
	sort.Strings(out)
	return out
}

func checkID(parts ...any) string {
	var b strings.Builder
	for _, part := range parts {
		if b.Len() > 0 {
			b.WriteString("|")
		}
		b.WriteString(fmt.Sprint(part))
	}
	sum := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:16]
}

func (r *NetworkProbeLabReconciler) ensureAndReadProbeJobs(ctx context.Context, lab *labv1alpha1.NetworkProbeLab, checks []probe.Check) ([]probe.Report, bool, error) {
	namespace := r.reportNamespace()
	grouped := map[string][]probe.Check{}
	for _, check := range checks {
		grouped[check.SourceNode] = append(grouped[check.SourceNode], check)
	}
	nodes := make([]string, 0, len(grouped))
	for node := range grouped {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)

	var reports []probe.Report
	pending := false
	for _, node := range nodes {
		jobName := probeJobName(lab.Name, node)
		nodeChecks := grouped[node]
		expectedHash := checksHash(nodeChecks)
		var job batchv1.Job
		err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: jobName}, &job)
		if apierrors.IsNotFound(err) {
			if err := r.Create(ctx, buildProbeJob(lab, namespace, jobName, node, nodeChecks, expectedHash, r.probeImage())); err != nil {
				return reports, pending, err
			}
			pending = true
			continue
		}
		if err != nil {
			return reports, pending, err
		}
		if job.DeletionTimestamp != nil {
			pending = true
			continue
		}
		if job.Annotations["lab.smtx.io/checks-hash"] != expectedHash || job.Annotations["lab.smtx.io/generation"] != fmt.Sprint(lab.Generation) {
			if err := r.Delete(ctx, &job); err != nil && !apierrors.IsNotFound(err) {
				return reports, pending, err
			}
			pending = true
			continue
		}
		if job.Status.Succeeded > 0 {
			report, err := r.readProbeJobReport(ctx, &job)
			if err != nil {
				return reports, pending, err
			}
			reports = append(reports, report)
			continue
		}
		if job.Status.Failed > 0 {
			return reports, pending, fmt.Errorf("probe job %s/%s failed", namespace, jobName)
		}
		pending = true
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].SourceNode < reports[j].SourceNode })
	return reports, pending, nil
}

func buildProbeJob(lab *labv1alpha1.NetworkProbeLab, namespace, name, sourceNode string, checks []probe.Check, hash, image string) *batchv1.Job {
	payload, _ := json.Marshal(checks)
	backoffLimit := int32(0)
	ttlSeconds := int32(3600)
	activeDeadlineSeconds := probeJobDeadlineSeconds(lab, len(checks))
	labels := probeLabels(lab.Name)
	labels["lab.smtx.io/source-node-hash"] = shortHash(sourceNode)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels:    labels,
			Annotations: map[string]string{
				"lab.smtx.io/source-node":  sourceNode,
				"lab.smtx.io/checks-hash":  hash,
				"lab.smtx.io/generation":   fmt.Sprint(lab.Generation),
				"lab.smtx.io/checks-count": fmt.Sprint(len(checks)),
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttlSeconds,
			ActiveDeadlineSeconds:   &activeDeadlineSeconds,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					NodeName:      sourceNode,
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:            "probe",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"/probe"},
							Env: []corev1.EnvVar{
								{Name: "SMTX_SOURCE_NODE", Value: sourceNode},
								{Name: "SMTX_PROBE_CHECKS", Value: string(payload)},
								{Name: "SMTX_PROBE_COUNT", Value: fmt.Sprint(defaultInt32(lab.Spec.Traffic.Count, 1))},
								{Name: "SMTX_PROBE_TIMEOUT", Value: fmt.Sprintf("%ds", defaultInt32(lab.Spec.Traffic.TimeoutSeconds, 5))},
							},
						},
					},
				},
			},
		},
	}
}

func checksHash(checks []probe.Check) string {
	copied := append([]probe.Check(nil), checks...)
	sort.Slice(copied, func(i, j int) bool { return copied[i].ID < copied[j].ID })
	payload, _ := json.Marshal(copied)
	sum := sha1.Sum(payload)
	return hex.EncodeToString(sum[:])[:16]
}

func probeJobDeadlineSeconds(lab *labv1alpha1.NetworkProbeLab, checkCount int) int64 {
	count := int64(defaultInt32(lab.Spec.Traffic.Count, 1))
	timeout := int64(defaultInt32(lab.Spec.Traffic.TimeoutSeconds, 5))
	if checkCount <= 0 {
		checkCount = 1
	}
	deadline := int64(checkCount)*count*timeout + 60
	if deadline < 120 {
		return 120
	}
	if deadline > 3600 {
		return 3600
	}
	return deadline
}

func (r *NetworkProbeLabReconciler) readProbeJobReport(ctx context.Context, job *batchv1.Job) (probe.Report, error) {
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(job.Namespace), client.MatchingLabels{"job-name": job.Name}); err != nil {
		return probe.Report{}, err
	}
	for _, pod := range pods.Items {
		if !ownedByJob(pod, job.UID) {
			continue
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name != "probe" || status.State.Terminated == nil {
				continue
			}
			var report probe.Report
			if err := json.Unmarshal([]byte(status.State.Terminated.Message), &report); err != nil {
				return probe.Report{}, fmt.Errorf("parse probe job report from pod %s/%s: %w", pod.Namespace, pod.Name, err)
			}
			sourcePod := pod.Namespace + "/" + pod.Name
			for idx := range report.Results {
				report.Results[idx].SourcePod = sourcePod
				report.Results[idx].SourceIP = pod.Status.PodIP
			}
			return report, nil
		}
	}
	return probe.Report{}, fmt.Errorf("probe job %s/%s completed without a readable report from current job pod", job.Namespace, job.Name)
}

func ownedByJob(pod corev1.Pod, uid types.UID) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "Job" && ref.UID == uid {
			return true
		}
	}
	return false
}

func (r *NetworkProbeLabReconciler) listNodesForChecks(ctx context.Context, checks []probe.Check) ([]corev1.Node, error) {
	names := map[string]struct{}{}
	for _, check := range checks {
		if check.SourceNode != "" {
			names[check.SourceNode] = struct{}{}
		}
		if check.TargetNode != "" {
			names[check.TargetNode] = struct{}{}
		}
	}
	var out []corev1.Node
	for name := range names {
		var node corev1.Node
		if err := r.Get(ctx, types.NamespacedName{Name: name}, &node); err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r *NetworkProbeLabReconciler) listObservationNodes(ctx context.Context, nodeSelector map[string]string, checks []probe.Check) ([]corev1.Node, error) {
	var list corev1.NodeList
	opts := []client.ListOption{}
	if len(nodeSelector) > 0 {
		opts = append(opts, client.MatchingLabelsSelector{Selector: labels.SelectorFromSet(nodeSelector)})
	}
	if err := r.List(ctx, &list, opts...); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return r.listNodesForChecks(ctx, checks)
	}
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].Name < list.Items[j].Name })
	return list.Items, nil
}

func (r *NetworkProbeLabReconciler) collectSnapshots(ctx context.Context, nodes []corev1.Node, spec labv1alpha1.NetworkObservabilitySpec) []agent.Snapshot {
	req := snapshotRequestFromSpec(spec)
	var snapshots []agent.Snapshot
	for _, node := range nodes {
		snapshot, err := r.collectSnapshot(ctx, node, req)
		if err != nil {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

func (r *NetworkProbeLabReconciler) collectSnapshot(ctx context.Context, node corev1.Node, req agent.SnapshotRequest) (agent.Snapshot, error) {
	if r.SnapshotFunc != nil {
		return r.SnapshotFunc(ctx, node, req)
	}
	address := nodeAddress(node)
	if address == "" {
		return agent.Snapshot{}, fmt.Errorf("node %s has no usable address", node.Name)
	}
	port := r.AgentPort
	if port == 0 {
		port = 18080
	}
	body, err := json.Marshal(req)
	if err != nil {
		return agent.Snapshot{}, err
	}
	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	url := fmt.Sprintf("http://%s:%d/snapshot", address, port)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return agent.Snapshot{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return agent.Snapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return agent.Snapshot{}, fmt.Errorf("node-agent snapshot on %s returned HTTP %d", node.Name, resp.StatusCode)
	}
	var snapshot agent.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return agent.Snapshot{}, err
	}
	if snapshot.NodeName == "" {
		snapshot.NodeName = node.Name
	}
	return snapshot, nil
}

func snapshotRequestFromSpec(spec labv1alpha1.NetworkObservabilitySpec) agent.SnapshotRequest {
	anySet := spec.CollectCNI || spec.CollectIptables || spec.CollectIPVS || spec.CollectConntrack || spec.CollectRoutes
	return agent.SnapshotRequest{
		CollectCNI:       spec.CollectCNI || !anySet,
		CollectIptables:  spec.CollectIptables || !anySet,
		CollectIPVS:      spec.CollectIPVS || !anySet,
		CollectConntrack: spec.CollectConntrack || !anySet,
		CollectRoutes:    spec.CollectRoutes || !anySet,
		ChainAllowlist:   spec.ChainAllowlist,
		ConntrackFilter: agent.ConntrackFilter{
			Protocols:  spec.ConntrackFilter.Protocols,
			MaxEntries: int(spec.ConntrackFilter.MaxEntries),
		},
	}
}

func snapshotToNodeResult(snapshot agent.Snapshot, snapshotConfigMapName string) labv1alpha1.NetworkNodeResult {
	snapshotRef := labv1alpha1.SnapshotReference{}
	if snapshotConfigMapName != "" {
		snapshotRef = labv1alpha1.SnapshotReference{Kind: "ConfigMap", Name: snapshotConfigMapName}
	}
	podChains, serviceChains := analyzer.SummarizeIptables(snapshot.Iptables.Lines)
	return labv1alpha1.NetworkNodeResult{
		NodeName: snapshot.NodeName,
		CNI: labv1alpha1.CNIStatus{
			Type:        snapshot.CNI.Type,
			Mode:        snapshot.CNI.Mode,
			OverlayMode: snapshot.CNI.OverlayMode,
			Calico: labv1alpha1.CalicoStatus{
				OverlayMode:       snapshot.CNI.Calico.OverlayMode,
				IPIPInterface:     snapshot.CNI.Calico.IPIPInterface,
				VXLANInterface:    snapshot.CNI.Calico.VXLANInterface,
				WorkloadInterface: snapshot.CNI.Calico.WorkloadInterfaces,
				ConfigHints:       snapshot.CNI.Calico.ConfigHints,
			},
		},
		Iptables: labv1alpha1.IptablesStatus{
			Captured:      snapshot.Iptables.Captured,
			ChainCount:    int32(snapshot.Iptables.LineCount),
			SnapshotRef:   snapshotRef,
			PodChains:     toAPIChains(podChains),
			ServiceChains: toAPIChains(serviceChains),
		},
		IPVS: labv1alpha1.IPVSStatus{
			Enabled:         snapshot.IPVS.Enabled,
			ServiceCount:    int32(snapshot.IPVS.ServiceCount),
			RealServerCount: int32(snapshot.IPVS.RealServerCount),
		},
		Conntrack: labv1alpha1.ConntrackStatus{
			Captured:       snapshot.Conntrack.Captured,
			EntriesMatched: int32(snapshot.Conntrack.LineCount),
		},
	}
}

func toAPIChains(chains []analyzer.IptablesChainSummary) []labv1alpha1.IptablesChain {
	out := make([]labv1alpha1.IptablesChain, 0, len(chains))
	for _, chain := range chains {
		out = append(out, labv1alpha1.IptablesChain{
			Name:      chain.Name,
			Category:  chain.Category,
			RuleCount: int32(chain.RuleCount),
			Purpose:   chain.Purpose,
		})
	}
	return out
}

func probeResultFromReport(labName string, item probe.Result, nodeIPs map[string]string) labv1alpha1.NetworkProbeResult {
	return labv1alpha1.NetworkProbeResult{
		SourcePod:     defaultString(item.SourcePod, "job/"+probeJobName(labName, item.SourceNode)),
		SourcePodIP:   item.SourceIP,
		SourceNode:    item.SourceNode,
		SourceNodeIP:  nodeIPs[item.SourceNode],
		TargetPod:     item.TargetPod,
		TargetPodIP:   item.TargetIP,
		TargetNode:    item.TargetNode,
		TargetNodeIP:  nodeIPs[item.TargetNode],
		TargetService: item.TargetService,
		ServiceIP:     item.ServiceIP,
		Protocol:      item.Protocol,
		Port:          item.Port,
		Path:          item.Path,
		Success:       item.Success,
		Error:         item.Error,
		LatencyMsP50:  item.LatencyMsP50,
		LatencyMsP95:  item.LatencyMsP95,
	}
}

func nodeAddress(node corev1.Node) string {
	for _, typ := range []corev1.NodeAddressType{corev1.NodeInternalIP, corev1.NodeExternalIP, corev1.NodeHostName} {
		for _, addr := range node.Status.Addresses {
			if addr.Type == typ && addr.Address != "" {
				return addr.Address
			}
		}
	}
	return ""
}

func nodeIPMap(nodes []corev1.Node) map[string]string {
	out := map[string]string{}
	for _, node := range nodes {
		out[node.Name] = nodeAddress(node)
	}
	return out
}

func (r *NetworkProbeLabReconciler) writeRawSnapshotConfigMaps(ctx context.Context, lab *labv1alpha1.NetworkProbeLab, snapshots []agent.Snapshot) (map[string]string, []string, error) {
	namespace := r.reportNamespace()
	refs := map[string]string{}
	names := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		name := snapshotConfigMapName(lab.Name, snapshot.NodeName)
		payload, truncated, err := encodeSnapshot(snapshot)
		if err != nil {
			return nil, nil, err
		}
		labels := networkSnapshotLabels(lab)
		annotations := map[string]string{
			"lab.smtx.io/node":      snapshot.NodeName,
			"lab.smtx.io/truncated": fmt.Sprint(truncated),
		}
		key := types.NamespacedName{Namespace: namespace, Name: name}
		var existing corev1.ConfigMap
		if err := r.Get(ctx, key, &existing); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, nil, err
			}
			cm := corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   namespace,
					Name:        name,
					Labels:      labels,
					Annotations: annotations,
				},
				BinaryData: map[string][]byte{"snapshot.json.gz": payload},
			}
			if err := r.Create(ctx, &cm); err != nil {
				return nil, nil, err
			}
		} else {
			if existing.Labels == nil {
				existing.Labels = map[string]string{}
			}
			for k, v := range labels {
				existing.Labels[k] = v
			}
			if existing.Annotations == nil {
				existing.Annotations = map[string]string{}
			}
			for k, v := range annotations {
				existing.Annotations[k] = v
			}
			if existing.BinaryData == nil {
				existing.BinaryData = map[string][]byte{}
			}
			existing.BinaryData["snapshot.json.gz"] = payload
			if err := r.Update(ctx, &existing); err != nil {
				return nil, nil, err
			}
		}
		refs[snapshot.NodeName] = name
		names = append(names, name)
	}
	sort.Strings(names)
	return refs, names, nil
}

func encodeSnapshot(snapshot agent.Snapshot) ([]byte, bool, error) {
	payload, err := gzipJSON(snapshot)
	if err != nil {
		return nil, false, err
	}
	if len(payload) <= 900*1024 {
		return payload, false, nil
	}
	truncated := truncateSnapshot(snapshot, 500)
	payload, err = gzipJSON(truncated)
	if err != nil {
		return nil, true, err
	}
	return payload, true, nil
}

func gzipJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func truncateSnapshot(snapshot agent.Snapshot, maxLines int) agent.Snapshot {
	snapshot.Errors = append(snapshot.Errors, "snapshot artifact truncated to fit ConfigMap size budget")
	snapshot.Iptables = truncateCommandSnapshot(snapshot.Iptables, maxLines)
	snapshot.Conntrack = truncateCommandSnapshot(snapshot.Conntrack, maxLines)
	snapshot.IPVS.Lines = truncateLines(snapshot.IPVS.Lines, maxLines)
	snapshot.CNI.DetectionLog = truncateLines(snapshot.CNI.DetectionLog, maxLines)
	return snapshot
}

func truncateCommandSnapshot(snapshot agent.CommandSnapshot, maxLines int) agent.CommandSnapshot {
	snapshot.Lines = truncateLines(snapshot.Lines, maxLines)
	snapshot.LineCount = len(snapshot.Lines)
	snapshot.Truncated = true
	return snapshot
}

func truncateLines(lines []string, maxLines int) []string {
	if len(lines) <= maxLines {
		return lines
	}
	return append(append([]string(nil), lines[:maxLines]...), fmt.Sprintf("... truncated %d additional lines", len(lines)-maxLines))
}

func (r *NetworkProbeLabReconciler) writeNetworkExcelConfigMap(ctx context.Context, lab *labv1alpha1.NetworkProbeLab, report []byte) error {
	namespace := r.reportNamespace()
	name := excelName(lab.Spec.Output.Excel.ConfigMapName, lab.Name, "network")
	key := types.NamespacedName{Namespace: namespace, Name: name}

	var existing corev1.ConfigMap
	if err := r.Get(ctx, key, &existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		cm := corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
				Labels:    networkReportLabels(lab),
			},
			BinaryData: map[string][]byte{
				"network-probe-results.xlsx": report,
			},
		}
		return r.Create(ctx, &cm)
	}
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	for k, v := range networkReportLabels(lab) {
		existing.Labels[k] = v
	}
	if existing.BinaryData == nil {
		existing.BinaryData = map[string][]byte{}
	}
	existing.BinaryData["network-probe-results.xlsx"] = report
	return r.Update(ctx, &existing)
}

func (r *NetworkProbeLabReconciler) writeNetworkHTMLConfigMap(ctx context.Context, lab *labv1alpha1.NetworkProbeLab, report []byte) error {
	namespace := r.reportNamespace()
	name := htmlName(lab.Spec.Output.HTML.ConfigMapName, lab.Name, "network")
	key := types.NamespacedName{Namespace: namespace, Name: name}

	var existing corev1.ConfigMap
	if err := r.Get(ctx, key, &existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		cm := corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
				Labels:    networkReportLabels(lab),
			},
			BinaryData: map[string][]byte{
				"network-probe-results.html": report,
			},
		}
		return r.Create(ctx, &cm)
	}
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	for k, v := range networkReportLabels(lab) {
		existing.Labels[k] = v
	}
	if existing.BinaryData == nil {
		existing.BinaryData = map[string][]byte{}
	}
	existing.BinaryData["network-probe-results.html"] = report
	return r.Update(ctx, &existing)
}

func (r *NetworkProbeLabReconciler) reportNamespace() string {
	if r.ReportNamespace != "" {
		return r.ReportNamespace
	}
	if namespace := os.Getenv("POD_NAMESPACE"); namespace != "" {
		return namespace
	}
	return "smtx-lab-system"
}

func (r *NetworkProbeLabReconciler) probeImage() string {
	if r.ProbeImage != "" {
		return r.ProbeImage
	}
	if image := os.Getenv("SMTX_PROBE_IMAGE"); image != "" {
		return image
	}
	return "smtx/smtx-lab-operator:dev"
}

func networkReportLabels(lab *labv1alpha1.NetworkProbeLab) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":        "smtx-lab-operator",
		"app.kubernetes.io/component":   "network-report",
		"lab.smtx.io/networkprobe-hash": shortHash(lab.Name),
	}
}

func networkSnapshotLabels(lab *labv1alpha1.NetworkProbeLab) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":        "smtx-lab-operator",
		"app.kubernetes.io/component":   "network-snapshot",
		"lab.smtx.io/networkprobe-hash": shortHash(lab.Name),
	}
}

func probeLabels(labName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":        "smtx-lab-operator",
		"app.kubernetes.io/component":   "network-probe",
		"lab.smtx.io/networkprobe-hash": shortHash(labName),
	}
}

func probeJobName(labName, sourceNode string) string {
	base := "npl-" + sanitizeName(labName)
	hash := shortHash(sourceNode)
	maxBase := 63 - len(hash) - 1
	if len(base) > maxBase {
		base = strings.TrimRight(base[:maxBase], "-")
	}
	return base + "-" + hash
}

func snapshotConfigMapName(labName, nodeName string) string {
	base := "npl-snapshot-" + sanitizeName(labName)
	hash := shortHash(nodeName)
	maxBase := 63 - len(hash) - 1
	if len(base) > maxBase {
		base = strings.TrimRight(base[:maxBase], "-")
	}
	return base + "-" + hash
}

func sanitizeName(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "lab"
	}
	return out
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:10]
}

func uniqueCNI(nodes []labv1alpha1.NetworkNodeResult) []string {
	seen := map[string]struct{}{}
	for _, node := range nodes {
		if node.CNI.Type != "" {
			seen[node.CNI.Type] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func uniqueDatapathModes(results []labv1alpha1.NetworkProbeResult) []string {
	seen := map[string]struct{}{}
	for _, result := range results {
		if result.Datapath.KubeProxyMode != "" {
			seen[result.Datapath.KubeProxyMode] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func uniqueCalicoOverlayModes(nodes []labv1alpha1.NetworkNodeResult) []string {
	seen := map[string]struct{}{}
	for _, node := range nodes {
		if node.CNI.Type != "calico" {
			continue
		}
		if node.CNI.OverlayMode != "" {
			seen[node.CNI.OverlayMode] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func excelName(configured, labName, suffix string) string {
	if configured != "" {
		return configured
	}
	return fmt.Sprintf("%s-%s-report", labName, suffix)
}

func htmlName(configured, labName, suffix string) string {
	if configured != "" {
		return configured
	}
	return fmt.Sprintf("%s-%s-html-report", labName, suffix)
}
