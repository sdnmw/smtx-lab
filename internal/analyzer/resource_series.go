package analyzer

import (
	"math"
	"sort"
	"strings"

	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
	"github.com/smtx-lab/smtx-lab-operator/internal/metrics"
)

type ResourceAnalysisResult struct {
	Summary         labv1alpha1.ResourceAnalyzerSummary
	Recommendations []labv1alpha1.ResourceRecommendation
}

type seriesKey struct {
	Namespace string
	Pod       string
	Container string
}

func AnalyzeResourceSeries(cpu, memory, requestsCPU, requestsMemory, limitsCPU, limitsMemory []metrics.Series, target labv1alpha1.ResourceTargetSpec, policy labv1alpha1.RecommendationPolicySpec) ResourceAnalysisResult {
	profiles := map[seriesKey]*ContainerUsageProfile{}
	ensure := func(key seriesKey, labels map[string]string) *ContainerUsageProfile {
		profile, ok := profiles[key]
		if ok {
			return profile
		}
		workloadKind, workloadName := workloadFromLabels(labels, key.Pod)
		profile = &ContainerUsageProfile{
			Namespace:    key.Namespace,
			WorkloadKind: workloadKind,
			WorkloadName: workloadName,
			Pod:          key.Pod,
			Container:    key.Container,
			Language:     languageFor(policy.LanguageHints, key.Namespace, workloadName, key.Container),
		}
		profiles[key] = profile
		return profile
	}

	for _, series := range cpu {
		key, ok := keyFromMetric(series.Metric)
		if !ok || !targetMatches(key, target) {
			continue
		}
		profile := ensure(key, series.Metric)
		for _, sample := range series.Values {
			profile.CPUcores = append(profile.CPUcores, sample.Value)
			profile.CPUSamples = append(profile.CPUSamples, SamplePoint{
				Timestamp: sample.Timestamp.Unix(),
				Value:     sample.Value,
			})
		}
	}
	for _, series := range memory {
		key, ok := keyFromMetric(series.Metric)
		if !ok || !targetMatches(key, target) {
			continue
		}
		profile := ensure(key, series.Metric)
		for _, sample := range series.Values {
			profile.MemoryBytes = append(profile.MemoryBytes, sample.Value)
			profile.MemorySamples = append(profile.MemorySamples, SamplePoint{
				Timestamp: sample.Timestamp.Unix(),
				Value:     sample.Value,
			})
		}
	}
	for _, series := range requestsCPU {
		key, ok := keyFromMetric(series.Metric)
		if !ok || !targetMatches(key, target) {
			continue
		}
		ensure(key, series.Metric).Current.CPURequestMillicores = coresToMillicores(latestValue(series))
	}
	for _, series := range requestsMemory {
		key, ok := keyFromMetric(series.Metric)
		if !ok || !targetMatches(key, target) {
			continue
		}
		ensure(key, series.Metric).Current.MemoryRequestMiB = bytesToMiB(latestValue(series))
	}
	for _, series := range limitsCPU {
		key, ok := keyFromMetric(series.Metric)
		if !ok || !targetMatches(key, target) {
			continue
		}
		ensure(key, series.Metric).Current.CPULimitMillicores = coresToMillicores(latestValue(series))
	}
	for _, series := range limitsMemory {
		key, ok := keyFromMetric(series.Metric)
		if !ok || !targetMatches(key, target) {
			continue
		}
		ensure(key, series.Metric).Current.MemoryLimitMiB = bytesToMiB(latestValue(series))
	}

	keys := make([]seriesKey, 0, len(profiles))
	for key, profile := range profiles {
		if len(profile.CPUcores) == 0 && len(profile.MemoryBytes) == 0 {
			continue
		}
		if !workloadKindMatches(profile.WorkloadKind, target.WorkloadKinds) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Namespace != keys[j].Namespace {
			return keys[i].Namespace < keys[j].Namespace
		}
		if keys[i].Pod != keys[j].Pod {
			return keys[i].Pod < keys[j].Pod
		}
		return keys[i].Container < keys[j].Container
	})

	result := ResourceAnalysisResult{}
	namespaces := map[string]struct{}{}
	workloads := map[string]struct{}{}
	for _, key := range keys {
		profile := profiles[key]
		profile.Usage = BuildUsageWindows(profile.CPUSamples, profile.MemorySamples)
		rec := RecommendResources(*profile, policy)
		result.Recommendations = append(result.Recommendations, rec)
		namespaces[rec.Namespace] = struct{}{}
		workloads[rec.Namespace+"/"+rec.WorkloadKind+"/"+rec.WorkloadName] = struct{}{}
		result.Summary.AnalyzedContainers++
		if recommendationChanged(rec) {
			result.Summary.RecommendedChanges++
		}
		if delta := rec.Current.CPURequestMillicores - rec.Recommended.CPURequestMillicores; delta > 0 {
			result.Summary.PotentialCPURequestReductionMillicores += delta
		}
		if delta := rec.Current.MemoryRequestMiB - rec.Recommended.MemoryRequestMiB; delta > 0 {
			result.Summary.PotentialMemoryRequestReductionMiB += delta
		}
	}
	result.Summary.AnalyzedNamespaces = int32(len(namespaces))
	result.Summary.AnalyzedWorkloads = int32(len(workloads))
	return result
}

func BuildUsageWindows(cpuSamples, memorySamples []SamplePoint) labv1alpha1.ResourceUsageWindows {
	end := latestTimestamp(cpuSamples, memorySamples)
	if end == 0 {
		return labv1alpha1.ResourceUsageWindows{}
	}
	const daySeconds = int64(24 * 60 * 60)
	return labv1alpha1.ResourceUsageWindows{
		Current: currentUsageStats(cpuSamples, memorySamples),
		Last7d:  windowUsageStats(cpuSamples, memorySamples, end-7*daySeconds, end),
		Last14d: windowUsageStats(cpuSamples, memorySamples, end-14*daySeconds, end),
	}
}

func latestTimestamp(sampleSets ...[]SamplePoint) int64 {
	var latest int64
	for _, samples := range sampleSets {
		for _, sample := range samples {
			if sample.Timestamp > latest {
				latest = sample.Timestamp
			}
		}
	}
	return latest
}

func currentUsageStats(cpuSamples, memorySamples []SamplePoint) labv1alpha1.ResourceUsageStats {
	cpu, hasCPU := latestSample(cpuSamples)
	memory, hasMemory := latestSample(memorySamples)
	stats := labv1alpha1.ResourceUsageStats{}
	if hasCPU || hasMemory {
		stats.SampleCount = 1
	}
	if hasCPU {
		value := coresToMillicores(cpu.Value)
		stats.CPUMinMillicores = value
		stats.CPUAvgMillicores = value
		stats.CPUMaxMillicores = value
	}
	if hasMemory {
		value := bytesToMiB(memory.Value)
		stats.MemoryMinMiB = value
		stats.MemoryAvgMiB = value
		stats.MemoryMaxMiB = value
	}
	return stats
}

func latestSample(samples []SamplePoint) (SamplePoint, bool) {
	if len(samples) == 0 {
		return SamplePoint{}, false
	}
	latest := samples[0]
	for _, sample := range samples[1:] {
		if sample.Timestamp >= latest.Timestamp {
			latest = sample
		}
	}
	return latest, true
}

func windowUsageStats(cpuSamples, memorySamples []SamplePoint, start, end int64) labv1alpha1.ResourceUsageStats {
	cpuValues := valuesInWindow(cpuSamples, start, end)
	memoryValues := valuesInWindow(memorySamples, start, end)
	stats := labv1alpha1.ResourceUsageStats{
		SampleCount: int32(maxInt(len(cpuValues), len(memoryValues))),
	}
	stats.CPUMinMillicores, stats.CPUAvgMillicores, stats.CPUMaxMillicores = cpuStatsMillicores(cpuValues)
	stats.MemoryMinMiB, stats.MemoryAvgMiB, stats.MemoryMaxMiB = memoryStatsMiB(memoryValues)
	return stats
}

func valuesInWindow(samples []SamplePoint, start, end int64) []float64 {
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if sample.Timestamp >= start && sample.Timestamp <= end {
			values = append(values, sample.Value)
		}
	}
	return values
}

func cpuStatsMillicores(values []float64) (int64, int64, int64) {
	minValue, avgValue, maxValue := minAvgMax(values)
	return coresToMillicores(minValue), coresToMillicores(avgValue), coresToMillicores(maxValue)
}

func memoryStatsMiB(values []float64) (int64, int64, int64) {
	minValue, avgValue, maxValue := minAvgMax(values)
	return bytesToMiB(minValue), bytesToMiB(avgValue), bytesToMiB(maxValue)
}

func minAvgMax(values []float64) (float64, float64, float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	minValue := math.Inf(1)
	maxValue := math.Inf(-1)
	sum := 0.0
	for _, value := range values {
		if value < minValue {
			minValue = value
		}
		if value > maxValue {
			maxValue = value
		}
		sum += value
	}
	return minValue, sum / float64(len(values)), maxValue
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func keyFromMetric(metric map[string]string) (seriesKey, bool) {
	key := seriesKey{
		Namespace: metric["namespace"],
		Pod:       metric["pod"],
		Container: metric["container"],
	}
	if key.Namespace == "" || key.Pod == "" || key.Container == "" || key.Container == "POD" {
		return seriesKey{}, false
	}
	return key, true
}

func targetMatches(key seriesKey, target labv1alpha1.ResourceTargetSpec) bool {
	if containsString(target.ExcludeNamespaces, key.Namespace) {
		return false
	}
	if len(target.Namespaces) > 0 && !containsString(target.Namespaces, key.Namespace) {
		return false
	}
	return true
}

func workloadKindMatches(kind string, allowed []string) bool {
	if len(allowed) == 0 || kind == "" || kind == "Pod" {
		return true
	}
	for _, value := range allowed {
		if strings.EqualFold(value, kind) {
			return true
		}
	}
	return false
}

func workloadFromLabels(labels map[string]string, pod string) (string, string) {
	for _, kindKey := range []string{"workload_kind", "workload_type", "owner_kind", "created_by_kind"} {
		for _, nameKey := range []string{"workload", "workload_name", "owner_name", "created_by_name"} {
			if labels[kindKey] != "" && labels[nameKey] != "" {
				return normalizeKind(labels[kindKey]), labels[nameKey]
			}
		}
	}
	if name := labels["deployment"]; name != "" {
		return "Deployment", name
	}
	if name := labels["statefulset"]; name != "" {
		return "StatefulSet", name
	}
	if name := labels["daemonset"]; name != "" {
		return "DaemonSet", name
	}
	return "Pod", pod
}

func normalizeKind(kind string) string {
	switch strings.ToLower(kind) {
	case "deployment", "deployments":
		return "Deployment"
	case "statefulset", "statefulsets":
		return "StatefulSet"
	case "daemonset", "daemonsets":
		return "DaemonSet"
	case "replicaset", "replicasets":
		return "ReplicaSet"
	default:
		if kind == "" {
			return "Pod"
		}
		return kind
	}
}

func languageFor(hints labv1alpha1.LanguageHintsSpec, namespace, workload, container string) string {
	for _, override := range hints.Overrides {
		if override.Namespace != "" && override.Namespace != namespace {
			continue
		}
		if override.Workload != "" && override.Workload != workload {
			continue
		}
		if override.Container != "" && override.Container != container {
			continue
		}
		if override.Language != "" {
			return override.Language
		}
	}
	if hints.Default != "" {
		return hints.Default
	}
	return "Go"
}

func latestValue(series metrics.Series) float64 {
	if len(series.Values) == 0 {
		return 0
	}
	return series.Values[len(series.Values)-1].Value
}

func recommendationChanged(rec labv1alpha1.ResourceRecommendation) bool {
	return rec.Current.CPURequestMillicores != rec.Recommended.CPURequestMillicores ||
		rec.Current.CPULimitMillicores != rec.Recommended.CPULimitMillicores ||
		rec.Current.MemoryRequestMiB != rec.Recommended.MemoryRequestMiB ||
		rec.Current.MemoryLimitMiB != rec.Recommended.MemoryLimitMiB
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
