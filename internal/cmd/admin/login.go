package admin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// TokenBytes is how many random bytes a login token has.
const TokenBytes = 32

// TTL is how long a login link works.
const TTL = 60 * time.Second

// OptionPrefix starts the name of the option that holds a token's hash; the
// chart's wpfleet-login.php looks it up by the same name.
const OptionPrefix = "_wpfleet_login_"

// QueryParam is the query parameter of a login link.
const QueryParam = "wpfleet-login"

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

// NewToken reads TokenBytes from random and returns them as unpadded
// URL-safe base64, 43 characters.
func NewToken(random io.Reader) (string, error) {
	b := make([]byte, TokenBytes)

	_, err := io.ReadFull(random, b)
	if err != nil {
		return "", fmt.Errorf("making a login token: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// TokenHash is the hex SHA-256 of a token, the only form of it the site
// stores.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}

// OptionName is the option that holds a token's login.
func OptionName(token string) string {
	return OptionPrefix + TokenHash(token)
}

// Stored is what the option holds, as JSON that wp-cli turns into the PHP
// array the plugin reads.
type Stored struct {
	// Hash is the token's SHA-256, compared once more with hash_equals.
	Hash string `json:"hash"`
	// User is the ID of the user to log in.
	User int `json:"user"`
	// Expires is the Unix time, by the site's clock, after which the token
	// does nothing.
	Expires int64 `json:"expires"`
}

// Request is one login link to make.
type Request struct {
	// Pod is a running pod of the site.
	Pod string
	// BaseURL is the site's URL without a trailing slash, e.g.
	// https://blog.example.com.
	BaseURL string
	// User is the login to log in as; empty picks the first administrator.
	User string
}

// Login is a link that was made.
type Login struct {
	// URL is the one-time link. It is a credential until it expires.
	URL string `json:"url"`
	// UserID is the ID of the user it logs in.
	UserID int `json:"userId"`
	// Expires is when it stops working.
	Expires time.Time `json:"expires"`
}

// Issue picks the user, stores a new token's hash in the site through wp-cli
// and returns the link. The token itself never reaches the site.
func Issue(ctx context.Context, wp WP, req Request, random io.Reader) (Login, error) {
	// --url makes wp-cli act on the site that answers the link, which matters
	// on a multisite network.
	site := "--url=" + req.BaseURL + "/"

	userID, err := pickUser(ctx, wp, req.Pod, site, req.User)
	if err != nil {
		return Login{}, err
	}

	token, err := NewToken(random)
	if err != nil {
		return Login{}, err
	}

	// The expiry is set by the site's clock, the one the plugin checks it
	// against, so a wrong local clock cannot stretch the link's life.
	now, err := serverTime(ctx, wp, req.Pod, site)
	if err != nil {
		return Login{}, err
	}

	expires := now.Add(TTL)

	value, err := json.Marshal(Stored{Hash: TokenHash(token), User: userID, Expires: expires.Unix()})
	if err != nil {
		return Login{}, fmt.Errorf("encoding the login: %w", err)
	}

	_, err = wp.Output(ctx, req.Pod, "option", "add", OptionName(token), string(value),
		"--format=json", "--autoload=no", site)
	if err != nil {
		return Login{}, fmt.Errorf("storing the login token: %w", err)
	}

	return Login{URL: req.BaseURL + "/?" + QueryParam + "=" + token, UserID: userID, Expires: expires}, nil
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

// pickUser returns the ID of login, or of the site's first administrator: on
// a multisite network the first super admin, who may hold no role on the
// main site at all.
func pickUser(ctx context.Context, wp WP, pod, site, login string) (int, error) {
	if login != "" {
		if strings.HasPrefix(login, "-") {
			return 0, fmt.Errorf("%w: %q", ErrBadUser, login)
		}

		return userID(ctx, wp, pod, site, login)
	}

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
