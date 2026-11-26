package assets_test

import (
	"os"
	"testing"

	"github.com/rahacloud/wpfleet/internal/infra/assets"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

func TestWriteTemp(t *testing.T) {
	t.Parallel()

	path, cleanup, err := assets.WriteTemp(assets.RehostExtraName, assets.RehostExtra)
	testutil.NoError(t, err)
	testutil.Equal(t, "content", testutil.ReadFile(t, path), string(assets.RehostExtra))

	cleanup()

	_, err = os.Stat(path)
	testutil.ErrorIs(t, err, os.ErrNotExist)
}
