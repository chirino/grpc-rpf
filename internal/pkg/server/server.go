package server

import (
	"context"
	"fmt"
	"github.com/chirino/grpc-rpf/internal/pkg/grpcapi"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type ServiceConfig struct {
	Listen   string
	Listener net.Listener
}

type Config struct {
	Services map[string]ServiceConfig
	grpcapi.TLSConfig
	Log           *log.Logger
	AcceptTimeout time.Duration
}

func New(config Config) (*Server, error) {
	opts, err := grpcapi.NewServerOptions(config.TLSConfig)
	if err != nil {
		return nil, errors.Wrap(err, "invalid GRPC server configuration")
	}

	grpcServer := grpc.NewServer(opts...)
	s := &Server{
		Server:             grpcServer,
		services:           map[string]*serviceListener{},
		pendingConnections: map[string]net.Conn{},
		mu:                 sync.RWMutex{},
		log:                config.Log,
		acceptTimeout:      config.AcceptTimeout,
	}
	if s.log == nil {
		s.log = log.New(ioutil.Discard, "", 0)
	}
	s.SetServices(config.Services)
	grpcapi.RegisterRemoteHostServer(grpcServer, s)
	return s, nil
}

type Server struct {
	*grpc.Server
	services           map[string]*serviceListener
	pendingConnections map[string]net.Conn
	mu                 sync.RWMutex
	log                *log.Logger
	acceptTimeout      time.Duration
}

type serviceListener struct {
	name        string
	listener    net.Listener
	mu          sync.Mutex
	config      ServiceConfig
	stopChan    chan bool
	closeOnStop bool
}

func (s *Server) SetServices(services map[string]ServiceConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, config := range services {

		// Is it a new service...
		sl := s.services[name]
		if sl == nil {
			sl = &serviceListener{
				name:        name,
				config:      config,
				listener:    config.Listener,
				closeOnStop: false,
				stopChan:    make(chan bool, 0),
			}
		} else {
			if sl.closeOnStop && sl.config.Listen != config.Listen {
				sl.listener.Close()
				sl.closeOnStop = false
				sl.listener = config.Listener
			}
		}

		if sl.listener == nil {
			var err error
			sl.listener, err = net.Listen("tcp", config.Listen)
			if err != nil {
				s.log.Printf("error: service '%s': %v", name, err)
				continue
			}
			s.log.Printf("listening for '%s' connections on: %s", name, sl.listener.Addr())
			sl.closeOnStop = true
		}
		s.services[name] = sl
	}

	// removing of a service...
	for name, service := range s.services {
		if _, ok := services[name]; !ok {
			delete(s.services, name)
			close(service.stopChan)
		}
	}
}

func (s *Server) Start(l net.Listener) {
	go func() {
		s.log.Printf("listening for GRPC connections on: %s", l.Addr())
		err := s.Server.Serve(l)
		if err != nil {
			s.log.Println("could not start server: ", err)
		}
	}()
}

func (s *Server) Stop() {
	s.Server.Stop()
	for _, s := range s.services {
		if s.closeOnStop {
			s.listener.Close()
		}
	}
}

func (s *Server) registerConnection(c net.Conn) (token []byte) {
	token = GenerateRandomBytes(16)
	key := string(token)

	s.mu.Lock()

	for {
		if _, found := s.pendingConnections[key]; !found {
			s.pendingConnections[key] = c
			s.mu.Unlock()
			return token
		}
		// try again with a new token.. should not really happen often..
		token = GenerateRandomBytes(16)
		key = string(token)
	}
}

func GenerateRandomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

var InvalidToken = errors.New("invalid token")

func (s *Server) deregisterConnection(address *grpcapi.ConnectionAddress) (net.Conn, error) {
	key := string(address.Token)

	s.mu.Lock()
	conn, ok := s.pendingConnections[key]
	delete(s.pendingConnections, key)
	s.mu.Unlock()

	if !ok {
		return nil, InvalidToken
	}
	return conn, nil
}

func (s *Server) Connect(grpcapi.RemoteHost_ConnectServer) error {
	return fmt.Errorf("not implemented yet")
}

func (s *Server) Listen(address *grpcapi.ServiceAddress, listenServer grpcapi.RemoteHost_ListenServer) error {

	s.mu.RLock()
	service, found := s.services[address.ServiceName]
	s.mu.RUnlock()

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

		token := s.registerConnection(conn)

		address := &grpcapi.ConnectionAddress{
			// RedirectHostPort: "",
			Token: token,
		}

		if s.acceptTimeout != 0 {
			time.AfterFunc(s.acceptTimeout, func() {
				conn, err := s.deregisterConnection(address)
				if err == nil {
					s.log.Println("connection was not accepted within the timeout.  closing connection.")
					conn.Close()
				}
			})
		}

		// send an event to the client so he can accept the connection.
		err = listenServer.Send(address)
		if err != nil {
			return err
		}

	}
}

func (s *Server) AcceptConnection(grpcConnnection grpcapi.RemoteHost_AcceptConnectionServer) error {

	ctx, cancel := context.WithCancel(grpcConnnection.Context())
	defer cancel()

	msg, err := grpcConnnection.Recv()
	if err != nil {
		if status.Code(err) == codes.Canceled {
			return nil
		}
		s.log.Printf("error: %v", err)
		return nil
	}

	conn, err := s.deregisterConnection(msg.Address)
	if err != nil {
		return err
	}

	var isClosed int32 = 0
	defer func() {
		atomic.StoreInt32(&isClosed, 1)
		conn.Close()
	}()

	// start go routine to pump bytes from grpc to the socket...
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
				s.log.Printf("error receiving tunnel frame: %v", err)

				return
			}

			n, err := conn.Write(msg.Data)
			if err != nil {
				if atomic.LoadInt32(&isClosed) == 1 {
					return
				}
				s.log.Printf("error writing to connection: %v", err)
				return
			}
			if n != len(msg.Data) {
				// ErrShortWrite
				return
			}
		}
	}()

	// start go routine to pump bytes from the socket to grpc...
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
				s.log.Printf("error reading from connection: %v", err)
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
					s.log.Printf("error sending tunnel frame: %v", err)
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
