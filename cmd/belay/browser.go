package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"time"
)

var openBrowser = openBrowserDirect

func attemptBrowserOpen(
	ctx context.Context,
	url string,
	commandName string,
	stderr io.Writer,
) {
	openCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := openBrowser(openCtx, url); err != nil {
		fmt.Fprintf(
			stderr,
			"belay %s: browser could not be opened; use the printed Local URL\n",
			commandName,
		)
	}
}

func openBrowserDirect(ctx context.Context, url string) error {
	if url == "" {
		return errors.New("browser URL is required")
	}
	name, args, err := browserCommand(runtime.GOOS, url)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}

func browserCommand(goos, url string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "/usr/bin/open", []string{url}, nil
	case "linux":
		return "xdg-open", []string{url}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}, nil
	default:
		return "", nil, errors.New("browser opening is unsupported")
	}
}
