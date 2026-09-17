package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "brp:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: brp worker --socket PATH | brp version")
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Println("Usage: brp worker --socket PATH | brp version")
		return nil
	case "version":
		fs := flag.NewFlagSet("brp version", flag.ContinueOnError)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("version takes no arguments")
		}
		fmt.Println(version)
		return nil
	case "worker":
		return workerCommand(ctx, args[1:], version)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
