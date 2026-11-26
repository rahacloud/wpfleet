package kube

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ErrNoRunningPod is returned when a site has no running, ready pod.
var ErrNoRunningPod = errors.New("no running and ready pod")

// SiteSelector is the label selector of a site's pods: its release, without
// the backup CronJob's pods.
func SiteSelector(site string) string {
	return LabelInstance + "=" + site + "," + LabelComponent + "!=" + ComponentBackup
}

// SitePods lists the pods of a site, backup pods left out, sorted by name.
func (c *Client) SitePods(ctx context.Context, site string) ([]corev1.Pod, error) {
	//nolint:exhaustruct_v5 // a label selector is the only option needed
	list, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{LabelSelector: SiteSelector(site)})
	if err != nil {
		return nil, fmt.Errorf("listing pods of %s: %w", site, err)
	}

	pods := list.Items
	slices.SortFunc(pods, func(a, b corev1.Pod) int { return cmp.Compare(a.Name, b.Name) })

	return pods, nil
}

// RunningPod returns a pod of the site that runs the WordPress container, is
// ready and is not being deleted; the newest when there are several.
func (c *Client) RunningPod(ctx context.Context, site string) (corev1.Pod, error) {
	pods, err := c.SitePods(ctx, site)
	if err != nil {
		return corev1.Pod{}, err
	}

	pods = slices.DeleteFunc(pods, func(p corev1.Pod) bool { return !Serving(p) })
	if len(pods) == 0 {
		return corev1.Pod{}, fmt.Errorf("%s in %s: %w", site, c.namespace, ErrNoRunningPod)
	}

	return slices.MaxFunc(pods, func(a, b corev1.Pod) int {
		return a.CreationTimestamp.Compare(b.CreationTimestamp.Time)
	}), nil
}

// Serving reports whether a pod runs, is ready, is not being deleted and has
// the WordPress container.
func Serving(p corev1.Pod) bool {
	if p.Status.Phase != corev1.PodRunning || p.DeletionTimestamp != nil {
		return false
	}

	if !slices.ContainsFunc(p.Spec.Containers, func(c corev1.Container) bool { return c.Name == WordPressContainer }) {
		return false
	}

	return Ready(p)
}

// Ready reports whether a pod's Ready condition is true.
func Ready(p corev1.Pod) bool {
	return slices.ContainsFunc(p.Status.Conditions, func(c corev1.PodCondition) bool {
		return c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue
	})
}
