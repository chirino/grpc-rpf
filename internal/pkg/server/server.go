package server

import (
	"context"
	"fmt"
	"github.com/chirino/grpc-rpf/internal/pkg/grpcapi"
	"github.com/chirino/grpc-rpf/internal/pkg/utils"
	"github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_auth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
	grpc_recovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
	grpc_opentracing "github.com/grpc-ecosystem/go-grpc-middleware/tracing/opentracing"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var InvalidToken = errors.New("invalid token")
var BadProtocolExpectingAddress = errors.New("bad protocol: expecting address")
var NotAcceptingConnections = errors.New("not accepting connections")
var ServerShuttingDown = errors.New("server is shutting down")

type ServiceConfig struct {
	Listen   string
	Listener net.Listener
}

type Config struct {
	Services map[string]ServiceConfig
	grpcapi.TLSConfig
	Log               *log.Logger
	AcceptTimeout     time.Duration
	EnableOpenTracing bool
	EnablePrometheus  bool
	OnAuthFunc        grpc_auth.AuthFunc
	OnListenFunc      func(ctx context.Context, service string) (string, func(), error)
	OnConnectFunc     func(ctx context.Context, service string) (string, func(), error)
}

func New(config Config) (*Server, error) {
	opts, err := grpcapi.NewServerOptions(config.TLSConfig)
	if err != nil {
		return nil, errors.Wrap(err, "invalid GRPC server configuration")
	}

	var streamInterceptors []grpc.StreamServerInterceptor
	var unaryInterceptors []grpc.UnaryServerInterceptor

	if config.EnableOpenTracing {
		streamInterceptors = append(streamInterceptors, grpc_opentracing.StreamServerInterceptor())
		unaryInterceptors = append(unaryInterceptors, grpc_opentracing.UnaryServerInterceptor())
	}

	if config.EnablePrometheus {
		streamInterceptors = append(streamInterceptors, grpc_prometheus.StreamServerInterceptor)
		unaryInterceptors = append(unaryInterceptors, grpc_prometheus.UnaryServerInterceptor)
	}

	if config.OnAuthFunc != nil {
		streamInterceptors = append(streamInterceptors, grpc_auth.StreamServerInterceptor(config.OnAuthFunc))
		unaryInterceptors = append(unaryInterceptors, grpc_auth.UnaryServerInterceptor(config.OnAuthFunc))
	}

	streamInterceptors = append(streamInterceptors, grpc_recovery.StreamServerInterceptor())
	unaryInterceptors = append(unaryInterceptors, grpc_recovery.UnaryServerInterceptor())

	opts = append(opts, grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(streamInterceptors...)))
	opts = append(opts, grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(unaryInterceptors...)))

	grpcServer := grpc.NewServer(opts...)
	s := &Server{
		Server:         grpcServer,
		services:       map[string]*service{},
		pendingAccepts: map[string]utils.Conn{},
		mu:             sync.RWMutex{},
		log:            config.Log,
		acceptTimeout:  config.AcceptTimeout,
		OnListenFunc:   config.OnListenFunc,
		OnConnectFunc:  config.OnConnectFunc,
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
	services       map[string]*service
	pendingAccepts map[string]utils.Conn
	mu             sync.RWMutex
	log            *log.Logger
	acceptTimeout  time.Duration
	OnListenFunc   func(ctx context.Context, service string) (string, func(), error)
	OnConnectFunc  func(ctx context.Context, service string) (string, func(), error)
}

func (s *Server) SetServices(services map[string]ServiceConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, config := range services {

		// Is it a new service...
		sl := s.services[name]
		if sl == nil {
			sl = s.newServiceHandler(name)
			sl.config = config
		} else {
			sl.mu.Lock()
			sl.config = config
			sl.mu.Unlock()
			sl.stop()
		}
		s.services[name] = sl
	}

	// removing of a service...
	for name, service := range s.services {
		if _, ok := services[name]; !ok {
			delete(s.services, name)
			service.stop()
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
		s.stop()
	}
}

func (s *Server) registerConnection(c utils.Conn) (token []byte) {
	token = GenerateRandomBytes(16)
	key := string(token)

	s.mu.Lock()

	for {
		if _, found := s.pendingAccepts[key]; !found {
			s.pendingAccepts[key] = c
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

func (s *Server) deregisterAccept(address *grpcapi.ConnectionAddress) (utils.Conn, error) {
	key := string(address.Token)

	s.mu.Lock()
	conn, ok := s.pendingAccepts[key]
	delete(s.pendingAccepts, key)
	s.mu.Unlock()

	if !ok {
		return nil, InvalidToken
	}
	return conn, nil
}

type service struct {
	name string
	log  *log.Logger
	mu   sync.Mutex

	config ServiceConfig
	wg     sync.WaitGroup

	acceptChan      chan utils.Conn
	listenerCount   int32
	stopChan        chan struct{}
	listenerToClose net.Listener
}

func (s *Server) newServiceHandler(name string) *service {
	stopChan := make(chan struct{})
	close(stopChan)
	return &service{
		name:       name,
		log:        utils.AddLogPrefix(s.log, "service '"+name+"': "),
		stopChan:   stopChan,
		acceptChan: make(chan utils.Conn, 1),
	}
}

func isStopped(c chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func (s *service) stop() {
	s.mu.Lock()
	if !isStopped(s.stopChan) {
		close(s.stopChan)
		if s.listenerToClose != nil {
			s.listenerToClose.Close()
		}
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *service) start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !isStopped(s.stopChan) {
		return // we might already be running...
	}

	s.stopChan = make(chan struct{})

	if s.config.Listen == "" && s.config.Listener == nil {
		return // no need to start a listener...
	}
	s.wg.Add(1)
	go func() {
		defer func() {
			s.wg.Done()
		}()
		listener := s.config.Listener
		if listener == nil {
			var err error
			listener, err = net.Listen("tcp", s.config.Listen)
			if err != nil {
				s.log.Printf("error: %v", err)
				return
			}
		}
		s.mu.Lock()
		s.listenerToClose = listener
		s.mu.Unlock()

		s.log.Printf("listening on: %s", listener.Addr())
		for {
			select {
			case <-s.stopChan:
				return
			default:
			}

			conn, err := listener.Accept()
			if err != nil {
				if isStopped(s.stopChan) {
					return
				}
				s.log.Printf("accept failure: %v", err)
				return
			}
			select {
			case s.acceptChan <- conn:
			case <-s.stopChan:
				conn.Close()
			}

		}

	}()
}

func (s *service) onListenerConnect() {
	atomic.AddInt32(&s.listenerCount, 1)
	s.start()
}

func (s *service) onListenerDisconnect() {
	if atomic.AddInt32(&s.listenerCount, -1) == 0 {
		s.stop()
	}
}

func (s *Server) Connect(grpcConnnection grpcapi.RemoteHost_ConnectServer) error {

	msg, err := grpcConnnection.Recv()
	if err != nil {
		if status.Code(err) == codes.Canceled {
			return nil
		}
		s.log.Printf("error: %v", err)
		return nil
	}

	address := msg.GetAddress()
	if address == nil {
		return BadProtocolExpectingAddress
	}

	if s.OnConnectFunc != nil {
		redirectHostPort, closeFunc, err := s.OnConnectFunc(grpcConnnection.Context(), address.ServiceName)
		if err != nil {
			return err
		}

		if closeFunc != nil {
			defer closeFunc()
		}

		if redirectHostPort != "" {
			err := grpcConnnection.Send(&grpcapi.ConnectionAddressAndChunk{
				Address: &grpcapi.ConnectionAddress{
					RedirectHostPort: redirectHostPort,
				},
			})
			if err != nil {
				return fmt.Errorf("client error: %v", err)
			}
			return nil
		}
	}

	// Send an empty frame to let the client know quickly that the connection has been established..
	_ = grpcConnnection.Send(&grpcapi.ConnectionAddressAndChunk{})

	s.mu.RLock()
	service, found := s.services[address.ServiceName]
	s.mu.RUnlock()

	if !found {
		return NotAcceptingConnections
	}

	ctx, cancel := context.WithCancel(grpcConnnection.Context())
	defer cancel()
	service.acceptChan <- wrapConnectStream(ctx, grpcConnnection, cancel)

	<-ctx.Done()
	return nil
}

func wrapConnectStream(ctx context.Context, c grpcapi.RemoteHost_ConnectServer, cancel context.CancelFunc) utils.Conn {
	return &utils.WrapperConn{
		Send: func(data []byte) error {
			return c.Send(&grpcapi.ConnectionAddressAndChunk{
				Data: data,
			})
		},
		Recv: func() ([]byte, error) {
			msg, err := c.Recv()
			if err != nil {
				return nil, err
			}
			return msg.Data, nil
		},
		Context: ctx,
		Cancel:  cancel,
	}
}

func wrapAcceptStream(ctx context.Context, c grpcapi.RemoteHost_AcceptConnectionServer, cancel context.CancelFunc) utils.Conn {

	return &utils.WrapperConn{
		Send: func(data []byte) error {
			return c.Send(&grpcapi.Chunk{
				Data: data,
			})
		},
		Recv: func() ([]byte, error) {
			msg, err := c.Recv()
			if err != nil {
				return nil, err
			}
			return msg.Data, nil
		},
		Context: ctx,
		Cancel:  cancel,
	}
}

func (s *Server) Listen(address *grpcapi.ServiceAddress, listenServer grpcapi.RemoteHost_ListenServer) error {

	if s.OnListenFunc != nil {
		redirectHostPort, closeFunc, err := s.OnListenFunc(listenServer.Context(), address.ServiceName)
		if err != nil {
			return err
		}
		if closeFunc != nil {
			defer closeFunc()
		}
		if redirectHostPort != "" {
			err := listenServer.Send(&grpcapi.ConnectionAddress{
				RedirectHostPort: redirectHostPort,
			})
			if err != nil {
				return fmt.Errorf("client error: %v", err)
			}
			return nil
		}
	}

	s.mu.Lock()
	svc := s.services[address.ServiceName]
	if svc == nil {
		svc = s.newServiceHandler(address.ServiceName)
		s.services[address.ServiceName] = svc
	}
	s.mu.Unlock()

	svc.onListenerConnect()
	defer svc.onListenerDisconnect()

	for {
		select {
		case <-listenServer.Context().Done():
			return nil

		case <-svc.stopChan:
			return ServerShuttingDown

		case accept := <-svc.acceptChan:

			token := s.registerConnection(accept)
			address := &grpcapi.ConnectionAddress{
				// RedirectHostPort: "",
				Token: token,
			}

			if s.acceptTimeout != 0 {
				time.AfterFunc(s.acceptTimeout, func() {
					conn, err := s.deregisterAccept(address)
					if err == nil {
						s.log.Println("connection was not accepted within the timeout.  closing connection.")
						_ = conn.Close()
					}
				})
			}

			// send an event to the client so he can accept the connection.
			err := listenServer.Send(address)
			if err != nil {
				return fmt.Errorf("client error: %v", err)
			}
		}

	}
}

func (s *Server) AcceptConnection(acceptStream grpcapi.RemoteHost_AcceptConnectionServer) error {

	ctx, cancel := context.WithCancel(acceptStream.Context())
	defer cancel()

	msg, err := acceptStream.Recv()
	if err != nil {
		if status.Code(err) == codes.Canceled {
			return nil
		}
		s.log.Printf("error: %v", err)
		return nil
	}

	conn1, err := s.deregisterAccept(msg.Address)
	if err != nil {
		return err
	}

	var isClosed int32 = 0
	defer func() {
		atomic.StoreInt32(&isClosed, 1)
		_ = conn1.Close()
	}()
	conn2 := wrapAcceptStream(ctx, acceptStream, cancel)

	// start go routine to pump bytes from grpc to the socket...
	wg := sync.WaitGroup{}
	// start transfer pumps
	utils.Pump(&wg, conn2, conn1, &isClosed, s.log)
	utils.Pump(&wg, conn1, conn2, &isClosed, s.log)

	go func() {
		// When both transfer pumps are done...
		wg.Wait()
		cancel()
	}()

	select {
	case <-ctx.Done():
		// the request is canceled, when the pumps are done, or the user cancels the request.
	}
	return nil
}
