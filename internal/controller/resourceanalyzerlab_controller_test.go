package controller

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestWriteExcelConfigMapCreatesAndUpdatesReport(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := labv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	reconciler := &ResourceAnalyzerLabReconciler{
		Client:          fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme:          scheme,
		ReportNamespace: "reports",
	}
	lab := &labv1alpha1.ResourceAnalyzerLab{
		ObjectMeta: metav1.ObjectMeta{Name: "resource-14d"},
		Spec: labv1alpha1.ResourceAnalyzerLabSpec{
			Output: labv1alpha1.ResourceAnalyzerOutputSpec{
				Excel: labv1alpha1.ExcelOutputSpec{ConfigMapName: "resource-report"},
				HTML:  labv1alpha1.HTMLOutputSpec{ConfigMapName: "resource-report"},
			},
		},
	}

	ctx := context.Background()
	if err := reconciler.writeExcelConfigMap(ctx, lab, []byte("first")); err != nil {
		t.Fatal(err)
	}
	var cm corev1.ConfigMap
	if err := reconciler.Get(ctx, ctrlclient.ObjectKey{Namespace: "reports", Name: "resource-report"}, &cm); err != nil {
		t.Fatal(err)
	}
	if got := string(cm.BinaryData["resource-recommendations.xlsx"]); got != "first" {
		t.Fatalf("report = %q, want first", got)
	}

	if err := reconciler.writeExcelConfigMap(ctx, lab, []byte("second")); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Get(ctx, ctrlclient.ObjectKey{Namespace: "reports", Name: "resource-report"}, &cm); err != nil {
		t.Fatal(err)
	}
	if got := string(cm.BinaryData["resource-recommendations.xlsx"]); got != "second" {
		t.Fatalf("report = %q, want second", got)
	}
	if err := reconciler.writeHTMLConfigMap(ctx, lab, []byte("<html>resource</html>")); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Get(ctx, ctrlclient.ObjectKey{Namespace: "reports", Name: "resource-report"}, &cm); err != nil {
		t.Fatal(err)
	}
	if got := string(cm.BinaryData["resource-recommendations.xlsx"]); got != "second" {
		t.Fatalf("excel report after html write = %q, want second", got)
	}
	if got := string(cm.BinaryData["resource-recommendations.html"]); got != "<html>resource</html>" {
		t.Fatalf("html report = %q, want html payload", got)
	}
	if cm.Labels["lab.smtx.io/resourceanalyzer"] != "resource-14d" {
		t.Fatalf("resource analyzer label = %q", cm.Labels["lab.smtx.io/resourceanalyzer"])
	}
}

func TestResourceAnalyzerAutoDeployCreatesMonitoringAndWaits(t *testing.T) {
	scheme := resourceAnalyzerTestScheme(t)
	lab := &labv1alpha1.ResourceAnalyzerLab{
		ObjectMeta: metav1.ObjectMeta{Name: "auto-monitoring", Finalizers: []string{resourceAnalyzerLabFinalizer}},
		Spec: labv1alpha1.ResourceAnalyzerLabSpec{
			MonitoringStack: labv1alpha1.MonitoringStackSpec{
				AutoDeploy: true,
				Prometheus: labv1alpha1.MonitoringItem{
					Enabled: true,
				},
				Grafana: labv1alpha1.MonitoringItem{
					Enabled: true,
				},
			},
		},
	}
	reconciler := &ResourceAnalyzerLabReconciler{
		Client:          fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&labv1alpha1.ResourceAnalyzerLab{}).WithObjects(lab).Build(),
		Scheme:          scheme,
		ReportNamespace: "reports",
	}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: lab.Name}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter <= 0 {
		t.Fatalf("RequeueAfter = %v, want positive wait for Prometheus readiness", result.RequeueAfter)
	}
	var prom appsv1.Deployment
	if err := reconciler.Get(context.Background(), types.NamespacedName{Namespace: "reports", Name: monitoringPrometheusName}, &prom); err != nil {
		t.Fatal(err)
	}
	var grafana appsv1.Deployment
	if err := reconciler.Get(context.Background(), types.NamespacedName{Namespace: "reports", Name: monitoringGrafanaName}, &grafana); err != nil {
		t.Fatal(err)
	}
	var ksm appsv1.Deployment
	if err := reconciler.Get(context.Background(), types.NamespacedName{Namespace: "reports", Name: monitoringKubeStateMetricsName}, &ksm); err != nil {
		t.Fatal(err)
	}
	var ksmService corev1.Service
	if err := reconciler.Get(context.Background(), types.NamespacedName{Namespace: "reports", Name: monitoringKubeStateMetricsName}, &ksmService); err != nil {
		t.Fatal(err)
	}
	var promConfig corev1.ConfigMap
	if err := reconciler.Get(context.Background(), types.NamespacedName{Namespace: "reports", Name: monitoringPrometheusName}, &promConfig); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(promConfig.Data["prometheus.yml"], "job_name: kube-state-metrics") {
		t.Fatal("Prometheus config does not contain kube-state-metrics scrape job")
	}
	var updated labv1alpha1.ResourceAnalyzerLab
	if err := reconciler.Get(context.Background(), types.NamespacedName{Name: lab.Name}, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Phase != labv1alpha1.LabPhaseRunning {
		t.Fatalf("phase = %q, want Running", updated.Status.Phase)
	}
}

func TestResourceAnalyzerAutoDeployUsesDefaultPrometheusURLWhenReady(t *testing.T) {
	scheme := resourceAnalyzerTestScheme(t)
	lab := &labv1alpha1.ResourceAnalyzerLab{
		ObjectMeta: metav1.ObjectMeta{Name: "auto-monitoring-ready", Finalizers: []string{resourceAnalyzerLabFinalizer}},
		Spec: labv1alpha1.ResourceAnalyzerLabSpec{
			Target: labv1alpha1.ResourceTargetSpec{Namespaces: []string{"prod"}},
			Metrics: labv1alpha1.MetricsSourceSpec{
				LookbackDays:   14,
				Step:           "5m",
				TimeoutSeconds: 5,
			},
			Recommendation: labv1alpha1.RecommendationPolicySpec{
				CPU: labv1alpha1.ResourceRecommendationRule{
					RequestPercentile:    "p95",
					LimitPercentile:      "p99",
					RequestHeadroomRatio: 1.2,
					LimitHeadroomRatio:   1.5,
					MinRequestMillicores: 50,
				},
				Memory: labv1alpha1.ResourceRecommendationRule{
					RequestPercentile:    "p95",
					LimitPercentile:      "p99",
					RequestHeadroomRatio: 1.15,
					LimitHeadroomRatio:   1.3,
					MinRequestMiB:        64,
				},
			},
			MonitoringStack: labv1alpha1.MonitoringStackSpec{
				AutoDeploy: true,
				Prometheus: labv1alpha1.MonitoringItem{
					Enabled: true,
				},
			},
		},
	}
	prom := prometheusDeployment("reports")
	prom.Status.AvailableReplicas = 1
	prom.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:   appsv1.DeploymentAvailable,
		Status: corev1.ConditionTrue,
	}}
	seenDefaultHost := false
	reconciler := &ResourceAnalyzerLabReconciler{
		Client:          fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&labv1alpha1.ResourceAnalyzerLab{}).WithObjects(lab, prom).Build(),
		Scheme:          scheme,
		ReportNamespace: "reports",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host == "smtx-lab-prometheus.reports.svc:9090" {
				seenDefaultHost = true
			}
			body := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"namespace":"prod","pod":"orders-abc","container":"app","workload_kind":"Deployment","workload":"orders"},"values":[[100,"0.1"],[200,"0.2"]]}]}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		})},
	}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: lab.Name}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("RequeueAfter = %v, want no requeue", result.RequeueAfter)
	}
	if !seenDefaultHost {
		t.Fatal("Prometheus query did not use auto-deployed default service URL")
	}
	var updated labv1alpha1.ResourceAnalyzerLab
	if err := reconciler.Get(context.Background(), types.NamespacedName{Name: lab.Name}, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status.Phase != labv1alpha1.LabPhaseSucceeded {
		t.Fatalf("phase = %q, want Succeeded", updated.Status.Phase)
	}
	if updated.Status.Summary.AnalyzedContainers != 1 {
		t.Fatalf("AnalyzedContainers = %d, want 1", updated.Status.Summary.AnalyzedContainers)
	}
}

func resourceAnalyzerTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := labv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
