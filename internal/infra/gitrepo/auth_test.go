package gitrepo_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	cryptossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

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

	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	testutil.NoError(t, os.WriteFile(knownHosts, nil, 0o600))

	for url, user := range map[string]string{
		"git@git.example.com:example/fleet.git":        "git",
		"ssh://deploy@git.example.com/example/fleet":   "deploy",
		"ssh://git.example.com:2222/example/fleet.git": "git",
	} {
		auth, err := gitrepo.SSHAuthFor(url, key, knownHosts)
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

// sshServer accepts SSH handshakes on a loopback port with an ECDSA and an
// ed25519 host key, as most Git hosts have both, and returns its address
// and a known_hosts line with the ed25519 key only.
func sshServer(t *testing.T) (string, string) {
	t.Helper()

	config := &cryptossh.ServerConfig{ //nolint:exhaustruct_v5 // a test server needs only the key callback
		PublicKeyCallback: func(cryptossh.ConnMetadata, cryptossh.PublicKey) (*cryptossh.Permissions, error) {
			return &cryptossh.Permissions{CriticalOptions: nil, Extensions: nil, ExtraData: nil}, nil
		},
	}

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	testutil.NoError(t, err)

	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	testutil.NoError(t, err)

	var edSigner cryptossh.Signer

	for _, key := range []crypto.Signer{ecKey, edKey} {
		signer, err := cryptossh.NewSignerFromSigner(key)
		testutil.NoError(t, err)
		config.AddHostKey(signer)

		edSigner = signer
	}

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go func() {
				defer func() { _ = conn.Close() }()

				sc, chans, reqs, err := cryptossh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer func() { _ = sc.Close() }()

				go cryptossh.DiscardRequests(reqs)

				for ch := range chans {
					_ = ch.Reject(cryptossh.Prohibited, "test server")
				}
			}()
		}
	}()

	addr := ln.Addr().String()

	return addr, knownhosts.Line([]string{addr}, edSigner.PublicKey())
}

// A known_hosts file that lists only one of the host's keys, as GitHub's
// documentation gives it, must be enough: the client has to ask for the
// algorithms the file holds, or the server answers with the key it prefers
// and the check fails with a key mismatch.
func TestAuthSSHAsksForTheKnownHostKey(t *testing.T) {
	t.Parallel()

	addr, line := sshServer(t)

	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	testutil.NoError(t, os.WriteFile(knownHosts, []byte(line+"\n"), 0o600))

	auth, err := gitrepo.SSHAuthFor("ssh://git@"+addr+"/example/fleet.git", keyFile(t), knownHosts)
	testutil.NoError(t, err)

	keys, ok := auth.(*ssh.PublicKeys)
	testutil.True(t, "public keys", ok)

	cfg, err := keys.ClientConfig()
	testutil.NoError(t, err)

	client, err := cryptossh.Dial("tcp", addr, cfg)
	testutil.NoError(t, err)
	testutil.NoError(t, client.Close())
}

func TestAuthSSHNeedsKnownHosts(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing")

	_, err := gitrepo.SSHAuthFor("git@git.example.com:example/fleet.git", keyFile(t), missing)
	testutil.Error(t, err, "no known_hosts file")
}
