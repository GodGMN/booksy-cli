package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/GodGMN/booksy-cli/internal/booksy"
	"github.com/GodGMN/booksy-cli/internal/output"
)

type meView struct{ booksy.Me }

func (v meView) Pretty(w io.Writer, color bool) {
	fmt.Fprintf(w, "%s %s  %s\n", v.FirstName, v.LastName, output.Dim(fmt.Sprintf("#%d", v.ID), color))
	fmt.Fprintln(w, v.Email)
	if v.Phone != "" {
		fmt.Fprintln(w, v.Phone)
	}
	if v.City != "" {
		fmt.Fprintln(w, v.City)
	}
}

func (a *App) meCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "me", Short: "Your Booksy account"}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Account details",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireSession(); err != nil {
				return err
			}
			me, err := a.Client.Me(cmd.Context())
			if err != nil {
				return err
			}
			return a.Printer.Print(meView{me})
		},
	})
	var page int
	reviews := &cobra.Command{
		Use:   "reviews",
		Short: "Reviews you have written",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireSession(); err != nil {
				return err
			}
			rp, err := a.Client.MyReviews(cmd.Context(), page)
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
	cmd.AddCommand(reviews)
	return cmd
}

type bookmarksView []booksy.BusinessSummary

func (v bookmarksView) Pretty(w io.Writer, color bool) {
	searchView{booksy.SearchPage{Total: len(v), Page: 1, Businesses: v}}.Pretty(w, color)
}

func (a *App) bookmarkCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "bookmark", Aliases: []string{"bookmarks"}, Short: "Businesses saved to your account"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Saved businesses",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireSession(); err != nil {
				return err
			}
			bs, err := a.Client.Bookmarks(cmd.Context())
			if err != nil {
				return err
			}
			return a.Printer.Print(bookmarksView(bs))
		},
	})
	set := func(use, short string, on bool) *cobra.Command {
		return &cobra.Command{
			Use:   use + " <business>",
			Short: short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := a.requireSession(); err != nil {
					return err
				}
				id, err := booksy.ParseBusinessRef(args[0])
				if err != nil {
					return err
				}
				if err := a.Client.SetBookmark(cmd.Context(), id, on); err != nil {
					return err
				}
				return a.Printer.Print(map[string]any{"business_id": id, "bookmarked": on})
			},
		}
	}
	cmd.AddCommand(set("add", "Save a business", true), set("remove", "Unsave a business", false))
	return cmd
}
