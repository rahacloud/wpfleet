package ops

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	domain "github.com/rahacloud/wpfleet/internal/domain/backup"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/values"
)

// Names of the restore Job's containers, in the order they run.
const (
	ContainerFetch  = "fetch"
	ContainerImport = "import"
)

// Names of the backup CronJob's containers, in the order they run.
const (
	ContainerDump   = "dump"
	ContainerUpload = "upload"
)

// Limits of the restore Job.
const (
	restoreDeadline = 7200      // seconds; as long as the backup's own
	restoreTTL      = 7 * 86400 // seconds the finished Job stays to be read
)

// Paths inside the restore pod.
const (
	workDir    = "/work"
	uploadsDir = "/uploads"
)

// fetchScript copies the dump into the work volume and, with uploads, makes
// the uploads volume the mirror and then winds it back with the overlays.
// lost+found belongs to the file system, not to the site: rclone neither
// copies it nor deletes it.
const fetchScript = `base="s3:$BUCKET"
rclone copyto "$base/$DUMP" /work/database.sql.gz
ls -l /work/database.sql.gz
if [ "$UPLOADS" = true ]; then
  rclone sync "$base/$CURRENT" /uploads --exclude "/lost+found/**"
  for d in $OVERLAYS; do rclone copy "$base/$d" /uploads; done
  if [ -n "$UPLOADS_OWNER" ]; then chown -R "$UPLOADS_OWNER" /uploads; fi
  echo "restored the uploads from $CURRENT"
fi
`

// importScript imports the dump with the site's own user. The dump drops and
// recreates every table it holds; tables the dump does not hold are left.
const importScript = `set -o pipefail
gzip -t /work/database.sql.gz
gunzip -c /work/database.sql.gz | mariadb -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" "$DB_NAME"
echo "imported $DUMP into $DB_NAME"
`

// RestoreSpec is everything the restore Job is built from.
type RestoreSpec struct {
	// Name is the Job's name.
	Name string
	// Env is the name of the environment restored into.
	Env string
	// Site is the site restored into.
	Site domain.Site
	// Plan says what is restored, and from where in the bucket.
	Plan domain.RestorePlan
	// Bucket is the bucket the plan's keys are in.
	Bucket string
	// Endpoint is the bucket's endpoint as the cluster reaches it.
	Endpoint string
	// Tolerations are the site's, so the Job can run where its pod runs.
	Tolerations []corev1.Toleration
	// UploadsOwner is the user:group the restored uploads are given; empty
	// leaves them as rclone wrote them.
	UploadsOwner string
}

// RestoreJob builds the Job that restores a backup into a site: an init
// container with the chart's backup image copies the dump from S3 (and with
// uploads syncs the uploads back into the site's claim), then a container
// with the chart's MariaDB image imports the dump with the site's own user
// and password from the site's Secret. Like the backup CronJob, it mounts the
// site's uploads claim only when it needs it, and then it must run on the
// node of the site's pod: a ReadWriteOnce volume attaches to one node only.
func RestoreJob(spec RestoreSpec) batchv1.Job {
	labels := map[string]string{
		"app.kubernetes.io/name":       "wordpress",
		kube.LabelInstance:             spec.Site.Name,
		kube.LabelComponent:            kube.ComponentBackup,
		"app.kubernetes.io/managed-by": "wpfleet",
	}

	//nolint:exhaustruct_v5 // an empty emptyDir is the default one
	volumes := []corev1.Volume{podVolume("work", corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}})}

	var affinity *corev1.Affinity

	if spec.Plan.Uploads {
		//nolint:exhaustruct_v5 // a claim name is all a PVC volume needs
		volumes = append(volumes, podVolume("uploads", corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: spec.Site.Claim},
		}))
		affinity = BesideSite(spec.Site.Name)
	}

	backoff, deadline, ttl := int32(0), int64(restoreDeadline), int32(restoreTTL)

	//nolint:exhaustruct_v5 // a Job to create: the server fills in the rest
	return batchv1.Job{
		APIVersion: "batch/v1",
		Kind:       "Job",
		Name:       spec.Name,
		Labels:     labels,
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Labels: labels,
				Spec: corev1.PodSpec{
					RestartPolicy:  corev1.RestartPolicyNever,
					InitContainers: []corev1.Container{fetchContainer(spec)},
					Containers:     []corev1.Container{importContainer(spec)},
					Volumes:        volumes,
					Tolerations:    spec.Tolerations,
					Affinity:       affinity,
				},
			},
		},
	}
}

// BesideSite is the affinity that puts a pod on the node of the site's own
// pod, the one kube.Pods selects: the pod that holds the uploads volume. With
// a network-attached ReadWriteOnce volume, a pod on another node would wait
// for the attach until its deadline; with node-local storage, the volume's
// own node affinity already agrees.
func BesideSite(site string) *corev1.Affinity {
	//nolint:exhaustruct_v5 // only the pod affinity is set
	return &corev1.Affinity{
		PodAffinity: &corev1.PodAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
				//nolint:exhaustruct_v5 // a selector on two labels
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{kube.LabelInstance: site},
					MatchExpressions: []metav1.LabelSelectorRequirement{{
						Key:      kube.LabelComponent,
						Operator: metav1.LabelSelectorOpNotIn,
						Values:   []string{kube.ComponentBackup},
					}},
				},
				TopologyKey: corev1.LabelHostname,
			}},
		},
	}
}

// fetchContainer copies the backup out of S3 with the chart's rclone image.
func fetchContainer(spec RestoreSpec) corev1.Container {
	mounts := []corev1.VolumeMount{volumeMount("work", workDir, false)}
	if spec.Plan.Uploads {
		mounts = append(mounts, volumeMount("uploads", uploadsDir, false))
	}

	overlays := make([]string, 0, len(spec.Plan.Overlays))
	for _, s := range spec.Plan.Overlays {
		overlays = append(overlays, domain.ChangedKey(spec.Plan.Prefix, s))
	}

	//nolint:exhaustruct_v5 // a container sets only what it needs
	return corev1.Container{
		Name:    ContainerFetch,
		Image:   spec.Site.RcloneImage,
		Command: []string{"sh", "-ec", fetchScript},
		Env: append([]corev1.EnvVar{
			envVar("BUCKET", spec.Bucket),
			envVar("DUMP", spec.Plan.Dump),
			envVar("UPLOADS", strconv.FormatBool(spec.Plan.Uploads)),
			envVar("CURRENT", domain.CurrentKey(spec.Plan.Prefix)),
			envVar("OVERLAYS", strings.Join(overlays, " ")),
			envVar("UPLOADS_OWNER", spec.UploadsOwner),
			envVar("HOME", "/tmp"),
		}, rcloneEnv(spec)...),
		//nolint:exhaustruct_v5 // the backup CronJob's requests and limits
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")},
		},
		VolumeMounts: mounts,
	}
}

// importContainer imports the dump with the chart's MariaDB image, as the
// site's own user.
func importContainer(spec RestoreSpec) corev1.Container {
	//nolint:exhaustruct_v5 // a container sets only what it needs
	return corev1.Container{
		Name:    ContainerImport,
		Image:   spec.Site.MariaDBImage,
		Command: []string{"bash", "-ec", importScript},
		Env: []corev1.EnvVar{
			envVar("DB_HOST", spec.Site.DBHost),
			envVar("DB_PORT", spec.Site.DBPort),
			envVar("DB_NAME", spec.Site.DBName),
			envVar("DB_USER", spec.Site.DBUser),
			envVar("DUMP", spec.Plan.Dump),
			secretEnvVar("MYSQL_PWD", spec.Site.Secret, domain.DBPasswordKey),
		},
		VolumeMounts: []corev1.VolumeMount{volumeMount("work", workDir, true)},
	}
}

// rcloneEnv configures rclone's s3 remote the way the backup CronJob does.
func rcloneEnv(spec RestoreSpec) []corev1.EnvVar {
	return []corev1.EnvVar{
		envVar("RCLONE_CONFIG_S3_TYPE", "s3"),
		envVar("RCLONE_CONFIG_S3_PROVIDER", "Other"),
		envVar("RCLONE_CONFIG_S3_ENDPOINT", spec.Endpoint),
		secretEnvVar("RCLONE_CONFIG_S3_ACCESS_KEY_ID", spec.Site.S3Secret, "access-key-id"),
		secretEnvVar("RCLONE_CONFIG_S3_SECRET_ACCESS_KEY", spec.Site.S3Secret, "secret-access-key"),
	}
}

// envVar is a plain environment variable.
func envVar(name, value string) corev1.EnvVar {
	return corev1.EnvVar{Name: name, Value: value, ValueFrom: nil}
}

// secretEnvVar is an environment variable from a Secret's key.
func secretEnvVar(name, secret, key string) corev1.EnvVar {
	//nolint:exhaustruct_v5 // a secret key reference is the only source used
	return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{
		SecretKeyRef: &corev1.SecretKeySelector{Name: secret, Key: key},
	}}
}

// podVolume is a named volume.
func podVolume(name string, src corev1.VolumeSource) corev1.Volume {
	return corev1.Volume{Name: name, VolumeSource: src}
}

// volumeMount mounts a volume at path.
func volumeMount(name, path string, readOnly bool) corev1.VolumeMount {
	//nolint:exhaustruct_v5 // a plain mount
	return corev1.VolumeMount{Name: name, MountPath: path, ReadOnly: readOnly}
}

// PodOptions reads the site's tolerations and the owner its uploads must
// have from its values: podSecurityContext.fsGroup, which is www-data in the
// official image, as both user and group.
func PodOptions(v values.Values) ([]corev1.Toleration, string, error) {
	var tolerations []corev1.Toleration

	if raw, ok := v.Lookup("tolerations"); ok && raw != nil {
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, "", fmt.Errorf("tolerations: %w", err)
		}

		err = json.Unmarshal(data, &tolerations)
		if err != nil {
			return nil, "", fmt.Errorf("tolerations: %w", err)
		}
	}

	owner := ""

	if g := v.String("podSecurityContext.fsGroup"); g != "" {
		id, err := strconv.ParseInt(g, 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("podSecurityContext.fsGroup %q: %w", g, err)
		}

		owner = fmt.Sprintf("%d:%d", id, id)
	}

	return tolerations, owner, nil
}
