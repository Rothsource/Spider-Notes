package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/sovannroth/mynotes/internal/config"
	"github.com/sovannroth/mynotes/internal/memosimport"
	"github.com/sovannroth/mynotes/internal/service"
	"github.com/sovannroth/mynotes/internal/store/sqlite"
)

// importMemos copies notes from a Memos data folder (a copy of it) into Spider Notes.
//
//	app import-memos --from /data/memos-import [--archived] [--dry-run]
func importMemos(args []string) error {
	fs := flag.NewFlagSet("import-memos", flag.ContinueOnError)
	from := fs.String("from", "", "Memos data folder containing memos_prod.db (and assets/)")
	archived := fs.Bool("archived", false, "also import archived memos (tagged #archived)")
	dry := fs.Bool("dry-run", false, "only list what would be imported")
	verbose := fs.Bool("v", false, "print every memo")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return fmt.Errorf("--from is required, e.g. app import-memos --from /data/memos-import")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := sqlite.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	svc := service.New(service.Options{Store: db, Location: cfg.Location, AttachDir: cfg.AttachmentsDir, MaxUpload: cfg.MaxUploadBytes})

	logf := func(string, ...any) {}
	if *verbose || *dry {
		logf = func(f string, a ...any) { fmt.Printf(f+"\n", a...) }
	}
	rep, err := memosimport.Run(ctx, svc, *from, memosimport.Options{IncludeArchived: *archived, DryRun: *dry, Log: logf})
	if err != nil {
		return err
	}
	verb := "Imported"
	if *dry {
		verb = "Would import"
	}
	fmt.Printf("Found %d memos in Memos (%d archived%s).\n", rep.Found, rep.Archived, map[bool]string{true: ", included", false: ", skipped"}[*archived])
	fmt.Printf("%s %d memos, copied %d files. Already imported earlier: %d.\n", verb, rep.Imported, rep.FilesCopied, rep.Skipped)
	if len(rep.Problems) > 0 {
		fmt.Fprintf(os.Stderr, "%d problem(s):\n", len(rep.Problems))
		for _, p := range rep.Problems {
			fmt.Fprintln(os.Stderr, "  -", p)
		}
	}
	return nil
}
