package server

import (
	"fmt"
	"github.com/chirino/grpc-rpf/internal/cmd/app"
	"github.com/chirino/grpc-rpf/internal/pkg/grpcapi"
	"github.com/chirino/grpc-rpf/internal/pkg/server"
	"github.com/chirino/grpc-rpf/internal/pkg/utils"
	"github.com/spf13/cobra"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

var (
	listen     = ":34343"
	importsDir = ""
	config     = server.Config{
		Services:  map[string]server.ServiceConfig{},
		TLSConfig: grpcapi.TLSConfig{},
		Log:       log.New(os.Stdout, "", 0),
	}
	Command = &cobra.Command{
		Use:   "server",
		Short: "Runs the reverse port forward server.",
		Long: `Runs the reverse port forward server.  

Use this in your public network to import services that are exported from the client.`,
		RunE: commandRun,
	}
	imports []string
)

func init() {
	Command.Flags().StringVar(&listen, "listen", listen, "grpc address to bind")
	Command.Flags().StringArrayVar(&imports, "import", imports, "service address to bind in name=address:port format")
	Command.Flags().StringVar(&importsDir, "imports-dir", importsDir, "watch a directory holding import configurations")
	Command.Flags().BoolVar(&config.Insecure, "insecure", false, "private key file")
	Command.Flags().StringVar(&config.CAFile, "ca-file", "ca.crt", "certificate authority file")
	Command.Flags().StringVar(&config.CertFile, "crt-file", "importer.crt", "public certificate file")
	Command.Flags().StringVar(&config.KeyFile, "key-file", "importer.key", "private key file")
	app.Command.AddCommand(Command)
}

func commandRun(_ *cobra.Command, _ []string) error {
	if importsDir == "" && len(imports) == 0 {
		return fmt.Errorf("option --import or --imports-dir is required")
	}
	if importsDir != "" && len(imports) != 0 {
		return fmt.Errorf("option --import or --imports-dir are exclusive, use only one")
	}

	for _, s := range imports {
		sp := strings.Split(s, "=")
		if len(sp) != 2 {
			return fmt.Errorf("invalid --import argument: should be in host:port=name format, got: %s", s)
		}
		name := sp[0]
		hostPort := sp[1]
		config.Services[name] = server.ServiceConfig{
			Listen: hostPort,
		}
	}

	app.HandleErrorWithExitCode(func() error {
		importerListener, err := net.Listen("tcp", listen)
		if err != nil {
			return err
		}
		defer importerListener.Close()

		s, err := server.New(config)
		if err != nil {
			return err
		}
		s.Start(importerListener)
		defer s.Stop()

		if importsDir != "" {
			err := loadImporterConfigs(s)
			if err != nil {
				return err
			}
			stopWatching, err := utils.WatchDir(importsDir, func() {
				config.Log.Println("config change detected")
				err := loadImporterConfigs(s)
				if err != nil {
					config.Log.Println(err)
				}
			})
			if err != nil {
				return err
			}
			defer stopWatching()
		}

		// Wait for shutdown signal...
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		<-signals
		config.Log.Println("shutting down")
		return nil
	}, 2)
	return nil
}

func loadImporterConfigs(serverInstance *server.Server) error {
	files, err := ioutil.ReadDir(importsDir)
	if err != nil {
		return fmt.Errorf("error loading imports directory: %v", err)
	}

	type ImportConfig struct {
		Name   string `yaml:"name"`
		Listen string `yaml:"listen"`
	}

	config.Services = map[string]server.ServiceConfig{}
	for _, f := range files {
		value := ImportConfig{}
		file := filepath.Join(importsDir, f.Name())
		err := utils.ReadConfigFile(file, &value)
		if err != nil {
			config.Log.Printf("error loading import file '%s': %v", file, err)
			continue
		}

		if value.Name == "" {
			config.Log.Printf("error loading import file '%s': service name not configured", file)
			continue
		}

		if value.Listen == "" {
			config.Log.Printf("error loading import file '%s': service listen address not configured", file)
			continue
		}

		config.Services[value.Name] = server.ServiceConfig{
			Listen: value.Listen,
		}
	}
	serverInstance.SetServices(config.Services)
	return nil
}
