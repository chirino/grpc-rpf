package client

import (
	"context"
	"fmt"
	"github.com/chirino/grpc-rpf/internal/cmd/app"
	"github.com/chirino/grpc-rpf/internal/pkg/client"
	"github.com/spf13/cobra"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

var (
	config = client.Config{
		ImporterAddress: "localhost:34343",
		Services:        map[string]string{},
	}
	Command = &cobra.Command{
		Use:   "client",
		Short: "Runs the reverse port forward client.",
		Long: `Runs the reverse port forward client.  

Use this in your private network to export services to a reverse port forward server`,
		RunE: commandRun,
	}
	services = []string{}
)

func init() {

	Command.Flags().StringVar(&config.ImporterAddress, "server", config.ImporterAddress, "server address in address:port format")
	Command.Flags().StringArrayVar(&services, "export", services, "service to export in name=address:port format")
	_ = Command.MarkFlagRequired("export")
	Command.Flags().BoolVar(&config.Insecure, "insecure", false, "connect to the server using an insecure TLS connection.")
	Command.Flags().StringVar(&config.CAFile, "ca-file", "ca.crt", "certificate authority file")
	Command.Flags().StringVar(&config.CertFile, "crt-file", "importer.crt", "public certificate file")
	Command.Flags().StringVar(&config.KeyFile, "key-file", "importer.key", "private key file")
	app.Command.AddCommand(Command)
}

func commandRun(cmd *cobra.Command, args []string) error {

	for _, s := range services {
		sp := strings.Split(s, "=")
		if len(sp) != 2 {
			return fmt.Errorf("invalid --export argument: should be in name=host:port format, got: %s", s)
		}
		name := sp[0]
		hostPort := sp[1]
		config.Services[name] = hostPort
	}

	app.HandleErrorWithExitCode(func() error {
		stopExporter, err := client.Serve(context.Background(), config)
		if err != nil {
			return err
		}

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		<-sigs
		fmt.Println("shutting down")

		stopExporter()
		return nil
	}, 2)
	return nil
}
