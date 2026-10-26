package booksy

import "fmt"

// Kind classifies failures so the CLI can map them to exit codes and hints
// without string-sniffing messages.
type Kind int

const (
	KindGeneric    Kind = iota // 1
	KindUsage                  // 2: caller passed something Booksy cannot take
	KindAuth                   // 3: no access token or token rejected
	KindNotFound               // 4
	KindBlocked                // 5: bot protection or 429
	KindNetwork                // 6
	KindAPIChanged             // 7: known endpoint, unexpected shape or status
)

func (k Kind) String() string {
	switch k {
	case KindUsage:
		return "usage"
	case KindAuth:
		return "auth"
	case KindNotFound:
		return "not_found"
	case KindBlocked:
		return "blocked"
	case KindNetwork:
		return "network"
	case KindAPIChanged:
		return "api_changed"
	}
	return "generic"
}

// ExitCode follows the table in docs/cli-spec.md.
func (k Kind) ExitCode() int {
	switch k {
	case KindUsage:
		return 2
	case KindAuth:
		return 3
	case KindNotFound:
		return 4
	case KindBlocked:
		return 5
	case KindNetwork:
		return 6
	case KindAPIChanged:
		return 7
	}
	return 1
}

// Error is every failure the API layer produces. Body is already redacted and
// truncated, so it is safe to print or to put in an issue link.
type Error struct {
	Kind      Kind
	Status    int
	APICode   string
	Endpoint  string
	Body      string
	Msg       string
	Retryable bool
	// KeyRejected marks a refusal of the public web API key (not the login).
	KeyRejected bool
	// Reason is a stable machine-readable cause for agents, when there is one
	// (card_required, slot_unavailable). Exit codes stay coarse; this does not.
	Reason string
}

func (e *Error) Error() string { return e.Msg }

// Usage builds a KindUsage error for input the caller can fix.
func Usage(format string, args ...any) *Error {
	return &Error{Kind: KindUsage, Msg: fmt.Sprintf(format, args...)}
}

// NotFound builds a KindNotFound error with a human subject.
func NotFound(format string, args ...any) *Error {
	return &Error{Kind: KindNotFound, Msg: fmt.Sprintf(format, args...)}
}
