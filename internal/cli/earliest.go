package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/GodGMN/booksy-cli/internal/booksy"
	"github.com/GodGMN/booksy-cli/internal/output"
)

type treatmentsView []booksy.Treatment

func (v treatmentsView) Pretty(w io.Writer, color bool) {
	rows := [][]string{}
	for _, t := range v {
		rows = append(rows, []string{strconv.Itoa(t.ID), t.Name})
	}
	output.Table(w, color, []string{"ID", "TREATMENT"}, rows)
}

func (a *App) treatmentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "treatment",
		Short: "Booksy's shared service catalogue (ids for earliest --treatment)",
		Long: `Each business names its services as it likes ("Arreglo de barba"), but every
service is filed under one of Booksy's catalogue treatments ("Recorte de la
barba", 71). Treatments are how one service is compared across businesses.`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "search <text...>",
		Short:   "Find treatments matching text, as the booksy.com search box does",
		Example: `  booksy treatment search barba`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ts, err := a.Client.Treatments(cmd.Context(), strings.Join(args, " "))
			if err != nil {
				return err
			}
			return a.Printer.Print(treatmentsView(ts))
		},
	})
	return cmd
}

// EarliestSlot is one row of `booksy earliest`: everything needed to book it.
type EarliestSlot struct {
	booksy.Slot
	BusinessID  int    `json:"business_id"`
	Business    string `json:"business"`
	DistanceM   int    `json:"distance_m,omitempty"`
	Address     string `json:"address,omitempty"`
	Service     string `json:"service"`
	VariantID   int    `json:"variant_id"`
	DurationMin int    `json:"duration_min"`
	Price       string `json:"price,omitempty"`
	URL         string `json:"url,omitempty"`
}

type EarliestResult struct {
	Treatment booksy.Treatment `json:"treatment"`
	From      string           `json:"from"`
	To        string           `json:"to"`
	Checked   int              `json:"businesses_checked"`
	Results   []EarliestSlot   `json:"results"`
}

func (r EarliestResult) Pretty(w io.Writer, color bool) {
	rows := [][]string{}
	for _, s := range r.Results {
		day := s.Date
		if t, err := time.Parse(time.DateOnly, s.Date); err == nil {
			day = t.Format("Mon 02 Jan")
		}
		dist := ""
		if s.DistanceM > 0 {
			dist = formatDistance(s.DistanceM)
		}
		rows = append(rows, []string{day, s.Time, output.Truncate(s.Business, 30), dist, output.Truncate(s.Service, 26), s.Price, strconv.Itoa(s.BusinessID), strconv.Itoa(s.VariantID)})
	}
	output.Table(w, color, []string{"DAY", "TIME", "BUSINESS", "DIST", "SERVICE", "PRICE", "BUSINESS_ID", "VARIANT"}, rows)
	fmt.Fprintln(w, output.Dim(fmt.Sprintf("%s · %d businesses checked, %s to %s · book with: booksy book BUSINESS_ID --service VARIANT --at DATETIME", r.Treatment.Name, r.Checked, r.From, r.To), color))
}

// earliestConcurrency caps parallel slot queries so one command does not
// look like a burst of bots to Booksy.
const earliestConcurrency = 4

func (a *App) earliestCmd() *cobra.Command {
	var sf slotFlags
	var w where
	var treatmentID, limit, perBusiness, businesses int
	cmd := &cobra.Command{
		Use:   "earliest [treatment text...]",
		Short: "Earliest open slots for one treatment across nearby businesses",
		Long: `Find the earliest open slots for one kind of service across many businesses.

Matching goes by Booksy's catalogue treatment, not by service names, which each
business picks itself: give --treatment ID (see ` + "`booksy treatment search`" + `) or
text, which is resolved to the best treatment match. Businesses come from the
same search as ` + "`booksy search`" + ` (--near, --geo, else the profile location), nearest
first. Results are sorted by start time and carry the business and variant
ids to pass to ` + "`booksy book`" + `.

Cancellation fees and prepayments only show at booking time: run
` + "`booksy book ... --dry-run`" + ` on the pick before booking.`,
		Example: `  booksy earliest barba --near Xàtiva --when "mon-fri 16:00-close" --when sat
  booksy earliest --treatment 71 --geo 38.99,-0.52 --days 21 --limit 5 --format pretty
  booksy earliest "corte caballero" --near Valencia --when "weekdays 18:00-" --per-business 3`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if sf.service != "" || sf.staff != "" {
				return output.Usagef("earliest picks each business's own service for the treatment; --service and --staff only apply to `slots` and `book`")
			}
			filter, err := sf.filter()
			if err != nil {
				return err
			}
			from, to, err := sf.window(time.Now())
			if err != nil {
				return err
			}
			tr, err := a.resolveTreatment(ctx, treatmentID, strings.Join(args, " "))
			if err != nil {
				return err
			}
			p := booksy.SearchParams{Treatment: tr.ID, Sort: "distance", PerPage: businesses}
			if err := a.resolveWhere(ctx, w, &p); err != nil {
				return err
			}
			if p.Lat == 0 && p.Lng == 0 {
				p.Sort = ""
			}
			page, err := a.Client.Search(ctx, p)
			if err != nil {
				return err
			}
			res := EarliestResult{Treatment: tr, From: from.Format(time.DateOnly), To: to.Format(time.DateOnly), Results: []EarliestSlot{}}
			var mu sync.Mutex
			var wg sync.WaitGroup
			sem := make(chan struct{}, earliestConcurrency)
			for _, b := range page.Businesses {
				cands := earliestCandidates(b, 2)
				if len(cands) == 0 {
					continue
				}
				res.Checked++
				wg.Add(1)
				go func() {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					found := a.earliestAt(ctx, b, cands, from, to, filter, perBusiness)
					mu.Lock()
					res.Results = append(res.Results, found...)
					mu.Unlock()
				}()
			}
			wg.Wait()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			sort.SliceStable(res.Results, func(i, j int) bool {
				ri, rj := res.Results[i], res.Results[j]
				if ri.Start != rj.Start {
					return ri.Start < rj.Start
				}
				return ri.DistanceM < rj.DistanceM
			})
			if limit > 0 && len(res.Results) > limit {
				res.Results = res.Results[:limit]
			}
			if a.Printer.Format == output.JSONL {
				return a.Printer.Print(res.Results)
			}
			return a.Printer.Print(res)
		},
	}
	w.bind(cmd)
	sf.bind(cmd, 14)
	f := cmd.Flags()
	f.IntVar(&treatmentID, "treatment", 0, "catalogue treatment id (see `booksy treatment search`)")
	f.IntVar(&limit, "limit", 10, "at most this many slots in total (0 for all)")
	f.IntVar(&perBusiness, "per-business", 1, "at most this many slots from each business")
	f.IntVar(&businesses, "businesses", 15, "how many nearby businesses to check")
	_ = f.MarkHidden("service")
	_ = f.MarkHidden("staff")
	return cmd
}

func (a *App) resolveTreatment(ctx context.Context, id int, text string) (booksy.Treatment, error) {
	switch {
	case id != 0 && text != "":
		return booksy.Treatment{}, output.Usagef("pass --treatment or treatment text, not both")
	case id != 0:
		return booksy.Treatment{ID: id, Name: fmt.Sprintf("treatment %d", id)}, nil
	case text == "":
		return booksy.Treatment{}, output.Usagef("say which treatment: text such as \"barba\", or --treatment ID")
	}
	ts, err := a.Client.Treatments(ctx, text)
	if err != nil {
		return booksy.Treatment{}, err
	}
	if len(ts) == 0 {
		return booksy.Treatment{}, booksy.NotFound("no booksy treatment matches %q; try other words or `booksy treatment search`", text)
	}
	if len(ts) > 1 {
		var alt []string
		for _, t := range ts[1:min(len(ts), 4)] {
			alt = append(alt, fmt.Sprintf("%d %s", t.ID, t.Name))
		}
		a.notice("using treatment %d %q for %q (others: %s; pass --treatment to pick)", ts[0].ID, ts[0].Name, text, strings.Join(alt, ", "))
	}
	return ts[0], nil
}

type candidate struct {
	svc booksy.Service
	v   booksy.Variant
}

// earliestCandidates picks up to max bookable services of the treatment at
// one business, using each one's first variant (its base duration and price).
func earliestCandidates(b booksy.BusinessSummary, max int) []candidate {
	var out []candidate
	for _, s := range b.Services {
		if !s.Bookable || len(s.Variants) == 0 {
			continue
		}
		out = append(out, candidate{s, s.Variants[0]})
		if len(out) == max {
			break
		}
	}
	return out
}

func (a *App) earliestAt(ctx context.Context, b booksy.BusinessSummary, cands []candidate, from, to time.Time, filter booksy.SlotFilter, per int) []EarliestSlot {
	var out []EarliestSlot
	for _, c := range cands {
		slots, err := a.Client.Slots(ctx, booksy.SlotsQuery{BusinessID: b.ID, VariantID: c.v.ID, StafferID: -1, From: from, To: to})
		if err != nil {
			if ctx.Err() == nil {
				a.notice("skipping %s (%d): %v", b.Name, b.ID, err)
			}
			return out
		}
		for _, s := range filter.Apply(slots) {
			out = append(out, EarliestSlot{
				Slot: s, BusinessID: b.ID, Business: b.Name, DistanceM: b.DistanceM, Address: b.Address, URL: b.URL,
				Service: serviceLabel(c.svc, c.v), VariantID: c.v.ID, DurationMin: c.v.DurationMin, Price: c.v.PriceText,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	// Two services of one business can open the same minute; keep one.
	dedup := out[:0]
	seen := map[string]bool{}
	for _, s := range out {
		if !seen[s.Start] {
			seen[s.Start] = true
			dedup = append(dedup, s)
		}
	}
	if per > 0 && len(dedup) > per {
		dedup = dedup[:per]
	}
	return dedup
}
