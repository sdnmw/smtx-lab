package exporter

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
	"github.com/xuri/excelize/v2"
)

func WriteResourceRecommendations(w io.Writer, recommendations []labv1alpha1.ResourceRecommendation) error {
	file := excelize.NewFile()
	defer file.Close()

	const sheet = "Resource Recommendations"
	index, err := file.NewSheet(sheet)
	if err != nil {
		return err
	}
	file.SetActiveSheet(index)
	_ = file.DeleteSheet("Sheet1")

	headers := []string{
		"Namespace", "Workload Kind", "Workload Name", "Pod", "Container", "Language",
		"Current CPU Request (m)", "Current CPU Limit (m)", "Current Memory Request (MiB)", "Current Memory Limit (MiB)",
		"Current CPU Usage (m)", "Current Memory Usage (MiB)",
		"7d CPU Min (m)", "7d CPU Avg (m)", "7d CPU Max (m)", "7d Memory Min (MiB)", "7d Memory Avg (MiB)", "7d Memory Max (MiB)",
		"14d CPU Min (m)", "14d CPU Avg (m)", "14d CPU Max (m)", "14d Memory Min (MiB)", "14d Memory Avg (MiB)", "14d Memory Max (MiB)",
		"CPU p50 (m)", "CPU p95 (m)", "CPU p99 (m)", "Memory p50 (MiB)", "Memory p95 (MiB)", "Memory p99 (MiB)",
		"Recommended CPU Request (m)", "Recommended CPU Limit (m)",
		"Recommended Memory Request (MiB)", "Recommended Memory Limit (MiB)", "Reason",
	}
	for idx, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(idx+1, 1)
		_ = file.SetCellValue(sheet, cell, header)
	}

	for row, rec := range recommendations {
		values := []any{
			rec.Namespace, rec.WorkloadKind, rec.WorkloadName, rec.Pod, rec.Container, rec.Language,
			rec.Current.CPURequestMillicores, rec.Current.CPULimitMillicores, rec.Current.MemoryRequestMiB, rec.Current.MemoryLimitMiB,
			rec.Usage.Current.CPUMaxMillicores, rec.Usage.Current.MemoryMaxMiB,
			rec.Usage.Last7d.CPUMinMillicores, rec.Usage.Last7d.CPUAvgMillicores, rec.Usage.Last7d.CPUMaxMillicores,
			rec.Usage.Last7d.MemoryMinMiB, rec.Usage.Last7d.MemoryAvgMiB, rec.Usage.Last7d.MemoryMaxMiB,
			rec.Usage.Last14d.CPUMinMillicores, rec.Usage.Last14d.CPUAvgMillicores, rec.Usage.Last14d.CPUMaxMillicores,
			rec.Usage.Last14d.MemoryMinMiB, rec.Usage.Last14d.MemoryAvgMiB, rec.Usage.Last14d.MemoryMaxMiB,
			rec.Observed.CPUP50Millicores, rec.Observed.CPUP95Millicores, rec.Observed.CPUP99Millicores,
			rec.Observed.MemoryP50MiB, rec.Observed.MemoryP95MiB, rec.Observed.MemoryP99MiB,
			rec.Recommended.CPURequestMillicores, rec.Recommended.CPULimitMillicores,
			rec.Recommended.MemoryRequestMiB, rec.Recommended.MemoryLimitMiB, rec.Reason,
		}
		for col, value := range values {
			cell, _ := excelize.CoordinatesToCellName(col+1, row+2)
			_ = file.SetCellValue(sheet, cell, value)
		}
	}

	lastCol, _ := excelize.ColumnNumberToName(len(headers))
	if err := file.AutoFilter(sheet, "A1:"+lastCol+strconv.Itoa(len(recommendations)+1), nil); err != nil {
		return err
	}
	_ = file.SetPanes(sheet, &excelize.Panes{
		Freeze:      true,
		Split:       false,
		XSplit:      0,
		YSplit:      1,
		TopLeftCell: "A2",
		ActivePane:  "bottomLeft",
	})
	for col := 1; col <= len(headers); col++ {
		name, _ := excelize.ColumnNumberToName(col)
		_ = file.SetColWidth(sheet, name, name, 18)
	}

	if err := addSummarySheet(file, recommendations); err != nil {
		return err
	}
	_, err = file.WriteTo(w)
	return err
}

func WriteNetworkResults(w io.Writer, results []labv1alpha1.NetworkProbeResult, nodes []labv1alpha1.NetworkNodeResult) error {
	file := excelize.NewFile()
	defer file.Close()

	const testSheet = "Network Tests"
	index, err := file.NewSheet(testSheet)
	if err != nil {
		return err
	}
	file.SetActiveSheet(index)
	_ = file.DeleteSheet("Sheet1")

	headers := []string{
		"Source Pod", "Source Pod IP", "Source Node", "Source Node IP",
		"Target Pod", "Target Pod IP", "Target Node", "Target Node IP", "Target Service", "Service IP",
		"Protocol", "Port", "Path", "Success", "Error", "Latency p50 ms", "Latency p95 ms",
		"CNI", "Calico Overlay Mode", "Kube Proxy Mode", "Pod Forward Chains", "Service Chains", "Relevant Chains", "Conntrack Matched",
	}
	for idx, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(idx+1, 1)
		_ = file.SetCellValue(testSheet, cell, header)
	}
	for row, result := range results {
		values := []any{
			result.SourcePod, result.SourcePodIP, result.SourceNode, result.SourceNodeIP,
			result.TargetPod, result.TargetPodIP, result.TargetNode, result.TargetNodeIP, result.TargetService, result.ServiceIP,
			result.Protocol, result.Port, result.Path, result.Success, result.Error, result.LatencyMsP50, result.LatencyMsP95,
			result.Datapath.CNI, result.Datapath.CalicoOverlayMode, result.Datapath.KubeProxyMode,
			strings.Join(result.Datapath.PodForwardChains, "\n"), strings.Join(result.Datapath.ServiceChains, "\n"),
			fmt.Sprintf("%v", result.Datapath.RelevantChains), result.Datapath.ConntrackMatched,
		}
		for col, value := range values {
			cell, _ := excelize.CoordinatesToCellName(col+1, row+2)
			_ = file.SetCellValue(testSheet, cell, value)
		}
	}

	const nodeSheet = "Datapath"
	if _, err := file.NewSheet(nodeSheet); err != nil {
		return err
	}
	nodeHeaders := []string{
		"Node", "CNI", "CNI Mode", "Calico Overlay Mode", "Calico IPIP Interface", "Calico VXLAN Interface", "Calico Workload Interfaces", "Calico Config Hints",
		"iptables Captured", "iptables Chains", "Pod Forward Chain Count", "Service Chain Count", "IPVS Enabled", "IPVS Services", "Conntrack Captured", "Conntrack Matches",
	}
	for idx, header := range nodeHeaders {
		cell, _ := excelize.CoordinatesToCellName(idx+1, 1)
		_ = file.SetCellValue(nodeSheet, cell, header)
	}
	for row, node := range nodes {
		values := []any{
			node.NodeName, node.CNI.Type, node.CNI.Mode, node.CNI.OverlayMode, node.CNI.Calico.IPIPInterface, node.CNI.Calico.VXLANInterface,
			strings.Join(node.CNI.Calico.WorkloadInterface, "\n"), strings.Join(node.CNI.Calico.ConfigHints, "\n"),
			node.Iptables.Captured, node.Iptables.ChainCount, len(node.Iptables.PodChains), len(node.Iptables.ServiceChains),
			node.IPVS.Enabled, node.IPVS.ServiceCount, node.Conntrack.Captured, node.Conntrack.EntriesMatched,
		}
		for col, value := range values {
			cell, _ := excelize.CoordinatesToCellName(col+1, row+2)
			_ = file.SetCellValue(nodeSheet, cell, value)
		}
	}

	if err := addIptablesChainSheet(file, "Pod Forward Chains", nodes, func(node labv1alpha1.NetworkNodeResult) []labv1alpha1.IptablesChain {
		return node.Iptables.PodChains
	}); err != nil {
		return err
	}
	if err := addIptablesChainSheet(file, "Service Chains", nodes, func(node labv1alpha1.NetworkNodeResult) []labv1alpha1.IptablesChain {
		return node.Iptables.ServiceChains
	}); err != nil {
		return err
	}

	_, err = file.WriteTo(w)
	return err
}

func addIptablesChainSheet(file *excelize.File, sheet string, nodes []labv1alpha1.NetworkNodeResult, chainsFor func(labv1alpha1.NetworkNodeResult) []labv1alpha1.IptablesChain) error {
	if _, err := file.NewSheet(sheet); err != nil {
		return err
	}
	headers := []string{"Node", "Chain", "Category", "Rule Count", "Purpose"}
	for idx, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(idx+1, 1)
		_ = file.SetCellValue(sheet, cell, header)
	}
	row := 2
	for _, node := range nodes {
		for _, chain := range chainsFor(node) {
			values := []any{node.NodeName, chain.Name, chain.Category, chain.RuleCount, chain.Purpose}
			for col, value := range values {
				cell, _ := excelize.CoordinatesToCellName(col+1, row)
				_ = file.SetCellValue(sheet, cell, value)
			}
			row++
		}
	}
	if row > 2 {
		_ = file.AutoFilter(sheet, "A1:E"+strconv.Itoa(row-1), nil)
	}
	_ = file.SetColWidth(sheet, "A", "A", 30)
	_ = file.SetColWidth(sheet, "B", "B", 34)
	_ = file.SetColWidth(sheet, "C", "D", 16)
	_ = file.SetColWidth(sheet, "E", "E", 72)
	return nil
}

func addSummarySheet(file *excelize.File, recommendations []labv1alpha1.ResourceRecommendation) error {
	const sheet = "Summary"
	if _, err := file.NewSheet(sheet); err != nil {
		return err
	}
	totalCPUDelta := int64(0)
	totalMemoryDelta := int64(0)
	for _, rec := range recommendations {
		totalCPUDelta += rec.Current.CPURequestMillicores - rec.Recommended.CPURequestMillicores
		totalMemoryDelta += rec.Current.MemoryRequestMiB - rec.Recommended.MemoryRequestMiB
	}
	rows := [][]any{
		{"Metric", "Value"},
		{"Containers", len(recommendations)},
		{"Potential CPU request reduction (m)", totalCPUDelta},
		{"Potential memory request reduction (MiB)", totalMemoryDelta},
	}
	for r, row := range rows {
		for c, value := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
			_ = file.SetCellValue(sheet, cell, value)
		}
	}
	_ = file.SetColWidth(sheet, "A", "B", 32)
	return nil
}
