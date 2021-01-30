package app

import (
	"fmt"
	"github.com/spf13/cobra"
	"os"
)

var (
	Command = &cobra.Command{
		Use:   "hsvcproxy",
		Short: "The Hybrid Service Proxy command line tool",
	}
	DevMode = false
)

func init() {
	Command.PersistentFlags().BoolVar(&DevMode, "dev-mode", false, "run in development mode. increases output verbosity.")
}


func HandleErrorWithExitCode(function func () error, code int) {
	if err := function(); err != nil {
		if DevMode {
			fmt.Printf("%+v", err)
		} else {
			fmt.Printf("%v", err)
		}
		os.Exit(code)
	}
}