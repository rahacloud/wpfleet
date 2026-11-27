package ops

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// LoginTokenBytes is how many random bytes a login token has.
const LoginTokenBytes = 32

// LoginTTL is how long a login link works.
const LoginTTL = 60 * time.Second

// LoginOptionPrefix starts the name of the option that holds a token's hash; the
// chart's wpfleet-login.php looks it up by the same name.
const LoginOptionPrefix = "_wpfleet_login_"

// LoginQueryParam is the query parameter of a login link.
const LoginQueryParam = "wpfleet-login"

// ErrNoAdmin is returned when a site has no user to log in as.
var ErrNoAdmin = errors.New("the site has no administrator; name a user with --user")

// ErrBadUser is returned for a --user that wp-cli would read as a flag, or
// for a user ID wp-cli did not give as a number.
var ErrBadUser = errors.New("invalid user")

// ErrBadClock is returned when the site's time is not a Unix time.
var ErrBadClock = errors.New("wp-cli gave the site's time as")

// WP runs wp-cli in a site's pod; *wpcli.Runner is one.
type WP interface {
	// Output runs wp-cli with args in pod and returns its trimmed output.
	Output(ctx context.Context, pod string, args ...string) (string, error)
}

// NewLoginToken reads LoginTokenBytes from random and returns them as unpadded
// URL-safe base64, 43 characters.
func NewLoginToken(random io.Reader) (string, error) {
	b := make([]byte, LoginTokenBytes)

	_, err := io.ReadFull(random, b)
	if err != nil {
		return "", fmt.Errorf("making a login token: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LoginTokenHash is the hex SHA-256 of a token, the only form of it the site
// stores.
func LoginTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}

// LoginOptionName is the option that holds a token's login.
func LoginOptionName(token string) string {
	return LoginOptionPrefix + LoginTokenHash(token)
}

// StoredLogin is what the option holds, as JSON that wp-cli turns into the PHP
// array the plugin reads.
type StoredLogin struct {
	// Hash is the token's SHA-256, compared once more with hash_equals.
	Hash string `json:"hash"`
	// User is the ID of the user to log in.
	User int `json:"user"`
	// Expires is the Unix time, by the site's clock, after which the token
	// does nothing.
	Expires int64 `json:"expires"`
}

// LoginRequest is one login link to make.
type LoginRequest struct {
	// Pod is a running pod of the site.
	Pod string
	// BaseURL is the site's URL without a trailing slash, e.g.
	// https://blog.example.com.
	BaseURL string
	// User is the login, email or ID to log in as; empty picks one.
	User string
	// MatchEmail, when User is empty, prefers the WordPress user with this
	// email if that user is an administrator (or a super admin on a
	// multisite); otherwise the first administrator is picked as usual.
	MatchEmail string
}

// Login is a link that was made.
type Login struct {
	// URL is the one-time link. It is a credential until it expires.
	URL string `json:"url"`
	// UserID is the ID of the user it logs in.
	UserID int `json:"userId"`
	// Expires is when it stops working.
	Expires time.Time `json:"expires"`
	// MatchedEmail says whether the user is the one LoginRequest.MatchEmail
	// named. The CLI's JSON leaves it out.
	MatchedEmail bool `json:"-"`
}

// IssueLogin picks the user, stores a new token's hash in the site through wp-cli
// and returns the link. The token itself never reaches the site.
func IssueLogin(ctx context.Context, wp WP, req LoginRequest, random io.Reader) (Login, error) {
	// --url makes wp-cli act on the site that answers the link, which matters
	// on a multisite network.
	site := "--url=" + req.BaseURL + "/"

	userID, matched, err := pickUser(ctx, wp, req.Pod, site, req)
	if err != nil {
		return Login{}, err
	}

	token, err := NewLoginToken(random)
	if err != nil {
		return Login{}, err
	}

	// The expiry is set by the site's clock, the one the plugin checks it
	// against, so a wrong local clock cannot stretch the link's life.
	now, err := serverTime(ctx, wp, req.Pod, site)
	if err != nil {
		return Login{}, err
	}

	expires := now.Add(LoginTTL)

	value, err := json.Marshal(StoredLogin{Hash: LoginTokenHash(token), User: userID, Expires: expires.Unix()})
	if err != nil {
		return Login{}, fmt.Errorf("encoding the login: %w", err)
	}

	_, err = wp.Output(ctx, req.Pod, "option", "add", LoginOptionName(token), string(value),
		"--format=json", "--autoload=no", site)
	if err != nil {
		return Login{}, fmt.Errorf("storing the login token: %w", err)
	}

	return Login{
		URL: req.BaseURL + "/?" + LoginQueryParam + "=" + token, UserID: userID, Expires: expires, MatchedEmail: matched,
	}, nil
}

// serverTime is the current time by the clock of the site's pod.
func serverTime(ctx context.Context, wp WP, pod, site string) (time.Time, error) {
	out, err := wp.Output(ctx, pod, "eval", "echo time();", site)
	if err != nil {
		return time.Time{}, fmt.Errorf("reading the site's clock: %w", err)
	}

	sec, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q", ErrBadClock, out)
	}

	return time.Unix(sec, 0), nil
}

// pickUser returns the ID of the user to log in, and whether it is the one
// req.MatchEmail named: req.User when given, else the administrator whose
// email is req.MatchEmail, else the site's first administrator.
func pickUser(ctx context.Context, wp WP, pod, site string, req LoginRequest) (int, bool, error) {
	if req.User != "" {
		if strings.HasPrefix(req.User, "-") {
			return 0, false, fmt.Errorf("%w: %q", ErrBadUser, req.User)
		}

		id, err := userID(ctx, wp, pod, site, req.User)

		return id, false, err
	}

	if id, ok := adminByEmail(ctx, wp, pod, site, req.MatchEmail); ok {
		return id, true, nil
	}

	id, err := firstAdmin(ctx, wp, pod, site)

	return id, false, err
}

// adminByEmail returns the ID of the user with email when that user is an
// administrator of the site or a super admin of its network. Any failure
// (no such user, a wp-cli error) means no match: the caller falls back to
// the first administrator, as the CLI does.
func adminByEmail(ctx context.Context, wp WP, pod, site, email string) (int, bool) {
	if email == "" || strings.HasPrefix(email, "-") || !strings.Contains(email, "@") {
		return 0, false
	}

	id, err := userID(ctx, wp, pod, site, email)
	if err != nil {
		slog.DebugContext(ctx, "no WordPress user with the email", "err", err)

		return 0, false
	}

	// The ID is a parsed positive integer, so it is safe in PHP code.
	code := fmt.Sprintf(`$u = get_userdata(%d); echo ($u && (in_array("administrator", (array) $u->roles, true)`+
		` || is_super_admin(%d))) ? "yes" : "no";`, id, id)

	out, err := wp.Output(ctx, pod, "eval", code, site)
	if err != nil || strings.TrimSpace(out) != "yes" {
		slog.DebugContext(ctx, "the WordPress user with the email is not an administrator", "user", id, "err", err)

		return 0, false
	}

	return id, true
}

// firstAdmin returns the ID of the site's first administrator: on a
// multisite network the first super admin, who may hold no role on the main
// site at all.
func firstAdmin(ctx context.Context, wp WP, pod, site string) (int, error) {
	multisite, err := wp.Output(ctx, pod, "eval", "echo is_multisite() ? 'yes' : 'no';", site)
	if err != nil {
		return 0, fmt.Errorf("checking for multisite: %w", err)
	}

	if multisite == "yes" {
		logins, err := wp.Output(ctx, pod, "super-admin", "list", site)
		if err != nil {
			return 0, fmt.Errorf("listing super admins: %w", err)
		}

		first, _, _ := strings.Cut(logins, "\n")
		if first = strings.TrimSpace(first); first != "" {
			return userID(ctx, wp, pod, site, first)
		}
	}

	out, err := wp.Output(ctx, pod, "user", "list", "--role=administrator", "--field=ID",
		"--number=1", "--orderby=ID", "--order=ASC", site)
	if err != nil {
		return 0, fmt.Errorf("finding an administrator: %w", err)
	}

	if out == "" {
		return 0, ErrNoAdmin
	}

	return parseID(out)
}

// userID returns the ID of the user with login.
func userID(ctx context.Context, wp WP, pod, site, login string) (int, error) {
	out, err := wp.Output(ctx, pod, "user", "get", login, "--field=ID", site)
	if err != nil {
		return 0, fmt.Errorf("finding user %s: %w", login, err)
	}

	return parseID(out)
}

// parseID parses a user ID that wp-cli printed.
func parseID(out string) (int, error) {
	id, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: wp-cli gave the user ID %q", ErrBadUser, out)
	}

	return id, nil
}
