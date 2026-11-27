package gitrepo_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	cryptossh "golang.org/x/crypto/ssh"

	"github.com/rahacloud/wpfleet/internal/infra/gitrepo"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// keyFile writes a fresh unencrypted ed25519 key in OpenSSH format.
func keyFile(t *testing.T) string {
	t.Helper()

	_, key, err := ed25519.GenerateKey(rand.Reader)
	testutil.NoError(t, err)

	block, err := cryptossh.MarshalPrivateKey(key, "")
	testutil.NoError(t, err)

	path := filepath.Join(t.TempDir(), "id_ed25519")
	testutil.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))

	return path
}

func config(url, key, token string) gitrepo.Config {
	return gitrepo.Config{
		URL: url, Branch: "", Dir: "", SSHKeyFile: key, Token: token,
		Committer: gitrepo.Signature{Name: "", Email: ""},
	}
}

func TestAuthNone(t *testing.T) {
	t.Parallel()

	auth, err := gitrepo.AuthFor(config("file:///srv/fleet.git", "", ""))
	testutil.NoError(t, err)
	testutil.True(t, "no auth", auth == nil)
}

func TestAuthSSH(t *testing.T) {
	t.Parallel()

	key := keyFile(t)

	for url, user := range map[string]string{
		"git@git.example.com:example/fleet.git":        "git",
		"ssh://deploy@git.example.com/example/fleet":   "deploy",
		"ssh://git.example.com:2222/example/fleet.git": "git",
	} {
		auth, err := gitrepo.AuthFor(config(url, key, ""))
		testutil.NoError(t, err)

		keys, ok := auth.(*ssh.PublicKeys)
		testutil.True(t, "public keys for "+url, ok)
		testutil.Equal(t, "user of "+url, keys.User, user)
	}

	_, err := gitrepo.AuthFor(config("https://git.example.com/example/fleet.git", key, ""))
	testutil.ErrorIs(t, err, gitrepo.ErrInvalid)

	_, err = gitrepo.AuthFor(config("git@git.example.com:example/fleet.git", filepath.Join(t.TempDir(), "missing"), ""))
	testutil.Error(t, err, "a missing key file")
}

func TestAuthToken(t *testing.T) {
	t.Parallel()

	auth, err := gitrepo.AuthFor(config("https://git.example.com/example/fleet.git", "", "secret-token"))
	testutil.NoError(t, err)

	basic, ok := auth.(*http.BasicAuth)
	testutil.True(t, "basic auth", ok)
	testutil.Equal(t, "user", basic.Username, gitrepo.DefaultTokenUser)
	testutil.Equal(t, "token", basic.Password, "secret-token")

	auth, err = gitrepo.AuthFor(config("https://bot@git.example.com/example/fleet.git", "", "secret-token"))
	testutil.NoError(t, err)

	basic, ok = auth.(*http.BasicAuth)
	testutil.True(t, "basic auth", ok)
	testutil.Equal(t, "user from the URL", basic.Username, "bot")

	_, err = gitrepo.AuthFor(config("git@git.example.com:example/fleet.git", "", "secret-token"))
	testutil.ErrorIs(t, err, gitrepo.ErrInvalid)
}

// Basic auth over plain http would send the token in clear text, so only a
// loopback address, which never leaves the machine, may take it.
func TestAuthTokenNeedsTLS(t *testing.T) {
	t.Parallel()

	_, err := gitrepo.AuthFor(config("http://git.example.com/example/fleet.git", "", "secret-token"))
	testutil.ErrorIs(t, err, gitrepo.ErrInvalid)

	for _, url := range []string{
		"http://localhost:3000/example/fleet.git",
		"http://127.0.0.1:3000/example/fleet.git",
		"http://[::1]:3000/example/fleet.git",
	} {
		auth, err := gitrepo.AuthFor(config(url, "", "secret-token"))
		testutil.NoError(t, err)

		_, ok := auth.(*http.BasicAuth)
		testutil.True(t, "basic auth for "+url, ok)
	}

	auth, err := gitrepo.AuthFor(config("http://git.example.com/example/fleet.git", "", ""))
	testutil.NoError(t, err)
	testutil.True(t, "a public repository over http needs no auth", auth == nil)
}
