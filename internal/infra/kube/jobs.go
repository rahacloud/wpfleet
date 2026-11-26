package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ErrJobFailed is returned by WaitJob for a Job that failed.
var ErrJobFailed = errors.New("job failed")

// annotationInstantiate marks a Job started by hand from a CronJob, as
// kubectl create job --from does.
const annotationInstantiate = "cronjob.kubernetes.io/instantiate"

// labelJobName is the label the Job controller puts on a Job's pods.
const labelJobName = "batch.kubernetes.io/job-name"

// JobFromCronJob creates a Job named jobName from the CronJob's job template,
// owned by the CronJob so that it is cleaned up with it.
func (c *Client) JobFromCronJob(ctx context.Context, cronJob, jobName string) (batchv1.Job, error) {
	//nolint:exhaustruct_v5 // get options are all optional
	cj, err := c.clientset.BatchV1().CronJobs(c.namespace).Get(ctx, cronJob, metav1.GetOptions{})
	if err != nil {
		return batchv1.Job{}, fmt.Errorf("reading CronJob %s: %w", cronJob, err)
	}

	job := NewJobFromCronJob(*cj, jobName)

	//nolint:exhaustruct_v5 // create options are all optional
	created, err := c.clientset.BatchV1().Jobs(c.namespace).Create(ctx, &job, metav1.CreateOptions{})
	if err != nil {
		return batchv1.Job{}, fmt.Errorf("creating Job %s: %w", jobName, err)
	}

	return *created, nil
}

// NewJobFromCronJob builds the Job that JobFromCronJob creates.
func NewJobFromCronJob(cj batchv1.CronJob, jobName string) batchv1.Job {
	tmpl := cj.Spec.JobTemplate

	annotations := maps.Clone(tmpl.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}

	annotations[annotationInstantiate] = "manual"

	controller := true

	//nolint:exhaustruct_v5 // a Job to create: the server fills in the rest
	return batchv1.Job{
		APIVersion:  "batch/v1",
		Kind:        "Job",
		Name:        jobName,
		Namespace:   cj.Namespace,
		Labels:      maps.Clone(tmpl.Labels),
		Annotations: annotations,
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: "batch/v1",
			Kind:       "CronJob",
			Name:       cj.Name,
			UID:        cj.UID,
			Controller: &controller,
		}},
		Spec: *tmpl.Spec.DeepCopy(),
	}
}

// WaitJob polls the Job every PollInterval until it completes or fails, or
// ctx ends.
func (c *Client) WaitJob(ctx context.Context, name string) error {
	ticker := time.NewTicker(c.PollInterval)
	defer ticker.Stop()

	for {
		//nolint:exhaustruct_v5 // get options are all optional
		job, err := c.clientset.BatchV1().Jobs(c.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("reading Job %s: %w", name, err)
		}

		done, err := JobDone(*job)
		if done {
			return err
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for Job %s: %w", name, context.Cause(ctx))
		case <-ticker.C:
		}
	}
}

// JobDone reports whether a Job has finished, and its error when it failed.
func JobDone(job batchv1.Job) (bool, error) {
	for _, cond := range job.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}

		switch cond.Type {
		case batchv1.JobComplete:
			return true, nil
		case batchv1.JobFailed:
			return true, fmt.Errorf("%w: %s: %s: %s", ErrJobFailed, job.Name, cond.Reason, cond.Message)
		case batchv1.JobSuspended, batchv1.JobFailureTarget, batchv1.JobSuccessCriteriaMet:
			// Complete or Failed follows these; wait for it.
		}
	}

	return false, nil
}

// JobPods lists the pods of a Job, oldest first.
func (c *Client) JobPods(ctx context.Context, job string) ([]corev1.Pod, error) {
	//nolint:exhaustruct_v5 // a label selector is the only option needed
	opts := metav1.ListOptions{LabelSelector: labelJobName + "=" + job}

	list, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing pods of Job %s: %w", job, err)
	}

	pods := list.Items
	slices.SortFunc(pods, func(a, b corev1.Pod) int {
		return a.CreationTimestamp.Compare(b.CreationTimestamp.Time)
	})

	return pods, nil
}

// StreamLogs follows the log of a container into w until the container ends
// or ctx does.
func (c *Client) StreamLogs(ctx context.Context, pod, container string, w io.Writer) error {
	//nolint:exhaustruct_v5 // follow one container; the rest are optional
	opts := &corev1.PodLogOptions{Container: container, Follow: true}

	stream, err := c.clientset.CoreV1().Pods(c.namespace).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		return fmt.Errorf("reading logs of %s/%s: %w", pod, container, err)
	}

	defer func() { _ = stream.Close() }()

	_, err = io.Copy(w, stream)
	if err != nil {
		return fmt.Errorf("reading logs of %s/%s: %w", pod, container, err)
	}

	return nil
}
