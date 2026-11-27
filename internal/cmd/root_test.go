package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd"
	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/infra/config"
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

// helpName is the name of the root's help command.
const helpName = "help"

// commandPaths lists every command below root as the names from the root
// down, the help command left out.
func commandPaths(c *cli.Command, prefix []string) [][]string {
	var paths [][]string

	for _, sub := range c.Commands {
		if sub.Name == helpName {
			continue
		}

		path := append(slices.Clone(prefix), sub.Name)
		paths = append(paths, path)
		paths = append(paths, commandPaths(sub, path)...)
	}

	return paths
}

// Every way of asking for a command's help describes that command: --help
// and -h after it, after a leaf command's arguments too, and help before
// it. urfave/cli took a leaf's first argument for a subcommand to describe,
// and its help command stopped at the first name.
func TestHelpOfEveryCommand(t *testing.T) {
	t.Parallel()

	paths := commandPaths(cmd.New(), nil)
	nested := slices.ContainsFunc(paths, func(p []string) bool { return len(p) > 1 })
	testutil.True(t, "the tree has nested commands", nested)

	for _, path := range paths {
		name := strings.Join(path, " ")
		command := cmd.New()

		for _, p := range path {
			command = command.Command(p)
		}

		want := "NAME:\n   wpfleet " + name + " - " + command.Usage + "\n"

		asks := [][]string{
			append(slices.Clone(path), "--help"),
			append(slices.Clone(path), "-h"),
			append([]string{helpName}, path...),
		}
		if len(command.Commands) == 0 {
			asks = append(asks, append(slices.Clone(path), "blog", "shop", "--help"))
		}

		for _, args := range asks {
			code, out, errOut := run(t, nil, args...)
			what := strings.Join(args, " ")
			testutil.Equal(t, what+" exit code", code, 0)
			testutil.Equal(t, what+" stderr", errOut, "")
			testutil.True(t, what+" describes "+name+", got:\n"+out, strings.HasPrefix(out, want))
		}
	}
}

// The help command is listed in the root's help, so asking for its own help
// describes it rather than calling it unknown.
func TestHelpOfHelp(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{helpName, helpName}, {helpName, "h"}, {"h", helpName}} {
		code, out, errOut := run(t, nil, args...)
		what := strings.Join(args, " ")
		testutil.Equal(t, what+" exit code", code, 0)
		testutil.Equal(t, what+" stderr", errOut, "")
		testutil.True(t, what+" describes help, got:\n"+out, strings.HasPrefix(out, "NAME:\n   wpfleet help - "))
	}
}

func TestHelpOfAnUnknownCommand(t *testing.T) {
	t.Parallel()

	code, _, errOut := run(t, nil, helpName, "backup", "nope")
	testutil.Equal(t, "exit code", code, 3)
	testutil.Equal(t, "stderr", errOut, "wpfleet: No help topic for 'backup nope'\n")

	code, _, errOut = run(t, nil, "sites", "nope", "--help")
	testutil.Equal(t, "group exit code", code, 3)
	testutil.Contains(t, "group stderr", errOut, "No help topic for 'nope'")
}

// flagEnvVars lists the environment variables of every flag in the tree.
func flagEnvVars(c *cli.Command) []string {
	var vars []string

	for _, f := range c.Flags {
		if df, ok := f.(cli.DocGenerationFlag); ok {
			vars = append(vars, df.GetEnvVars()...)
		}
	}

	for _, sub := range c.Commands {
		vars = append(vars, flagEnvVars(sub)...)
	}

	return vars
}

// A flag's variable is never also read as a configuration key, so
// WPFLEET_DATA cannot turn into a key named data.
func TestFlagVariablesAreReserved(t *testing.T) {
	t.Parallel()

	vars := flagEnvVars(cmd.New())
	for _, want := range []string{"WPFLEET_FLEET", "WPFLEET_ENV", "WPFLEET_OUTPUT", "WPFLEET_VERBOSE", "WPFLEET_CONFIG"} {
		testutil.True(t, want+" sets a flag", slices.Contains(vars, want))
	}

	for _, v := range vars {
		testutil.True(t, v+" is reserved", config.ReservedEnv(v))
	}
}

// WPFLEET_OUTPUT sets --output, as docs/design.md says, and the flag wins
// over it. Not parallel: it sets the process's environment.
func TestOutputFromTheEnvironment(t *testing.T) {
	t.Setenv("WPFLEET_OUTPUT", "json")

	format := func(args ...string) string {
		root := cmd.New()

		var got string

		//nolint:exhaustruct_v5 // a test command needs only these
		root.Commands = append(root.Commands, &cli.Command{
			Name:   "format",
			Hidden: true,
			Action: func(_ context.Context, c *cli.Command) error {
				got = c.String(cmdutil.FlagOutput)

				return nil
			},
		})
		root.Writer, root.ErrWriter = io.Discard, io.Discard

		testutil.Equal(t, "exit code", cmd.Run(t.Context(), root, append([]string{"wpfleet"}, args...)), 0)

		return got
	}

	testutil.Equal(t, "from the environment", format("format"), "json")
	testutil.Equal(t, "the flag wins", format("--output", "table", "format"), "table")

	t.Setenv("WPFLEET_OUTPUT", "yaml")

	code, _, errOut := run(t, nil, "completion", "bash")
	testutil.Equal(t, "a bad value exit code", code, 1)
	testutil.Contains(t, "a bad value", errOut, "unknown output format")
}
