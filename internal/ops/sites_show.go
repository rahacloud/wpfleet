package ops

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/helm"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/values"
)

// Values the chart names a site's objects by.
const (
	valueBackupEnabled = "backup.enabled"
	valueExistingClaim = "uploads.existingClaim"
	// fullnameLength is where the chart's wordpress.fullname helper cuts
	// the site name.
	fullnameLength = 50
)

// SiteDetail is one site in one environment in detail.
type SiteDetail struct {
	// Site is the site's name.
	Site string `json:"site"`
	// Env is the environment.
	Env string `json:"env"`
	// Namespace is the environment's namespace.
	Namespace string `json:"namespace"`
	// Host is the site's canonical host, from its values.
	Host string `json:"host"`
	// Image is the image its values deploy.
	Image string `json:"image"`
	// Release is what the release runs; nil when the site is not installed.
	Release *helm.Release `json:"release"`
	// Pods are the site's pods, backup pods left out.
	Pods []SitePod `json:"pods"`
	// Volume is the site's uploads claim.
	Volume SiteVolume `json:"volume"`
	// Backup is the state of the site's backup CronJob.
	Backup SiteBackup `json:"backup"`
}

// SitePod is one pod of a site.
type SitePod struct {
	// Name is the pod's name.
	Name string `json:"name"`
	// Node is the node it runs on; empty while it is not scheduled.
	Node string `json:"node"`
	// Phase is the pod's phase, e.g. Running.
	Phase string `json:"phase"`
	// Ready says whether the pod is ready.
	Ready bool `json:"ready"`
}

// SiteVolume is a site's uploads claim.
type SiteVolume struct {
	// Claim is the PersistentVolumeClaim's name.
	Claim string `json:"claim"`
	// Found says whether the claim exists.
	Found bool `json:"found"`
	// Phase is the claim's phase, e.g. Bound.
	Phase string `json:"phase,omitempty"`
	// Capacity is the claim's capacity, e.g. 10Gi.
	Capacity string `json:"capacity,omitempty"`
	// StorageClass is the claim's storage class.
	StorageClass string `json:"storageClass,omitempty"`
}

// SiteBackup is the state of a site's backup CronJob.
type SiteBackup struct {
	// Enabled says whether the values turn the backup on.
	Enabled bool `json:"enabled"`
	// CronJob is the CronJob's name.
	CronJob string `json:"cronJob"`
	// Found says whether the CronJob exists.
	Found bool `json:"found"`
	// LastSuccess is when a backup last succeeded, if ever.
	LastSuccess *time.Time `json:"lastSuccess"`
}

// ShowOptions are the options of ShowSite.
type ShowOptions struct {
	// Cluster reads the site's objects; nil uses the environment's kube
	// client.
	Cluster Cluster
	// Releases reads the site's release; nil uses the environment's Helm
	// client.
	Releases ReleaseStatus
}

// ShowSite gathers one site's detail from its values, its release and its
// cluster: host, image, release revision, pods, node, uploads volume and the
// last successful run of its backup CronJob. A site without a release has
// a nil Release.
func ShowSite(ctx context.Context, env *Env, site fleet.Site, opts ShowOptions) (SiteDetail, error) {
	defaults, err := chartDefaults(env.Fleet)
	if err != nil {
		return SiteDetail{}, err
	}

	v, host, image, err := siteValues(defaults, site)
	if err != nil {
		return SiteDetail{}, err
	}

	releases, err := statusOf(env, opts.Releases)
	if err != nil {
		return SiteDetail{}, err
	}

	cluster := opts.Cluster
	if cluster == nil {
		cluster, err = kubeCluster(env)
		if err != nil {
			return SiteDetail{}, err
		}
	}

	ns := env.Env.Namespace

	var rel *helm.Release

	status, err := releases.Status(site.Name, ns)

	switch {
	case errors.Is(err, helm.ErrReleaseNotFound):
	case err != nil:
		return SiteDetail{}, err
	default:
		rel = &status
	}

	pods, vol, bak, err := observe(ctx, cluster, ns, site.Name, v)
	if err != nil {
		return SiteDetail{}, err
	}

	return SiteDetail{
		Site: site.Name, Env: env.Env.Name, Namespace: ns, Host: host, Image: image.String(),
		Release: rel, Pods: pods, Volume: vol, Backup: bak,
	}, nil
}

// observe reads a site's pods, uploads claim and backup CronJob.
func observe(
	ctx context.Context, cluster Cluster, namespace, site string, v values.Values,
) ([]SitePod, SiteVolume, SiteBackup, error) {
	list, err := cluster.SitePods(ctx, site)
	if err != nil {
		return nil, SiteVolume{}, SiteBackup{}, err
	}

	pods := make([]SitePod, 0, len(list))
	for _, p := range list {
		pods = append(pods, SitePod{Name: p.Name, Node: p.Spec.NodeName, Phase: string(p.Status.Phase), Ready: kube.Ready(p)})
	}

	vol, err := siteVolume(ctx, cluster, namespace, claimName(v))
	if err != nil {
		return nil, SiteVolume{}, SiteBackup{}, err
	}

	bak, err := siteBackup(ctx, cluster, namespace, fullname(v)+"-backup", v.Bool(valueBackupEnabled))
	if err != nil {
		return nil, SiteVolume{}, SiteBackup{}, err
	}

	return pods, vol, bak, nil
}

// fullname is the chart's wordpress.fullname: the site value, cut to 50
// characters.
func fullname(v values.Values) string {
	name := v.String(fleet.ValueSite)
	if len(name) > fullnameLength {
		name = name[:fullnameLength]
	}

	return name
}

// claimName is the uploads claim the chart mounts: uploads.existingClaim, or
// the one it creates.
func claimName(v values.Values) string {
	if claim := v.String(valueExistingClaim); claim != "" {
		return claim
	}

	return fullname(v) + "-uploads"
}

// siteVolume reads the uploads claim; a missing claim is not an error.
func siteVolume(ctx context.Context, cluster Cluster, namespace, claim string) (SiteVolume, error) {
	out := SiteVolume{Claim: claim, Found: false, Phase: "", Capacity: "", StorageClass: ""}

	//nolint:exhaustruct_v5 // get options are all optional
	pvc, err := cluster.Clientset().CoreV1().PersistentVolumeClaims(namespace).Get(ctx, claim, metav1.GetOptions{})

	switch {
	case apierrors.IsNotFound(err):
		return out, nil
	case err != nil:
		return SiteVolume{}, fmt.Errorf("reading claim %s: %w", claim, err)
	}

	out.Found = true
	out.Phase = string(pvc.Status.Phase)

	if capacity, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
		out.Capacity = capacity.String()
	} else if request, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		out.Capacity = request.String()
	}

	if pvc.Spec.StorageClassName != nil {
		out.StorageClass = *pvc.Spec.StorageClassName
	}

	return out, nil
}

// siteBackup reads the backup CronJob; a missing one is not an error.
func siteBackup(ctx context.Context, cluster Cluster, namespace, cronJob string, enabled bool) (SiteBackup, error) {
	out := SiteBackup{Enabled: enabled, CronJob: cronJob, Found: false, LastSuccess: nil}

	//nolint:exhaustruct_v5 // get options are all optional
	cj, err := cluster.Clientset().BatchV1().CronJobs(namespace).Get(ctx, cronJob, metav1.GetOptions{})

	switch {
	case apierrors.IsNotFound(err):
		return out, nil
	case err != nil:
		return SiteBackup{}, fmt.Errorf("reading CronJob %s: %w", cronJob, err)
	}

	out.Found = true

	if t := cj.Status.LastSuccessfulTime; t != nil {
		out.LastSuccess = new(t.UTC())
	}

	return out, nil
}

// BackupEnabled says whether the site's values over the chart's defaults
// turn its backup CronJob on.
func BackupEnabled(env *Env, site fleet.Site) (bool, error) {
	v, err := env.ChartValues(site)
	if err != nil {
		return false, err
	}

	return v.Bool(valueBackupEnabled), nil
}
