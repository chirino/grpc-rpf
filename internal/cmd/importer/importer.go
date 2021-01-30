package importer

import (
	"fmt"
	"github.com/chirino/hsvcproxy/internal/cmd/app"
	"github.com/chirino/hsvcproxy/internal/pkg/grpcapi"
	"github.com/chirino/hsvcproxy/internal/pkg/importer"
	"github.com/spf13/cobra"
	"log"
	"net"
	"os"
)

var (
	listen  = ":0"
	config  = importer.Config{}
	Command = &cobra.Command{
		Use:   "importer",
		Short: "Runs the importer",
		Run:   commandRun,
	}
)

func init() {
	Command.Flags().StringVar(&listen, "listen", listen, "address to ")
	//_ = Command.MarkFlagRequired("config")
	app.Command.AddCommand(Command)
}

func commandRun(cmd *cobra.Command, args []string) {
	app.HandleErrorWithExitCode(run, 2)
}

func run() error {
	importerListener, err := net.Listen("tcp", listen)

	// Run the importer.  It does not connect out to any services.  It receives connections
	// from the exporter and from other apps trying to connect to the private services exposed
	// by this importer.
	s, err := importer.New(config)
	FatalOnError(t, err)
	go s.Serve(importerListener)

	//log.Println("importer service is running at", importerListener.Addr())
	return importerListener, func() {
		//log.Printf("stopping importer")
		s.Stop()
	}
}
