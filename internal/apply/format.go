package apply

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/jaeyeom/gh-actionpins/internal/diff"
)

// Format names accepted by the CLI.
const (
	FormatTable = "table"
	FormatJSON  = "json"
)

// Write formats result to w. format is "table" (default) or "json".
func Write(w io.Writer, result *Result, format string) error {
	if result == nil {
		return fmt.Errorf("result is nil")
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", FormatTable:
		return writeTable(w, result)
	case FormatJSON:
		return writeJSON(w, result)
	default:
		return fmt.Errorf("unknown format %q (want %q or %q)", format, FormatTable, FormatJSON)
	}
}

func writeTable(w io.Writer, result *Result) error {
	if len(result.Changes) == 0 {
		msg := "No changes."
		if result.DryRun {
			msg = "No changes (dry-run)."
		}
		if _, err := fmt.Fprintln(w, msg); err != nil {
			return fmt.Errorf("write table: %w", err)
		}
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(tw, "FILE\tLINE\tACTION\tOLD\tNEW"); err != nil {
			return fmt.Errorf("write table header: %w", err)
		}
		for _, c := range result.Changes {
			if _, err := fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n",
				c.File, c.Line, c.Action, c.OldUses, c.NewUses); err != nil {
				return fmt.Errorf("write table row: %w", err)
			}
		}
		if err := tw.Flush(); err != nil {
			return fmt.Errorf("flush table: %w", err)
		}
	}
	return writeSummaryLine(w, result)
}

func writeSummaryLine(w io.Writer, r *Result) error {
	mode := "applied"
	if r.DryRun {
		mode = "would apply"
	}
	unknown := 0
	ok := 0
	for _, s := range r.Skipped {
		switch s.Status {
		case diff.StatusUnknown:
			unknown++
		case diff.StatusOK:
			ok++
		}
	}
	_, err := fmt.Fprintf(w, "summary: %s %d change(s); skipped unknown=%d ok=%d\n",
		mode, len(r.Changes), unknown, ok)
	if err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}

func writeJSON(w io.Writer, result *Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}
