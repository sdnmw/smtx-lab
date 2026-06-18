package analyzer

import (
	"testing"

	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
)

func TestPercentile(t *testing.T) {
	values := []float64{10, 20, 30, 40, 50}
	if got := Percentile(values, 0.50); got != 30 {
		t.Fatalf("p50 = %v, want 30", got)
	}
	if got := Percentile(values, 0.95); got != 48 {
		t.Fatalf("p95 = %v, want 48", got)
	}
}

func TestRecommendResourcesJavaMemoryHeadroom(t *testing.T) {
	rec := RecommendResources(ContainerUsageProfile{
		Namespace:    "prod",
		WorkloadKind: "Deployment",
		WorkloadName: "orders",
		Pod:          "orders-abc",
		Container:    "app",
		Language:     "Java",
		CPUcores:     []float64{0.1, 0.5, 1.0},
		MemoryBytes:  []float64{512 * 1024 * 1024, 1024 * 1024 * 1024, 1536 * 1024 * 1024},
		Current: labv1alpha1.ContainerResourceValues{
			CPURequestMillicores: 1000,
			MemoryRequestMiB:     2048,
		},
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
	})
	if rec.Recommended.MemoryRequestMiB < 1792 {
		t.Fatalf("memory request = %d, want Java headroom applied", rec.Recommended.MemoryRequestMiB)
	}
	if rec.Recommended.CPURequestMillicores < 1100 {
		t.Fatalf("cpu request = %d, want p95 with headroom", rec.Recommended.CPURequestMillicores)
	}
}

func TestRecommendResourcesLimitNotBelowRequest(t *testing.T) {
	rec := RecommendResources(ContainerUsageProfile{
		Namespace:    "test",
		WorkloadKind: "Pod",
		WorkloadName: "nginx-abc",
		Pod:          "nginx-abc",
		Container:    "nginx",
		Language:     "Go",
		CPUcores:     []float64{0.001, 0.002, 0.003},
		MemoryBytes:  []float64{20 * 1024 * 1024, 21 * 1024 * 1024, 22 * 1024 * 1024},
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
	})
	if rec.Recommended.CPULimitMillicores < rec.Recommended.CPURequestMillicores {
		t.Fatalf("cpu limit = %d, want >= request %d", rec.Recommended.CPULimitMillicores, rec.Recommended.CPURequestMillicores)
	}
	if rec.Recommended.MemoryLimitMiB < rec.Recommended.MemoryRequestMiB {
		t.Fatalf("memory limit = %d, want >= request %d", rec.Recommended.MemoryLimitMiB, rec.Recommended.MemoryRequestMiB)
	}
}
