package analyzer

import (
	"fmt"
	"math"
	"sort"
	"strings"

	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
)

type ContainerUsageProfile struct {
	Namespace     string
	WorkloadKind  string
	WorkloadName  string
	Pod           string
	Container     string
	Language      string
	CPUcores      []float64
	MemoryBytes   []float64
	CPUSamples    []SamplePoint
	MemorySamples []SamplePoint
	Usage         labv1alpha1.ResourceUsageWindows
	Current       labv1alpha1.ContainerResourceValues
}

type SamplePoint struct {
	Timestamp int64
	Value     float64
}

func RecommendResources(profile ContainerUsageProfile, policy labv1alpha1.RecommendationPolicySpec) labv1alpha1.ResourceRecommendation {
	language := strings.TrimSpace(profile.Language)
	if language == "" {
		language = policy.LanguageHints.Default
	}
	if language == "" {
		language = "Go"
	}

	cpuRequestP := percentileName(policy.CPU.RequestPercentile, "p95")
	cpuLimitP := percentileName(policy.CPU.LimitPercentile, "p99")
	memRequestP := percentileName(policy.Memory.RequestPercentile, "p95")
	memLimitP := percentileName(policy.Memory.LimitPercentile, "p99")

	cpuP50 := coresToMillicores(Percentile(profile.CPUcores, 0.50))
	cpuP95 := coresToMillicores(Percentile(profile.CPUcores, 0.95))
	cpuP99 := coresToMillicores(Percentile(profile.CPUcores, 0.99))
	memP50 := bytesToMiB(Percentile(profile.MemoryBytes, 0.50))
	memP95 := bytesToMiB(Percentile(profile.MemoryBytes, 0.95))
	memP99 := bytesToMiB(Percentile(profile.MemoryBytes, 0.99))

	cpuRequestBase := maxInt64(coresToMillicores(Percentile(profile.CPUcores, cpuRequestP)), profile.Usage.Last14d.CPUAvgMillicores)
	cpuRequest := applyHeadroom(
		cpuRequestBase,
		policy.CPU.RequestHeadroomRatio,
		minMillicores(policy.CPU.MinRequestMillicores),
	)
	cpuLimit := applyHeadroom(
		maxInt64(coresToMillicores(Percentile(profile.CPUcores, cpuLimitP)), profile.Usage.Last14d.CPUMaxMillicores),
		languageCPULimitHeadroom(language, policy.CPU.LimitHeadroomRatio),
		0,
	)
	memRequestBase := maxInt64(bytesToMiB(Percentile(profile.MemoryBytes, memRequestP)), profile.Usage.Last14d.MemoryAvgMiB)
	memRequest := applyHeadroom(
		memRequestBase,
		languageMemoryRequestHeadroom(language, policy.Memory.RequestHeadroomRatio),
		minMiB(policy.Memory.MinRequestMiB),
	)
	memLimit := applyHeadroom(
		maxInt64(bytesToMiB(Percentile(profile.MemoryBytes, memLimitP)), profile.Usage.Last14d.MemoryMaxMiB),
		languageMemoryLimitHeadroom(language, policy.Memory.LimitHeadroomRatio),
		0,
	)

	recommendedCPURequest := roundMillicores(cpuRequest)
	recommendedCPULimit := roundMillicores(cpuLimit)
	recommendedMemoryRequest := roundMiB(memRequest)
	recommendedMemoryLimit := roundMiB(memLimit)
	if recommendedCPULimit > 0 && recommendedCPULimit < recommendedCPURequest {
		recommendedCPULimit = recommendedCPURequest
	}
	if recommendedMemoryLimit > 0 && recommendedMemoryLimit < recommendedMemoryRequest {
		recommendedMemoryLimit = recommendedMemoryRequest
	}

	return labv1alpha1.ResourceRecommendation{
		Namespace:    profile.Namespace,
		WorkloadKind: profile.WorkloadKind,
		WorkloadName: profile.WorkloadName,
		Pod:          profile.Pod,
		Container:    profile.Container,
		Language:     language,
		Current:      profile.Current,
		Observed: labv1alpha1.ObservedResourceValues{
			CPUP50Millicores: cpuP50,
			CPUP95Millicores: cpuP95,
			CPUP99Millicores: cpuP99,
			MemoryP50MiB:     memP50,
			MemoryP95MiB:     memP95,
			MemoryP99MiB:     memP99,
		},
		Usage: profile.Usage,
		Recommended: labv1alpha1.ContainerResourceValues{
			CPURequestMillicores: recommendedCPURequest,
			CPULimitMillicores:   recommendedCPULimit,
			MemoryRequestMiB:     recommendedMemoryRequest,
			MemoryLimitMiB:       recommendedMemoryLimit,
		},
		Reason: fmt.Sprintf("request uses max(14d avg,%s) with headroom; limit uses 14d peak/%s with %s runtime headroom", policy.CPU.RequestPercentile, policy.CPU.LimitPercentile, language),
	}
}

func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		p = 0
	}
	if p >= 1 {
		p = 1
	}
	copied := append([]float64(nil), values...)
	sort.Float64s(copied)
	if len(copied) == 1 {
		return copied[0]
	}
	rank := p * float64(len(copied)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return copied[lower]
	}
	weight := rank - float64(lower)
	return copied[lower]*(1-weight) + copied[upper]*weight
}

func percentileName(name, fallback string) float64 {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "p50":
		return 0.50
	case "p90":
		return 0.90
	case "p95", "":
		if name == "" {
			return percentileName(fallback, "p95")
		}
		return 0.95
	case "p99":
		return 0.99
	default:
		return percentileName(fallback, "p95")
	}
}

func coresToMillicores(cores float64) int64 {
	return int64(math.Ceil(cores * 1000))
}

func bytesToMiB(bytes float64) int64 {
	return int64(math.Ceil(bytes / 1024 / 1024))
}

func applyHeadroom(value int64, ratio float64, minValue int64) int64 {
	if ratio <= 0 {
		ratio = 1
	}
	out := int64(math.Ceil(float64(value) * ratio))
	if out < minValue {
		return minValue
	}
	return out
}

func minMillicores(value int64) int64 {
	if value <= 0 {
		return 50
	}
	return value
}

func minMiB(value int64) int64 {
	if value <= 0 {
		return 64
	}
	return value
}

func languageCPULimitHeadroom(language string, configured float64) float64 {
	switch strings.ToLower(language) {
	case "java":
		return maxFloat(configured, 1.5)
	case "python":
		return maxFloat(configured, 1.4)
	default:
		return maxFloat(configured, 1.2)
	}
}

func languageMemoryRequestHeadroom(language string, configured float64) float64 {
	switch strings.ToLower(language) {
	case "java":
		return maxFloat(configured, 1.25)
	case "python":
		return maxFloat(configured, 1.2)
	default:
		return maxFloat(configured, 1.1)
	}
}

func languageMemoryLimitHeadroom(language string, configured float64) float64 {
	switch strings.ToLower(language) {
	case "java":
		return maxFloat(configured, 1.5)
	case "python":
		return maxFloat(configured, 1.35)
	default:
		return maxFloat(configured, 1.25)
	}
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func roundMillicores(value int64) int64 {
	if value <= 100 {
		return roundUp(value, 10)
	}
	return roundUp(value, 50)
}

func roundMiB(value int64) int64 {
	if value <= 512 {
		return roundUp(value, 32)
	}
	return roundUp(value, 128)
}

func roundUp(value, step int64) int64 {
	if value <= 0 {
		return 0
	}
	return ((value + step - 1) / step) * step
}
