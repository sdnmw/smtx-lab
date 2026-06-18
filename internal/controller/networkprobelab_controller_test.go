package controller

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
	"github.com/smtx-lab/smtx-lab-operator/internal/agent"
	"github.com/smtx-lab/smtx-lab-operator/internal/probe"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestBuildProbeChecksCrossNode(t *testing.T) {
	lab := &labv1alpha1.NetworkProbeLab{
		ObjectMeta: metav1.ObjectMeta{Name: "cross-node"},
		Spec: labv1alpha1.NetworkProbeLabSpec{
			Traffic: labv1alpha1.NetworkProbeTrafficSpec{
				Protocols:         []string{"TCP"},
				Ports:             []int32{80},
				CrossNodeOnly:     true,
				IncludePodIP:      true,
				IncludeServiceVIP: true,
				IncludeDNS:        true,
			},
		},
	}
	pods := []corev1.Pod{
		probeTargetPod("default", "app-a", "node-a", "10.0.0.1"),
		probeTargetPod("default", "app-b", "node-b", "10.0.0.2"),
	}
	services := []corev1.Service{{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app"},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.96.0.10",
			Ports:     []corev1.ServicePort{{Port: 80}},
		},
	}}

	checks := buildProbeChecks(lab, pods, services)
	if len(checks) != 6 {
		t.Fatalf("checks = %d, want 6", len(checks))
	}
	podIPChecks := 0
	for _, check := range checks {
		if check.Path != "podIP" {
			continue
		}
		podIPChecks++
		if check.SourceNode == check.TargetNode {
			t.Fatalf("podIP check is not cross-node: %+v", check)
		}
	}
	if podIPChecks != 2 {
		t.Fatalf("podIP checks = %d, want 2", podIPChecks)
	}
}

func TestRunNetworkProbeCompletedWritesReport(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := labv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	lab := &labv1alpha1.NetworkProbeLab{
		ObjectMeta: metav1.ObjectMeta{Name: "cross-node"},
		Spec: labv1alpha1.NetworkProbeLabSpec{
			Target: labv1alpha1.NetworkProbeTargetSpec{
				Namespaces: []string{"default"},
				PodSelector: labv1alpha1.LabelSelectorSpec{
					MatchLabels: map[string]string{"app": "demo"},
				},
			},
			Traffic: labv1alpha1.NetworkProbeTrafficSpec{
				Protocols:     []string{"TCP"},
				Ports:         []int32{80},
				Count:         1,
				CrossNodeOnly: true,
				IncludePodIP:  true,
			},
			Output: labv1alpha1.NetworkProbeOutputSpec{
				Excel:              labv1alpha1.ExcelOutputSpec{Enabled: true, ConfigMapName: "network-report"},
				HTML:               labv1alpha1.HTMLOutputSpec{Enabled: true, ConfigMapName: "network-html-report"},
				RetainRawSnapshots: true,
			},
		},
	}
	podA := probeTargetPod("default", "app-a", "node-a", "10.0.0.1")
	podB := probeTargetPod("default", "app-b", "node-b", "10.0.0.2")
	podA.Labels = map[string]string{"app": "demo"}
	podB.Labels = map[string]string{"app": "demo"}
	checks := buildProbeChecks(lab, []corev1.Pod{podA, podB}, nil)

	jobA := completedProbeJob(lab, "node-a", "reports", checks)
	jobB := completedProbeJob(lab, "node-b", "reports", checks)
	reportA := probe.Report{SourceNode: "node-a", Results: []probe.Result{{
		CheckID:    "a",
		SourceNode: "node-a",
		TargetPod:  "default/app-b",
		TargetNode: "node-b",
		Protocol:   "TCP",
		Address:    "10.0.0.2",
		Port:       80,
		Path:       "podIP",
		TargetIP:   "10.0.0.2",
		Success:    true,
	}}}
	reportB := probe.Report{SourceNode: "node-b", Results: []probe.Result{{
		CheckID:    "b",
		SourceNode: "node-b",
		TargetPod:  "default/app-a",
		TargetNode: "node-a",
		Protocol:   "TCP",
		Address:    "10.0.0.1",
		Port:       80,
		Path:       "podIP",
		TargetIP:   "10.0.0.1",
		Success:    true,
	}}}
	staleReport := probe.Report{SourceNode: "node-a", Results: []probe.Result{{
		CheckID:    "stale",
		SourceNode: "node-a",
		TargetPod:  "default/app-b",
		TargetNode: "node-b",
		Protocol:   "TCP",
		Address:    "10.0.0.2",
		Port:       80,
		Path:       "podIP",
		TargetIP:   "10.0.0.2",
		Success:    false,
		Error:      "stale report should be ignored",
	}}}
	stalePod := probeJobPod(jobA, staleReport)
	stalePod.Name = jobA.Name + "-old-pod"
	stalePod.OwnerReferences[0].UID = types.UID("old-job-uid")

	reconciler := &NetworkProbeLabReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&podA,
			&podB,
			probeNode("node-a", "192.168.1.10"),
			probeNode("node-b", "192.168.1.11"),
			jobA,
			jobB,
			stalePod,
			probeJobPod(jobA, reportA),
			probeJobPod(jobB, reportB),
		).Build(),
		Scheme:          scheme,
		ReportNamespace: "reports",
		SnapshotFunc: func(_ context.Context, node corev1.Node, _ agent.SnapshotRequest) (agent.Snapshot, error) {
			return agent.Snapshot{
				NodeName: node.Name,
				Time:     time.Unix(100, 0),
				CNI:      agent.CNISnapshot{Type: "calico", Mode: "iptables"},
				Iptables: agent.CommandSnapshot{
					Available: true,
					Captured:  true,
					LineCount: 1,
					Lines:     []string{"-A KUBE-SERVICES -d 10.0.0.2/32 -j ACCEPT"},
				},
				Conntrack: agent.CommandSnapshot{
					Available: true,
					Captured:  true,
					LineCount: 1,
					Lines:     []string{"tcp 6 ESTABLISHED src=10.1.0.1 dst=10.0.0.2 sport=12345 dport=80"},
				},
			}, nil
		},
	}

	result, err := reconciler.runNetworkProbe(context.Background(), lab)
	if err != nil {
		t.Fatal(err)
	}
	if result.Pending {
		t.Fatal("runNetworkProbe returned pending, want complete")
	}
	if result.Summary.TotalTests != 2 || result.Summary.Succeeded != 2 || result.Summary.Failed != 0 {
		t.Fatalf("summary = %+v, want 2 total/2 succeeded/0 failed", result.Summary)
	}
	if len(result.NodeResults) != 2 {
		t.Fatalf("node results = %d, want 2", len(result.NodeResults))
	}
	if len(result.RawSnapshotConfigMapNames) != 2 {
		t.Fatalf("raw snapshot refs = %d, want 2", len(result.RawSnapshotConfigMapNames))
	}
	if result.NodeResults[0].Iptables.SnapshotRef.Kind != "ConfigMap" || result.NodeResults[0].Iptables.SnapshotRef.Name == "" {
		t.Fatalf("snapshot ref was not set on node result: %+v", result.NodeResults[0].Iptables.SnapshotRef)
	}
	if len(result.ProbeResults) != 2 {
		t.Fatalf("probe results = %d, want 2", len(result.ProbeResults))
	}
	if result.ProbeResults[0].Datapath.CNI != "calico" {
		t.Fatalf("datapath cni = %q, want calico", result.ProbeResults[0].Datapath.CNI)
	}

	var cm corev1.ConfigMap
	if err := reconciler.Get(context.Background(), types.NamespacedName{Namespace: "reports", Name: "network-report"}, &cm); err != nil {
		t.Fatal(err)
	}
	if len(cm.BinaryData["network-probe-results.xlsx"]) == 0 {
		t.Fatal("network Excel report was not written")
	}
	var htmlCM corev1.ConfigMap
	if err := reconciler.Get(context.Background(), types.NamespacedName{Namespace: "reports", Name: "network-html-report"}, &htmlCM); err != nil {
		t.Fatal(err)
	}
	if len(htmlCM.BinaryData["network-probe-results.html"]) == 0 {
		t.Fatal("network HTML report was not written")
	}

	var snapshotCM corev1.ConfigMap
	if err := reconciler.Get(context.Background(), types.NamespacedName{Namespace: "reports", Name: result.RawSnapshotConfigMapNames[0]}, &snapshotCM); err != nil {
		t.Fatal(err)
	}
	if snapshotCM.Labels["app.kubernetes.io/component"] != "network-snapshot" {
		t.Fatalf("snapshot component label = %q", snapshotCM.Labels["app.kubernetes.io/component"])
	}
	var decoded agent.Snapshot
	if err := gunzipJSON(snapshotCM.BinaryData["snapshot.json.gz"], &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.NodeName == "" || decoded.CNI.Type != "calico" {
		t.Fatalf("decoded snapshot = %+v, want node name and calico cni", decoded)
	}
}

func TestEnsureProbeJobsDeletesAndRecreatesStaleJob(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	lab := &labv1alpha1.NetworkProbeLab{
		ObjectMeta: metav1.ObjectMeta{Name: "cross-node", Generation: 2},
		Spec: labv1alpha1.NetworkProbeLabSpec{
			Traffic: labv1alpha1.NetworkProbeTrafficSpec{
				Count:          1,
				TimeoutSeconds: 5,
			},
		},
	}
	checks := []probe.Check{{
		ID:         "fresh-check",
		SourceNode: "node-a",
		TargetNode: "node-b",
		Protocol:   "TCP",
		Address:    "10.0.0.2",
		Port:       80,
		Path:       "podIP",
		TargetIP:   "10.0.0.2",
	}}
	stale := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "reports",
			Name:      probeJobName(lab.Name, "node-a"),
			Labels:    probeLabels(lab.Name),
			Annotations: map[string]string{
				"lab.smtx.io/checks-hash": "old",
				"lab.smtx.io/generation":  "2",
			},
		},
	}
	reconciler := &NetworkProbeLabReconciler{
		Client:          fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale).Build(),
		ReportNamespace: "reports",
		ProbeImage:      "probe:test",
	}

	reports, pending, err := reconciler.ensureAndReadProbeJobs(context.Background(), lab, checks)
	if err != nil {
		t.Fatal(err)
	}
	if !pending || len(reports) != 0 {
		t.Fatalf("reports=%d pending=%v, want no reports and pending", len(reports), pending)
	}
	var deleted batchv1.Job
	err = reconciler.Get(context.Background(), types.NamespacedName{Namespace: "reports", Name: probeJobName(lab.Name, "node-a")}, &deleted)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("stale job get error = %v, want not found", err)
	}

	reports, pending, err = reconciler.ensureAndReadProbeJobs(context.Background(), lab, checks)
	if err != nil {
		t.Fatal(err)
	}
	if !pending || len(reports) != 0 {
		t.Fatalf("reports=%d pending=%v after recreate, want no reports and pending", len(reports), pending)
	}
	var created batchv1.Job
	if err := reconciler.Get(context.Background(), types.NamespacedName{Namespace: "reports", Name: probeJobName(lab.Name, "node-a")}, &created); err != nil {
		t.Fatal(err)
	}
	if created.Annotations["lab.smtx.io/checks-hash"] != checksHash(checks) {
		t.Fatalf("checks hash annotation = %q, want %q", created.Annotations["lab.smtx.io/checks-hash"], checksHash(checks))
	}
	if created.Annotations["lab.smtx.io/generation"] != "2" {
		t.Fatalf("generation annotation = %q, want 2", created.Annotations["lab.smtx.io/generation"])
	}
}

func TestBuildProbeJobSetsDeadlineAndStabilityMetadata(t *testing.T) {
	lab := &labv1alpha1.NetworkProbeLab{
		ObjectMeta: metav1.ObjectMeta{Name: "cross-node", Generation: 7},
		Spec: labv1alpha1.NetworkProbeLabSpec{
			Traffic: labv1alpha1.NetworkProbeTrafficSpec{
				Count:          2,
				TimeoutSeconds: 10,
			},
		},
	}
	checks := []probe.Check{
		{ID: "b", SourceNode: "node-a"},
		{ID: "a", SourceNode: "node-a"},
		{ID: "c", SourceNode: "node-a"},
		{ID: "d", SourceNode: "node-a"},
		{ID: "e", SourceNode: "node-a"},
	}
	hash := checksHash(checks)

	job := buildProbeJob(lab, "reports", "probe-job", "node-a", checks, hash, "probe:test")
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != 160 {
		t.Fatalf("active deadline = %v, want 160", job.Spec.ActiveDeadlineSeconds)
	}
	if job.Annotations["lab.smtx.io/checks-hash"] != hash {
		t.Fatalf("checks hash annotation = %q, want %q", job.Annotations["lab.smtx.io/checks-hash"], hash)
	}
	if job.Annotations["lab.smtx.io/generation"] != "7" {
		t.Fatalf("generation annotation = %q, want 7", job.Annotations["lab.smtx.io/generation"])
	}
	if job.Annotations["lab.smtx.io/checks-count"] != "5" {
		t.Fatalf("checks count annotation = %q, want 5", job.Annotations["lab.smtx.io/checks-count"])
	}
}

func probeTargetPod(namespace, name, node, ip string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{{
				Name:  "app",
				Image: "demo",
				Ports: []corev1.ContainerPort{{ContainerPort: 80}},
			}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: ip,
		},
	}
}

func probeNode(name, ip string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: ip}},
		},
	}
}

func completedProbeJob(lab *labv1alpha1.NetworkProbeLab, sourceNode, namespace string, checks []probe.Check) *batchv1.Job {
	nodeChecks := checksForSource(checks, sourceNode)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      probeJobName(lab.Name, sourceNode),
			UID:       types.UID(probeJobName(lab.Name, sourceNode) + "-uid"),
			Labels:    probeLabels(lab.Name),
			Annotations: map[string]string{
				"lab.smtx.io/source-node":  sourceNode,
				"lab.smtx.io/checks-hash":  checksHash(nodeChecks),
				"lab.smtx.io/generation":   fmt.Sprint(lab.Generation),
				"lab.smtx.io/checks-count": fmt.Sprint(len(nodeChecks)),
			},
		},
		Status: batchv1.JobStatus{Succeeded: 1},
	}
}

func checksForSource(checks []probe.Check, sourceNode string) []probe.Check {
	out := make([]probe.Check, 0, len(checks))
	for _, check := range checks {
		if check.SourceNode == sourceNode {
			out = append(out, check)
		}
	}
	return out
}

func probeJobPod(job *batchv1.Job, report probe.Report) *corev1.Pod {
	payload, _ := json.Marshal(report)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: job.Namespace,
			Name:      job.Name + "-pod",
			Labels:    map[string]string{"job-name": job.Name},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "batch/v1",
				Kind:       "Job",
				Name:       job.Name,
				UID:        job.UID,
			}},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "probe",
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
					ExitCode: 0,
					Message:  string(payload),
				}},
			}},
		},
	}
}

func gunzipJSON(payload []byte, out any) error {
	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
