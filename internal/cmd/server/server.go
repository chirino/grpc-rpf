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
	"path/filepath"
	"strings"
)

var (
	listen     = ":34343"
	importsDir = "imports.d"
	config     = server.Config{
		Services:  map[string]server.ServiceConfig{},
		TLSConfig: grpcapi.TLSConfig{},
	}
	Command = &cobra.Command{
		Use:   "server",
		Short: "Runs the reverse port forward server.",
		Long: `Runs the reverse port forward server.  

Use this in your public network to import services that are exported from the client.`,
		RunE: commandRun,
	}
	services = []string{}
)

func init() {
	Command.Flags().StringVar(&listen, "listen", listen, "grpc address to bind")
	Command.Flags().StringArrayVar(&services, "import", services, "service address to bind in name=address:port format")
	Command.Flags().StringVar(&importsDir, "imports-dir", importsDir, "directory holding import configurations")
	Command.Flags().BoolVar(&config.Insecure, "insecure", false, "private key file")
	Command.Flags().StringVar(&config.CAFile, "ca-file", "ca.crt", "certificate authority file")
	Command.Flags().StringVar(&config.CertFile, "crt-file", "importer.crt", "public certificate file")
	Command.Flags().StringVar(&config.KeyFile, "key-file", "importer.key", "private key file")
	app.Command.AddCommand(Command)
}

type ImportConfig struct {
	Name   string
	Listen string
}

func commandRun(cmd *cobra.Command, args []string) error {

	for _, s := range services {
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

		s, err := server.New(config)
		if err != nil {
			return err
		}
		s.Serve(importerListener)

		if importsDir != "" {
			stopWatching, err := utils.WatchDir(importsDir, loadImporterConfigs)
			if err != nil {
				return err
			}
			defer stopWatching()
		}

		////log.Printf("stopping importer")
		//s.Stop()
		return nil
	}, 2)
	return nil
}

func loadImporterConfigs() {
	files, err := ioutil.ReadDir(importsDir)
	if err != nil {
		log.Printf("error loading imports directory: %v", err)
		return
	}

	var values []ImportConfig
	for _, f := range files {
		value := ImportConfig{}
		file := filepath.Join(importsDir, f.Name())
		err := utils.ReadConfigFile(file, &value)
		if err != nil {
			log.Printf("error loading import file '%s': %v", file, err)
			continue
		}
		values = append(values, value)
	}
}
