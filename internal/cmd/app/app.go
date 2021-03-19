package app

import (
	"fmt"
	"github.com/spf13/cobra"
	"os"
)

var (
	Command = &cobra.Command{
		Use:   "grpc-rpf",
		Short: "GRPC Remote Port Forward",
	}
	DevMode = false
)

func init() {
	Command.PersistentFlags().BoolVar(&DevMode, "dev-mode", false, "run in development mode. increases output verbosity.")
}

func HandleErrorWithExitCode(function func() error, code int) {
	if err := function(); err != nil {
		if DevMode {
			fmt.Printf("%+v", err)
		} else {
			fmt.Printf("%v", err)
		}
		os.Exit(code)
	}
}
