package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/GodGMN/booksy-cli/internal/booksy"
	"github.com/GodGMN/booksy-cli/internal/output"
)

// where is the shared location flag set: --near TEXT or --geo LAT,LNG,
// falling back to the profile's saved location, else the whole country.
type where struct {
	near string
	geo  string
	any  bool
}

func (w *where) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&w.near, "near", "", `search around a place: "Valencia", "Calle Mayor 1, Madrid"`)
	cmd.Flags().StringVar(&w.geo, "geo", "", "search around LAT,LNG")
	cmd.Flags().BoolVar(&w.any, "anywhere", false, "ignore the profile's saved location and search the whole country")
	cmd.MarkFlagsMutuallyExclusive("near", "geo", "anywhere")
}

func (a *App) resolveWhere(ctx context.Context, w where, p *booksy.SearchParams) error {
	switch {
	case w.geo != "":
		lat, lng, ok := parseLatLng(w.geo)
		if !ok {
			return output.Usagef("--geo wants LAT,LNG such as 40.4168,-3.7038, got %q", w.geo)
		}
		p.Lat, p.Lng = lat, lng
	case w.near != "":
		place, err := a.Client.ResolvePlace(ctx, w.near)
		if err != nil {
			return err
		}
		p.Lat, p.Lng = place.Lat, place.Lng
	case w.any:
	default:
		if pc, ok := a.Cfg.Profiles[a.Profile]; ok && pc.Location != nil {
			p.Lat, p.Lng = pc.Location.Lat, pc.Location.Lng
		}
	}
	return nil
}

func parseLatLng(s string) (float64, float64, bool) {
	la, ln, ok := strings.Cut(s, ",")
	if !ok {
		return 0, 0, false
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(la), 64)
	lng, err2 := strconv.ParseFloat(strings.TrimSpace(ln), 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, 0, false
	}
	return lat, lng, true
}

type searchView struct{ booksy.SearchPage }

func (v searchView) Pretty(w io.Writer, color bool) {
	rows := [][]string{}
	for _, b := range v.Businesses {
		dist := ""
		if b.DistanceM > 0 {
			dist = formatDistance(b.DistanceM)
		}
		rows = append(rows, []string{strconv.Itoa(b.ID), output.Truncate(b.Name, 36), fmt.Sprintf("%.1f (%d)", b.Rating, b.Reviews), dist, output.Truncate(b.Address, 44)})
	}
	output.Table(w, color, []string{"ID", "NAME", "RATING", "DIST", "ADDRESS"}, rows)
	fmt.Fprintln(w, output.Dim(fmt.Sprintf("%d of %d, page %d", len(v.Businesses), v.Total, v.Page), color))
}

func formatDistance(m int) string {
	if m < 1000 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%.1fkm", float64(m)/1000)
}

func (a *App) searchCmd() *cobra.Command {
	var p booksy.SearchParams
	var w where
	var limit int
	cmd := &cobra.Command{
		Use:   "search [query...]",
		Short: "Search businesses by keyword, category and place",
		Long: `Search businesses the way booksy.com does. The query matches business and
service names ("barberia", "uñas", "masaje"). Results centre on --near or
--geo, else the profile's saved location, else the whole country.`,
		Example: `  booksy search barberia --near Valencia --format pretty
  booksy search --category 45 --geo 40.4168,-3.7038 --sort distance
  booksy search "corte de pelo" --limit 50 --format jsonl | jq -r .name`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p.Query = strings.Join(args, " ")
			p.PerPage = limit
			if p.Sort != "" && p.Sort != "score" && p.Sort != "distance" {
				return output.Usagef("--sort must be score or distance")
			}
			if err := a.resolveWhere(cmd.Context(), w, &p); err != nil {
				return err
			}
			page, err := a.Client.Search(cmd.Context(), p)
			if err != nil {
				return err
			}
			if a.Printer.Format == output.JSONL {
				return a.Printer.Print(page.Businesses)
			}
			return a.Printer.Print(searchView{page})
		},
	}
	w.bind(cmd)
	f := cmd.Flags()
	f.IntVar(&p.Category, "category", 0, "category id (see `booksy category list`)")
	f.StringVar(&p.Sort, "sort", "", "score (default) or distance")
	f.IntVar(&limit, "limit", 20, "results per page")
	f.IntVar(&p.Page, "page", 1, "page number")
	return cmd
}

type categoryView []booksy.Category

func (v categoryView) Pretty(w io.Writer, color bool) {
	rows := [][]string{}
	for _, c := range v {
		rows = append(rows, []string{strconv.Itoa(c.ID), c.Name})
	}
	output.Table(w, color, []string{"ID", "NAME"}, rows)
}

func (a *App) categoryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "category", Short: "Business categories"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List category ids for `search --category`",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cats, err := a.Client.Categories(cmd.Context())
			if err != nil {
				return err
			}
			return a.Printer.Print(categoryView(cats))
		},
	})
	return cmd
}

func (a *App) placeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "place", Short: "Resolve places to coordinates"}
	cmd.AddCommand(&cobra.Command{
		Use:     "resolve <text...>",
		Short:   "Show what --near would search around",
		Example: `  booksy place resolve "Plaza Mayor, Madrid"`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.Client.ResolvePlace(cmd.Context(), strings.Join(args, " "))
			if err != nil {
				return err
			}
			return a.Printer.Print(p)
		},
	})
	return cmd
}

// business

func (a *App) loadBusiness(ctx context.Context, ref string) (booksy.Business, error) {
	id, err := booksy.ParseBusinessRef(ref)
	if err != nil {
		return booksy.Business{}, err
	}
	return a.Client.Business(ctx, id)
}

var weekdayNames = []string{"", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

type businessView struct{ booksy.Business }

func (v businessView) Pretty(w io.Writer, color bool) {
	b := v.Business
	title := b.Name
	if color {
		title = "\x1b[1m" + title + "\x1b[0m"
	}
	fmt.Fprintf(w, "%s  %s\n", title, output.Dim(fmt.Sprintf("#%d", b.ID), color))
	fmt.Fprintf(w, "%s\n", b.Address)
	fmt.Fprintf(w, "rating %.2f from %d reviews\n", b.Rating, b.Reviews)
	if b.BookingPolicy != "" {
		fmt.Fprintf(w, "policy: %s\n", b.BookingPolicy)
	}
	fmt.Fprintln(w, b.URL)
	if len(b.OpenHours) > 0 {
		fmt.Fprintln(w)
		hoursView(b.OpenHours).Pretty(w, color)
	}
	if len(b.Staff) > 0 {
		var names []string
		for _, s := range b.Staff {
			names = append(names, s.Name)
		}
		fmt.Fprintf(w, "\nstaff: %s\n", strings.Join(names, ", "))
	}
	fmt.Fprintln(w)
	servicesView(b.Services).Pretty(w, color)
}

type servicesView []booksy.Service

func (v servicesView) Pretty(w io.Writer, color bool) {
	rows := [][]string{}
	last := ""
	for _, s := range v {
		cat := ""
		if s.Category != last {
			cat, last = s.Category, s.Category
		}
		for _, vr := range s.Variants {
			name := s.Name
			if vr.Label != "" {
				name += " · " + vr.Label
			}
			price := vr.PriceText
			if price == "" {
				price = strconv.FormatFloat(vr.Price, 'f', 2, 64)
			}
			rows = append(rows, []string{output.Truncate(cat, 18), strconv.Itoa(vr.ID), output.Truncate(name, 40), fmt.Sprintf("%dmin", vr.DurationMin), price})
		}
	}
	output.Table(w, color, []string{"CATEGORY", "VARIANT", "SERVICE", "TIME", "PRICE"}, rows)
}

type staffView []booksy.Staffer

func (v staffView) Pretty(w io.Writer, color bool) {
	rows := [][]string{}
	for _, s := range v {
		rows = append(rows, []string{strconv.Itoa(s.ID), s.Name, s.Position})
	}
	output.Table(w, color, []string{"ID", "NAME", "POSITION"}, rows)
}

type hoursView []booksy.OpenDay

func (v hoursView) Pretty(w io.Writer, color bool) {
	rows := [][]string{}
	for _, d := range v {
		name := strconv.Itoa(d.Weekday)
		if d.Weekday >= 1 && d.Weekday <= 7 {
			name = weekdayNames[d.Weekday]
		}
		rows = append(rows, []string{name, d.From + "–" + d.Till})
	}
	output.Table(w, color, nil, rows)
}

type reviewsView struct{ booksy.ReviewPage }

func (v reviewsView) Pretty(w io.Writer, color bool) {
	for _, r := range v.Reviews {
		head := fmt.Sprintf("%s %s", strings.Repeat("★", r.Rating)+strings.Repeat("☆", 5-r.Rating), r.Author)
		if r.BusinessName != "" {
			head += " @ " + r.BusinessName
		}
		fmt.Fprintf(w, "%s  %s\n", head, output.Dim(r.Created, color))
		if len(r.Services) > 0 || len(r.Staff) > 0 {
			fmt.Fprintln(w, output.Dim(strings.Join(append(r.Services, r.Staff...), " · "), color))
		}
		if r.Text != "" {
			fmt.Fprintln(w, r.Text)
		}
		if r.Reply != "" {
			fmt.Fprintln(w, output.Dim("  reply: "+r.Reply, color))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, output.Dim(fmt.Sprintf("%d reviews, %.2f average, page %d", v.Total, v.Rating, v.Page), color))
}

func (a *App) businessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "business",
		Short: "Inspect a business: details, services, staff, reviews, hours",
		Long: `Every business subcommand takes a numeric id or a booksy.com business URL,
for example https://booksy.com/es-es/3349_barberia-cavalieri-estudios-12_barberia_53009_madrid.`,
	}
	one := func(use, short string, run func(ctx context.Context, b booksy.Business) error) *cobra.Command {
		return &cobra.Command{
			Use:   use + " <business>",
			Short: short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				b, err := a.loadBusiness(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return run(cmd.Context(), b)
			},
		}
	}
	cmd.AddCommand(
		one("show", "Everything about a business", func(_ context.Context, b booksy.Business) error {
			return a.Printer.Print(businessView{b})
		}),
		one("services", "Services and their bookable variants (ids for --service)", func(_ context.Context, b booksy.Business) error {
			return a.Printer.Print(servicesView(b.Services))
		}),
		one("staff", "Staff members (ids for --staff)", func(_ context.Context, b booksy.Business) error {
			return a.Printer.Print(staffView(b.Staff))
		}),
		one("hours", "Opening hours", func(_ context.Context, b booksy.Business) error {
			return a.Printer.Print(hoursView(b.OpenHours))
		}),
		one("open", "Print the business's booksy.com URL", func(_ context.Context, b booksy.Business) error {
			fmt.Fprintln(a.Stdout, b.URL)
			return nil
		}),
	)

	var page, per int
	reviews := &cobra.Command{
		Use:   "reviews <business>",
		Short: "Customer reviews, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := booksy.ParseBusinessRef(args[0])
			if err != nil {
				return err
			}
			rp, err := a.Client.Reviews(cmd.Context(), id, page, per)
			if err != nil {
				return err
			}
			if a.Printer.Format == output.JSONL {
				return a.Printer.Print(rp.Reviews)
			}
			return a.Printer.Print(reviewsView{rp})
		},
	}
	reviews.Flags().IntVar(&page, "page", 1, "page number")
	reviews.Flags().IntVar(&per, "limit", 10, "reviews per page")
	cmd.AddCommand(reviews)
	return cmd
}
