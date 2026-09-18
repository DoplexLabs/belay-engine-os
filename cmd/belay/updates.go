package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/updatecheck"
)

func runUpdates(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("updates", flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "Belay Local state directory (default BELAY_HOME or ~/.belay)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, `usage: belay updates [status|check|on|off] [--home DIR]

Belay checks the public GitHub release list at most once every 18 hours and
shows a gentle Local notification when a newer compatible release is available.
No Belay identifier or session information is sent. "off" stops automatic
checks; BELAY_UPDATE_CHECK=0 also works.`)
	}
	action := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("updates accepts one action: status, check, on, or off")
	}
	paths, err := localapp.ResolvePaths(*home)
	if err != nil {
		return err
	}
	client := updatecheck.New(paths.Root, buildVersion)
	switch action {
	case "status":
	case "check":
		if _, err := client.Check(ctx, true); err != nil {
			return err
		}
	case "on":
		if err := client.SetOptOut(false); err != nil {
			return err
		}
	case "off":
		if err := client.SetOptOut(true); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown updates action %q", action)
	}
	return writeJSON(stdout, client.Status())
}
