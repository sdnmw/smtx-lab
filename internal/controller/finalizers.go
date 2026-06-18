package controller

import (
	"context"

	labv1alpha1 "github.com/smtx-lab/smtx-lab-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	networkProbeLabFinalizer     = "networkprobelabs.lab.smtx.io/finalizer"
	resourceAnalyzerLabFinalizer = "resourceanalyzerlabs.lab.smtx.io/finalizer"
)

func hasFinalizer(obj metav1.Object, finalizer string) bool {
	for _, item := range obj.GetFinalizers() {
		if item == finalizer {
			return true
		}
	}
	return false
}

func addFinalizer(obj metav1.Object, finalizer string) bool {
	if hasFinalizer(obj, finalizer) {
		return false
	}
	obj.SetFinalizers(append(obj.GetFinalizers(), finalizer))
	return true
}

func removeFinalizer(obj metav1.Object, finalizer string) bool {
	finalizers := obj.GetFinalizers()
	next := finalizers[:0]
	removed := false
	for _, item := range finalizers {
		if item == finalizer {
			removed = true
			continue
		}
		next = append(next, item)
	}
	if removed {
		obj.SetFinalizers(next)
	}
	return removed
}

func (r *NetworkProbeLabReconciler) cleanupNetworkProbeArtifacts(ctx context.Context, lab *labv1alpha1.NetworkProbeLab) error {
	namespace := r.reportNamespace()
	match := map[string]string{"lab.smtx.io/networkprobe-hash": shortHash(lab.Name)}
	if err := r.deleteJobsByLabels(ctx, namespace, match); err != nil {
		return err
	}
	return r.deleteConfigMapsByLabels(ctx, namespace, match)
}

func (r *ResourceAnalyzerLabReconciler) cleanupResourceAnalyzerArtifacts(ctx context.Context, lab *labv1alpha1.ResourceAnalyzerLab) error {
	namespace := r.reportNamespace()
	return r.deleteConfigMapsByLabels(ctx, namespace, map[string]string{"lab.smtx.io/resourceanalyzer": lab.Name})
}

func (r *NetworkProbeLabReconciler) deleteJobsByLabels(ctx context.Context, namespace string, match map[string]string) error {
	var jobs batchv1.JobList
	if err := r.List(ctx, &jobs, client.InNamespace(namespace), client.MatchingLabels(match)); err != nil {
		return err
	}
	for i := range jobs.Items {
		job := jobs.Items[i]
		if err := r.Delete(ctx, &job); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *NetworkProbeLabReconciler) deleteConfigMapsByLabels(ctx context.Context, namespace string, match map[string]string) error {
	return deleteConfigMapsByLabels(ctx, r.Client, namespace, match)
}

func (r *ResourceAnalyzerLabReconciler) deleteConfigMapsByLabels(ctx context.Context, namespace string, match map[string]string) error {
	return deleteConfigMapsByLabels(ctx, r.Client, namespace, match)
}

func deleteConfigMapsByLabels(ctx context.Context, c client.Client, namespace string, match map[string]string) error {
	var configMaps corev1.ConfigMapList
	if err := c.List(ctx, &configMaps, client.InNamespace(namespace), client.MatchingLabels(match)); err != nil {
		return err
	}
	for i := range configMaps.Items {
		configMap := configMaps.Items[i]
		if err := c.Delete(ctx, &configMap); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}
