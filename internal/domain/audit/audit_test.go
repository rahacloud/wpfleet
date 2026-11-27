package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/domain/audit"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// line is an audit line as the cluster's log collection reads it.
type line struct {
	Msg     string `json:"msg"`
	Audit   bool   `json:"audit"`
	Action  string `json:"action"`
	Allowed bool   `json:"allowed"`
	Result  string `json:"result"`
	At      string `json:"at"`
	Env     string `json:"env"`
	Site    string `json:"site"`
	Commit  string `json:"commit"`
	Detail  string `json:"detail"`
	WPUser  string `json:"wpUser"`
	Actor   struct {
		Sub    string   `json:"sub"`
		Email  string   `json:"email"`
		Name   string   `json:"name"`
		Groups []string `json:"groups"`
	} `json:"actor"`
}

// write logs the record as the server does and returns the line and its raw
// JSON keys.
func write(t *testing.T, r audit.Record) (line, map[string]json.RawMessage) {
	t.Helper()

	var buf bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.LogAttrs(context.Background(), slog.LevelInfo, audit.Message, r.Attrs()...)

	var (
		got  line
		keys map[string]json.RawMessage
	)

	testutil.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	testutil.NoError(t, json.Unmarshal(buf.Bytes(), &keys))

	return got, keys
}

func TestAttrsLogin(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 27, 10, 30, 0, 0, time.FixedZone("Tehran", 3*3600+1800))

	got, keys := write(t, audit.Record{
		Time: at,
		Actor: access.Identity{
			Subject: "u-123",
			Email:   "editor@example.com",
			Name:    "Example Editor",
			Groups:  []string{"shop-team", "staff"},
		},
		Action:  "login",
		Env:     "staging",
		Site:    "shop",
		Commit:  "",
		Result:  audit.ResultOK,
		Detail:  "",
		WPUser:  "editor (7)",
		Allowed: true,
	})

	testutil.Equal(t, "msg", got.Msg, audit.Message)
	testutil.True(t, "audit", got.Audit)
	testutil.Equal(t, "action", got.Action, "login")
	testutil.True(t, "allowed", got.Allowed)
	testutil.Equal(t, "result", got.Result, audit.ResultOK)
	testutil.Equal(t, "at", got.At, "2026-11-27T07:00:00Z")
	testutil.Equal(t, "env", got.Env, "staging")
	testutil.Equal(t, "site", got.Site, "shop")
	testutil.Equal(t, "wpUser", got.WPUser, "editor (7)")
	testutil.Equal(t, "actor.sub", got.Actor.Sub, "u-123")
	testutil.Equal(t, "actor.email", got.Actor.Email, "editor@example.com")
	testutil.DeepEqual(t, "actor.groups", got.Actor.Groups, []string{"shop-team", "staff"})
	testutil.Equal(t, "actor.name", got.Actor.Name, "")

	for _, key := range []string{"commit", "detail"} {
		_, ok := keys[key]
		testutil.True(t, key+" is left out when empty", !ok)
	}
}

func TestAttrsDenied(t *testing.T) {
	t.Parallel()

	var actor access.Identity

	actor.Subject = "u-9"

	got, keys := write(t, audit.Record{
		Time:    time.Time{},
		Actor:   actor,
		Action:  "restore",
		Env:     "production",
		Site:    "blog",
		Commit:  "",
		Result:  audit.ResultDenied,
		Detail:  "no restore permission",
		WPUser:  "",
		Allowed: false,
	})

	testutil.True(t, "audit", got.Audit)
	testutil.True(t, "not allowed", !got.Allowed)
	testutil.Equal(t, "result", got.Result, audit.ResultDenied)
	testutil.Equal(t, "detail", got.Detail, "no restore permission")
	testutil.Equal(t, "groups is an empty list, not null", string(keysOf(t, keys["actor"])["groups"]), "[]")

	for _, key := range []string{"at", "wpUser", "commit"} {
		_, ok := keys[key]
		testutil.True(t, key+" is left out when empty", !ok)
	}
}

func TestAttrsDeploy(t *testing.T) {
	t.Parallel()

	got, _ := write(t, audit.Record{
		Time:    time.Unix(0, 0),
		Actor:   access.Identity{Subject: "u-1", Email: "ops@example.com", Name: "", Groups: []string{"platform"}},
		Action:  "deploy",
		Env:     "production",
		Site:    "blog",
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		Result:  audit.ResultFailed,
		Detail:  "context deadline exceeded",
		WPUser:  "",
		Allowed: true,
	})

	testutil.Equal(t, "commit", got.Commit, "0123456789abcdef0123456789abcdef01234567")
	testutil.Equal(t, "result", got.Result, audit.ResultFailed)
	testutil.Equal(t, "at", got.At, "1970-01-01T00:00:00Z")
}

func TestAttrsStartWithAudit(t *testing.T) {
	t.Parallel()

	var r audit.Record

	attrs := r.Attrs()
	testutil.Equal(t, "first key", attrs[0].Key, "audit")
	testutil.True(t, "first value", attrs[0].Value.Bool())
}

func keysOf(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()

	var out map[string]json.RawMessage

	testutil.NoError(t, json.Unmarshal(raw, &out))

	return out
}
