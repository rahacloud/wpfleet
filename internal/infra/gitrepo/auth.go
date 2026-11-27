package gitrepo

import (
	"fmt"

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

// sshAuth reads a deploy key for an SSH endpoint.
//
//nolint:ireturn // go-git takes credentials only as this interface
func sshAuth(ep *transport.Endpoint, keyFile string) (transport.AuthMethod, error) {
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

	return keys, nil
}

// tokenAuth sends a token with basic auth to an HTTPS endpoint.
//
//nolint:ireturn // go-git takes credentials only as this interface
func tokenAuth(ep *transport.Endpoint, token string) (transport.AuthMethod, error) {
	if ep.Protocol != "https" && ep.Protocol != "http" {
		return nil, fmt.Errorf("%w: a token needs an HTTPS URL, not %s", ErrInvalid, ep.Protocol)
	}

	user := ep.User
	if user == "" {
		user = defaultTokenUser
	}

	return &http.BasicAuth{Username: user, Password: token}, nil
}
