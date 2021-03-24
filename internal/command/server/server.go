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
	services  []string
	serviceDB string
)

func init() {
	Command.Flags().StringVar(&listen, "listen", listen, "grpc address to bind")
	Command.Flags().StringArrayVar(&services, "service", services, "service address to bind in name(=address:port) format")
	Command.Flags().StringVar(&servicesDir, "service-dir", servicesDir, "watch a directory holding service configurations")
	Command.Flags().StringVar(&serviceDB, "service-db", serviceDB, "database to use for configuration and state sharing")
	Command.Flags().StringVar(&advertisedAddress, "advertised-address", advertisedAddress, "the host:port that clients can connect to")
	Command.Flags().BoolVar(&config.Insecure, "insecure", false, "private key file")
	Command.Flags().StringVar(&config.CAFile, "ca-file", "ca.crt", "certificate authority file")
	Command.Flags().StringVar(&config.CertFile, "crt-file", "importer.crt", "public certificate file")
	Command.Flags().StringVar(&config.KeyFile, "key-file", "importer.key", "private key file")
	app.Command.AddCommand(Command)
}

func commandRun(_ *cobra.Command, _ []string) error {

	if serviceDB == "" && servicesDir == "" && len(services) == 0 {
		return fmt.Errorf("option --service-db, --service, or --service-dir is required")
	}
	if serviceDB != "" && servicesDir != "" && len(services) != 0 {
		return fmt.Errorf("option --service-db, --service, and --service-dir are exclusive, use only one")
	}
	if advertisedAddress == "" && serviceDB == "" {
		return fmt.Errorf("when --service-db is used, you must also specify the advertised-address option")
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

		var err error
		var serverStore store.Store
		if serviceDB != "" {
			serverStore, err = store.NewDBStore(serviceDB, advertisedAddress)
			if err != nil {
				return err
			}
		} else if servicesDir != "" {
			serverStore, err = store.NewDirStore(servicesDir)
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
