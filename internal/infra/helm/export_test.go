package helm

import (
	"fmt"
	"io"
	"log/slog"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	kubefake "helm.sh/helm/v4/pkg/kube/fake"
	releasei "helm.sh/helm/v4/pkg/release"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
)

// NewInMemory makes a client whose releases live in memory and whose cluster
// is Helm's printing fake, so install and upgrade run without a cluster.
func NewInMemory(ch *chartv2.Chart) *Client {
	mem := driver.NewMemory()

	return &Client{
		chart: ch,
		configure: func(namespace string) (*action.Configuration, error) {
			mem.SetNamespace(namespace)

			cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.DiscardHandler))
			cfg.KubeClient = &kubefake.PrintingKubeClient{Out: io.Discard, LogOutput: io.Discard}
			cfg.Releases = storage.Init(mem)
			cfg.Capabilities = common.DefaultCapabilities.Copy()

			return cfg, nil
		},
		Timeout: defaultTimeout,
	}
}

// Newest is newest, for tests.
func Newest(versions []releasei.Releaser) *releasev1.Release {
	return newest(versions)
}

// Store writes a release record straight into the client's storage.
func (c *Client) Store(namespace string, rel *releasev1.Release) error {
	cfg, err := c.configure(namespace)
	if err != nil {
		return err
	}

	err = cfg.Releases.Create(rel)
	if err != nil {
		return fmt.Errorf("storing %s v%d: %w", rel.Name, rel.Version, err)
	}

	return nil
}
