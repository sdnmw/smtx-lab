package controller

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
	"github.com/smtx-lab/smtx-lab-operator/internal/analyzer"
	"github.com/smtx-lab/smtx-lab-operator/internal/exporter"
	"github.com/smtx-lab/smtx-lab-operator/internal/metrics"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResourceAnalyzerLabReconciler reconciles ResourceAnalyzerLab objects.
type ResourceAnalyzerLabReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	HTTPClient      *http.Client
	ReportNamespace string
	Now             func() time.Time
}

// +kubebuilder:rbac:groups=lab.smtx.io,resources=resourceanalyzerlabs,verbs=get;list;watch
// +kubebuilder:rbac:groups=lab.smtx.io,resources=resourceanalyzerlabs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=lab.smtx.io,resources=resourceanalyzerlabs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods;namespaces;services;configmaps;events,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;daemonsets,verbs=get;list;watch;create;update;patch
func (r *ResourceAnalyzerLabReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("resourceAnalyzerLab", req.NamespacedName)

	var lab labv1alpha1.ResourceAnalyzerLab
	if err := r.Get(ctx, req.NamespacedName, &lab); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if lab.GetDeletionTimestamp() != nil {
		if hasFinalizer(&lab, resourceAnalyzerLabFinalizer) {
			if err := r.cleanupResourceAnalyzerArtifacts(ctx, &lab); err != nil {
				return ctrl.Result{}, err
			}
			removeFinalizer(&lab, resourceAnalyzerLabFinalizer)
			return ctrl.Result{}, r.Update(ctx, &lab)
		}
		return ctrl.Result{}, nil
	}
	if addFinalizer(&lab, resourceAnalyzerLabFinalizer) {
		return ctrl.Result{}, r.Update(ctx, &lab)
	}

	next := lab.Status
	next.ObservedGeneration = lab.Generation
	next.Artifacts.ExcelConfigMapName = excelName(lab.Spec.Output.Excel.ConfigMapName, lab.Name, "resource")
	next.Artifacts.HTMLConfigMapName = htmlName(lab.Spec.Output.HTML.ConfigMapName, lab.Name, "resource")

	setCondition(&next.Conditions, lab.Generation, "SpecAccepted", metav1.ConditionTrue, "SpecAccepted", "ResourceAnalyzerLab spec is accepted.")
	requeue := false
	prometheusURL := lab.Spec.Metrics.PrometheusURL
	if lab.Spec.MonitoringStack.AutoDeploy {
		monitoring, err := r.ensureMonitoringStack(ctx, &lab)
		if err != nil {
			next.Phase = labv1alpha1.LabPhaseFailed
			setCondition(&next.Conditions, lab.Generation, "MonitoringStackReady", metav1.ConditionFalse, "ApplyFailed", err.Error())
			setCondition(&next.Conditions, lab.Generation, "PrometheusReachable", metav1.ConditionFalse, "MonitoringStackFailed", "Prometheus cannot be queried until the monitoring stack is applied.")
			setCondition(&next.Conditions, lab.Generation, "AnalysisCompleted", metav1.ConditionFalse, "Blocked", "Analysis cannot run until the monitoring stack is ready.")
			return r.updateResourceAnalyzerStatus(ctx, &lab, next, requeue, log)
		}
		if prometheusURL == "" && monitoring.PrometheusEnabled {
			prometheusURL = monitoring.PrometheusURL
		}
		if monitoring.PrometheusEnabled && !monitoring.PrometheusReady {
			next.Phase = labv1alpha1.LabPhaseRunning
			requeue = true
			setCondition(&next.Conditions, lab.Generation, "MonitoringStackReady", metav1.ConditionUnknown, "WaitingForPrometheus", "Monitoring stack resources are applied; waiting for Prometheus deployment availability.")
			setCondition(&next.Conditions, lab.Generation, "PrometheusReachable", metav1.ConditionUnknown, "WaitingForPrometheus", "Prometheus deployment is not available yet.")
			setCondition(&next.Conditions, lab.Generation, "AnalysisCompleted", metav1.ConditionFalse, "WaitingForMonitoringStack", "Analysis will run after Prometheus becomes available.")
			return r.updateResourceAnalyzerStatus(ctx, &lab, next, requeue, log)
		}
		setCondition(&next.Conditions, lab.Generation, "MonitoringStackReady", metav1.ConditionTrue, "ResourcesReady", "Monitoring stack resources are applied and Prometheus is available.")
	}

	if prometheusURL == "" {
		next.Phase = labv1alpha1.LabPhaseFailed
		setCondition(&next.Conditions, lab.Generation, "PrometheusReachable", metav1.ConditionFalse, "MissingPrometheusURL", "spec.metrics.prometheusURL is required for resource analysis.")
		setCondition(&next.Conditions, lab.Generation, "AnalysisCompleted", metav1.ConditionFalse, "Blocked", "Analysis cannot run without a Prometheus endpoint.")
	} else {
		runLab := lab
		runLab.Spec.Metrics.PrometheusURL = prometheusURL
		result, err := r.runResourceAnalysis(ctx, &runLab)
		if err != nil {
			next.Phase = labv1alpha1.LabPhaseFailed
			setCondition(&next.Conditions, lab.Generation, "PrometheusReachable", metav1.ConditionFalse, "QueryFailed", err.Error())
			setCondition(&next.Conditions, lab.Generation, "AnalysisCompleted", metav1.ConditionFalse, "Failed", "Resource analysis failed.")
		} else {
			next.Phase = labv1alpha1.LabPhaseSucceeded
			next.Summary = result.Summary
			next.Recommendations = result.Recommendations
			setCondition(&next.Conditions, lab.Generation, "PrometheusReachable", metav1.ConditionTrue, "QuerySucceeded", "Prometheus range queries completed.")
			setCondition(&next.Conditions, lab.Generation, "AnalysisCompleted", metav1.ConditionTrue, "Completed", "Resource analysis and recommendation generation completed.")
			if lab.Spec.Output.Excel.Enabled {
				setCondition(&next.Conditions, lab.Generation, "ExcelExported", metav1.ConditionTrue, "ConfigMapWritten", "Excel report was written to ConfigMap "+next.Artifacts.ExcelConfigMapName+".")
			}
			if lab.Spec.Output.HTML.Enabled {
				setCondition(&next.Conditions, lab.Generation, "HTMLExported", metav1.ConditionTrue, "ConfigMapWritten", "HTML report was written to ConfigMap "+next.Artifacts.HTMLConfigMapName+".")
			}
		}
	}

	return r.updateResourceAnalyzerStatus(ctx, &lab, next, requeue, log)
}

func (r *ResourceAnalyzerLabReconciler) updateResourceAnalyzerStatus(ctx context.Context, lab *labv1alpha1.ResourceAnalyzerLab, next labv1alpha1.ResourceAnalyzerLabStatus, requeue bool, log logr.Logger) (ctrl.Result, error) {
	if reflect.DeepEqual(lab.Status, next) {
		if requeue {
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		return ctrl.Result{}, nil
	}

	lab.Status = next
	if err := r.Status().Update(ctx, lab); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("updated ResourceAnalyzerLab status")
	if requeue {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

func (r *ResourceAnalyzerLabReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&labv1alpha1.ResourceAnalyzerLab{}).
		Complete(r)
}

func (r *ResourceAnalyzerLabReconciler) runResourceAnalysis(ctx context.Context, lab *labv1alpha1.ResourceAnalyzerLab) (analyzer.ResourceAnalysisResult, error) {
	timeout := time.Duration(defaultInt32(lab.Spec.Metrics.TimeoutSeconds, 30)) * time.Second
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	end := time.Now().UTC()
	if r.Now != nil {
		end = r.Now().UTC()
	}
	lookback := time.Duration(defaultInt32(lab.Spec.Metrics.LookbackDays, 14)) * 24 * time.Hour
	step, err := time.ParseDuration(defaultString(lab.Spec.Metrics.Step, "5m"))
	if err != nil {
		return analyzer.ResourceAnalysisResult{}, err
	}
	start := end.Add(-lookback)

	prom := metrics.PrometheusClient{
		BaseURL:    lab.Spec.Metrics.PrometheusURL,
		HTTPClient: r.HTTPClient,
	}
	cpu, err := prom.QueryRange(queryCtx, metrics.ContainerCPUUsageQuery, start, end, step)
	if err != nil {
		return analyzer.ResourceAnalysisResult{}, err
	}
	memory, err := prom.QueryRange(queryCtx, metrics.ContainerMemoryWorkingSetQuery, start, end, step)
	if err != nil {
		return analyzer.ResourceAnalysisResult{}, err
	}
	cpuRequests, err := prom.QueryRange(queryCtx, metrics.ContainerCPURequestsQuery, start, end, step)
	if err != nil {
		return analyzer.ResourceAnalysisResult{}, err
	}
	memoryRequests, err := prom.QueryRange(queryCtx, metrics.ContainerMemoryRequestsQuery, start, end, step)
	if err != nil {
		return analyzer.ResourceAnalysisResult{}, err
	}
	cpuLimits, err := prom.QueryRange(queryCtx, metrics.ContainerCPULimitsQuery, start, end, step)
	if err != nil {
		return analyzer.ResourceAnalysisResult{}, err
	}
	memoryLimits, err := prom.QueryRange(queryCtx, metrics.ContainerMemoryLimitsQuery, start, end, step)
	if err != nil {
		return analyzer.ResourceAnalysisResult{}, err
	}

	result := analyzer.AnalyzeResourceSeries(cpu, memory, cpuRequests, memoryRequests, cpuLimits, memoryLimits, lab.Spec.Target, lab.Spec.Recommendation)
	if lab.Spec.Output.Excel.Enabled {
		var buf bytes.Buffer
		if err := exporter.WriteResourceRecommendations(&buf, result.Recommendations); err != nil {
			return analyzer.ResourceAnalysisResult{}, err
		}
		if err := r.writeExcelConfigMap(ctx, lab, buf.Bytes()); err != nil {
			return analyzer.ResourceAnalysisResult{}, err
		}
	}
	if lab.Spec.Output.HTML.Enabled {
		var buf bytes.Buffer
		if err := exporter.WriteResourceHTML(&buf, lab.Name, result.Summary, result.Recommendations); err != nil {
			return analyzer.ResourceAnalysisResult{}, err
		}
		if err := r.writeHTMLConfigMap(ctx, lab, buf.Bytes()); err != nil {
			return analyzer.ResourceAnalysisResult{}, err
		}
	}
	return result, nil
}

func (r *ResourceAnalyzerLabReconciler) writeExcelConfigMap(ctx context.Context, lab *labv1alpha1.ResourceAnalyzerLab, report []byte) error {
	namespace := r.reportNamespace()
	name := excelName(lab.Spec.Output.Excel.ConfigMapName, lab.Name, "resource")
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
				Labels:    resourceReportLabels(lab),
			},
			BinaryData: map[string][]byte{
				"resource-recommendations.xlsx": report,
			},
		}
		return r.Create(ctx, &cm)
	}
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	for k, v := range resourceReportLabels(lab) {
		existing.Labels[k] = v
	}
	if existing.BinaryData == nil {
		existing.BinaryData = map[string][]byte{}
	}
	existing.BinaryData["resource-recommendations.xlsx"] = report
	return r.Update(ctx, &existing)
}

func (r *ResourceAnalyzerLabReconciler) writeHTMLConfigMap(ctx context.Context, lab *labv1alpha1.ResourceAnalyzerLab, report []byte) error {
	namespace := r.reportNamespace()
	name := htmlName(lab.Spec.Output.HTML.ConfigMapName, lab.Name, "resource")
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
				Labels:    resourceReportLabels(lab),
			},
			BinaryData: map[string][]byte{
				"resource-recommendations.html": report,
			},
		}
		return r.Create(ctx, &cm)
	}
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	for k, v := range resourceReportLabels(lab) {
		existing.Labels[k] = v
	}
	if existing.BinaryData == nil {
		existing.BinaryData = map[string][]byte{}
	}
	existing.BinaryData["resource-recommendations.html"] = report
	return r.Update(ctx, &existing)
}

func (r *ResourceAnalyzerLabReconciler) reportNamespace() string {
	if r.ReportNamespace != "" {
		return r.ReportNamespace
	}
	if namespace := os.Getenv("POD_NAMESPACE"); namespace != "" {
		return namespace
	}
	return "smtx-lab-system"
}

func resourceReportLabels(lab *labv1alpha1.ResourceAnalyzerLab) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "smtx-lab-operator",
		"app.kubernetes.io/component":  "resource-report",
		"lab.smtx.io/resourceanalyzer": lab.Name,
	}
}

func defaultInt32(value, fallback int32) int32 {
	if value <= 0 {
		return fallback
	}
	return value
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
