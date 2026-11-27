package gitrepo_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/rahacloud/wpfleet/internal/infra/gitrepo"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

func TestVerifyWebhook(t *testing.T) {
	t.Parallel()

	secret := []byte("webhook-secret")
	body := []byte(`{"ref":"refs/heads/main"}`)

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	sum := hex.EncodeToString(mac.Sum(nil))

	for name, tc := range map[string]struct {
		secret []byte
		body   []byte
		header string
		want   bool
	}{
		"github":         {secret, body, "sha256=" + sum, true},
		"gitea":          {secret, body, sum, true},
		"spaces":         {secret, body, " sha256=" + sum + "\n", true},
		"other body":     {secret, []byte(`{}`), "sha256=" + sum, false},
		"other secret":   {[]byte("other"), body, "sha256=" + sum, false},
		"no secret":      {nil, body, "sha256=" + sum, false},
		"no header":      {secret, body, "", false},
		"sha1":           {secret, body, "sha1=" + sum[:40], false},
		"not hex":        {secret, body, "sha256=zz", false},
		"short":          {secret, body, "sha256=" + sum[:10], false},
		"prefix only":    {secret, body, "sha256=", false},
		"upper-case hex": {secret, body, "sha256=" + upper(sum), true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			testutil.Equal(t, "verified", gitrepo.VerifyWebhook(tc.secret, tc.body, tc.header), tc.want)
		})
	}
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'f' {
			b[i] = c - 'a' + 'A'
		}
	}

	return string(b)
}
