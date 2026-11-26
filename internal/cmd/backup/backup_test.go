package backup_test

import (
	"encoding/json"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/rahacloud/wpfleet/internal/cmd/backup"
	bt "github.com/rahacloud/wpfleet/internal/cmd/backup/backuptest"
	domain "github.com/rahacloud/wpfleet/internal/domain/backup"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/kube/kubetest"
	"github.com/rahacloud/wpfleet/internal/infra/s3"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// objects is two nights of blog in staging; the older one's uploads changed
// on the night after it.
func objects() map[string]int64 {
	return map[string]int64{
		"wp-staging/blog/database/20261125T031500Z.sql.gz":               1536,
		"wp-staging/blog/database/20261126T031500Z.sql.gz":               2 * 1024 * 1024,
		"wp-staging/blog/uploads/current/2026/11/a.jpg":                  10,
		"wp-staging/blog/uploads/changed/20261126T031500Z/2026/11/a.jpg": 9,
	}
}

func cronJob(namespace string) *batchv1.CronJob {
	//nolint:exhaustruct_v5 // the template's containers are what the Job copies
	return &batchv1.CronJob{
		Name:      "blog-backup",
		Namespace: namespace,
		Spec: batchv1.CronJobSpec{JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				InitContainers: []corev1.Container{{Name: backup.ContainerDump}},
				Containers:     []corev1.Container{{Name: backup.ContainerUpload}},
			}},
		}}},
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	h := bt.New(t, nil)
	h.Seed(t, bt.Staging, cronJob(bt.Namespace(bt.Staging)))

	testutil.NoError(t, h.Run(t, backup.NewCommand(h.Deps), "", "-e", bt.Staging, "backup", "run", "blog"))

	jobs := h.Jobs(t, bt.Staging)
	testutil.Equal(t, "jobs", len(jobs), 1)
	testutil.Equal(t, "job name", jobs[0].Name, "blog-backup-20261126t101500z")
	testutil.Equal(t, "owner", jobs[0].OwnerReferences[0].Name, "blog-backup")

	testutil.Contains(t, "logs", h.Out.String(),
		"==> blog-backup-20261126t101500z-x1/dump <==\nfake logs",
		"==> blog-backup-20261126t101500z-x1/upload <==\nfake logs")
	testutil.Contains(t, "stderr", h.Err.String(),
		"Started Job blog-backup-20261126t101500z in wp-staging", "Backed up blog")
}

func TestRunFails(t *testing.T) {
	t.Parallel()

	h := bt.New(t, nil)
	h.Seed(t, bt.Staging, cronJob(bt.Namespace(bt.Staging)))
	h.FailJobs()

	err := h.Run(t, backup.NewCommand(h.Deps), "", "-e", bt.Staging, "backup", "run", "blog")
	testutil.ErrorIs(t, err, kube.ErrJobFailed)

	err = h.Run(t, backup.NewCommand(h.Deps), "", "-e", bt.Production, "backup", "run", "blog")
	testutil.Error(t, err, "a site without a CronJob")
	testutil.Contains(t, "error", err.Error(), "backup.enabled")

	err = h.Run(t, backup.NewCommand(h.Deps), "", "backup", "run", "blog")
	testutil.Error(t, err, "no --env")

	err = h.Run(t, backup.NewCommand(h.Deps), "", "-e", bt.Staging, "backup", "run")
	testutil.ErrorIs(t, err, backup.ErrNoSite)
}

func TestList(t *testing.T) {
	t.Parallel()

	h := bt.New(t, objects())

	testutil.NoError(t, h.Run(t, backup.NewCommand(h.Deps), "", "-e", bt.Staging, "backup", "list", "blog"))
	testutil.Equal(t, "table", h.Out.String(), ""+
		"STAMP             DUMP     CHANGED  EXPIRES\n"+
		"20261126T031500Z  2.0 MiB  1        2026-12-10 03:15 UTC\n"+
		"20261125T031500Z  1.5 KiB  0        2026-12-09 03:15 UTC\n")

	testutil.NoError(t, h.Run(t, backup.NewCommand(h.Deps), "", "-e", bt.Staging, "-o", "json", "backup", "list", "blog"))

	var rows []backup.Row
	testutil.NoError(t, json.Unmarshal(h.Out.Bytes(), &rows))
	testutil.Equal(t, "rows", len(rows), 2)
	testutil.Equal(t, "stamp", rows[0].Stamp.String(), "20261126T031500Z")
	testutil.Equal(t, "changed", rows[0].ChangedFiles, 1)
	testutil.Contains(t, "json", h.Out.String(), `"stamp": "20261126T031500Z"`, `"dumpSize": 2097152`)

	h.WriteSite(t, bt.Staging, "blog", "backup:\n  keepDays: 2\n")
	testutil.NoError(t, h.Run(t, backup.NewCommand(h.Deps), "", "-e", bt.Staging, "backup", "list", "blog"))
	testutil.Contains(t, "keepDays from the site", h.Out.String(), "2026-11-28 03:15 UTC")
}

func TestListNeedsKeys(t *testing.T) {
	t.Parallel()

	h := bt.New(t, objects())
	h.Deps.Getenv = func(string) string { return "" }

	err := h.Run(t, backup.NewCommand(h.Deps), "", "-e", bt.Staging, "backup", "list", "blog")
	testutil.ErrorIs(t, err, s3.ErrNoCredentials)
}

func TestRestoreAsks(t *testing.T) {
	t.Parallel()

	h := bt.New(t, objects())
	args := []string{"-e", bt.Staging, "backup", "restore", "blog", "--stamp", "latest"}

	err := h.Run(t, backup.NewCommand(h.Deps), "n\n", args...)
	testutil.ErrorIs(t, err, backup.ErrNotConfirmed)
	testutil.Contains(t, "question", h.Err.String(),
		"Restoring the database of blog in staging from s3://wordpress-backups/wp-staging/blog at 20261126T031500Z.",
		"Overwrite blog in staging? [y/N]")
	testutil.Equal(t, "no job", len(h.Jobs(t, bt.Staging)), 0)

	err = h.Run(t, backup.NewCommand(h.Deps), "", args...)
	testutil.ErrorIs(t, err, backup.ErrNotConfirmed)

	testutil.NoError(t, h.Run(t, backup.NewCommand(h.Deps), "yes\n", args...))
	testutil.Equal(t, "job", len(h.Jobs(t, bt.Staging)), 1)
}

func TestRestoreDatabase(t *testing.T) {
	t.Parallel()

	h := bt.New(t, objects())
	h.WriteSite(t, bt.Staging, "blog", "tolerations:\n  - key: dedicated\n    operator: Equal\n    value: wordpress\n")

	testutil.NoError(t, h.Run(t, backup.NewCommand(h.Deps), "",
		"-e", bt.Staging, "backup", "restore", "blog", "--stamp", "20261125T031500Z", "--yes"))

	jobs := h.Jobs(t, bt.Staging)
	testutil.Equal(t, "jobs", len(jobs), 1)

	job := jobs[0]
	testutil.Equal(t, "name", job.Name, "blog-restore-20261126t101500z")
	testutil.Equal(t, "component", job.Labels[kube.LabelComponent], kube.ComponentBackup)
	testutil.Equal(t, "no retries", *job.Spec.BackoffLimit, int32(0))

	pod := job.Spec.Template.Spec
	testutil.Equal(t, "volumes", len(pod.Volumes), 1)
	testutil.Equal(t, "toleration", pod.Tolerations[0].Value, "wordpress")
	testutil.True(t, "no affinity without the uploads", pod.Affinity == nil)

	fetch, imp := pod.InitContainers[0], pod.Containers[0]
	testutil.Equal(t, "rclone image", fetch.Image, "docker.io/rclone/rclone:1.72.0")
	testutil.Equal(t, "mariadb image", imp.Image, "docker.io/library/mariadb:12.3.3")

	for name, want := range map[string]string{
		"BUCKET":                    bt.Bucket,
		"DUMP":                      "wp-staging/blog/database/20261125T031500Z.sql.gz",
		"UPLOADS":                   "false",
		"RCLONE_CONFIG_S3_ENDPOINT": h.S3.URL,
	} {
		got, _ := bt.Env(fetch, name)
		testutil.Equal(t, name, got, want)
	}

	_, key := bt.Env(fetch, "RCLONE_CONFIG_S3_SECRET_ACCESS_KEY")
	testutil.Equal(t, "s3 secret", key.Name+"/"+key.Key, "backup-s3/secret-access-key")

	for name, want := range map[string]string{
		"DB_HOST": "mariadb", "DB_PORT": "3306", "DB_NAME": "wp_blog", "DB_USER": "wp_blog",
	} {
		got, _ := bt.Env(imp, name)
		testutil.Equal(t, name, got, want)
	}

	_, pwd := bt.Env(imp, "MYSQL_PWD")
	testutil.Equal(t, "password from the site Secret", pwd.Name+"/"+pwd.Key, "blog/"+domain.DBPasswordKey)

	testutil.Contains(t, "logs", h.Out.String(), "/fetch <==", "/import <==")
	testutil.Contains(t, "stderr", h.Err.String(), "Restored blog from 20261125T031500Z.")
}

func TestRestoreUploads(t *testing.T) {
	t.Parallel()

	h := bt.New(t, objects())
	h.WriteSite(t, bt.Staging, "blog", "backup:\n  s3:\n    endpoint: http://minio.backup.svc:9000\n")

	restore := func() error {
		return h.Run(t, backup.NewCommand(h.Deps), "",
			"-e", bt.Staging, "backup", "restore", "blog", "--stamp", "20261125T031500Z", "--uploads", "-y")
	}

	// Without a running pod of the site, the volume's holder, nothing starts.
	err := restore()
	testutil.ErrorIs(t, err, kube.ErrNoRunningPod)
	testutil.Equal(t, "no job", len(h.Jobs(t, bt.Staging)), 0)

	h.Seed(t, bt.Staging, kubetest.RunningPod(bt.Namespace(bt.Staging), "blog", "blog-1"))
	testutil.NoError(t, restore())

	testutil.Contains(t, "warning", h.Err.String(), "wound back through 1 later runs")

	pod := h.Jobs(t, bt.Staging)[0].Spec.Template.Spec
	testutil.Equal(t, "claim", pod.Volumes[1].PersistentVolumeClaim.ClaimName, "blog-uploads")
	testutil.DeepEqual(t, "beside the site's pod", pod.Affinity, backup.BesideSite("blog"))

	term := pod.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0]
	testutil.Equal(t, "same node", term.TopologyKey, "kubernetes.io/hostname")
	testutil.Equal(t, "the site's pods", term.LabelSelector.MatchLabels[kube.LabelInstance], "blog")
	testutil.DeepEqual(t, "not a backup pod", term.LabelSelector.MatchExpressions[0].Values, []string{"backup"})

	fetch := pod.InitContainers[0]
	testutil.Equal(t, "mounts", len(fetch.VolumeMounts), 2)

	for name, want := range map[string]string{
		"UPLOADS":                   "true",
		"CURRENT":                   "wp-staging/blog/uploads/current",
		"OVERLAYS":                  "wp-staging/blog/uploads/changed/20261126T031500Z",
		"UPLOADS_OWNER":             "33:33",
		"RCLONE_CONFIG_S3_ENDPOINT": "http://minio.backup.svc:9000",
	} {
		got, _ := bt.Env(fetch, name)
		testutil.Equal(t, name, got, want)
	}

	testutil.True(t, "the import does not mount the uploads", len(pod.Containers[0].VolumeMounts) == 1)
}

func TestRestoreErrors(t *testing.T) {
	t.Parallel()

	h := bt.New(t, objects())

	restore := func(env, stamp string) error {
		return h.Run(t, backup.NewCommand(h.Deps), "", "-e", env, "backup", "restore", "blog", "--stamp", stamp, "-y")
	}

	testutil.ErrorIs(t, restore(bt.Staging, "20261124T031500Z"), domain.ErrUnknownStamp)
	testutil.ErrorIs(t, restore(bt.Staging, "yesterday"), domain.ErrInvalidStamp)
	testutil.ErrorIs(t, restore(bt.Production, "latest"), domain.ErrNoBackups)

	err := h.Run(t, backup.NewCommand(h.Deps), "", "-e", bt.Staging, "backup", "restore", "blog", "-y")
	testutil.Error(t, err, "no --stamp")

	h.FailJobs()

	testutil.ErrorIs(t, restore(bt.Staging, "latest"), kube.ErrJobFailed)
	testutil.NotContains(t, "stderr", h.Err.String(), "Restored")
}

func TestSize(t *testing.T) {
	t.Parallel()

	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 5 << 30: "5.0 GiB"} {
		testutil.Equal(t, "size", backup.Size(n), want)
	}
}

func TestNewCommandLists(t *testing.T) {
	t.Parallel()

	commands := backup.Command().Commands

	names := make([]string, 0, len(commands))
	for _, c := range commands {
		names = append(names, c.Name)
	}

	testutil.Equal(t, "subcommands", strings.Join(names, " "), "run list restore")
}
