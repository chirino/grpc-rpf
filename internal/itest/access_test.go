package itest

import (
	"bufio"
	"context"
	"github.com/chirino/grpc-rpf/internal/pkg/exporter"
	"github.com/chirino/grpc-rpf/internal/pkg/server"
	grpc_auth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
	g "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net"
	"testing"
)

var PermissionDenied = status.Error(codes.PermissionDenied, "permission denied")

func TestAccess(t *testing.T) {
	serverConfig := func(idx int, c server.Config) server.Config {
		//c.Log = log.New(os.Stdout, "server", 0)
		c.OnListenFunc = func(ctx context.Context, service string) (string, func(), error) {
			token, err := grpc_auth.AuthFromMD(ctx, "Bearer")
			if err != nil {
				return "", nil, err
			}
			if token == "good" {
				return "", nil, nil
			}
			return "", nil, PermissionDenied
		}
		return c
	}

	RunServicesForNServers(t, 1, runGoodAccess(t),
		serverConfig,
		func(c exporter.Config) exporter.Config {
			c.AccessToken = "good"
			return c
		},
	)

	RunServicesForNServers(t, 1, runBadAccess(t),
		serverConfig,
		func(c exporter.Config) exporter.Config {
			//c.Log = log.New(os.Stdout, "client", 0)
			c.AccessToken = "bad"
			return c
		},
	)
}

func runGoodAccess(t testing.TB) func(s []*testServer, c *exporter.Client) {
	g.RegisterTestingT(t)
	return func(s []*testServer, c *exporter.Client) {

		g.Eventually(func() string {
			err := c.GetServiceState("echo").Error
			if err != nil {
				return err.Error()
			}
			return ""
		}, "2s", "500ms").Should(g.Equal(""))

		conn, err := net.Dial("tcp", s[0].appPort.Addr().String())
		FatalOnError(t, err)
		defer conn.Close()

		//log.Println("client: sending hello!")
		_, err = conn.Write([]byte("hello!\n"))
		FatalOnError(t, err)

		r := bufio.NewReader(conn)

		data, err := r.ReadString('\n')
		FatalOnError(t, err)
		assert.Equal(t, "hello!\n", data)
	}
}

func runBadAccess(t testing.TB) func(s []*testServer, c *exporter.Client) {
	g.RegisterTestingT(t)
	return func(s []*testServer, c *exporter.Client) {
		g.Eventually(func() string {
			err := c.GetServiceState("echo").Error
			if err != nil {
				return err.Error()
			}
			return ""
		}, "2s", "500ms").Should(g.Equal(PermissionDenied.Error()))
	}
}
