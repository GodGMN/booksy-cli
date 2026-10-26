package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/GodGMN/booksy-cli/internal/booksy"
	"github.com/GodGMN/booksy-cli/internal/config"
	"github.com/GodGMN/booksy-cli/internal/output"
	"github.com/GodGMN/booksy-cli/internal/sink"
	"github.com/GodGMN/booksy-cli/internal/store"
	"github.com/GodGMN/booksy-cli/internal/watch"
)

const (
	defaultInterval = 15 * time.Minute
	// minInterval keeps a watch from hammering one business's calendar.
	minInterval = 2 * time.Minute
)

func (a *App) watchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Get told when a slot opens: add, check, run, schedule",
		Long: `A watch remembers which slots were open for one service and reports the ones
that open later, typically because someone cancelled. Watches live locally in
the state database; Booksy knows nothing about them.

The first check records what is open without reporting it (--emit-initial
reports it). Slots that appear only because the booking window rolled forward
a day are not reported either.

"check" runs due watches once, "run" loops in the foreground, and "service
install" sets up a systemd timer or launchd agent that runs "check".`,
	}
	add := &cobra.Command{Use: "add", Short: "Create a watch"}
	add.AddCommand(a.watchAddSlotsCmd())
	cmd.AddCommand(add, a.watchListCmd(), a.watchRemoveCmd(), a.watchCheckCmd(), a.watchRunCmd(), a.watchEventsCmd(), a.watchServiceCmd())
	return cmd
}

var watchName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (a *App) configuredInterval(flag time.Duration) (time.Duration, error) {
	iv := flag
	if iv == 0 && a.Cfg.Watch.Interval != "" {
		d, err := time.ParseDuration(a.Cfg.Watch.Interval)
		if err != nil {
			return 0, output.Usagef("config watch.interval: %v", err)
		}
		iv = d
	}
	if iv == 0 {
		iv = defaultInterval
	}
	if iv < minInterval {
		return 0, output.Usagef("interval %s is below the floor of %s", iv, minInterval)
	}
	return iv, nil
}

func (a *App) checkSinks(names []string) error {
	for _, n := range names {
		cfg, ok := a.Cfg.Sinks[n]
		if !ok {
			return output.Usagef("no sink named %q in config.toml (see `booksy sink list`)", n)
		}
		if err := sink.Validate(n, cfg); err != nil {
			return output.Usagef("%v", err)
		}
	}
	return nil
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

func (a *App) watchAddSlotsCmd() *cobra.Command {
	var sf slotFlags
	var name string
	var notify []string
	var interval time.Duration
	var emitInitial bool
	cmd := &cobra.Command{
		Use:   "slots <business>",
		Short: "Watch one service for newly opened slots",
		Example: `  booksy watch add slots 3349 --service corte --staff Javier --between 17:00-20:00
  booksy watch add slots 3349 --service 273164 --before 2026-11-07 --notify phone --name barber
  booksy watch add slots 3349 --service corte --weekdays sat --days 21 --interval 10m`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			iv, err := a.configuredInterval(interval)
			if err != nil {
				return err
			}
			if err := a.checkSinks(notify); err != nil {
				return err
			}
			if sf.from != "" {
				return output.Usagef("a watch always looks from today; use --before or --weekdays to narrow it")
			}
			b, err := a.loadBusiness(ctx, args[0])
			if err != nil {
				return err
			}
			svc, v, err := b.ResolveVariant(sf.service)
			if err != nil {
				return err
			}
			staffID, staffName, err := resolveStaff(b, sf.staff)
			if err != nil {
				return err
			}
			filter, err := sf.filter()
			if err != nil {
				return err
			}
			if name == "" {
				name = slugify(b.Name + " " + svc.Name)
			}
			if !watchName.MatchString(name) {
				return output.Usagef("watch name %q: use letters, digits, dot, dash or underscore", name)
			}
			t := watch.SlotsTarget{
				BusinessID: b.ID, Business: b.Name, URL: b.URL, VariantID: v.ID, Service: serviceLabel(svc, v),
				Staffer: staffName, Days: sf.days, Filter: filter,
			}
			if staffID > 0 {
				t.StafferID = staffID
			}
			raw, _ := json.Marshal(t)
			st, err := a.Store()
			if err != nil {
				return err
			}
			w, err := st.AddWatch(ctx, store.Watch{Name: name, Profile: a.Profile, Kind: store.KindSlots, Target: raw, Sinks: notify, Interval: iv})
			if errors.Is(err, store.ErrExists) {
				return output.Usagef("a watch named %q already exists; pass --name", name)
			} else if err != nil {
				return err
			}
			// Baseline right away so the first scheduled check already diffs.
			res, err := watch.Check(ctx, a.Client, st, w, time.Now(), emitInitial)
			if err != nil {
				_ = st.RemoveWatch(ctx, w.Name)
				return err
			}
			if err := a.deliver(ctx, w, res.Events); err != nil {
				a.notice("%v", err)
			}
			if err := st.CommitCheck(ctx, w.ID, res.Present, res.Removed, res.Events); err != nil {
				return err
			}
			w, _ = st.Watch(ctx, w.Name)
			a.notice("watching %s: %d slots open now", w.Name, len(res.Open))
			if emitInitial && len(res.Events) > 0 {
				return a.Printer.Print(eventList(res.Events))
			}
			return a.Printer.Print(watchView{w})
		},
	}
	sf.bind(cmd, 14)
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "watch name (default from business and service)")
	f.StringSliceVar(&notify, "notify", nil, "sinks from config.toml to deliver events to")
	f.DurationVar(&interval, "interval", 0, "check at most this often (default watch.interval, else 15m)")
	f.BoolVar(&emitInitial, "emit-initial", false, "report the slots open right now as an event")
	return cmd
}

func describeTarget(w store.Watch) string {
	var t watch.SlotsTarget
	if json.Unmarshal(w.Target, &t) != nil {
		return string(w.Target)
	}
	parts := []string{fmt.Sprintf("%s @ %s", t.Service, t.Business)}
	if t.Staffer != "" {
		parts = append(parts, "with "+t.Staffer)
	}
	if t.Filter.Between != "" {
		parts = append(parts, t.Filter.Between)
	}
	if len(t.Filter.Weekdays) > 0 {
		var ds []string
		for _, d := range t.Filter.Weekdays {
			ds = append(ds, weekdayNames[d])
		}
		parts = append(parts, strings.Join(ds, ","))
	}
	if t.Filter.Before != "" {
		parts = append(parts, "before "+t.Filter.Before)
	}
	for _, w := range t.Filter.Windows {
		parts = append(parts, windowLabel(w))
	}
	return strings.Join(parts, " · ")
}

func windowLabel(w booksy.Window) string {
	label := strings.Join(append(dayRuns(w.Weekdays), w.Dates...), ",")
	switch {
	case w.Off:
		return label + " off"
	case w.From == "00:00" && w.Till == "24:00":
		return label
	}
	till := w.Till
	if till == "24:00" {
		till = "close"
	}
	return label + " " + w.From + "-" + till
}

// dayRuns renders ISO weekdays compactly: [1 2 3 4 5 7] is Mon-Fri, Sun.
func dayRuns(days []int) []string {
	var out []string
	for i := 0; i < len(days); {
		j := i
		for j+1 < len(days) && days[j+1] == days[j]+1 {
			j++
		}
		switch {
		case j-i >= 2:
			out = append(out, weekdayNames[days[i]]+"-"+weekdayNames[days[j]])
		case j > i:
			out = append(out, weekdayNames[days[i]], weekdayNames[days[j]])
		default:
			out = append(out, weekdayNames[days[i]])
		}
		i = j + 1
	}
	return out
}

type watchView struct{ store.Watch }

func (v watchView) Pretty(w io.Writer, color bool) { watchList{v.Watch}.Pretty(w, color) }

type watchList []store.Watch

func (l watchList) Pretty(w io.Writer, color bool) {
	rows := [][]string{}
	for _, wt := range l {
		last := "never"
		if !wt.LastCheckAt.IsZero() {
			last = wt.LastCheckAt.Local().Format("01-02 15:04")
		}
		rows = append(rows, []string{wt.Name, output.Truncate(describeTarget(wt), 60), wt.Interval.String(), strings.Join(wt.Sinks, ","), output.Dim(last, color)})
	}
	output.Table(w, color, []string{"NAME", "TARGET", "EVERY", "SINKS", "LAST CHECK"}, rows)
}

func (a *App) watchListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use: "list", Short: "Watches of the profile (--all for every profile)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.Store()
			if err != nil {
				return err
			}
			profile := a.Profile
			if all {
				profile = ""
			}
			ws, err := st.Watches(cmd.Context(), profile)
			if err != nil {
				return err
			}
			return a.Printer.Print(watchList(ws))
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "every profile")
	return cmd
}

func (a *App) watchRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use: "remove <name...>", Short: "Delete watches and their history", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.Store()
			if err != nil {
				return err
			}
			for _, n := range args {
				if _, err := a.ownWatch(cmd.Context(), st, n); err != nil {
					return err
				}
				if err := st.RemoveWatch(cmd.Context(), n); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

type eventList []store.Event

func (l eventList) Pretty(w io.Writer, color bool) {
	if len(l) == 0 {
		fmt.Fprintln(w, output.Dim("no events", color))
		return
	}
	rows := [][]string{}
	for _, ev := range l {
		title, body, link := sink.Summary(ev)
		rows = append(rows, []string{output.Dim(ev.At.Local().Format("01-02 15:04"), color), ev.Watch, output.Truncate(title, 48), output.Truncate(body, 40), output.Dim(link, color)})
	}
	output.Table(w, color, []string{"WHEN", "WATCH", "EVENT", "DETAIL", "URL"}, rows)
}

// deliver sends events to the watch's sinks. It returns an error when any
// delivery failed, so the caller can leave the check uncommitted: the next
// check then reports the same slots again. A duplicate alert beats a missed
// one, since a missed cancellation is exactly what a watch exists to catch.
func (a *App) deliver(ctx context.Context, w store.Watch, events []store.Event) error {
	failed := 0
	for _, ev := range events {
		for _, name := range w.Sinks {
			cfg, ok := a.Cfg.Sinks[name]
			if !ok {
				a.notice("watch %s: sink %s is no longer in config.toml", w.Name, name)
				continue
			}
			if err := sink.Deliver(ctx, name, cfg, ev); err != nil {
				a.notice("sink %s: %v", name, err)
				failed++
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("watch %s: %d deliveries failed; the slots stay unreported and will be sent again on the next check", w.Name, failed)
	}
	return nil
}

// runChecks checks the given watches, delivers events, commits, and returns
// every event produced. Per-watch failures are reported on stderr and do not
// stop the others; the first failure is returned so the exit code reflects it.
func (a *App) runChecks(ctx context.Context, st *store.Store, ws []store.Watch) ([]store.Event, error) {
	all := []store.Event{}
	var firstErr error
	for _, w := range ws {
		res, err := watch.Check(ctx, a.Client, st, w, time.Now(), false)
		if err != nil {
			if ctx.Err() != nil {
				return all, ctx.Err()
			}
			a.notice("watch %s: %v", w.Name, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		all = append(all, res.Events...)
		if err := a.deliver(ctx, w, res.Events); err != nil {
			a.notice("%v", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := st.CommitCheck(ctx, w.ID, res.Present, res.Removed, res.Events); err != nil {
			return all, err
		}
	}
	return all, firstErr
}

// ownWatch loads a watch by name, refusing one that belongs to another
// profile: it would run with this profile's login and country.
func (a *App) ownWatch(ctx context.Context, st *store.Store, name string) (store.Watch, error) {
	w, err := st.Watch(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return w, booksy.NotFound("no watch named %q", name)
	} else if err != nil {
		return w, err
	}
	if w.Profile != a.Profile {
		return w, booksy.NotFound("watch %q belongs to profile %q; pass --profile %s", name, w.Profile, w.Profile)
	}
	return w, nil
}

// selectWatches picks named watches, or the due ones (or all with force).
func (a *App) selectWatches(ctx context.Context, st *store.Store, names []string, force bool) ([]store.Watch, error) {
	if len(names) > 0 {
		var out []store.Watch
		for _, n := range names {
			w, err := a.ownWatch(ctx, st, n)
			if err != nil {
				return nil, err
			}
			out = append(out, w)
		}
		return out, nil
	}
	ws, err := st.Watches(ctx, a.Profile)
	if err != nil {
		return nil, err
	}
	if force {
		return ws, nil
	}
	now := time.Now()
	due := ws[:0]
	for _, w := range ws {
		// A little slack so a timer firing on the interval is not skipped
		// because the previous run ended a few seconds late.
		if w.LastCheckAt.IsZero() || now.Sub(w.LastCheckAt) >= w.Interval-30*time.Second {
			due = append(due, w)
		}
	}
	return due, nil
}

func (a *App) watchCheckCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "check [name...]",
		Short: "Run due watches once and print their events",
		Long: `Run watches once. With names, those watches run regardless of schedule.
Without names, watches whose interval has elapsed run; --all forces every
watch. Prints the events produced (none is fine: exit 0) and delivers them to
the watches' sinks.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			a.rotateServiceLog()
			st, err := a.Store()
			if err != nil {
				return err
			}
			ws, err := a.selectWatches(cmd.Context(), st, args, all)
			if err != nil {
				return err
			}
			events, err := a.runChecks(cmd.Context(), st, ws)
			if perr := a.Printer.Print(eventList(events)); perr != nil {
				return perr
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "run every watch of the profile, due or not")
	return cmd
}

func (a *App) watchRunCmd() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Check due watches in a loop until Ctrl-C",
		Long: `Foreground loop. Every tick runs the due watches and prints events as they
happen (use --format jsonl to stream). The tick defaults to the shortest watch
interval, with up to 10% jitter.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			st, err := a.Store()
			if err != nil {
				return err
			}
			if interval != 0 && interval < minInterval {
				return output.Usagef("--interval %s is below the floor of %s", interval, minInterval)
			}
			for {
				ws, err := a.selectWatches(ctx, st, nil, false)
				if err != nil {
					return err
				}
				events, err := a.runChecks(ctx, st, ws)
				if ctx.Err() != nil {
					return nil
				}
				var be *booksy.Error
				if errors.As(err, &be) && be.Kind == booksy.KindBlocked {
					a.notice("booksy is blocking requests; backing off for 15 minutes")
					select {
					case <-ctx.Done():
						return nil
					case <-time.After(15 * time.Minute):
					}
					continue
				}
				for _, ev := range events {
					if err := a.Printer.Print(ev); err != nil {
						return err
					}
				}
				tick := interval
				if tick == 0 {
					tick = a.shortestInterval(ctx, st)
				}
				tick += watch.Jitter(tick, time.Now().UnixNano())
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(tick):
				}
			}
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 0, "tick length (default: shortest watch interval)")
	return cmd
}

func (a *App) shortestInterval(ctx context.Context, st *store.Store) time.Duration {
	ws, err := st.Watches(ctx, a.Profile)
	if err != nil || len(ws) == 0 {
		return defaultInterval
	}
	m := ws[0].Interval
	for _, w := range ws[1:] {
		m = min(m, w.Interval)
	}
	return max(m, minInterval)
}

func (a *App) watchEventsCmd() *cobra.Command {
	var since time.Duration
	var limit int
	cmd := &cobra.Command{
		Use: "events [name]", Short: "Show stored events, newest first", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.Store()
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			var from time.Time
			if since > 0 {
				from = time.Now().Add(-since)
			}
			evs, err := st.Events(cmd.Context(), a.Profile, name, from, limit)
			if err != nil {
				return err
			}
			return a.Printer.Print(eventList(evs))
		},
	}
	cmd.Flags().DurationVar(&since, "since", 0, "only events newer than this, e.g. 24h")
	cmd.Flags().IntVar(&limit, "limit", 50, "at most this many")
	return cmd
}

// sinks

type sinkRow struct {
	Name string `json:"name"`
	config.SinkConfig
	Error string `json:"error,omitempty"`
}

type sinkList []sinkRow

func (l sinkList) Pretty(w io.Writer, color bool) {
	rows := [][]string{}
	for _, s := range l {
		target := s.URL
		if s.Type == "ntfy" {
			target = strings.TrimRight(s.URL, "/") + "/" + s.Topic
		}
		if s.Type == "exec" {
			target = strings.Join(s.Command, " ")
		}
		rows = append(rows, []string{s.Name, s.Type, target, s.Error})
	}
	output.Table(w, color, []string{"NAME", "TYPE", "TARGET", "PROBLEM"}, rows)
}

// redactSink hides the parts of a sink that grant access: webhook paths
// carry tokens and an ntfy topic is its own password. `config get` still
// shows them for the user who asks explicitly.
func redactSink(c config.SinkConfig) config.SinkConfig {
	if u, err := url.Parse(c.URL); err == nil && u.Host != "" {
		if u.Path != "" && u.Path != "/" || u.RawQuery != "" {
			c.URL = u.Scheme + "://" + u.Host + "/…"
		}
	}
	if len(c.Topic) > 3 {
		c.Topic = c.Topic[:2] + "…"
	}
	return c
}

func (a *App) sinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sink",
		Short: "Notification targets for watches (ntfy, webhook, exec)",
		Long: `Sinks are declared in config.toml, for example:

  [sinks.phone]
  type = "ntfy"
  url = "https://ntfy.sh"
  topic = "my-secret-topic"

  [sinks.discord]
  type = "webhook"
  url = "https://discord.com/api/webhooks/..."
  template = "discord"

  [sinks.script]
  type = "exec"
  command = ["~/bin/on-slot.sh"]   # gets the event JSON on stdin

or with ` + "`booksy config set sinks.phone.type ntfy`" + ` and friends.`,
	}
	cmd.AddCommand(&cobra.Command{
		Use: "list", Short: "Configured sinks", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			names := make([]string, 0, len(a.Cfg.Sinks))
			for n := range a.Cfg.Sinks {
				names = append(names, n)
			}
			sort.Strings(names)
			out := sinkList{}
			for _, n := range names {
				row := sinkRow{Name: n, SinkConfig: redactSink(a.Cfg.Sinks[n])}
				if err := sink.Validate(n, row.SinkConfig); err != nil {
					row.Error = err.Error()
				}
				out = append(out, row)
			}
			return a.Printer.Print(out)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "test <name>", Short: "Send a sample event to a sink", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.checkSinks(args); err != nil {
				return err
			}
			data, _ := json.Marshal(watch.Opened{
				BusinessID: 1, Business: "Booksy CLI test", Service: "Test slot", URL: "https://booksy.com",
				Earliest: booksy.Slot{Date: time.Now().Format(time.DateOnly), Time: "18:00", Start: time.Now().Format(time.DateOnly) + "T18:00"},
			})
			ev := store.Event{Type: watch.EventSlotsOpened, Watch: "test", Profile: a.Profile, At: time.Now().UTC(), Data: data}
			if err := sink.Deliver(cmd.Context(), args[0], a.Cfg.Sinks[args[0]], ev); err != nil {
				return err
			}
			a.notice("sent a test event to %s", args[0])
			return nil
		},
	})
	return cmd
}
