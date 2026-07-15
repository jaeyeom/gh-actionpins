package update

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// Format names accepted by the CLI.
const (
	FormatTable = "table"
	FormatJSON  = "json"
)

// WriteCheck formats a check-updates result.
func WriteCheck(w io.Writer, result *CheckResult, format string) error {
	if result == nil {
		return fmt.Errorf("result is nil")
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", FormatTable:
		return writeCheckTable(w, result)
	case FormatJSON:
		return writeJSON(w, result)
	default:
		return fmt.Errorf("unknown format %q (want %q or %q)", format, FormatTable, FormatJSON)
	}
}

// WriteProposal formats a propose-bump result.
func WriteProposal(w io.Writer, p *Proposal, format string) error {
	if p == nil {
		return fmt.Errorf("proposal is nil")
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", FormatTable:
		return writeProposalText(w, p)
	case FormatJSON:
		return writeJSON(w, p)
	default:
		return fmt.Errorf("unknown format %q (want %q or %q)", format, FormatTable, FormatJSON)
	}
}

func writeCheckTable(w io.Writer, result *CheckResult) error {
	if len(result.Entries) == 0 {
		if _, err := fmt.Fprintln(w, "No catalog actions."); err != nil {
			return fmt.Errorf("write table: %w", err)
		}
		return writeCheckSummary(w, result)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "STATUS\tACTION\tCURRENT\tLATEST\tAGE\tELIGIBLE\tDETAIL"); err != nil {
		return fmt.Errorf("write table header: %w", err)
	}
	for _, e := range result.Entries {
		latest := "-"
		age := "-"
		eligible := "-"
		if e.Latest != nil {
			latest = e.Latest.Version
			if !e.Latest.PublishedAt.IsZero() {
				age = formatAge(e.Latest.Age)
			} else if e.Latest.Age > 0 {
				age = formatAge(e.Latest.Age)
			}
		}
		if e.Eligible != nil {
			eligible = e.Eligible.Version
			if e.Eligible.SHA != "" {
				eligible = e.Eligible.Version + " " + shortSHA(e.Eligible.SHA)
			}
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			e.Status, e.Action, e.CurrentVersion, latest, age, eligible, e.Detail); err != nil {
			return fmt.Errorf("write table row: %w", err)
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("flush table: %w", err)
	}
	return writeCheckSummary(w, result)
}

func writeCheckSummary(w io.Writer, r *CheckResult) error {
	flag := "no updates"
	if r.Summary.Updates {
		flag = "updates available"
	}
	_, err := fmt.Fprintf(w, "summary: %s  available=%d too-new=%d blocked=%d current=%d error=%d  min_age=%s prefer=%s\n",
		flag,
		r.Summary.Available, r.Summary.TooNew, r.Summary.Blocked, r.Summary.Current, r.Summary.Error,
		formatDuration(r.MinAge), r.Prefer)
	if err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}

func writeProposalText(w io.Writer, p *Proposal) error {
	age := "-"
	if !p.PublishedAt.IsZero() {
		age = formatAge(p.Age)
	}
	published := "-"
	if !p.PublishedAt.IsZero() {
		published = p.PublishedAt.UTC().Format(time.RFC3339)
	}
	_, err := fmt.Fprintf(w, `proposed bump (catalog not modified)
  action:      %s
  from:        %s (%s)
  to:          %s (%s)
  published:   %s
  age:         %s (min_age %s)
  prefer:      %s
  note:        %s
`,
		p.Action,
		p.FromVersion, p.FromSHA,
		p.ToVersion, p.ToSHA,
		published,
		age, formatDuration(p.MinAge),
		p.Prefer,
		p.Note,
	)
	if err != nil {
		return fmt.Errorf("write proposal: %w", err)
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
