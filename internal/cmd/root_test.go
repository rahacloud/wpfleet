package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// errBoom is what the test command fails with.
var errBoom = errors.New("boom")

// run runs wpfleet with args, plus a hidden "fail" command that returns the
// given error, and returns the exit code, stdout and stderr.
func run(t *testing.T, fail error, args ...string) (int, string, string) {
	t.Helper()

	root := cmd.New()

	//nolint:exhaustruct_v5 // a test command needs only these
	root.Commands = append(root.Commands, &cli.Command{
		Name:   "fail",
		Hidden: true,
		Action: func(context.Context, *cli.Command) error { return fail },
	})

	var stdout, stderr bytes.Buffer

	root.Writer = &stdout
	root.ErrWriter = &stderr

	code := cmd.Run(t.Context(), root, append([]string{"wpfleet"}, args...))

	return code, stdout.String(), stderr.String()
}

func TestCompletion(t *testing.T) {
	t.Parallel()

	for _, shell := range []string{"bash", "zsh", "fish"} {
		code, out, _ := run(t, nil, "completion", shell)
		testutil.Equal(t, "completion "+shell+" exit code", code, 0)
		testutil.Contains(t, "completion "+shell, out, "wpfleet")
	}
}

func TestVersionAndHelp(t *testing.T) {
	t.Parallel()

	code, out, _ := run(t, nil, "--version")
	testutil.Equal(t, "--version exit code", code, 0)
	testutil.Contains(t, "--version", out, "wpfleet version ")

	code, out, _ = run(t, nil, "--help")
	testutil.Equal(t, "--help exit code", code, 0)
	testutil.Contains(t, "help", out, "--fleet", "--env", "--output", "--verbose", "WPFLEET_FLEET", "WPFLEET_ENV")
}

func TestErrors(t *testing.T) {
	t.Parallel()

	code, _, errOut := run(t, nil, "--output", "yaml", "completion", "bash")
	testutil.Equal(t, "bad --output exit code", code, 1)
	testutil.Contains(t, "stderr", errOut, "wpfleet: ", "unknown output format")

	code, _, errOut = run(t, errBoom, "fail")
	testutil.Equal(t, "plain error exit code", code, 1)
	testutil.Equal(t, "plain error", errOut, "wpfleet: boom\n")

	// diff exits 1 on a difference without printing an error.
	code, _, errOut = run(t, cli.Exit("", 1), "fail")
	testutil.Equal(t, "silent exit code", code, 1)
	testutil.Equal(t, "silent exit", errOut, "")

	code, _, errOut = run(t, cli.Exit("bad", 3), "fail")
	testutil.Equal(t, "exit code 3", code, 3)
	testutil.Equal(t, "exit 3 message", errOut, "wpfleet: bad\n")

	code, _, _ = run(t, nil, "--verbose", "fail")
	testutil.Equal(t, "success", code, 0)
}

// Every top-level command of docs/design.md, and the subcommands of the
// groups, is registered on the root command, so a merge that drops one fails
// here and not only in the command's own package tests. completion is added
// by urfave/cli when the command runs; TestCompletion covers it.
func TestCommandsRegistered(t *testing.T) {
	t.Parallel()

	root := cmd.New()

	for name, subcommands := range map[string][]string{
		"sites":   {"list", "show"},
		"deploy":  nil,
		"diff":    nil,
		"build":   nil,
		"promote": nil,
		"admin":   nil,
		"backup":  {"run", "list", "restore"},
		"copy":    nil,
	} {
		command := root.Command(name)
		testutil.True(t, name+" is registered", command != nil)

		for _, sub := range subcommands {
			testutil.True(t, name+" "+sub+" is registered", command.Command(sub) != nil)
		}
	}

	code, out, _ := run(t, nil, "--help")
	testutil.Equal(t, "--help exit code", code, 0)
	testutil.Contains(t, "commands", out, "sites", "deploy", "diff", "build", "promote", "admin", "backup", "copy")

	code, out, _ = run(t, nil, "backup", "--help")
	testutil.Equal(t, "backup --help exit code", code, 0)
	testutil.Contains(t, "backup commands", out, "run", "list", "restore")
}
