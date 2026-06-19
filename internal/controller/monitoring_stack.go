package controller

import (
	"context"
	"fmt"

	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	monitoringPrometheusName       = "smtx-lab-prometheus"
	monitoringGrafanaName          = "smtx-lab-grafana"
	monitoringKubeStateMetricsName = "kube-state-metrics"
	defaultPrometheusImage         = "registry.cn-hangzhou.aliyuncs.com/smtxlab/prometheus:v2.53.1"
	defaultKubeStateMetricsImage   = "registry.cn-hangzhou.aliyuncs.com/smtxlab/kube-state-metrics:v2.13.0"
	defaultGrafanaImage            = "registry.cn-hangzhou.aliyuncs.com/smtxlab/grafana:11.1.4"
)

type monitoringApplyResult struct {
	Namespace         string
	PrometheusURL     string
	PrometheusEnabled bool
	GrafanaEnabled    bool
	PrometheusReady   bool
	KubeStateMetrics  bool
}

func (r *ResourceAnalyzerLabReconciler) ensureMonitoringStack(ctx context.Context, lab *labv1alpha1.ResourceAnalyzerLab) (monitoringApplyResult, error) {
	namespace := r.reportNamespace()
	prometheusEnabled := lab.Spec.MonitoringStack.Prometheus.Enabled || !lab.Spec.MonitoringStack.Grafana.Enabled
	grafanaEnabled := lab.Spec.MonitoringStack.Grafana.Enabled
	result := monitoringApplyResult{
		Namespace:         namespace,
		PrometheusURL:     defaultPrometheusURL(namespace),
		PrometheusEnabled: prometheusEnabled,
		GrafanaEnabled:    grafanaEnabled,
		KubeStateMetrics:  prometheusEnabled,
	}
	if prometheusEnabled {
		if err := r.upsertConfigMap(ctx, prometheusConfigMap(namespace)); err != nil {
			return result, err
		}
		if err := r.upsertDeployment(ctx, kubeStateMetricsDeployment(namespace)); err != nil {
			return result, err
		}
		if err := r.upsertService(ctx, kubeStateMetricsService(namespace)); err != nil {
			return result, err
		}
		if err := r.upsertDeployment(ctx, prometheusDeployment(namespace)); err != nil {
			return result, err
		}
		if err := r.upsertService(ctx, prometheusService(namespace)); err != nil {
			return result, err
		}
		ready, err := r.deploymentAvailable(ctx, namespace, monitoringPrometheusName)
		if err != nil {
			return result, err
		}
		result.PrometheusReady = ready
	}
	if grafanaEnabled {
		if err := r.upsertDeployment(ctx, grafanaDeployment(namespace)); err != nil {
			return result, err
		}
		if err := r.upsertService(ctx, grafanaService(namespace)); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (r *ResourceAnalyzerLabReconciler) upsertConfigMap(ctx context.Context, desired *corev1.ConfigMap) error {
	var existing corev1.ConfigMap
	key := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
	if err := r.Get(ctx, key, &existing); err != nil {
		if apierrors.IsNotFound(err) {
			return r.Create(ctx, desired)
		}
		return err
	}
	existing.Labels = mergeStringMap(existing.Labels, desired.Labels)
	existing.Data = desired.Data
	existing.BinaryData = desired.BinaryData
	return r.Update(ctx, &existing)
}

func (r *ResourceAnalyzerLabReconciler) upsertDeployment(ctx context.Context, desired *appsv1.Deployment) error {
	var existing appsv1.Deployment
	key := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
	if err := r.Get(ctx, key, &existing); err != nil {
		if apierrors.IsNotFound(err) {
			return r.Create(ctx, desired)
		}
		return err
	}
	existing.Labels = mergeStringMap(existing.Labels, desired.Labels)
	existing.Spec = desired.Spec
	return r.Update(ctx, &existing)
}

func (r *ResourceAnalyzerLabReconciler) upsertService(ctx context.Context, desired *corev1.Service) error {
	var existing corev1.Service
	key := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
	if err := r.Get(ctx, key, &existing); err != nil {
		if apierrors.IsNotFound(err) {
			return r.Create(ctx, desired)
		}
		return err
	}
	clusterIP := existing.Spec.ClusterIP
	clusterIPs := existing.Spec.ClusterIPs
	existing.Labels = mergeStringMap(existing.Labels, desired.Labels)
	existing.Spec = desired.Spec
	existing.Spec.ClusterIP = clusterIP
	existing.Spec.ClusterIPs = clusterIPs
	return r.Update(ctx, &existing)
}

func (r *ResourceAnalyzerLabReconciler) deploymentAvailable(ctx context.Context, namespace, name string) (bool, error) {
	var deployment appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &deployment); err != nil {
		return false, err
	}
	if deployment.Status.AvailableReplicas <= 0 {
		return false, nil
	}
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentAvailable && condition.Status == corev1.ConditionTrue {
			return true, nil
		}
	}
	return false, nil
}

func prometheusConfigMap(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      monitoringPrometheusName,
			Labels:    monitoringLabels("prometheus"),
		},
		Data: map[string]string{
			"prometheus.yml": `global:
  scrape_interval: 30s
  evaluation_interval: 30s
scrape_configs:
  - job_name: kubernetes-cadvisor
    scheme: https
    kubernetes_sd_configs:
      - role: node
    tls_config:
      insecure_skip_verify: true
    bearer_token_file: /var/run/secrets/kubernetes.io/serviceaccount/token
    metrics_path: /metrics/cadvisor
    relabel_configs:
      - action: labelmap
        regex: __meta_kubernetes_node_label_(.+)
  - job_name: kube-state-metrics
    kubernetes_sd_configs:
      - role: endpoints
    relabel_configs:
      - source_labels: [__meta_kubernetes_service_name]
        action: keep
        regex: kube-state-metrics|smtx-lab-kube-state-metrics
`,
		},
	}
}

func prometheusDeployment(namespace string) *appsv1.Deployment {
	replicas := int32(1)
	labels := monitoringLabels("prometheus")
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      monitoringPrometheusName,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: "smtx-lab-operator",
					Containers: []corev1.Container{
						{
							Name:  "prometheus",
							Image: defaultPrometheusImage,
							Args: []string{
								"--config.file=/etc/prometheus/prometheus.yml",
								"--storage.tsdb.path=/prometheus",
								"--storage.tsdb.retention.time=15d",
								"--web.enable-lifecycle",
							},
							Ports: []corev1.ContainerPort{{Name: "web", ContainerPort: 9090}},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "config", MountPath: "/etc/prometheus"},
								{Name: "data", MountPath: "/prometheus"},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("256Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1000m"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: monitoringPrometheusName}}}},
						{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
}

func prometheusService(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      monitoringPrometheusName,
			Labels:    monitoringLabels("prometheus"),
		},
		Spec: corev1.ServiceSpec{
			Selector: monitoringLabels("prometheus"),
			Ports: []corev1.ServicePort{{
				Name:       "web",
				Port:       9090,
				TargetPort: intstr.FromString("web"),
			}},
		},
	}
}

func kubeStateMetricsDeployment(namespace string) *appsv1.Deployment {
	replicas := int32(1)
	labels := monitoringLabels("kube-state-metrics")
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      monitoringKubeStateMetricsName,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: "smtx-lab-operator",
					Containers: []corev1.Container{
						{
							Name:  "kube-state-metrics",
							Image: defaultKubeStateMetricsImage,
							Args: []string{
								"--port=8080",
								"--telemetry-port=8081",
							},
							Ports: []corev1.ContainerPort{
								{Name: "http", ContainerPort: 8080},
								{Name: "telemetry", ContainerPort: 8081},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
							},
						},
					},
				},
			},
		},
	}
}

func kubeStateMetricsService(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      monitoringKubeStateMetricsName,
			Labels:    monitoringLabels("kube-state-metrics"),
		},
		Spec: corev1.ServiceSpec{
			Selector: monitoringLabels("kube-state-metrics"),
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       8080,
					TargetPort: intstr.FromString("http"),
				},
				{
					Name:       "telemetry",
					Port:       8081,
					TargetPort: intstr.FromString("telemetry"),
				},
			},
		},
	}
}

func grafanaDeployment(namespace string) *appsv1.Deployment {
	replicas := int32(1)
	labels := monitoringLabels("grafana")
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      monitoringGrafanaName,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "grafana",
							Image: defaultGrafanaImage,
							Env: []corev1.EnvVar{
								{Name: "GF_SECURITY_ADMIN_USER", Value: "admin"},
								{Name: "GF_SECURITY_ADMIN_PASSWORD", Value: "admin"},
								{Name: "GF_AUTH_ANONYMOUS_ENABLED", Value: "true"},
								{Name: "GF_AUTH_ANONYMOUS_ORG_ROLE", Value: "Viewer"},
							},
							Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 3000}},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
							},
						},
					},
				},
			},
		},
	}
}

func grafanaService(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      monitoringGrafanaName,
			Labels:    monitoringLabels("grafana"),
		},
		Spec: corev1.ServiceSpec{
			Selector: monitoringLabels("grafana"),
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       3000,
				TargetPort: intstr.FromString("http"),
			}},
		},
	}
}

func monitoringLabels(component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "smtx-lab-operator",
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/managed-by": "smtx-lab-operator",
	}
}

func defaultPrometheusURL(namespace string) string {
	return fmt.Sprintf("http://%s.%s.svc:9090", monitoringPrometheusName, namespace)
}

func mergeStringMap(existing, desired map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range existing {
		out[k] = v
	}
	for k, v := range desired {
		out[k] = v
	}
	return out
}
