package gitrepo

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// signaturePrefix starts GitHub's X-Hub-Signature-256 header; Gitea's
// X-Gitea-Signature is the bare hex digest.
const signaturePrefix = "sha256="

// VerifyWebhook reports whether signatureHeader is the HMAC-SHA256 of body
// under secret, as "sha256=<hex>" (GitHub's X-Hub-Signature-256) or bare
// hex (Gitea's X-Gitea-Signature). The comparison takes constant time. An
// empty secret verifies nothing, so a server without one refuses every
// webhook.
func VerifyWebhook(secret, body []byte, signatureHeader string) bool {
	if len(secret) == 0 {
		return false
	}

	got, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(signatureHeader), signaturePrefix))
	if err != nil || len(got) != sha256.Size {
		return false
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)

	return hmac.Equal(got, mac.Sum(nil))
}
