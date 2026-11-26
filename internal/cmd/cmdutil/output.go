package cmdutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/urfave/cli/v3"
)

// Format is how a command prints its result.
type Format string

// The output formats.
const (
	// FormatTable is an aligned table for people.
	FormatTable Format = "table"
	// FormatJSON is a JSON array for scripts.
	FormatJSON Format = "json"
)

// ErrUnknownFormat is returned for an --output that is not a format.
var ErrUnknownFormat = errors.New("unknown output format, want table or json")

// tabPadding separates table columns.
const tabPadding = 2

// ParseFormat parses an --output value.
func ParseFormat(s string) (Format, error) {
	switch f := Format(strings.ToLower(s)); f {
	case FormatTable, FormatJSON:
		return f, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownFormat, s)
	}
}

// Column is one column of a table of rows of type R.
type Column[R any] struct {
	// Header is the column's title, printed in upper case.
	Header string
	// Value formats the column's cell for a row.
	Value func(R) string
}

// Write prints rows in format: a table with the columns, or the rows as a
// JSON array (by their json tags, ignoring the columns). An empty table
// prints only its header; empty JSON is [].
func Write[R any](w io.Writer, format Format, rows []R, columns ...Column[R]) error {
	if format == FormatJSON {
		if rows == nil {
			rows = []R{}
		}

		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		err := enc.Encode(rows)
		if err != nil {
			return fmt.Errorf("writing JSON: %w", err)
		}

		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, tabPadding, ' ', 0)

	headers := make([]string, 0, len(columns))
	for _, c := range columns {
		headers = append(headers, strings.ToUpper(c.Header))
	}

	_, _ = fmt.Fprintln(tw, strings.Join(headers, "\t"))

	cells := make([]string, len(columns))

	for _, row := range rows {
		for i, c := range columns {
			cells[i] = cell(c.Value(row))
		}

		_, _ = fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}

	err := tw.Flush()
	if err != nil {
		return fmt.Errorf("writing table: %w", err)
	}

	return nil
}

// Print writes rows to the command's writer in the format of --output.
func Print[R any](cmd *cli.Command, rows []R, columns ...Column[R]) error {
	format, err := ParseFormat(cmd.String(FlagOutput))
	if err != nil {
		return err
	}

	return Write(cmd.Root().Writer, format, rows, columns...)
}

// cell keeps a value on one line and shows an empty one as a dash.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "-"
	}

	return s
}
