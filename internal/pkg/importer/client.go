package importer

import (
	"context"
	"github.com/chirino/grpc-rpf/internal/pkg/grpcapi"
	"github.com/chirino/grpc-rpf/internal/pkg/utils"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/oauth"
	"google.golang.org/grpc/status"
	"io/ioutil"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	ServerAddress string
	grpcapi.TLSConfig
	Log         *log.Logger
	Services    map[string]ServiceConfig
	AccessToken string
}

type ServiceConfig struct {
	Listen   string
	Listener net.Listener
}

type Client struct {
	config      Config
	ctx         context.Context
	cancel      func()
	wg          sync.WaitGroup
	log         *log.Logger
	services    map[string]*service
	mu          sync.Mutex
	accessToken string
	dailOptions []grpc.DialOption
}

func New(config Config) (*Client, error) {
	c := &Client{
		log:         config.Log,
		config:      config,
		services:    map[string]*service{},
		accessToken: config.AccessToken,
	}
	if c.log == nil {
		c.log = log.New(ioutil.Discard, "", 0)
	}
	return c, nil
}

func (client *Client) Start() error {
	client.ctx, client.cancel = context.WithCancel(context.Background())
	opts, err := grpcapi.NewDialOptions(client.config.TLSConfig)

	if err != nil {
		return err
	}

	if client.accessToken != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(oauth.NewOauthAccess(&oauth2.Token{
			AccessToken: client.accessToken,
		})))
	}
	client.dailOptions = opts

	client.SetServices(client.config.Services)
	return nil
}

func (client *Client) Stop() {
	client.cancel()
	client.wg.Wait()
}

func (client *Client) SetServices(services map[string]ServiceConfig) {
	client.mu.Lock()
	defer client.mu.Unlock()

	for name, config := range services {

		// Is it a new service...
		s := client.services[name]
		if s == nil {
			stopChan := make(chan struct{})
			close(stopChan)
			s = &service{
				client:        client,
				serverAddress: client.config.ServerAddress,
				name:          name,
				config:        config,
				stopChan:      stopChan,
				log:           utils.AddLogPrefix(client.log, "service '"+name+"': "),
			}
			client.services[name] = s
			s.start()
		} else {
			s.stop()
			s.config = config
			s.start()
		}
	}

	// removing of a service...
	for name, service := range client.services {
		if _, ok := services[name]; !ok {
			delete(client.services, name)
			service.stop()
		}
	}
}

type service struct {
	client *Client
	name   string
	log    *log.Logger
	mu     sync.Mutex

	config ServiceConfig
	wg     sync.WaitGroup

	listenerCount   int32
	stopChan        chan struct{}
	listenerToClose net.Listener
	serverAddress   string
	remoteHost      grpcapi.RemoteHostClient
}

func (s *Client) newServiceHandler(name string) *service {
	stopChan := make(chan struct{})
	close(stopChan)
	return &service{
		name:     name,
		log:      utils.AddLogPrefix(s.log, "service '"+name+"': "),
		stopChan: stopChan,
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

	if s.config.Listen == "" && s.config.Listener == nil {
		return // no need to start a listener...
	}

	s.stopChan = make(chan struct{})
	s.wg.Add(1)
	go func() {
		defer func() {
			s.wg.Done()
		}()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		for {

			select {
			case <-ctx.Done():
				return
			default:
			}

			listener := s.config.Listener
			if listener == nil {
				var err error
				listener, err = net.Listen("tcp", s.config.Listen)
				if err != nil {
					s.log.Printf("error: %v", err)
					time.Sleep(1 * time.Second) // TODO: make backoff a config option...
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

				go s.connect(ctx, conn)
			}
		}
	}()
}

func (s *service) SetServerAddress(serverAddress string) {
	s.mu.Lock()
	s.serverAddress = serverAddress
	s.remoteHost = nil
	s.mu.Unlock()
}

func (s *service) GetRemoteHost() (grpcapi.RemoteHostClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.remoteHost != nil {
		return s.remoteHost, nil
	}

	// dial each service separately to allow the server to load balance them..
	s.log.Println("connecting to GRPC server at:", s.serverAddress)
	c, err := grpc.Dial(s.serverAddress, s.client.dailOptions...)
	if err != nil {
		return nil, err
	}

	s.remoteHost = grpcapi.NewRemoteHostClient(c)
	return s.remoteHost, nil
}

func (s *service) connect(ctx context.Context, conn1 utils.Conn) {
	s.wg.Add(1)
	defer s.wg.Done()
	defer conn1.Close()

	// create a new context for the connection...
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Try to establish the initial connection.. handling redirects...
	var grpcConnection grpcapi.RemoteHost_ConnectClient
	for {
		remoteHost, err := s.GetRemoteHost()
		if err != nil {
			s.log.Printf("error opening connection: %v", err)
			return
		}
		grpcConnection, err = remoteHost.Connect(ctx)
		if err != nil {
			if status.Code(err) == codes.Canceled {
				return
			}
			s.log.Printf("error opening connection: %v", err)
			return
		}
		err = grpcConnection.Send(&grpcapi.ServiceAddressAndChunk{
			Address: &grpcapi.ServiceAddress{
				ServiceName: s.name,
			},
		})
		if err != nil {
			_ = grpcConnection.CloseSend()
			if status.Code(err) == codes.Canceled {
				return
			}
			s.log.Printf("error opening connection: %v", err)
			return
		}
		recv, err := grpcConnection.Recv()
		if err != nil {
			_ = grpcConnection.CloseSend()
			if status.Code(err) == codes.Canceled {
				return
			}
			s.log.Printf("error opening connection: %v", err)
			return
		}

		if recv.Address != nil && recv.Address.RedirectHostPort != "" {
			_ = grpcConnection.CloseSend()
			s.SetServerAddress(recv.Address.RedirectHostPort)
			continue
		}
		break
	}

	var isClosed int32 = 0
	defer func() {
		atomic.StoreInt32(&isClosed, 1)
		defer grpcConnection.CloseSend()
	}()

	conn2 := wrapStream(ctx, grpcConnection, cancel)

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
	}
}

func wrapStream(ctx context.Context, c grpcapi.RemoteHost_ConnectClient, cancel context.CancelFunc) utils.Conn {
	return &utils.WrapperConn{
		Send: func(data []byte) error {
			return c.Send(&grpcapi.ServiceAddressAndChunk{
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
