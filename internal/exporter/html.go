package exporter

import (
	"fmt"
	"html/template"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
)

func WriteNetworkHTML(w io.Writer, labName string, summary labv1alpha1.NetworkProbeSummary, results []labv1alpha1.NetworkProbeResult, nodes []labv1alpha1.NetworkNodeResult) error {
	report := networkHTMLReport{
		Title:       "Network Probe Report",
		LabName:     labName,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Summary:     summary,
		Results:     append([]labv1alpha1.NetworkProbeResult(nil), results...),
		Nodes:       append([]labv1alpha1.NetworkNodeResult(nil), nodes...),
	}
	sort.Slice(report.Results, func(i, j int) bool {
		a := report.Results[i]
		b := report.Results[j]
		for _, cmp := range []struct{ left, right string }{
			{a.SourceNode, b.SourceNode},
			{a.Path, b.Path},
			{a.Protocol, b.Protocol},
			{a.TargetPod + a.TargetService, b.TargetPod + b.TargetService},
		} {
			if cmp.left != cmp.right {
				return cmp.left < cmp.right
			}
		}
		return a.Port < b.Port
	})
	sort.Slice(report.Nodes, func(i, j int) bool {
		return report.Nodes[i].NodeName < report.Nodes[j].NodeName
	})
	for _, result := range report.Results {
		if !result.Success {
			report.FailedResults = append(report.FailedResults, result)
		}
	}
	for _, node := range report.Nodes {
		report.TotalPodChains += len(node.Iptables.PodChains)
		report.TotalServiceChains += len(node.Iptables.ServiceChains)
		report.TotalConntrack += int(node.Conntrack.EntriesMatched)
	}
	return networkHTMLTemplate.Execute(w, report)
}

func WriteResourceHTML(w io.Writer, labName string, summary labv1alpha1.ResourceAnalyzerSummary, recommendations []labv1alpha1.ResourceRecommendation) error {
	report := resourceHTMLReport{
		Title:           "Resource Recommendation Report",
		LabName:         labName,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		Summary:         summary,
		Recommendations: append([]labv1alpha1.ResourceRecommendation(nil), recommendations...),
	}
	sort.Slice(report.Recommendations, func(i, j int) bool {
		a := report.Recommendations[i]
		b := report.Recommendations[j]
		for _, cmp := range []struct{ left, right string }{
			{a.Namespace, b.Namespace},
			{a.Pod, b.Pod},
			{a.Container, b.Container},
		} {
			if cmp.left != cmp.right {
				return cmp.left < cmp.right
			}
		}
		return false
	})
	report.NamespaceRows = resourceNamespaces(report.Recommendations)
	report.TopCPU = topResourceDeltas(report.Recommendations, func(rec labv1alpha1.ResourceRecommendation) int64 {
		return rec.Current.CPURequestMillicores - rec.Recommended.CPURequestMillicores
	})
	report.TopMemory = topResourceDeltas(report.Recommendations, func(rec labv1alpha1.ResourceRecommendation) int64 {
		return rec.Current.MemoryRequestMiB - rec.Recommended.MemoryRequestMiB
	})
	return resourceHTMLTemplate.Execute(w, report)
}

type networkHTMLReport struct {
	Title              string
	LabName            string
	GeneratedAt        string
	Summary            labv1alpha1.NetworkProbeSummary
	Results            []labv1alpha1.NetworkProbeResult
	FailedResults      []labv1alpha1.NetworkProbeResult
	Nodes              []labv1alpha1.NetworkNodeResult
	TotalPodChains     int
	TotalServiceChains int
	TotalConntrack     int
}

type resourceHTMLReport struct {
	Title           string
	LabName         string
	GeneratedAt     string
	Summary         labv1alpha1.ResourceAnalyzerSummary
	Recommendations []labv1alpha1.ResourceRecommendation
	NamespaceRows   []namespaceUsageRow
	TopCPU          []labv1alpha1.ResourceRecommendation
	TopMemory       []labv1alpha1.ResourceRecommendation
}

type namespaceUsageRow struct {
	Namespace  string
	Containers int
	CPURequest int64
	CPURec     int64
	MemRequest int64
	MemRec     int64
}

func resourceNamespaces(recommendations []labv1alpha1.ResourceRecommendation) []namespaceUsageRow {
	byNamespace := map[string]*namespaceUsageRow{}
	for _, rec := range recommendations {
		row := byNamespace[rec.Namespace]
		if row == nil {
			row = &namespaceUsageRow{Namespace: rec.Namespace}
			byNamespace[rec.Namespace] = row
		}
		row.Containers++
		row.CPURequest += rec.Current.CPURequestMillicores
		row.CPURec += rec.Recommended.CPURequestMillicores
		row.MemRequest += rec.Current.MemoryRequestMiB
		row.MemRec += rec.Recommended.MemoryRequestMiB
	}
	out := make([]namespaceUsageRow, 0, len(byNamespace))
	for _, row := range byNamespace {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Namespace < out[j].Namespace
	})
	return out
}

func topResourceDeltas(recommendations []labv1alpha1.ResourceRecommendation, delta func(labv1alpha1.ResourceRecommendation) int64) []labv1alpha1.ResourceRecommendation {
	out := append([]labv1alpha1.ResourceRecommendation(nil), recommendations...)
	sort.Slice(out, func(i, j int) bool {
		return delta(out[i]) > delta(out[j])
	})
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

var htmlFuncs = template.FuncMap{
	"join": func(values []string) string {
		return strings.Join(values, ", ")
	},
	"statusClass": func(success bool) string {
		if success {
			return "ok"
		}
		return "bad"
	},
	"statusText": func(success bool) string {
		if success {
			return "OK"
		}
		return "FAIL"
	},
	"passRate": func(succeeded, total int32) string {
		if total <= 0 {
			return "0%"
		}
		return fmt.Sprintf("%.1f%%", float64(succeeded)/float64(total)*100)
	},
	"int": func(value int32) int {
		return int(value)
	},
	"add": func(a, b int) int {
		return a + b
	},
	"delta": func(current, recommended int64) int64 {
		return current - recommended
	},
	"positiveDelta": func(current, recommended int64) int64 {
		delta := current - recommended
		if delta < 0 {
			return 0
		}
		return delta
	},
	"ratioWidth": func(current, recommended int64) string {
		if current <= 0 {
			return "0%"
		}
		ratio := float64(recommended) / float64(current) * 100
		ratio = math.Max(2, math.Min(100, ratio))
		return fmt.Sprintf("%.0f%%", ratio)
	},
	"chainNames": func(chains []labv1alpha1.IptablesChain, limit int) string {
		names := make([]string, 0, len(chains))
		for idx, chain := range chains {
			if idx >= limit {
				names = append(names, fmt.Sprintf("+%d more", len(chains)-idx))
				break
			}
			names = append(names, chain.Name)
		}
		return strings.Join(names, ", ")
	},
	"resourceID": func(rec labv1alpha1.ResourceRecommendation) string {
		return rec.Namespace + "/" + rec.Pod + "/" + rec.Container
	},
}

var networkHTMLTemplate = template.Must(template.New("network-html").Funcs(htmlFuncs).Parse(baseHTMLStart + `
<body>
<main class="page">
  <section class="hero">
    <div>
      <p class="eyebrow">smtx-lab-operator</p>
      <h1>{{.Title}}</h1>
      <p class="meta">Lab: {{.LabName}} · Generated: {{.GeneratedAt}}</p>
    </div>
    <span class="badge {{if eq .Summary.Failed 0}}ok{{else}}bad{{end}}">{{if eq .Summary.Failed 0}}Succeeded{{else}}Failed{{end}}</span>
  </section>

  <section class="grid cards">
    <article><span>Total tests</span><strong>{{.Summary.TotalTests}}</strong></article>
    <article><span>Pass rate</span><strong>{{passRate .Summary.Succeeded .Summary.TotalTests}}</strong></article>
    <article><span>CNI</span><strong>{{join .Summary.CNIDetected}}</strong></article>
    <article><span>Calico overlay</span><strong>{{join .Summary.CalicoOverlayModes}}</strong></article>
    <article><span>Pod chains</span><strong>{{.TotalPodChains}}</strong></article>
    <article><span>Service chains</span><strong>{{.TotalServiceChains}}</strong></article>
  </section>

  {{if .FailedResults}}
  <section class="panel danger">
    <div class="panel-title"><h2>Failed probes</h2><span>{{len .FailedResults}}</span></div>
    <div class="table-wrap">
      <table>
        <thead><tr><th>Source</th><th>Target</th><th>Service IP</th><th>Protocol</th><th>Path</th><th>Error</th></tr></thead>
        <tbody>
        {{range .FailedResults}}
          <tr><td>{{.SourceNode}}<small>{{.SourcePodIP}}</small></td><td>{{.TargetNode}}<small>{{.TargetPodIP}}</small></td><td>{{.ServiceIP}}</td><td>{{.Protocol}}</td><td>{{.Path}}</td><td>{{.Error}}</td></tr>
        {{end}}
        </tbody>
      </table>
    </div>
  </section>
  {{end}}

  <section class="panel">
    <div class="panel-title"><h2>Node datapath</h2><span>{{len .Nodes}} nodes</span></div>
    <div class="table-wrap">
      <table>
        <thead><tr><th>Node</th><th>CNI</th><th>Overlay</th><th>Interface</th><th>iptables</th><th>Pod chains</th><th>Service chains</th><th>Conntrack</th></tr></thead>
        <tbody>
        {{range .Nodes}}
          <tr>
            <td>{{.NodeName}}</td>
            <td>{{.CNI.Type}}</td>
            <td><span class="pill">{{.CNI.OverlayMode}}</span></td>
            <td>{{.CNI.Calico.IPIPInterface}}{{if .CNI.Calico.VXLANInterface}}{{.CNI.Calico.VXLANInterface}}{{end}}</td>
            <td>{{.Iptables.ChainCount}}</td>
            <td>{{len .Iptables.PodChains}}<small>{{chainNames .Iptables.PodChains 4}}</small></td>
            <td>{{len .Iptables.ServiceChains}}<small>{{chainNames .Iptables.ServiceChains 4}}</small></td>
            <td>{{.Conntrack.EntriesMatched}}</td>
          </tr>
        {{end}}
        </tbody>
      </table>
    </div>
  </section>

  <section class="panel">
    <div class="panel-title"><h2>Traffic validation</h2><span>{{.Summary.Succeeded}} passed</span></div>
    <div class="table-wrap">
      <table>
        <thead><tr><th>Status</th><th>Source Pod IP</th><th>Source Node IP</th><th>Target Pod IP</th><th>Target Node IP</th><th>Service IP</th><th>Protocol</th><th>Path</th><th>p95 ms</th></tr></thead>
        <tbody>
        {{range .Results}}
          <tr>
            <td><span class="badge {{statusClass .Success}}">{{statusText .Success}}</span></td>
            <td>{{.SourcePodIP}}</td>
            <td>{{.SourceNodeIP}}</td>
            <td>{{.TargetPodIP}}</td>
            <td>{{.TargetNodeIP}}</td>
            <td>{{.ServiceIP}}</td>
            <td>{{.Protocol}}</td>
            <td>{{.Path}}</td>
            <td>{{printf "%.3f" .LatencyMsP95}}</td>
          </tr>
        {{end}}
        </tbody>
      </table>
    </div>
  </section>
</main>
</body>
</html>
`))

var resourceHTMLTemplate = template.Must(template.New("resource-html").Funcs(htmlFuncs).Parse(baseHTMLStart + `
<body>
<main class="page">
  <section class="hero">
    <div>
      <p class="eyebrow">smtx-lab-operator</p>
      <h1>{{.Title}}</h1>
      <p class="meta">Lab: {{.LabName}} · Generated: {{.GeneratedAt}}</p>
    </div>
    <span class="badge ok">Succeeded</span>
  </section>

  <section class="grid cards">
    <article><span>Namespaces</span><strong>{{.Summary.AnalyzedNamespaces}}</strong></article>
    <article><span>Workloads</span><strong>{{.Summary.AnalyzedWorkloads}}</strong></article>
    <article><span>Containers</span><strong>{{.Summary.AnalyzedContainers}}</strong></article>
    <article><span>Recommendations</span><strong>{{.Summary.RecommendedChanges}}</strong></article>
    <article><span>CPU request saving</span><strong>{{.Summary.PotentialCPURequestReductionMillicores}}m</strong></article>
    <article><span>Memory request saving</span><strong>{{.Summary.PotentialMemoryRequestReductionMiB}}Mi</strong></article>
  </section>

  <section class="panel">
    <div class="panel-title"><h2>Namespace summary</h2><span>{{len .NamespaceRows}} namespaces</span></div>
    <div class="table-wrap">
      <table>
        <thead><tr><th>Namespace</th><th>Containers</th><th>CPU request</th><th>CPU recommended</th><th>Memory request</th><th>Memory recommended</th></tr></thead>
        <tbody>
        {{range .NamespaceRows}}
          <tr>
            <td>{{.Namespace}}</td><td>{{.Containers}}</td>
            <td>{{.CPURequest}}m</td><td>{{.CPURec}}m</td>
            <td>{{.MemRequest}}Mi</td><td>{{.MemRec}}Mi</td>
          </tr>
        {{end}}
        </tbody>
      </table>
    </div>
  </section>

  <section class="grid two">
    <article class="panel">
      <div class="panel-title"><h2>Top CPU request reductions</h2><span>mCPU</span></div>
      {{range .TopCPU}}
      <div class="rank-row">
        <div><strong>{{resourceID .}}</strong><small>{{.Usage.Last14d.CPUMaxMillicores}}m 14d peak</small></div>
        <div class="bar"><span style="width: {{ratioWidth .Current.CPURequestMillicores .Recommended.CPURequestMillicores}}"></span></div>
        <b>{{positiveDelta .Current.CPURequestMillicores .Recommended.CPURequestMillicores}}m</b>
      </div>
      {{end}}
    </article>
    <article class="panel">
      <div class="panel-title"><h2>Top memory request reductions</h2><span>MiB</span></div>
      {{range .TopMemory}}
      <div class="rank-row">
        <div><strong>{{resourceID .}}</strong><small>{{.Usage.Last14d.MemoryMaxMiB}}Mi 14d peak</small></div>
        <div class="bar"><span style="width: {{ratioWidth .Current.MemoryRequestMiB .Recommended.MemoryRequestMiB}}"></span></div>
        <b>{{positiveDelta .Current.MemoryRequestMiB .Recommended.MemoryRequestMiB}}Mi</b>
      </div>
      {{end}}
    </article>
  </section>

  <section class="panel">
    <div class="panel-title"><h2>Container recommendations</h2><span>{{len .Recommendations}} containers</span></div>
    <div class="table-wrap">
      <table>
        <thead>
          <tr><th>Namespace</th><th>Pod</th><th>Container</th><th>Current CPU</th><th>Rec CPU</th><th>14d CPU min/avg/max</th><th>Current Mem</th><th>Rec Mem</th><th>14d Mem min/avg/max</th><th>Reason</th></tr>
        </thead>
        <tbody>
        {{range .Recommendations}}
          <tr>
            <td>{{.Namespace}}</td>
            <td>{{.Pod}}</td>
            <td>{{.Container}}</td>
            <td>{{.Current.CPURequestMillicores}}m / {{.Current.CPULimitMillicores}}m</td>
            <td>{{.Recommended.CPURequestMillicores}}m / {{.Recommended.CPULimitMillicores}}m</td>
            <td>{{.Usage.Last14d.CPUMinMillicores}} / {{.Usage.Last14d.CPUAvgMillicores}} / {{.Usage.Last14d.CPUMaxMillicores}}m</td>
            <td>{{.Current.MemoryRequestMiB}}Mi / {{.Current.MemoryLimitMiB}}Mi</td>
            <td>{{.Recommended.MemoryRequestMiB}}Mi / {{.Recommended.MemoryLimitMiB}}Mi</td>
            <td>{{.Usage.Last14d.MemoryMinMiB}} / {{.Usage.Last14d.MemoryAvgMiB}} / {{.Usage.Last14d.MemoryMaxMiB}}Mi</td>
            <td>{{.Reason}}</td>
          </tr>
        {{end}}
        </tbody>
      </table>
    </div>
  </section>
</main>
</body>
</html>
`))

const baseHTMLStart = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root{color-scheme:dark;--bg:#0f1117;--panel:#181b23;--panel-2:#1f2430;--text:#eef2f8;--muted:#99a4b5;--line:#2b3240;--green:#40d98c;--red:#ff6b7a;--yellow:#f3c969;--blue:#60a5fa;--cyan:#50d3d8}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--text);font:14px/1.45 Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
.page{width:min(1480px,calc(100vw - 32px));margin:0 auto;padding:28px 0 44px}
.hero{display:flex;justify-content:space-between;gap:24px;align-items:flex-start;margin-bottom:18px;padding-bottom:18px;border-bottom:1px solid var(--line)}
.eyebrow{margin:0 0 8px;color:var(--cyan);font-size:12px;font-weight:700;text-transform:uppercase;letter-spacing:.08em}
h1{margin:0;font-size:30px;line-height:1.1;letter-spacing:0}
h2{margin:0;font-size:18px;letter-spacing:0}
.meta{margin:10px 0 0;color:var(--muted)}
.badge,.pill{display:inline-flex;align-items:center;justify-content:center;white-space:nowrap;border-radius:999px;padding:4px 10px;font-weight:700;font-size:12px;border:1px solid var(--line);background:var(--panel-2);color:var(--text)}
.badge.ok{color:#07110b;background:var(--green);border-color:var(--green)}
.badge.bad{color:#170507;background:var(--red);border-color:var(--red)}
.grid{display:grid;gap:14px}
.cards{grid-template-columns:repeat(6,minmax(0,1fr));margin-bottom:14px}
.two{grid-template-columns:repeat(2,minmax(0,1fr));margin-bottom:14px}
.cards article,.panel{background:var(--panel);border:1px solid var(--line);border-radius:8px;box-shadow:0 14px 40px rgba(0,0,0,.18)}
.cards article{padding:16px;min-height:94px}
.cards span{display:block;color:var(--muted);font-size:12px;margin-bottom:10px}
.cards strong{font-size:24px;line-height:1.1;overflow-wrap:anywhere}
.panel{padding:16px;margin-bottom:14px}
.panel.danger{border-color:rgba(255,107,122,.45)}
.panel-title{display:flex;justify-content:space-between;align-items:center;gap:16px;margin-bottom:12px}
.panel-title span{color:var(--muted);font-weight:700}
.table-wrap{overflow:auto;border:1px solid var(--line);border-radius:6px}
table{width:100%;border-collapse:collapse;min-width:980px}
th,td{padding:10px 12px;text-align:left;border-bottom:1px solid var(--line);vertical-align:top}
th{position:sticky;top:0;background:#202532;color:#c8d2e2;font-size:12px;text-transform:uppercase;letter-spacing:.04em;z-index:1}
td small{display:block;color:var(--muted);margin-top:4px;max-width:520px;overflow-wrap:anywhere}
tbody tr:hover{background:rgba(96,165,250,.08)}
.rank-row{display:grid;grid-template-columns:minmax(0,1fr) 160px 72px;gap:12px;align-items:center;padding:10px 0;border-bottom:1px solid var(--line)}
.rank-row:last-child{border-bottom:0}
.rank-row strong{display:block;overflow-wrap:anywhere}
.rank-row small{display:block;color:var(--muted);margin-top:3px}
.rank-row b{text-align:right;color:var(--green)}
.bar{height:10px;border-radius:999px;background:#2b3240;overflow:hidden}
.bar span{display:block;height:100%;border-radius:999px;background:linear-gradient(90deg,var(--blue),var(--cyan))}
@media (max-width:1100px){.cards{grid-template-columns:repeat(3,minmax(0,1fr))}.two{grid-template-columns:1fr}.hero{flex-direction:column}.page{width:min(100vw - 20px,1480px)}}
@media (max-width:640px){.cards{grid-template-columns:repeat(2,minmax(0,1fr))}h1{font-size:24px}.cards strong{font-size:20px}.rank-row{grid-template-columns:1fr}.rank-row b{text-align:left}}
</style>
</head>`
