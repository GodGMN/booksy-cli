package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/GodGMN/booksy-cli/internal/output"
	"github.com/GodGMN/booksy-cli/internal/updater"
)

// UpdateStatus is the JSON contract of `booksy update --check`.
type UpdateStatus struct {
	Current   string `json:"current_version"`
	Latest    string `json:"latest_version"`
	Available bool   `json:"update_available"`
	Updated   bool   `json:"updated,omitempty"`
}

func (a *App) updateCmd() *cobra.Command {
	var yes, check bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update booksy to the latest release",
		Long: `Check GitHub for the latest stable release and replace this binary with it,
after verifying it against the release's checksums.txt. --check only reports.

A private repository needs a token: GITHUB_TOKEN, GH_TOKEN, BOOKSY_GITHUB_TOKEN,
or a logged-in gh CLI. Development builds (not vX.Y.Z) are never replaced, and
a binary from ` + "`go install`" + ` is pointed back to go install.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := updater.New()
			r, err := c.Latest(cmd.Context())
			if err != nil {
				return err
			}
			st := UpdateStatus{Current: a.Version, Latest: r.Tag, Available: updater.Newer(r.Tag, a.Version)}
			if !updater.Stable(a.Version) {
				st.Available = false
				if !check {
					return output.Usagef("this is a development build (%s); install a release first (latest is %s)", a.Version, r.Tag)
				}
			}
			if check || !st.Available {
				return a.Printer.Print(st)
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			if exe, err = filepath.EvalSymlinks(exe); err != nil {
				return err
			}
			if updater.GoInstalled(exe) {
				return output.Usagef("installed with go install; update with `go install github.com/%s/cmd/booksy@%s`", updater.Repo, r.Tag)
			}
			if err := a.confirm(yes, fmt.Sprintf("Update booksy %s to %s?", a.Version, r.Tag)); err != nil {
				return err
			}
			if err := c.Install(cmd.Context(), r, exe); err != nil {
				return err
			}
			st.Updated = true
			a.notice("updated to %s", r.Tag)
			return a.Printer.Print(st)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "update without asking")
	cmd.Flags().BoolVar(&check, "check", false, "only report whether an update exists")
	return cmd
}

// updateNotice prints one line on stderr when a newer release exists. It
// runs at most once a day, only in an interactive terminal, never for
// watch/update/completion, and never changes the command's output or exit
// code. BOOKSY_NO_UPDATE_CHECK=1 turns it off.
func (a *App) updateNotice(ctx context.Context, cmd *cobra.Command) {
	if !a.Interactive || !updater.Stable(a.Version) || os.Getenv("BOOKSY_NO_UPDATE_CHECK") != "" || a.Paths.StateDir == "" {
		return
	}
	top := cmd
	for top.Parent() != nil && top.Parent().Parent() != nil {
		top = top.Parent()
	}
	switch top.Name() {
	case "update", "watch", "completion", "help", "auth":
		return
	}
	stamp := filepath.Join(a.Paths.StateDir, "update-check")
	if info, err := os.Stat(stamp); err == nil && time.Since(info.ModTime()) < 24*time.Hour {
		return
	}
	if os.MkdirAll(a.Paths.StateDir, 0o700) != nil || os.WriteFile(stamp, []byte(time.Now().UTC().Format(time.RFC3339)), 0o600) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	r, err := updater.New().Latest(ctx)
	if err != nil {
		return
	}
	if updater.Newer(r.Tag, a.Version) {
		a.notice("%s is available (you have %s); run `booksy update`", r.Tag, strings.TrimPrefix(a.Version, "v"))
	}
}
