// Package output renders command results (json, jsonl, pretty, toon) and
// errors (text or JSON envelope), and owns the exit-code mapping.
package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/GodGMN/booksy-cli/internal/booksy"
	toon "github.com/toon-format/toon-go"
)

type Format string

const (
	JSON   Format = "json"
	JSONL  Format = "jsonl"
	Pretty Format = "pretty"
	Toon   Format = "toon"
)

func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case JSON, JSONL, Pretty, Toon:
		return Format(s), nil
	}
	return "", booksy.Usage("unknown format %q. Use json, jsonl, pretty or toon", s)
}

// Prettier is implemented by result types that have a human rendering. Types
// without one fall back to indented JSON in pretty mode.
type Prettier interface {
	Pretty(w io.Writer, color bool)
}

// Printer writes results to stdout in the chosen format.
type Printer struct {
	W      io.Writer
	Format Format
	Color  bool
}

// Print renders one value. In jsonl mode a slice is written one element per
// line; anything else is one line.
func (p Printer) Print(v any) error {
	switch p.Format {
	case JSONL:
		if items, ok := asSlice(v); ok {
			for _, it := range items {
				if _, err := fmt.Fprintf(p.W, "%s\n", it); err != nil {
					return err
				}
			}
			return nil
		}
		return json.NewEncoder(p.W).Encode(v)
	case Pretty:
		if pr, ok := v.(Prettier); ok {
			pr.Pretty(p.W, p.Color)
			return nil
		}
		enc := json.NewEncoder(p.W)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	case Toon:
		// Round-trip through JSON so toon keys follow the json tags, keeping the
		// field names identical to the JSON contract.
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var generic any
		if err := json.Unmarshal(raw, &generic); err != nil {
			return err
		}
		out, err := toon.Marshal(generic)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(p.W, string(out))
		return err
	}
	enc := json.NewEncoder(p.W)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// asSlice splits a JSON array into its elements as raw JSON, so each jsonl
// line keeps the field order of the json output.
func asSlice(v any) ([]json.RawMessage, bool) {
	raw, err := json.Marshal(v)
	if err != nil || len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, false
	}
	return items, true
}

// Table is the shared pretty renderer: tab-aligned columns, header bolded
// when colour is on. Deliberately plain; no box drawing.
func Table(w io.Writer, color bool, header []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if len(header) > 0 {
		h := strings.Join(header, "\t")
		if color {
			h = "\x1b[1m" + h + "\x1b[0m"
		}
		fmt.Fprintln(tw, h)
	}
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
}

// Truncate cuts s to n runes with an ellipsis, for table cells.
func Truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\t", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Dim wraps s in the faint SGR when colour is on.
func Dim(s string, color bool) string {
	if !color {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
}

// Errors

// ErrorFormat selects stderr rendering.
type ErrorFormat string

const (
	ErrText ErrorFormat = "text"
	ErrJSON ErrorFormat = "json"
)

// Envelope is the machine-readable error, printed on stderr with --error-format json.
type Envelope struct {
	Code              int      `json:"code"`
	Category          string   `json:"category"`
	Retryable         bool     `json:"retryable"`
	Message           string   `json:"message"`
	HTTPStatus        int      `json:"http_status,omitempty"`
	Endpoint          string   `json:"endpoint,omitempty"`
	SuggestedCommands []string `json:"suggested_commands,omitempty"`
	IssueURL          string   `json:"issue_url,omitempty"`
}

// Classify turns any error into its envelope. Command is the noun/verb path
// (no argument values) used in the issue link.
func Classify(err error, version, command string) Envelope {
	env := Envelope{Code: 1, Category: "generic", Message: err.Error()}
	var we *booksy.Error
	if errors.As(err, &we) {
		env.Code = we.Kind.ExitCode()
		env.Category = we.Kind.String()
		env.Retryable = we.Retryable
		env.HTTPStatus = we.Status
		env.Endpoint = we.Endpoint
		switch we.Kind {
		case booksy.KindAuth:
			env.SuggestedCommands = []string{"booksy auth status", "booksy auth login"}
		case booksy.KindAPIChanged:
			env.IssueURL = IssueURL(version, command, we)
			env.SuggestedCommands = []string{"booksy doctor", "rerun with --debug"}
		case booksy.KindBlocked:
			env.SuggestedCommands = []string{"wait a few minutes and retry"}
		}
	}
	var ue *UsageError
	if errors.As(err, &ue) {
		env.Code = 2
		env.Category = "usage"
	}
	return env
}

// UsageError marks caller mistakes detected in the CLI layer (bad flags,
// missing arguments); cobra parse failures are wrapped into it too.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

func Usagef(format string, args ...any) error { return &UsageError{Msg: fmt.Sprintf(format, args...)} }

const repo = "https://github.com/GodGMN/booksy-cli"

// IssueURL prefills the api-change issue form. Field ids match
// .github/ISSUE_TEMPLATE/api-change.yml. No argument values are included.
func IssueURL(version, command string, e *booksy.Error) string {
	q := url.Values{}
	q.Set("template", "api-change.yml")
	q.Set("title", "[api change] "+strings.TrimSpace(e.Endpoint+" "+command))
	q.Set("version", version)
	q.Set("os", runtime.GOOS+"/"+runtime.GOARCH)
	q.Set("command", command)
	q.Set("endpoint", e.Endpoint)
	if e.Status != 0 {
		q.Set("status", fmt.Sprint(e.Status))
	}
	q.Set("body_excerpt", e.Body)
	return repo + "/issues/new?" + q.Encode()
}

// PrintError writes the error to stderr in the chosen shape and returns the exit code.
func PrintError(w io.Writer, err error, format ErrorFormat, version, command string) int {
	env := Classify(err, version, command)
	if format == ErrJSON {
		_ = json.NewEncoder(w).Encode(env)
		return env.Code
	}
	fmt.Fprintf(w, "booksy: %s\n", env.Message)
	if env.Category == "api_changed" {
		fmt.Fprintf(w, "\nbooksy may have changed its api. Please report it (fields are prefilled):\n  %s\n", env.IssueURL)
		fmt.Fprintln(w, "rerun with --debug for the full request log.")
	} else if len(env.SuggestedCommands) > 0 {
		fmt.Fprintf(w, "  try: %s\n", strings.Join(env.SuggestedCommands, "  |  "))
	}
	return env.Code
}

// ColorEnabled follows NO_COLOR, TERM=dumb and TTY-ness of the writer.
func ColorEnabled(noColorFlag bool, f *os.File) bool {
	if noColorFlag || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return IsTerminal(f)
}

// IsTerminal reports whether f is a character device (a TTY).
func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
