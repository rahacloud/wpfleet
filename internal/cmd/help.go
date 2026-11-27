package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"
)

// helpExitCode is the exit code of help for a command that does not exist,
// as urfave/cli's own help command uses.
const helpExitCode = 3

// fixHelp makes help name the command it was asked for in two cases where
// urfave/cli v3 does not.
//
// A command's --help runs urfave/cli's help action, which takes the first
// positional argument as the name of a subcommand to describe. A leaf
// command has none, so "wpfleet sites show blog --help" failed with
// "No help topic for 'blog'" instead of describing sites show. Every leaf
// command gets a CommandNotFound that prints its own help, which is what
// that action calls when the name matches no subcommand.
//
// The root's help command describes only the first name it is given, so
// "wpfleet help backup restore" described backup. The root gets a help
// command of its own that follows every name down the tree.
func fixHelp(root *cli.Command) {
	_ = root.Walk(func(c *cli.Command) error {
		if len(c.Commands) == 0 {
			c.CommandNotFound = showOwnHelp
		}

		return nil
	})

	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	root.Commands = append(root.Commands, &cli.Command{
		Name:      "help",
		Aliases:   []string{"h"},
		Usage:     cli.UsageCommandHelp,
		ArgsUsage: "[command [subcommand]]",
		HideHelp:  true,
		Action:    helpAction,
	})
}

// showOwnHelp prints the help of c itself, whatever name it was asked about.
func showOwnHelp(_ context.Context, c *cli.Command, _ string) {
	printHelp(c)
}

// helpAction prints the help of the command its arguments name, from the
// root down, or of the root without arguments. "help help" describes the
// help command itself, as urfave/cli's own help command does.
func helpAction(_ context.Context, help *cli.Command) error {
	target := help.Root()

	for _, name := range help.Args().Slice() {
		next := target.Command(name)
		if next == nil {
			path := strings.Join(append(target.Path()[1:], name), " ")

			//nolint:wrapcheck // the exit code is the point; Run prints the message as it is
			return cli.Exit(fmt.Sprintf("No help topic for '%s'", path), helpExitCode)
		}

		target = next
	}

	printHelp(target)

	return nil
}

// printHelp prints the help of c with the template urfave/cli uses for it.
func printHelp(c *cli.Command) {
	switch {
	case c.Root() == c:
		_ = cli.ShowRootCommandHelp(c)
	case c.CustomHelpTemplate != "":
		cli.HelpPrinter(c.Root().Writer, c.CustomHelpTemplate, c)
	case len(c.VisibleCommands()) == 0:
		cli.HelpPrinter(c.Root().Writer, cli.CommandHelpTemplate, c)
	default:
		_ = cli.ShowSubcommandHelp(c)
	}
}
