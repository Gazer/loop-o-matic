package main

import (
	"context"
	"fmt"
	"os"

	"loop-o-matic/internal/app"
	"loop-o-matic/internal/commands"
)

func main() {
	ctx := context.Background()
	if err := commands.RunLoopdCLI(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(app.ExitCode(err))
	}
}
