//go:generate bash gencerts.sh
package itest

import (
	"bufio"
	"github.com/chirino/grpc-rpf/internal/pkg/exporter"
	"github.com/stretchr/testify/assert"
	"net"
	"testing"
)

func BenchmarkTestEndToEnd(b *testing.B) {
	tb := testing.TB(b)
	pt := runHelloPingTest(tb, 10000)
	RunServicesForNServers(tb, 1, func(s []*testServer, c *exporter.Client) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				pt(s, c)
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
	RunServicesForNServers(t, 1, runHelloPingTest(t, 10000))
}

func runHelloPingTest(t testing.TB, iterations int) func(s []*testServer, c *exporter.Client) {
	return func(s []*testServer, c *exporter.Client) {
		// Now lets simulate a client connecting to the echo service
		// via the importer.  Note the we connect to the address created
		// on the B side of the diagram.
		//log.Println("connecting to", portB.Addr())
		conn, err := net.Dial("tcp", s[0].appPort.Addr().String())
		FatalOnError(t, err)
		defer conn.Close()

		//log.Println("client: sending hello!")
		for i := 0; i < iterations; i++ {
			_, err = conn.Write([]byte("hello!\n"))
			FatalOnError(t, err)
		}

		r := bufio.NewReader(conn)

		for i := 0; i < iterations; i++ {
			data, err := r.ReadString('\n')
			FatalOnError(t, err)
			assert.Equal(t, "hello!\n", data)
			//log.Println("client: received hello!")
		}
	}
}
