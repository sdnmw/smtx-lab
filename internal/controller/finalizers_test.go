package controller

import (
	"context"
	"testing"

	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCleanupNetworkProbeArtifactsDeletesOnlyMatchingObjects(t *testing.T) {
	scheme := cleanupTestScheme(t)
	lab := &labv1alpha1.NetworkProbeLab{ObjectMeta: metav1.ObjectMeta{Name: "cross-node"}}
	matchingJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "reports",
			Name:      "matching-job",
			Labels:    probeLabels(lab.Name),
		},
	}
	matchingReport := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "reports",
			Name:      "matching-report",
			Labels:    networkReportLabels(lab),
		},
	}
	matchingSnapshot := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "reports",
			Name:      "matching-snapshot",
			Labels:    networkSnapshotLabels(lab),
		},
	}
	unrelated := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "reports",
			Name:      "unrelated",
			Labels:    map[string]string{"lab.smtx.io/networkprobe-hash": "different"},
		},
	}
	reconciler := &NetworkProbeLabReconciler{
		Client:          fake.NewClientBuilder().WithScheme(scheme).WithObjects(matchingJob, matchingReport, matchingSnapshot, unrelated).Build(),
		ReportNamespace: "reports",
	}

	if err := reconciler.cleanupNetworkProbeArtifacts(context.Background(), lab); err != nil {
		t.Fatal(err)
	}
	assertNotFound(t, reconciler.Client, types.NamespacedName{Namespace: "reports", Name: "matching-job"}, &batchv1.Job{})
	assertNotFound(t, reconciler.Client, types.NamespacedName{Namespace: "reports", Name: "matching-report"}, &corev1.ConfigMap{})
	assertNotFound(t, reconciler.Client, types.NamespacedName{Namespace: "reports", Name: "matching-snapshot"}, &corev1.ConfigMap{})
	var kept corev1.ConfigMap
	if err := reconciler.Get(context.Background(), types.NamespacedName{Namespace: "reports", Name: "unrelated"}, &kept); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupResourceAnalyzerArtifactsKeepsSharedMonitoringStack(t *testing.T) {
	scheme := cleanupTestScheme(t)
	lab := &labv1alpha1.ResourceAnalyzerLab{ObjectMeta: metav1.ObjectMeta{Name: "resource-14d"}}
	report := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "reports",
			Name:      "resource-report",
			Labels:    resourceReportLabels(lab),
		},
	}
	prometheus := prometheusDeployment("reports")
	reconciler := &ResourceAnalyzerLabReconciler{
		Client:          fake.NewClientBuilder().WithScheme(scheme).WithObjects(report, prometheus).Build(),
		ReportNamespace: "reports",
	}

	if err := reconciler.cleanupResourceAnalyzerArtifacts(context.Background(), lab); err != nil {
		t.Fatal(err)
	}
	assertNotFound(t, reconciler.Client, types.NamespacedName{Namespace: "reports", Name: "resource-report"}, &corev1.ConfigMap{})
	var kept appsv1.Deployment
	if err := reconciler.Get(context.Background(), types.NamespacedName{Namespace: "reports", Name: monitoringPrometheusName}, &kept); err != nil {
		t.Fatal(err)
	}
}

func cleanupTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := labv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func assertNotFound(t *testing.T, c client.Client, key types.NamespacedName, obj client.Object) {
	t.Helper()
	err := c.Get(context.Background(), key, obj)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("Get(%s) err = %v, want NotFound", key.String(), err)
	}
}
