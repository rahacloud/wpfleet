package gitrepo

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// defaultSSHUser is the user of an SSH URL that names none; every common
// Git host takes deploy keys as git.
const defaultSSHUser = "git"

// defaultTokenUser is the basic-auth user sent with a token when the URL
// names none. GitHub needs exactly a non-empty user beside a token; Gitea and
// GitLab accept any user with a personal access token.
const defaultTokenUser = "x-access-token"

// authFor gives the transport auth of c: a deploy key, a token, or none (for
// file:// and public repositories).
//
//nolint:ireturn // go-git takes credentials only as this interface
func authFor(c Config) (transport.AuthMethod, error) {
	if c.SSHKeyFile == "" && c.Token == "" {
		return nil, nil //nolint:nilnil // no credentials is a valid choice: go-git takes a nil AuthMethod as none
	}

	ep, err := transport.NewEndpoint(c.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalid, c.URL, err)
	}

	if c.SSHKeyFile != "" {
		return sshAuth(ep, c.SSHKeyFile)
	}

	return tokenAuth(ep, c.Token)
}

// sshAuth reads a deploy key for an SSH endpoint and checks the host's key
// against the knownHosts files, or with none against SSH_KNOWN_HOSTS, else
// ~/.ssh/known_hosts and /etc/ssh/ssh_known_hosts.
//
// It also asks the host for exactly the key types those files hold for it.
// go-git leaves the algorithms empty with a deploy key, and a host then
// answers with the key it prefers (ECDSA before ed25519), so a known_hosts
// that lists only the ed25519 key, as GitHub's documentation gives it, would
// fail with a key mismatch.
//
//nolint:ireturn // go-git takes credentials only as this interface
func sshAuth(ep *transport.Endpoint, keyFile string, knownHosts ...string) (transport.AuthMethod, error) {
	if ep.Protocol != "ssh" {
		return nil, fmt.Errorf("%w: an SSH key needs an SSH URL, not %s", ErrInvalid, ep.Protocol)
	}

	user := ep.User
	if user == "" {
		user = defaultSSHUser
	}

	keys, err := ssh.NewPublicKeysFromFile(user, keyFile, "")
	if err != nil {
		return nil, fmt.Errorf("reading the SSH key %s: %w", keyFile, err)
	}

	db, err := ssh.NewKnownHostsDb(knownHosts...)
	if err != nil {
		return nil, fmt.Errorf("reading the known hosts to check %s: %w", ep.Host, err)
	}

	port := ep.Port
	if port <= 0 {
		port = ssh.DefaultPort
	}

	keys.HostKeyCallback = db.HostKeyCallback()
	keys.HostKeyAlgorithms = db.HostKeyAlgorithms(net.JoinHostPort(ep.Host, strconv.Itoa(port)))

	return keys, nil
}

// tokenAuth sends a token with basic auth to an HTTPS endpoint. Plain http
// is refused, because basic auth would carry the token in clear text, except
// to a loopback address, which never leaves the machine (local tests).
//
//nolint:ireturn // go-git takes credentials only as this interface
func tokenAuth(ep *transport.Endpoint, token string) (transport.AuthMethod, error) {
	switch {
	case ep.Protocol == "https":
	case ep.Protocol == "http" && loopback(ep.Host):
	default:
		return nil, fmt.Errorf("%w: a token needs an HTTPS URL, not %s", ErrInvalid, ep.Protocol)
	}

	user := ep.User
	if user == "" {
		user = defaultTokenUser
	}

	return &http.BasicAuth{Username: user, Password: token}, nil
}

// loopback reports whether host is localhost or a loopback address; go-git
// keeps the brackets of an IPv6 host.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))

	return ip != nil && ip.IsLoopback()
}
