package server

import (
	"context"
	"fmt"
	"github.com/chirino/grpc-rpf/internal/command/app"
	"github.com/chirino/grpc-rpf/internal/pkg/grpcapi"
	"github.com/chirino/grpc-rpf/internal/pkg/server"
	"github.com/chirino/grpc-rpf/internal/pkg/store"
	grpc_auth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

var (
	listen            = ":8080"
	advertisedAddress = ""
	servicesDir       = ""
	config            = server.Config{
		Services:  map[string]server.ServiceConfig{},
		TLSConfig: grpcapi.TLSConfig{},
		Log:       log.New(os.Stdout, "", 0),
	}
	Command = &cobra.Command{
		Use:   "server",
		Short: "Runs the reverse port forward server.",
		Long: `Runs the reverse port forward server.  

Use this in your public network to import services that are exported from the client.`,
		PersistentPreRunE: app.BindEnv("GRPC_RPF"),
		RunE:              commandRun,
	}
	services       []string
	storeType      string
	postgresConfig = store.DefaultPostgresConfig()
)

func init() {
	Command.Flags().StringVar(&listen, "listen", listen, "grpc address to bind")
	Command.Flags().StringArrayVar(&services, "service", services, "service address to bind in name(=address:port) format")

	Command.Flags().StringVar(&storeType, "store-type", storeType, "Where to load service configuration from: can be one of: dir|postgresql")
	Command.Flags().StringVar(&servicesDir, "service-dir", servicesDir, "directory that will hold service configurations")

	Command.Flags().StringVar(&postgresConfig.Host, "postgresql-host", postgresConfig.Host, "postgresql host name")
	Command.Flags().IntVar(&postgresConfig.Port, "postgresql-port", postgresConfig.Port, "postgresql port")
	Command.Flags().StringVar(&postgresConfig.SslMode, "postgresql-ssl-mode", postgresConfig.SslMode, "postgresql ssl mode")
	Command.Flags().StringVar(&postgresConfig.Database, "postgresql-database", postgresConfig.Database, "postgresql database name")
	Command.Flags().StringVar(&postgresConfig.User, "postgresql-user", postgresConfig.User, "postgresql user name")
	Command.Flags().StringVar(&postgresConfig.Password, "postgresql-password", postgresConfig.Password, "postgresql password")

	Command.Flags().StringVar(&advertisedAddress, "advertised-address", advertisedAddress, "the host:port that clients can connect to")
	Command.Flags().BoolVar(&config.Insecure, "insecure", false, "private key file")
	Command.Flags().StringVar(&config.CAFile, "ca-file", "ca.crt", "certificate authority file")
	Command.Flags().StringVar(&config.CertFile, "crt-file", "importer.crt", "public certificate file")
	Command.Flags().StringVar(&config.KeyFile, "key-file", "importer.key", "private key file")
	app.Command.AddCommand(Command)
}

func commandRun(_ *cobra.Command, _ []string) error {

	if storeType == "" && len(services) == 0 {
		return fmt.Errorf("option  --service or --store-type is required")
	}
	if storeType != "" && len(services) != 0 {
		return fmt.Errorf("option --service and --store-type are exclusive, use only one")
	}
	switch storeType {
	case "":
	case "dir":
		if servicesDir == "" {
			return fmt.Errorf("when --store-type=dir is used, you must also specify the --service-dir option")
		}
	case "postgresql":
		if advertisedAddress == "" {
			return fmt.Errorf("when --store-type=postgresql is used, you must also specify the --advertised-address option")
		}
	default:
		return fmt.Errorf("invalid value for --store-type option")
	}

	importerListener, listenErr := net.Listen("tcp", listen)

	// it might just be an ip address... lets fill in the port...
	if advertisedAddress != "" && listenErr == nil {
		host, _, err := net.SplitHostPort(advertisedAddress)
		if err != nil {
			if err, ok := err.(*net.AddrError); ok {
				if err.Err == "missing port in address" {
					// lets add the port...
					_, p, err := net.SplitHostPort(importerListener.Addr().String())
					if err == nil {
						advertisedAddress = fmt.Sprintf("%s:%s", advertisedAddress, p)
					}
				}
			} else {
				return fmt.Errorf("invalid --advertised-address value: %v", err)
			}
			host, _, err = net.SplitHostPort(advertisedAddress)
			if err != nil {
				return fmt.Errorf("invalid --advertised-address value: %v", err)
			}
		}
		if host == "" {
			return fmt.Errorf("invalid --advertised-address value: host must be specified")
		}
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
	}

	app.HandleErrorWithExitCode(func() error {

		if listenErr != nil {
			return listenErr
		}

		var err error
		var serverStore store.Store
		switch storeType {
		case "":
		case "dir":
			serverStore, err = store.NewDirStore(servicesDir)
			if err != nil {
				return err
			}
		case "postgresql":
			serverStore, err = store.NewDBStore(postgresConfig, advertisedAddress)
			if err != nil {
				return err
			}
		}

		if serverStore != nil {
			err = serverStore.Start()
			if err != nil {
				return err
			}
			defer serverStore.Stop()

			config.OnListenFunc = func(ctx context.Context, service string) (string, func(), error) {
				token, err := grpc_auth.AuthFromMD(ctx, "Bearer")
				if err != nil {
					return "", nil, err
				}
				return serverStore.OnListen(service, token, getPeerAddress(ctx))
			}

			config.OnConnectFunc = func(ctx context.Context, service string) (string, func(), error) {
				token, err := grpc_auth.AuthFromMD(ctx, "Bearer")
				if err != nil {
					return "", nil, err
				}
				return serverStore.OnConnect(service, token, getPeerAddress(ctx))
			}
		}

		defer importerListener.Close()

		s, err := server.New(config)
		if err != nil {
			return err
		}
		s.Start(importerListener)
		defer s.Stop()

		// Wait for shutdown signal...
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		<-signals
		config.Log.Println("shutting down")
		return nil
	}, 2)
	return nil
}

func getPeerAddress(ctx context.Context) string {
	source := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		source = getForwardedFor(http.Header(md))
	}
	if source == "" {
		if p, ok := peer.FromContext(ctx); ok {
			source = p.Addr.String()
		}
	}
	return source
}

func getForwardedFor(headers http.Header) string {
	h := headers.Get("Forwarded")
	if h != "" {
		for _, kv := range strings.Split(h, ";") {
			if pair := strings.SplitN(kv, "=", 2); len(pair) == 2 {
				switch strings.ToLower(pair[0]) {
				case "for":
					return pair[1]
				}
			}
		}
	}
	return headers.Get("x-forwarded-for")
}
