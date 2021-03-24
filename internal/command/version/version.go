package version

import (
	"fmt"
	"github.com/chirino/grpc-rpf/internal/command/app"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	Version       = "v0.0.0-next"
	showGoVersion = false
	Command       = &cobra.Command{
		Use:   "version",
		Short: "Print version information for this executable",
		Run:   run,
	}
)

func init() {
	Command.Flags().BoolVar(&showGoVersion, "go", showGoVersion, "get the go runtime version")
	app.Command.AddCommand(Command)
}

func run(cmd *cobra.Command, args []string) {
	if showGoVersion {
		fmt.Println(runtime.Version())
	} else {
		fmt.Println(Version)
	}
}
