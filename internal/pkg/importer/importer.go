package importer

import (
	"context"
	"fmt"
	"github.com/chirino/rtsvc/internal/pkg/grpcapi"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	_log "log"
	"math/rand"
	"net"
	"os"
	"sync"
	"sync/atomic"
)

var log *_log.Logger

func init() {
	log = _log.New(os.Stderr, "importer: ", 0)
}

type ServiceConfig struct {
	Name   string
	Listen string
}

type Config struct {
	Services map[string]ServiceConfig
	grpcapi.TLSConfig
}

func New(config Config) (*server, error) {
	opts, err := grpcapi.NewServerOptions(config.TLSConfig)
	if err != nil {
		return nil, errors.Wrap(err, "invalid GRPC server configuration")
	}

	grpcServer := grpc.NewServer(opts...)
	s := &server{
		Server:             grpcServer,
		services:           map[string]*serviceListener{},
		pendingConnections: map[int64]net.Conn{},
		id:                 rand.Int31(),
	}
	grpcapi.RegisterRemoteHostServer(grpcServer, s)

	return s, nil
}

type server struct {
	*grpc.Server
	services           map[string]*serviceListener
	id                 int32
	lastConnectionId   int64
	pendingConnections map[int64]net.Conn
	mu                 sync.Mutex
}

type serviceListener struct {
	name     string
	listener net.Listener
	mu       sync.Mutex
	config   ServiceConfig
}

func (s *server) setServices(services map[string]ServiceConfig) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, config := range services {

		// Is it a new service...
		if s.services[name] == nil {

			l, err := net.Listen("tcp", config.Listen)
			if err != nil {
				log.Println("Could not listen ")
			}

			s.services[name] = &serviceListener{
				name:     name,
				config:   config,
				listener: l,
			}
		} else {

			// Did the service config change?
		}

	}

}

func (s *server) registerConnection(conn net.Conn) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastConnectionId += 1
	s.pendingConnections[s.lastConnectionId] = conn
	return s.lastConnectionId
}

func (s *server) deregisterConnection(connectionId int64) net.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	conn := s.pendingConnections[connectionId]
	if conn != nil {
		delete(s.pendingConnections, connectionId)
	}
	return conn
}

func (s *server) Listen(address *grpcapi.ServiceAddress, listenServer grpcapi.RemoteHost_ListenServer) error {

	service, found := s.services[address.ServiceName]
	if !found {
		return fmt.Errorf("serice not found: %s", address.ServiceName)
	}

	// Only allow one grpc client to 'own' receiving service events..  others will
	// block here and act as backups in case the first one dies.
	service.mu.Lock()
	defer service.mu.Unlock()

	for {

		conn, err := service.listener.Accept()
		if err != nil {
			return err
		}

		connId := s.registerConnection(conn)

		// send an event to the client so he can accept the connection.
		err = listenServer.Send(&grpcapi.ConnectionAddress{
			ServerId:     s.id,
			ConnectionId: connId,
		})
		if err != nil {
			return err
		}

	}
}

func (s *server) AcceptConnection(grpcConnnection grpcapi.RemoteHost_AcceptConnectionServer) error {

	ctx, cancel := context.WithCancel(grpcConnnection.Context())

	msg, err := grpcConnnection.Recv()
	if err != nil {
		if status.Code(err) == codes.Canceled {
			return nil
		}
		log.Printf("error: %v", err)
		return nil
	}

	// In case request gets load balanced to the wrong server...
	if msg.Address.ServerId != s.id {
		return fmt.Errorf("server id does not match")
	}

	conn := s.deregisterConnection(msg.Address.ConnectionId)
	if conn == nil {
		return fmt.Errorf("connection id not found: %d", msg.Address.ConnectionId)
	}

	var isClosed int32 = 0
	defer func() {
		atomic.StoreInt32(&isClosed, 1)
		conn.Close()
	}()

	go func() {
		defer cancel()
		for {
			msg, err := grpcConnnection.Recv()
			if err != nil {
				if err == io.EOF {
					return
				}
				if status.Code(err) == codes.Canceled {
					cancel()
					return
				}
				log.Printf("error receiving tunnel frame: %v", err)

				return
			}

			n, err := conn.Write(msg.Data)
			if err != nil {
				if atomic.LoadInt32(&isClosed) == 1 {
					return
				}
				log.Printf("error writing to connection: %v", err)
				return
			}
			if n != len(msg.Data) {
				// ErrShortWrite
				return
			}
		}
	}()

	go func() {
		defer cancel()
		data := make([]byte, 1024*4)
		for {
			n, err := conn.Read(data)
			if err != nil {
				if atomic.LoadInt32(&isClosed) == 1 {
					return
				}
				if err == io.EOF {
					return
				}
				log.Printf("error reading from connection: %v", err)
				return
			}
			if n > 0 {
				err = grpcConnnection.Send(&grpcapi.Chunk{
					Data: data[0:n],
				})
				if err != nil {
					if status.Code(err) == codes.Canceled {
						cancel()
						return
					}
					log.Printf("error sending tunnel frame: %v", err)
					return
				}
			}
		}
	}()

	// Wait till the context is cancel...
	select {
	case <-ctx.Done():
	}
	return nil
}
