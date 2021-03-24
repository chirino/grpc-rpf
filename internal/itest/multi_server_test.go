//go:generate bash gencerts.sh
package itest

import (
	"context"
	"github.com/chirino/grpc-rpf/internal/pkg/exporter"
	"github.com/chirino/grpc-rpf/internal/pkg/server"
	g "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sync"
	"testing"
)

var NotReady = status.Error(codes.Unavailable, "not ready")

func TestRedirect(t *testing.T) {
	g.RegisterTestingT(t)
	mu := sync.Mutex{}

	servers := []*testServer{}
	serverConfig := func(idx int, c server.Config) server.Config {
		//c.Log = log.New(os.Stdout, "server", 0)
		c.OnListenFunc = func(ctx context.Context, service string) (string, func(), error) {
			if idx == 0 {
				// lets try to redirect to the 2nd server...
				mu.Lock()
				s := servers
				mu.Unlock()

				if len(s) == 2 {
					return s[1].grpcPort.Addr().String(), nil, nil
				}
				return "", nil, NotReady
			}
			return "", nil, nil
		}
		return c
	}

	RunServicesForNServers(t, 2,
		func(s []*testServer, c *exporter.Client) {

			// Make sure the client is first hitting the first sever...
			g.Eventually(func() exporter.ServiceState {
				return c.GetServiceState("echo")
			}, "2s", "200ms").Should(g.Equal(exporter.ServiceState{
				ServerAddress: s[0].grpcPort.Addr().String(),
				Error:         NotReady,
			}))

			mu.Lock()
			servers = s
			mu.Unlock()

			// Now make sure we are using the second server...
			g.Eventually(func() exporter.ServiceState {
				return c.GetServiceState("echo")
			}, "2s", "200ms").Should(g.Equal(exporter.ServiceState{
				ServerAddress: s[1].grpcPort.Addr().String(),
				Error:         nil,
			}))

			runHelloPingTest(t, 1)([]*testServer{servers[1]}, c)
		},
		serverConfig,
	)

}
