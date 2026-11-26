package kube_test

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rahacloud/wpfleet/internal/infra/kube/kubetest"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

func TestCreateJob(t *testing.T) {
	t.Parallel()

	k := kubetest.New(ns)

	//nolint:exhaustruct_v5 // a name and a stray namespace are what the test needs
	job := batchv1.Job{Name: "blog-restore", Namespace: "elsewhere"}

	created, err := k.CreateJob(t.Context(), job)
	testutil.NoError(t, err)
	testutil.Equal(t, "namespace", created.Namespace, ns)

	//nolint:exhaustruct_v5 // get options are all optional
	_, err = k.Clientset.BatchV1().Jobs(ns).Get(t.Context(), "blog-restore", metav1.GetOptions{})
	testutil.NoError(t, err)

	_, err = k.CreateJob(t.Context(), job)
	testutil.Error(t, err, "creating the same Job twice")
}
