package helm

import (
	"io"
	"log/slog"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	kubefake "helm.sh/helm/v4/pkg/kube/fake"
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
