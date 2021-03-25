//go:generate bash gencerts.sh
package itest

import (
	"context"
	"github.com/chirino/grpc-rpf/internal/pkg/exporter"
	"github.com/chirino/grpc-rpf/internal/pkg/server"
	"github.com/chirino/grpc-rpf/internal/pkg/store"
	grpc_auth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"testing"
)

func TestPostgreStore(t *testing.T) {

	postgresConfig := store.DefaultPostgresConfig()
	postgresConfig.Database = "grpc-rpf"
	postgresConfig.User = "grpc-rpf"
	postgresConfig.Password = "password"

	serverStore, err := store.NewDBStore(postgresConfig, "*")
	if err != nil {
		t.Skipf("DB setup failed: %v", err)
		t.SkipNow()
	}
	err = serverStore.Start()
	FatalOnError(t, err)
	defer serverStore.Stop()

	serverStore.DB.Where("1=1").Delete(store.Service{})
	serverStore.DB.Where("1=1").Delete(store.Binding{})
	err = serverStore.DB.Create(&store.Service{
		ID:               "echo",
		AllowedToListen:  []string{"token1"},
		AllowedToConnect: []string{"token2"},
	}).Error
	FatalOnError(t, err)

	exporterConfig := func(c exporter.Config) exporter.Config {
		c.AccessToken = "token1"
		return c
	}

	serverConfig := func(idx int, c server.Config) server.Config {

		c.OnListenFunc = func(ctx context.Context, service string) (string, func(), error) {
			token, err := grpc_auth.AuthFromMD(ctx, "Bearer")
			if err != nil {
				return "", nil, err
			}
			return serverStore.OnListen(service, token, "")
		}

		c.OnConnectFunc = func(ctx context.Context, service string) (string, func(), error) {
			token, err := grpc_auth.AuthFromMD(ctx, "Bearer")
			if err != nil {
				return "", nil, err
			}
			return serverStore.OnConnect(service, token, "")
		}

		return c
	}

	RunServicesForNServers(t, 1, runHelloPingTest(t, 10000), serverConfig, exporterConfig)
}
