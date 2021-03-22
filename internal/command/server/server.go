package server

import (
	"context"
	"fmt"
	"github.com/chirino/grpc-rpf/internal/command/app"
	"github.com/chirino/grpc-rpf/internal/pkg/grpcapi"
	"github.com/chirino/grpc-rpf/internal/pkg/server"
	"github.com/chirino/grpc-rpf/internal/pkg/utils"
	grpc_auth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"github.com/spf13/cobra"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

type ImportConfig struct {
	Name                 string   `yaml:"name"`
	Listen               string   `yaml:"listen"`
	AllowedListenTokens  []string `yaml:"allowed-listen-tokens"`
	AllowedConnectTokens []string `yaml:"allowed-connect-tokens"`
}

var (
	listen      = ":34343"
	servicesDir = ""
	config      = server.Config{
		Services:  map[string]server.ServiceConfig{},
		TLSConfig: grpcapi.TLSConfig{},
		Log:       log.New(os.Stdout, "", 0),
	}
	importsMap = map[string]ImportConfig{}
	Command    = &cobra.Command{
		Use:   "server",
		Short: "Runs the reverse port forward server.",
		Long: `Runs the reverse port forward server.  

Use this in your public network to import services that are exported from the client.`,
		PersistentPreRunE: app.BindEnv("GRPC_RPF"),
		RunE:              commandRun,
	}
	services       []string
	InvalidService = fmt.Errorf("invalid service")
	NotAuthorized  = fmt.Errorf("not authorized")
	mu             = sync.RWMutex{}
)

func init() {
	Command.Flags().StringVar(&listen, "listen", listen, "grpc address to bind")
	Command.Flags().StringArrayVar(&services, "service", services, "service address to bind in name(=address:port) format")
	Command.Flags().StringVar(&servicesDir, "service-dir", servicesDir, "watch a directory holding service configurations")
	Command.Flags().BoolVar(&config.Insecure, "insecure", false, "private key file")
	Command.Flags().StringVar(&config.CAFile, "ca-file", "ca.crt", "certificate authority file")
	Command.Flags().StringVar(&config.CertFile, "crt-file", "importer.crt", "public certificate file")
	Command.Flags().StringVar(&config.KeyFile, "key-file", "importer.key", "private key file")
	app.Command.AddCommand(Command)
}

func commandRun(_ *cobra.Command, _ []string) error {

	if servicesDir == "" && len(services) == 0 {
		return fmt.Errorf("option --import or --imports-dir is required")
	}
	if servicesDir != "" && len(services) != 0 {
		return fmt.Errorf("option --import or --imports-dir are exclusive, use only one")
	}

	for _, s := range services {
		name := ""
		hostPort := ""
		sp := strings.Split(s, "=")
		switch len(sp) {
		case 1:
			name = sp[0]
		case 2:
			name = sp[0]
			hostPort = sp[1]
		default:
			return fmt.Errorf("invalid --service argument: should be in name(=host:port) format, got: %s", s)
		}
		config.Services[name] = server.ServiceConfig{
			Listen: hostPort,
		}
		importsMap[name] = ImportConfig{
			Name:   name,
			Listen: hostPort,
		}
	}

	app.HandleErrorWithExitCode(func() error {
		importerListener, err := net.Listen("tcp", listen)
		if err != nil {
			return err
		}
		defer importerListener.Close()

		//config.AuthFunc = func(ctx context.Context) (context.Context, error) {
		//	_, err := grpc_auth.AuthFromMD(ctx, "Bearer")
		//	if err != nil {
		//		return ctx, err
		//	}
		//	return ctx, nil
		//}

		config.OnListenFunc = func(ctx context.Context, service string) (string, error) {
			mu.RLock()
			defer mu.RUnlock()
			s, found := importsMap[service]
			if !found {
				return "", InvalidService
			}
			if len(s.AllowedListenTokens) > 0 {
				token, err := grpc_auth.AuthFromMD(ctx, "Bearer")
				if err != nil {
					return "", err
				}
				for _, allowedToken := range s.AllowedListenTokens {
					if token == allowedToken {
						return "", nil
					}
				}
				return "", NotAuthorized
			}
			return "", nil
		}

		config.OnConnectFunc = func(ctx context.Context, service string) (string, error) {
			mu.RLock()
			defer mu.RUnlock()
			s, found := importsMap[service]
			if !found {
				return "", InvalidService
			}
			if len(s.AllowedConnectTokens) > 0 {
				token, err := grpc_auth.AuthFromMD(ctx, "Bearer")
				if err != nil {
					return "", err
				}
				for _, allowedToken := range s.AllowedConnectTokens {
					if token == allowedToken {
						return "", nil
					}
				}
				return "", NotAuthorized
			}
			return "", nil
		}

		s, err := server.New(config)
		if err != nil {
			return err
		}
		s.Start(importerListener)
		defer s.Stop()

		if servicesDir != "" {
			err := loadImporterConfigs(s)
			if err != nil {
				return err
			}
			stopWatching, err := utils.WatchDir(servicesDir, func() {
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
	files, err := ioutil.ReadDir(servicesDir)
	if err != nil {
		return fmt.Errorf("error loading imports directory: %v", err)
	}

	im := map[string]ImportConfig{}
	config.Services = map[string]server.ServiceConfig{}
	for _, f := range files {
		value := ImportConfig{}
		file := filepath.Join(servicesDir, f.Name())
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
		im[value.Name] = value
	}
	serverInstance.SetServices(config.Services)
	mu.Lock()
	importsMap = im
	mu.Unlock()
	return nil
}
