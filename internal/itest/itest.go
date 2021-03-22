//go:generate bash gencerts.sh
package itest

import (
	"github.com/chirino/grpc-rpf/internal/pkg/exporter"
	"github.com/chirino/grpc-rpf/internal/pkg/grpcapi"
	"github.com/chirino/grpc-rpf/internal/pkg/server"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

type testServer struct {
	appPort  net.Listener
	grpcPort net.Listener
	server   *server.Server
}

func FatalOnError(t testing.TB, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

func RunServicesForNServers(t testing.TB, n int, then func(s []*testServer, c *exporter.Client), customizers ...interface{}) {
	/*
	          A                                  B
	    ------------                        -----------
	              Firewall
	   +----------+  |    grpc/http2        +----------+
	   |   client | ----------------------> |  server  |
	   +----------+  |                      +----------+
	         |       |                            ^
	         v       |                            |
	   +----------+  |                      +----------+
	   |   echo   |  |                      |    app   |
	   +----------+  |                      +----------+
	*/

	// Start the importer on the B side of the diagram.
	// Note that it does not need to know any address of things on the A side
	// since it does not connect to it directly..
	testServers := []*testServer{}
	for i := 0; i < n; i++ {

		appPort, err := net.Listen("tcp", "127.0.0.1:0")
		FatalOnError(t, err)
		defer appPort.Close()

		// Start the importer service port.
		//log.Println("echo service proxy is running at", portB.Addr())
		grpcPort, s := StartServer(t, map[string]server.ServiceConfig{
			"echo": {
				Listener: appPort,
			},
		}, i, customizers)
		defer grpcPort.Close()
		defer s.Stop()

		testServers = append(testServers, &testServer{
			appPort:  appPort,
			grpcPort: grpcPort,
			server:   s,
		})
	}

	// Start the echo and exporter service on the A side of the diagram.
	// Exporter needs to know the addresses of the echo and importer service.
	echoPort, stopEchoService := StartEchoService(t)
	defer echoPort.Close()
	defer stopEchoService()
	c := StartClient(t, testServers[0].grpcPort.Addr().String(), map[string]exporter.ServiceConfig{
		"echo": {
			Address: echoPort.Addr().String(),
		},
	}, customizers)
	defer c.Stop()

	then(testServers, c)
}

func StartClient(t testing.TB, importerAddress string, services map[string]exporter.ServiceConfig, customizers []interface{}) *exporter.Client {
	config := exporter.Config{
		ServerAddress: importerAddress,
		Services:      services,
		TLSConfig: grpcapi.TLSConfig{
			CAFile:   "generated/ca.pem",
			CertFile: "generated/client.pem",
			KeyFile:  "generated/client.key",
		},
		//Log: log.New(os.Stdout, "client: ", 0),
	}

	for _, customizer := range customizers {
		switch customizer := customizer.(type) {
		case func(exporter.Config) exporter.Config:
			config = customizer(config)
		default:
		}
	}

	c, err := exporter.New(config)
	FatalOnError(t, err)
	FatalOnError(t, c.Start())
	return c
}

func StartServer(t testing.TB, services map[string]server.ServiceConfig, serverIdx int, customizers []interface{}) (net.Listener, *server.Server) {

	importerListener, err := net.Listen("tcp", "127.0.0.1:0")
	FatalOnError(t, err)

	// Run the importer.  It does not connect out to any services.  It receives connections
	// from the exporter and from other apps trying to connect to the private services exposed
	// by this importer.
	config := server.Config{
		Services: services,
		TLSConfig: grpcapi.TLSConfig{
			CAFile:   "generated/ca.pem",
			CertFile: "generated/server.pem",
			KeyFile:  "generated/server.key",
		},
		//Log: log.New(os.Stdout, "server: ", 0),
	}

	for _, customizer := range customizers {
		switch customizer := customizer.(type) {
		case func(int, server.Config) server.Config:
			config = customizer(serverIdx, config)
		default:
		}
	}

	s, err := server.New(config)
	FatalOnError(t, err)
	s.Start(importerListener)

	//log.Println("importer service is running at", importerListener.Addr())
	return importerListener, s
}

func StartEchoService(t testing.TB) (net.Listener, func()) {

	var wg sync.WaitGroup
	var done int32 = 0
	privateServiceListener, err := net.Listen("tcp", "127.0.0.1:0")
	FatalOnError(t, err)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for atomic.LoadInt32(&done) == 0 {
			conn, err := privateServiceListener.Accept()
			if err != nil {
				if opErr, ok := err.(*net.OpError); ok && opErr.Unwrap().Error() == "use of closed network connection" {
					return
				}
				FatalOnError(t, err)
			}

			wg.Add(1)
			go func() {
				defer wg.Done()

				defer conn.Close()
				_, err = io.Copy(conn, conn)
				FatalOnError(t, err)
			}()
		}
	}()

	// log.Println("echo service is running at", privateServiceListener.Addr())
	return privateServiceListener, func() {
		atomic.StoreInt32(&done, 1)
		privateServiceListener.Close()
		wg.Wait()
	}
}
