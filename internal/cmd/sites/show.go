package sites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/helm"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/values"
)

// ErrOneSite is returned when sites show is not given exactly one site.
var ErrOneSite = errors.New("sites show takes exactly one site")

// Values the chart names a site's objects by.
const (
	valueBackupEnabled = "backup.enabled"
	valueExistingClaim = "uploads.existingClaim"
	// fullnameLength is where the chart's wordpress.fullname helper cuts
	// the site name.
	fullnameLength = 50
)

// timeLayout is how sites show prints a time, always in UTC.
const timeLayout = "2006-01-02 15:04:05 UTC"

// Detail is one site in one environment, as sites show prints it.
type Detail struct {
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
	Pods []Pod `json:"pods"`
	// Volume is the site's uploads claim.
	Volume Volume `json:"volume"`
	// Backup is the state of the site's backup CronJob.
	Backup Backup `json:"backup"`
}

// Pod is one pod of a site.
type Pod struct {
	// Name is the pod's name.
	Name string `json:"name"`
	// Node is the node it runs on; empty while it is not scheduled.
	Node string `json:"node"`
	// Phase is the pod's phase, e.g. Running.
	Phase string `json:"phase"`
	// Ready says whether the pod is ready.
	Ready bool `json:"ready"`
}

// Volume is a site's uploads claim.
type Volume struct {
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

// Backup is the state of a site's backup CronJob.
type Backup struct {
	// Enabled says whether the values turn the backup on.
	Enabled bool `json:"enabled"`
	// CronJob is the CronJob's name.
	CronJob string `json:"cronJob"`
	// Found says whether the CronJob exists.
	Found bool `json:"found"`
	// LastSuccess is when a backup last succeeded, if ever.
	LastSuccess *time.Time `json:"lastSuccess"`
}

// showCommand builds wpfleet sites show.
func showCommand(d deps) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:      "show",
		Usage:     "one site of --env: host, image, release, pods, node, volume and last backup",
		ArgsUsage: "<site>",
		Description: "The last backup is the last successful run of the site's backup CronJob; " +
			"wpfleet backup list shows the backups in S3.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runShow(ctx, cmd, d)
		},
	}
}

// runShow shows one site.
func runShow(ctx context.Context, cmd *cli.Command, d deps) error {
	if cmd.Args().Len() != 1 {
		return ErrOneSite
	}

	format, err := cmdutil.ParseFormat(cmd.String(cmdutil.FlagOutput))
	if err != nil {
		return err
	}

	env, err := cmdutil.OpenEnv(cmd)
	if err != nil {
		return err
	}

	site, err := env.Site(cmd.Args().First())
	if err != nil {
		return err
	}

	detail, err := describe(ctx, env, site, d)
	if err != nil {
		return err
	}

	w := cmd.Root().Writer
	if format == cmdutil.FormatJSON {
		return writeJSON(w, detail)
	}

	return cmdutil.Write(w, format, detail.fields(), fieldColumns()...)
}

// describe gathers a site's detail from its values, its release and its
// cluster.
func describe(ctx context.Context, env *cmdutil.Env, site fleet.Site, d deps) (Detail, error) {
	defaults, err := chartDefaults(env.Fleet)
	if err != nil {
		return Detail{}, err
	}

	v, host, image, err := siteValues(defaults, site)
	if err != nil {
		return Detail{}, err
	}

	releases, err := d.releases(env)
	if err != nil {
		return Detail{}, err
	}

	cluster, err := d.cluster(env)
	if err != nil {
		return Detail{}, err
	}

	ns := env.Env.Namespace

	var rel *helm.Release

	status, err := releases.Status(site.Name, ns)

	switch {
	case errors.Is(err, helm.ErrReleaseNotFound):
	case err != nil:
		return Detail{}, err
	default:
		rel = &status
	}

	pods, vol, bak, err := observe(ctx, cluster, ns, site.Name, v)
	if err != nil {
		return Detail{}, err
	}

	return Detail{
		Site: site.Name, Env: env.Env.Name, Namespace: ns, Host: host, Image: image.String(),
		Release: rel, Pods: pods, Volume: vol, Backup: bak,
	}, nil
}

// observe reads a site's pods, uploads claim and backup CronJob.
func observe(
	ctx context.Context, cluster Cluster, namespace, site string, v values.Values,
) ([]Pod, Volume, Backup, error) {
	list, err := cluster.SitePods(ctx, site)
	if err != nil {
		return nil, Volume{}, Backup{}, err
	}

	pods := make([]Pod, 0, len(list))
	for _, p := range list {
		pods = append(pods, Pod{Name: p.Name, Node: p.Spec.NodeName, Phase: string(p.Status.Phase), Ready: kube.Ready(p)})
	}

	vol, err := volume(ctx, cluster, namespace, claimName(v))
	if err != nil {
		return nil, Volume{}, Backup{}, err
	}

	bak, err := backup(ctx, cluster, namespace, fullname(v)+"-backup", v.Bool(valueBackupEnabled))
	if err != nil {
		return nil, Volume{}, Backup{}, err
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

// volume reads the uploads claim; a missing claim is not an error.
func volume(ctx context.Context, cluster Cluster, namespace, claim string) (Volume, error) {
	out := Volume{Claim: claim, Found: false, Phase: "", Capacity: "", StorageClass: ""}

	//nolint:exhaustruct_v5 // get options are all optional
	pvc, err := cluster.Clientset().CoreV1().PersistentVolumeClaims(namespace).Get(ctx, claim, metav1.GetOptions{})

	switch {
	case apierrors.IsNotFound(err):
		return out, nil
	case err != nil:
		return Volume{}, fmt.Errorf("reading claim %s: %w", claim, err)
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

// backup reads the backup CronJob; a missing one is not an error.
func backup(ctx context.Context, cluster Cluster, namespace, cronJob string, enabled bool) (Backup, error) {
	out := Backup{Enabled: enabled, CronJob: cronJob, Found: false, LastSuccess: nil}

	//nolint:exhaustruct_v5 // get options are all optional
	cj, err := cluster.Clientset().BatchV1().CronJobs(namespace).Get(ctx, cronJob, metav1.GetOptions{})

	switch {
	case apierrors.IsNotFound(err):
		return out, nil
	case err != nil:
		return Backup{}, fmt.Errorf("reading CronJob %s: %w", cronJob, err)
	}

	out.Found = true

	if t := cj.Status.LastSuccessfulTime; t != nil {
		out.LastSuccess = new(t.UTC())
	}

	return out, nil
}

// field is one line of sites show's table.
type field struct {
	name, value string
}

// fieldColumns are the columns of sites show's table.
func fieldColumns() []cmdutil.Column[field] {
	return []cmdutil.Column[field]{
		{Header: "field", Value: func(f field) string { return f.name }},
		{Header: "value", Value: func(f field) string { return f.value }},
	}
}

// fields lays the detail out as lines for people.
func (d Detail) fields() []field {
	return []field{
		{"site", d.Site},
		{"env", d.Env},
		{"namespace", d.Namespace},
		{"host", d.Host},
		{"image", d.Image},
		{"release", d.releaseText()},
		{"pods", d.podsText()},
		{"node", d.nodesText()},
		{"volume", d.Volume.text()},
		{"last backup", d.Backup.text()},
	}
}

// releaseText describes the release.
func (d Detail) releaseText() string {
	r := d.Release
	if r == nil {
		return "not installed"
	}

	return fmt.Sprintf("revision %d, %s, chart %s, %s",
		r.Revision, r.Status, r.ChartVersion, r.Updated.UTC().Format(timeLayout))
}

// podsText describes the pods.
func (d Detail) podsText() string {
	if len(d.Pods) == 0 {
		return "none"
	}

	parts := make([]string, 0, len(d.Pods))

	for _, p := range d.Pods {
		state := "not ready"
		if p.Ready {
			state = "ready"
		}

		parts = append(parts, fmt.Sprintf("%s (%s, %s)", p.Name, p.Phase, state))
	}

	return strings.Join(parts, "; ")
}

// nodesText lists the nodes the pods run on.
func (d Detail) nodesText() string {
	var nodes []string

	for _, p := range d.Pods {
		if p.Node != "" && !slices.Contains(nodes, p.Node) {
			nodes = append(nodes, p.Node)
		}
	}

	return strings.Join(nodes, ", ")
}

// text describes the claim.
func (v Volume) text() string {
	if !v.Found {
		return v.Claim + " (not found)"
	}

	parts := slices.DeleteFunc([]string{v.Phase, v.Capacity, v.StorageClass}, func(s string) bool { return s == "" })

	return v.Claim + " (" + strings.Join(parts, ", ") + ")"
}

// text describes the last backup.
func (b Backup) text() string {
	switch {
	case !b.Enabled:
		return "disabled"
	case !b.Found:
		return "no CronJob " + b.CronJob
	case b.LastSuccess == nil:
		return "never"
	default:
		return b.LastSuccess.Format(timeLayout)
	}
}

// writeJSON writes the detail as one indented JSON object.
func writeJSON(w io.Writer, d Detail) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	err := enc.Encode(d)
	if err != nil {
		return fmt.Errorf("writing JSON: %w", err)
	}

	return nil
}
