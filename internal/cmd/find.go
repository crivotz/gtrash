package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/juju/ansiterm"
	"github.com/spf13/cobra"
	"github.com/umlx5h/gtrash/internal/glog"
	"github.com/umlx5h/gtrash/internal/trash"
	"github.com/umlx5h/gtrash/internal/tui"
)

type findCmd struct {
	cmd  *cobra.Command
	opts findOptions
}

type findOptions struct {
	directory string
	cwd       bool
	sortBy    trash.SortByType
	modeBy    trash.ModeByType

	// do options
	doRemove  bool
	doRestore bool
	force     bool

	dayNew int // unit day
	dayOld int

	sizeLarge string
	sizeSmall string

	reverse bool
	last    int

	// control display info
	showSize      bool
	showTrashPath bool

	restoreTo string
}

func newFindCmd() *findCmd {
	root := &findCmd{}
	cmd := &cobra.Command{
		Use:     "find [QUERY...]",
		Aliases: []string{"f"},
		Short:   "Find trashed files and do restore or remove them (f)",
		Long: `Description:
  All deleted files can be displayed and searched.
  You can search by passing a string as a command line argument.

  If you want to delete or restore the searched files, pass the --rm and --restore options, respectively.`,
		Example: `  # Show all trashed files
  $ gtrash find

  # Show files under the current directory
  $ gtrash find --cwd

  # Searching for files using regular expressions and do restore
  # If you use special symbols, please use quotes to prevent shell expansion
  $ gtrash find 'regex' --restore

  # Display the actual file path and file size at the same time
  $ gtrash find --show-size --show-trashpath

  # Showing the 10 most recently deleted
  $ gtrash find -n 10

  # Showing 10 files sorted by file size
  $ gtrash find -n 10 --sort size

  # Delete all files (CAUTION)
  $ gtrash find --rm

  # Restore all files
  $ gtrash find --restore

  # Remove files deleted more than a week ago
  $ gtrash find --day-old 7 --rm

  # Remove greater than 10MB trashed files
  $ gtrash find --size-large 10mb --rm

  # fuzzy find multiple items and remove them permanently
  # The -o in xargs is required to display the confirmation prompt.
  $ gtrash find | fzf --multi | awk -F'\t' '{print $2}' | xargs -o gtrash rm`,
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := findCmdRun(args, root.opts); err != nil {
				return err
			}
			if glog.ExitCode() > 0 {
				return errContinue
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&root.opts.directory, "directory", "d", "", "Filter by directory")
	cmd.Flags().StringVar(&root.opts.sizeLarge, "size-large", "", "Filter by size larger  (e.g. 5MB, 1GB)")
	cmd.Flags().StringVar(&root.opts.sizeSmall, "size-small", "", "Filter by size smaller (e.g. 5MB, 1GB)")
	cmd.Flags().BoolVarP(&root.opts.cwd, "cwd", "c", false, "Filter by current working directory")
	cmd.Flags().VarP(&root.opts.sortBy, "sort", "s", "Sort by")
	cmd.Flags().VarP(&root.opts.modeBy, "mode", "m", `query mode
regex (default):
    Go language regular expression engine is used.
    You can test it at the following site
    ref: https://regex101.com

glob:
    Glob patterns can be specified.
    The following engine is used, please refer to the following site for notation.
    ref: https://github.com/gobwas/glob

literal:
    Ignores case and performs literal matching
    If it matches part of the path, it will hit.

full:
    Matches an exact match to a full path.
    Case sensitive.`)
	cmd.Flags().BoolVar(&root.opts.doRemove, "rm", false, "Do remove PERMANENTLY")
	cmd.Flags().BoolVar(&root.opts.doRestore, "restore", false, "Do restore")
	cmd.Flags().BoolVarP(&root.opts.force, "force", "f", false, `Always do --rm or --restore without confirmation prompt
This is not necessary if running outside of a terminal`)
	cmd.Flags().IntVar(&root.opts.dayNew, "day-new", 0, "Filter by deletion date (within X day)")
	cmd.Flags().IntVar(&root.opts.dayOld, "day-old", 0, "Filter by deletion date (before X day)")
	cmd.Flags().BoolVarP(&root.opts.showSize, "show-size", "S", false, `Show size always
Automatically enabled if --sort size, --size-large, --size-small specified
If the size could not be obtained, it will be displayed as '-'`)
	cmd.Flags().BoolVar(&root.opts.showTrashPath, "show-trashpath", false, "Show trash path")
	cmd.Flags().BoolVarP(&root.opts.reverse, "reverse", "r", false, "Reverse sort order (default: ascending)")
	cmd.Flags().StringVar(&root.opts.restoreTo, "restore-to", "", "Restore to this path instead of original path")
	cmd.Flags().IntVarP(&root.opts.last, "last", "n", 0, "Show n last files")

	cmd.MarkFlagsMutuallyExclusive("rm", "restore")
	cmd.MarkFlagsMutuallyExclusive("directory", "cwd")
	cmd.MarkFlagsMutuallyExclusive("day-new", "day-old")
	cmd.MarkFlagsMutuallyExclusive("size-large", "size-small")

	if err := cmd.RegisterFlagCompletionFunc("sort", trash.SortByFlagCompletionFunc); err != nil {
		panic(err)
	}
	if err := cmd.RegisterFlagCompletionFunc("mode", trash.ModeByFlagCompletionFunc); err != nil {
		panic(err)
	}

	root.cmd = cmd
	return root
}

func findCmdRun(args []string, opts findOptions) error {
	slog.Debug("starting find", "args", args, "doRemove", opts.doRemove, "doRestore", opts.doRestore)

	if err := checkOptRestoreTo(&opts.restoreTo); err != nil {
		return err
	}

	box := trash.NewBox(
		trash.WithAscend(!opts.reverse),
		trash.WithGetSize(opts.showSize),
		trash.WithDirectory(opts.directory),
		trash.WithCWD(opts.cwd),
		trash.WithQueries(args),
		trash.WithSortBy(opts.sortBy),
		trash.WithQueryMode(opts.modeBy),
		trash.WithDay(opts.dayNew, opts.dayOld), // TODO: also set in restore?
		trash.WithSize(opts.sizeLarge, opts.sizeSmall),
		trash.WithLimitLast(opts.last),
	)
	if err := box.Open(); err != nil {
		return err
	}

	listFiles(box.Files, box.GetSize, opts.showTrashPath)

	if !opts.doRemove && !opts.doRestore {
		if isTerminal {
			fmt.Printf("\nFound %d trashed files. You can restore or remove PERMANENTLY these by --restore, --rm.\n", len(box.Files))
			if len(box.OrphanMeta) > 0 {
				fmt.Printf("\nFound invalid metadata: %d\nYou can remove invalid metadata by 'gtrash metafix'\n", len(box.OrphanMeta))
			}
		}
		return nil
	}

	fmt.Printf("\nFound %d trashed files\n", len(box.Files))

	if opts.doRemove {
		if !opts.force && isTerminal && !tui.BoolPrompt("Are you sure you want to remove PERMENANTLY? ") {
			return errors.New("do nothing")
		}
		if err := doRemove(box.Files); err != nil {
			return err
		}

	} else if opts.doRestore {
		if opts.restoreTo != "" {
			fmt.Printf("Will restore to %q instead of original path\n", opts.restoreTo)
		}

		if !opts.force && isTerminal && !tui.BoolPrompt("Are you sure you want to restore? ") {
			return errors.New("do nothing")
		}
		if err := doRestore(box.Files, opts.restoreTo); err != nil {
			return err
		}
	}

	return nil
}

// TODO: refactor
func listFiles(files []trash.File, showSize, showTrashPath bool) {
	if isTerminal {
		// colored, tabular view
		green := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))

		// replacement to tabwriter (color supported)
		w := ansiterm.NewTabWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if showSize {
			fmt.Fprintf(w, "%s\t%s\t%s", green.Render("Date"), green.Render("Size"), green.Render("Path"))
		} else {
			fmt.Fprintf(w, "%s\t%s", green.Render("Date"), green.Render("Path"))
		}

		if showTrashPath {
			fmt.Fprintf(w, "\t%s\n", green.Render("TrashPath"))
		} else {
			fmt.Fprintf(w, "\n")
		}

		for _, f := range files {
			if showSize {
				fmt.Fprintf(w, "%v\t%v\t%v", f.DeletedAt.Format(time.DateTime), f.SizeHuman(), f.OriginalPathFormat(false, true))
			} else {
				fmt.Fprintf(w, "%v\t%v", f.DeletedAt.Format(time.DateTime), f.OriginalPathFormat(false, true))
			}

			if showTrashPath {
				fmt.Fprintf(w, "\t%v\n", f.TrashPathColor())
			} else {
				fmt.Fprintf(w, "\n")
			}

		}
		w.Flush()

	} else {
		// no colored, splitted by TAB
		for _, f := range files {

			if showSize {
				fmt.Printf("%v\t%v\t%v", f.DeletedAt.Format(time.DateTime), f.SizeHuman(), f.OriginalPath)
			} else {
				fmt.Printf("%v\t%v", f.DeletedAt.Format(time.DateTime), f.OriginalPath)
			}

			if showTrashPath {
				fmt.Printf("\t%v\n", f.TrashPath)
			} else {
				fmt.Printf("\n")
			}
		}
	}

}
