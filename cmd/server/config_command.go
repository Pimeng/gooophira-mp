package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Pimeng/gooophira-mp/internal/config"
)

func runConfigCommand(args []string, stdout, stderr io.Writer) (handled bool, exitCode int) {
	if len(args) == 0 || args[0] != "config" {
		return false, 0
	}
	if len(args) < 2 || args[1] != "migrate" {
		fmt.Fprintln(stderr, "usage: phira-mp config migrate [-from server_config.yml] [-to config] [--dry-run]")
		return true, 2
	}

	fs := flag.NewFlagSet("config migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", defaultConfigPath, "legacy configuration file")
	to := fs.String("to", defaultConfigDir, "destination configuration directory")
	dryRun := fs.Bool("dry-run", false, "show planned files without writing")
	if err := fs.Parse(args[2:]); err != nil {
		return true, 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return true, 2
	}

	plan, err := config.BuildMigrationPlan(*from)
	if err != nil {
		fmt.Fprintf(stderr, "migration failed: %v\n", err)
		return true, 1
	}
	for _, name := range plan.Names() {
		fmt.Fprintln(stdout, name)
	}
	if *dryRun {
		return true, 0
	}
	if err := plan.Write(*to); err != nil {
		fmt.Fprintf(stderr, "migration failed: %v\n", err)
		return true, 1
	}
	return true, 0
}

func maybeRunConfigCommand() {
	if handled, code := runConfigCommand(os.Args[1:], os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}
}
