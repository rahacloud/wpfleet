package gitrepo

import (
	"fmt"

	"github.com/go-git/go-git/v5/plumbing/transport"
)

// AuthFor exposes authFor to the external tests.
var AuthFor = authFor //nolint:gochecknoglobals // a test hook, set once

// DefaultTokenUser exposes defaultTokenUser to the external tests.
const DefaultTokenUser = defaultTokenUser

// SSHAuthFor gives the SSH auth of url with keyFile, checking host keys
// against the knownHosts files instead of SSH_KNOWN_HOSTS.
//
//nolint:ireturn // go-git takes credentials only as this interface
func SSHAuthFor(url, keyFile string, knownHosts ...string) (transport.AuthMethod, error) {
	ep, err := transport.NewEndpoint(url)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}

	return sshAuth(ep, keyFile, knownHosts...)
}
