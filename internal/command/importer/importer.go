package importer

import (
	"fmt"
	"github.com/chirino/grpc-rpf/internal/command/app"
	"github.com/chirino/grpc-rpf/internal/pkg/importer"
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
	importsDir = ""
	config     = importer.Config{
		ServerAddress: "localhost:34343",
		Services:      map[string]importer.ServiceConfig{},
		Log:           log.New(os.Stdout, "", 0),
	}
	Command = &cobra.Command{
		Use:   "importer",
		Short: "Runs the reverse port forward client to import services from the server.",
		Long: `Runs the reverse port forward client to import services from the server.  

Use this in your private network to import services to a reverse port forward server`,
		PersistentPreRunE: app.BindEnv("GRPC_RPF"),
		RunE:              commandRun,
	}
	imports []string
)

func init() {
	Command.Flags().StringVar(&config.ServerAddress, "server", config.ServerAddress, "server address in address:port format")
	Command.Flags().StringArrayVar(&imports, "import", imports, "service to import in name=address:port format")
	Command.Flags().StringVar(&config.AccessToken, "access-token", config.AccessToken, "access token used to identify the client")
	Command.Flags().StringVar(&importsDir, "imports-dir", importsDir, "watch a directory holding import configurations")
	Command.Flags().BoolVar(&config.Insecure, "insecure", false, "connect to the server using an insecure TLS connection.")
	Command.Flags().StringVar(&config.CAFile, "ca-file", "ca.crt", "certificate authority file")
	Command.Flags().StringVar(&config.CertFile, "crt-file", "importer.crt", "public certificate file")
	Command.Flags().StringVar(&config.KeyFile, "key-file", "importer.key", "private key file")
	app.Command.AddCommand(Command)
}

func commandRun(_ *cobra.Command, _ []string) error {
	if importsDir == "" && len(imports) == 0 {
		return fmt.Errorf("option --import or --import-dir is required")
	}
	if importsDir != "" && len(imports) != 0 {
		return fmt.Errorf("option --import or --import-dir are exclusive, use only one")
	}

	for _, s := range imports {
		sp := strings.Split(s, "=")
		if len(sp) != 2 {
			return fmt.Errorf("invalid --import argument: should be in name=host:port format, got: %s", s)
		}
		name := sp[0]
		hostPort := sp[1]
		config.Services[name] = importer.ServiceConfig{
			Listen: hostPort,
		}
	}

	app.HandleErrorWithExitCode(func() error {
		c, err := importer.New(config)
		if err != nil {
			return err
		}

		err = c.Start()
		if err != nil {
			return err
		}

		defer c.Stop()
		if importsDir != "" {
			err := loadExporterConfigs(c)
			if err != nil {
				return err
			}
			stopWatching, err := utils.WatchDir(importsDir, func() {
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

func loadExporterConfigs(c *importer.Client) error {
	files, err := ioutil.ReadDir(importsDir)
	if err != nil {
		return fmt.Errorf("error loading imports directory: %v", err)
	}

	type ExporterConfig struct {
		Name   string `yaml:"name"`
		Listen string `yaml:"listen"`
	}

	config.Services = map[string]importer.ServiceConfig{}
	for _, f := range files {
		value := ExporterConfig{}
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

		config.Services[value.Name] = importer.ServiceConfig{
			Listen: value.Listen,
		}
	}
	c.SetServices(config.Services)
	return nil
}
