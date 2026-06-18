package analyzer

import (
	"testing"
	"time"

	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
	"github.com/smtx-lab/smtx-lab-operator/internal/metrics"
)

func TestAnalyzeResourceSeries(t *testing.T) {
	now := time.Unix(100, 0)
	cpu := []metrics.Series{
		{
			Metric: map[string]string{
				"namespace":     "prod",
				"pod":           "orders-abc",
				"container":     "app",
				"workload_kind": "Deployment",
				"workload":      "orders",
			},
			Values: []metrics.Sample{
				{Timestamp: now.Add(-10 * time.Minute), Value: 0.1},
				{Timestamp: now.Add(-5 * time.Minute), Value: 0.4},
				{Timestamp: now, Value: 0.8},
			},
		},
		{
			Metric: map[string]string{
				"namespace": "kube-system",
				"pod":       "ignored",
				"container": "app",
			},
			Values: []metrics.Sample{{Timestamp: now, Value: 1}},
		},
	}
	memory := []metrics.Series{
		{
			Metric: map[string]string{
				"namespace":     "prod",
				"pod":           "orders-abc",
				"container":     "app",
				"workload_kind": "Deployment",
				"workload":      "orders",
			},
			Values: []metrics.Sample{
				{Timestamp: now.Add(-10 * time.Minute), Value: 512 * 1024 * 1024},
				{Timestamp: now.Add(-5 * time.Minute), Value: 1024 * 1024 * 1024},
				{Timestamp: now, Value: 1536 * 1024 * 1024},
			},
		},
	}
	requestsCPU := []metrics.Series{series("prod", "orders-abc", "app", 1.0)}
	requestsMemory := []metrics.Series{series("prod", "orders-abc", "app", 2*1024*1024*1024)}
	limitsCPU := []metrics.Series{series("prod", "orders-abc", "app", 2.0)}
	limitsMemory := []metrics.Series{series("prod", "orders-abc", "app", 4*1024*1024*1024)}

	result := AnalyzeResourceSeries(cpu, memory, requestsCPU, requestsMemory, limitsCPU, limitsMemory, labv1alpha1.ResourceTargetSpec{
		Namespaces:        []string{"prod"},
		WorkloadKinds:     []string{"Deployment"},
		ExcludeNamespaces: []string{"kube-system"},
	}, labv1alpha1.RecommendationPolicySpec{
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
		LanguageHints: labv1alpha1.LanguageHintsSpec{
			Default: "Go",
			Overrides: []labv1alpha1.LanguageOverrideSpec{
				{Namespace: "prod", Workload: "orders", Container: "app", Language: "Java"},
			},
		},
	})

	if result.Summary.AnalyzedNamespaces != 1 {
		t.Fatalf("AnalyzedNamespaces = %d, want 1", result.Summary.AnalyzedNamespaces)
	}
	if result.Summary.AnalyzedWorkloads != 1 {
		t.Fatalf("AnalyzedWorkloads = %d, want 1", result.Summary.AnalyzedWorkloads)
	}
	if result.Summary.AnalyzedContainers != 1 {
		t.Fatalf("AnalyzedContainers = %d, want 1", result.Summary.AnalyzedContainers)
	}
	if len(result.Recommendations) != 1 {
		t.Fatalf("recommendations = %d, want 1", len(result.Recommendations))
	}
	rec := result.Recommendations[0]
	if rec.Language != "Java" {
		t.Fatalf("Language = %q, want Java", rec.Language)
	}
	if rec.WorkloadKind != "Deployment" || rec.WorkloadName != "orders" {
		t.Fatalf("workload = %s/%s, want Deployment/orders", rec.WorkloadKind, rec.WorkloadName)
	}
	if rec.Current.CPURequestMillicores != 1000 || rec.Current.MemoryRequestMiB != 2048 {
		t.Fatalf("current resources = %+v, want CPU 1000m and memory 2048MiB", rec.Current)
	}
	if rec.Usage.Current.CPUMaxMillicores != 800 || rec.Usage.Current.MemoryMaxMiB != 1536 {
		t.Fatalf("current usage = %+v, want latest CPU 800m and memory 1536MiB", rec.Usage.Current)
	}
	if rec.Usage.Last7d.CPUMinMillicores != 100 || rec.Usage.Last7d.CPUAvgMillicores != 434 || rec.Usage.Last7d.CPUMaxMillicores != 800 {
		t.Fatalf("7d CPU usage = %+v, want min 100m avg 434m max 800m", rec.Usage.Last7d)
	}
	if rec.Usage.Last14d.MemoryMinMiB != 512 || rec.Usage.Last14d.MemoryAvgMiB != 1024 || rec.Usage.Last14d.MemoryMaxMiB != 1536 {
		t.Fatalf("14d memory usage = %+v, want min 512MiB avg 1024MiB max 1536MiB", rec.Usage.Last14d)
	}
	if rec.Recommended.MemoryLimitMiB < rec.Observed.MemoryP99MiB {
		t.Fatalf("memory limit = %d, want >= observed p99 %d", rec.Recommended.MemoryLimitMiB, rec.Observed.MemoryP99MiB)
	}
}

func series(namespace, pod, container string, latest float64) metrics.Series {
	return metrics.Series{
		Metric: map[string]string{
			"namespace": namespace,
			"pod":       pod,
			"container": container,
		},
		Values: []metrics.Sample{{Timestamp: time.Unix(100, 0), Value: latest}},
	}
}
