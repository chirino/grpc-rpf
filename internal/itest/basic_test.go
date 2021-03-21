//go:generate bash gencerts.sh
package itest

import (
	"bufio"
	"context"
	"github.com/chirino/grpc-rpf/internal/pkg/client"
	"github.com/chirino/grpc-rpf/internal/pkg/grpcapi"
	"github.com/chirino/grpc-rpf/internal/pkg/server"
	"github.com/stretchr/testify/assert"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

func FatalOnError(t testing.TB, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

func BenchmarkTestEndToEnd(b *testing.B) {
	tb := testing.TB(b)
	pt := runHelloPingTest(tb)
	runServicesFor(tb, context.Background(), func(l net.Listener) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				pt(l)
			}
		})
	})
}

func TestEndToEnd(t *testing.T) {
	for i := 0; i < 100; i++ {
		testEndToEnd(testing.TB(t))
	}
}

func testEndToEnd(t testing.TB) {
	runServicesFor(t, context.Background(), runHelloPingTest(t))
}

func runHelloPingTest(t testing.TB) func(portB net.Listener) {
	return func(portB net.Listener) {
		// Now lets simulate a client connecting to the echo service
		// via the importer.  Note the we connect to the address created
		// on the B side of the diagram.
		//log.Println("connecting to", portB.Addr())
		conn, err := net.Dial("tcp", portB.Addr().String())
		FatalOnError(t, err)
		defer conn.Close()

		//log.Println("client: sending hello!")
		_, err = conn.Write([]byte("hello!\n"))
		FatalOnError(t, err)

		r := bufio.NewReader(conn)
		data, err := r.ReadString('\n')
		FatalOnError(t, err)
		assert.Equal(t, "hello!\n", data)
		//log.Println("client: received hello!")
	}
}

func runServicesFor(t testing.TB, ctx context.Context, then func(l net.Listener)) {
	/*
	          A                                  B
	    ------------                        -----------
	              Firewall
	   +----------+  |    grpc/http2        +----------+
	   | exporter | ----------------------> | importer |
	   +----------+  |                      +----------+
	         |       |                            ^
	         v       |                            |
	   +----------+  |                      +----------+
	   |   echo   |  |                      |  client  |
	   +----------+  |                      +----------+
	*/

	// Start the importer on the B side of the diagram.
	// Note that it does not need to know any address of things on the A side
	// since it does not connect to it directly..
	portB, err := net.Listen("tcp", "127.0.0.1:0")
	FatalOnError(t, err)
	defer portB.Close()
	// Start the importer service port.
	//log.Println("echo service proxy is running at", portB.Addr())
	importerPort, stopImporter := startServer(t, map[string]server.ServiceConfig{
		"echo": {
			Listener: portB,
		},
	})
	defer importerPort.Close()
	defer stopImporter()

	// Start the echo and exporter service on the A side of the diagram.
	// Exporter needs to know the addresses of the echo and importer service.
	portA, stopEchoService := startEchoService(t)
	defer portA.Close()
	defer stopEchoService()
	stopExporter := startClient(t, importerPort.Addr().String(), map[string]client.ServiceConfig{
		"echo": {
			Address: portA.Addr().String(),
		},
	})
	defer stopExporter()

	then(portB)
}

func startClient(t testing.TB, importerAddress string, services map[string]client.ServiceConfig) func() {
	c, err := client.New(client.Config{
		ImporterAddress: importerAddress,
		Services:        services,
		TLSConfig: grpcapi.TLSConfig{
			CAFile:   "generated/ca.pem",
			CertFile: "generated/client.pem",
			KeyFile:  "generated/client.key",
		},
		Log: log.New(os.Stdout, "client: ", 0),
	})
	FatalOnError(t, err)
	FatalOnError(t, c.Start())
	return c.Stop
}

func startServer(t testing.TB, services map[string]server.ServiceConfig) (net.Listener, func()) {

	importerListener, err := net.Listen("tcp", "127.0.0.1:0")
	FatalOnError(t, err)

	// Run the importer.  It does not connect out to any services.  It receives connections
	// from the exporter and from other apps trying to connect to the private services exposed
	// by this importer.
	s, err := server.New(server.Config{
		Services: services,
		TLSConfig: grpcapi.TLSConfig{
			CAFile:   "generated/ca.pem",
			CertFile: "generated/server.pem",
			KeyFile:  "generated/server.key",
		},
		Log: log.New(os.Stdout, "server: ", 0),
	})
	FatalOnError(t, err)
	s.Start(importerListener)

	//log.Println("importer service is running at", importerListener.Addr())
	return importerListener, func() {
		s.Stop()
	}
}

func startEchoService(t testing.TB) (net.Listener, func()) {

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
