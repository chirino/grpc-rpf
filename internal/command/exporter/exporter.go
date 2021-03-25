package exporter

import (
	"fmt"
	"github.com/chirino/grpc-rpf/internal/command/app"
	"github.com/chirino/grpc-rpf/internal/pkg/exporter"
	"github.com/chirino/grpc-rpf/internal/pkg/utils"
	"github.com/spf13/cobra"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

var (
	exportsDir = ""
	config     = exporter.Config{
		ServerAddress: "localhost:34343",
		Services:      map[string]exporter.ServiceConfig{},
		Log:           log.New(os.Stdout, "", 0),
	}
	Command = &cobra.Command{
		Use:   "exporter",
		Short: "Runs the reverse port forward client to export services to the server.",
		Long: `Runs the reverse port forward client to export services to the server.  

Use this in your private network to export services to a reverse port forward server`,
		PersistentPreRunE: app.BindEnv("GRPC_RPF"),
		RunE:              commandRun,
	}
	exports []string
)

func init() {
	Command.Flags().StringVar(&config.ServerAddress, "server", config.ServerAddress, "server address in address:port format")
	Command.Flags().StringArrayVar(&exports, "export", exports, "service to export in name=address:port format")
	Command.Flags().StringVar(&config.AccessToken, "access-token", config.AccessToken, "access token used to identify the client")
	Command.Flags().StringVar(&exportsDir, "exports-dir", exportsDir, "watch a directory holding export configurations")
	Command.Flags().BoolVar(&config.Insecure, "insecure", false, "connect to the server using an insecure TLS connection.")
	Command.Flags().StringVar(&config.CAFile, "ca-file", "", "certificate authority file")
	Command.Flags().StringVar(&config.CertFile, "crt-file", "", "public certificate file")
	Command.Flags().StringVar(&config.KeyFile, "key-file", "", "private key file")
	app.Command.AddCommand(Command)
}

func commandRun(_ *cobra.Command, _ []string) error {
	if exportsDir == "" && len(exports) == 0 {
		return fmt.Errorf("option --export or --export-dir is required")
	}
	if exportsDir != "" && len(exports) != 0 {
		return fmt.Errorf("option --export or --export-dir are exclusive, use only one")
	}

	for _, s := range exports {
		sp := strings.Split(s, "=")
		if len(sp) != 2 {
			return fmt.Errorf("invalid --export argument: should be in name=host:port format, got: %s", s)
		}
		name := sp[0]
		hostPort := sp[1]
		config.Services[name] = exporter.ServiceConfig{
			Address: hostPort,
		}
	}

	app.HandleErrorWithExitCode(func() error {
		c, err := exporter.New(config)
		if err != nil {
			return err
		}

		err = c.Start()
		if err != nil {
			return err
		}

		defer c.Stop()
		if exportsDir != "" {
			err := loadExporterConfigs(c)
			if err != nil {
				return err
			}
			stopWatching, err := utils.WatchDir(exportsDir, func() {
				config.Log.Println("config change detected")
				err := loadExporterConfigs(c)
				if err != nil {
					config.Log.Println(err)
				}
			})
			if err != nil {
				return err
			}
			defer stopWatching()
		}

		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		<-signals
		fmt.Println("shutting down")

		return nil
	}, 2)
	return nil
}

func loadExporterConfigs(c *exporter.Client) error {
	files, err := ioutil.ReadDir(exportsDir)
	if err != nil {
		return fmt.Errorf("error loading imports directory: %v", err)
	}

	type ExporterConfig struct {
		Name    string `yaml:"name"`
		Address string `yaml:"address"`
	}

	config.Services = map[string]exporter.ServiceConfig{}
	for _, f := range files {
		value := ExporterConfig{}
		file := filepath.Join(exportsDir, f.Name())
		err := utils.ReadConfigFile(file, &value)
		if err != nil {
			config.Log.Printf("error loading export file '%s': %v", file, err)
			continue
		}

		if value.Name == "" {
			config.Log.Printf("error loading export file '%s': service name not configured", file)
			continue
		}

		if value.Address == "" {
			config.Log.Printf("error loading export file '%s': service address not configured", file)
			continue
		}

		config.Services[value.Name] = exporter.ServiceConfig{
			Address: value.Address,
		}
	}
	c.SetServices(config.Services)
	return nil
}
