package kube

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JobCreator creates a Job that wpfleet built itself rather than one taken
// from a CronJob, such as the restore Job of wpfleet backup restore.
type JobCreator interface {
	// CreateJob creates job in the client's namespace and returns it as the
	// API server stored it.
	CreateJob(ctx context.Context, job batchv1.Job) (batchv1.Job, error)
}

var _ JobCreator = (*Client)(nil)

// CreateJob creates job in the client's namespace, whatever namespace job
// names.
func (c *Client) CreateJob(ctx context.Context, job batchv1.Job) (batchv1.Job, error) {
	job.Namespace = c.namespace

	//nolint:exhaustruct_v5 // create options are all optional
	created, err := c.clientset.BatchV1().Jobs(c.namespace).Create(ctx, &job, metav1.CreateOptions{})
	if err != nil {
		return batchv1.Job{}, fmt.Errorf("creating Job %s: %w", job.Name, err)
	}

	return *created, nil
}
