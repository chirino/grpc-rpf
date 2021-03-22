package exporter

import (
	"context"
	"fmt"
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
	Address string
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

var NotStarted = fmt.Errorf("not started")
var Stopped = fmt.Errorf("stopped")
var NotFound = fmt.Errorf("not found")

func (client *Client) SetServices(services map[string]ServiceConfig) {
	client.mu.Lock()
	defer client.mu.Unlock()

	for name, config := range services {

		// Is it a new service...
		s := client.services[name]
		if s == nil {
			s = &service{
				client:        client,
				serverAddress: client.config.ServerAddress,
				name:          name,
				_config:       config,
				err:           NotStarted,
				log:           utils.AddLogPrefix(client.log, "service '"+name+"': "),
			}
			client.services[name] = s

			s.start(client.ctx)
		} else {
			s.setConfig(config)
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

type ServiceState struct {
	ServerAddress string
	Error         error
}

func (client *Client) GetServiceState(name string) (result ServiceState) {
	client.mu.Lock()
	defer client.mu.Unlock()

	s := client.services[name]
	if s == nil {
		result.Error = NotFound
		return
	}

	s.mu.RLock()
	result.Error = s.err
	result.ServerAddress = s.serverAddress
	s.mu.RUnlock()
	return
}

type service struct {
	name          string
	client        *Client
	wg            sync.WaitGroup
	cancel        context.CancelFunc
	serverAddress string

	// mu guards access to the variables that start with _
	mu      sync.RWMutex
	_config ServiceConfig
	log     *log.Logger
	err     error
}

func (s *service) stop() {
	s.cancel()
	s.wg.Wait()
}

func (s *service) setConfig(config ServiceConfig) {
	s.mu.Lock()
	if s._config != config {
		s._config = config
		s.log.Printf("forwarding changed to: %s", config.Address)
	}
	s.mu.Unlock()
}

func (s *service) start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.client.wg.Add(1)
	s.wg.Add(1)

	go func() {
		defer func() {
			s.client.wg.Done()
			s.wg.Done()
			s.cancel()
			s.mu.Lock()
			s.err = Stopped
			s.mu.Unlock()
		}()

	outer:
		for {

			select {
			case <-ctx.Done():
				return
			default:
			}

			s.log.Println("connecting to GRPC server at:", s.serverAddress)

			// dial each service separately to allow the server to load balance them..
			c, err := grpc.Dial(s.serverAddress, s.client.dailOptions...)
			if err != nil {
				s.mu.Lock()
				s.err = err
				s.mu.Unlock()
				s.log.Println("error dialing server:", err)
				time.Sleep(1 * time.Second) // TODO: make backoff a config option...
				continue
			}
			remoteHost := grpcapi.NewRemoteHostClient(c)

			// Listen on remote server port
			//log.Printf("opening listener for service %s -> %s", serviceName, hostPort)
			serviceClient, err := remoteHost.Listen(ctx, &grpcapi.ServiceAddress{
				ServiceName: s.name,
			})
			if err != nil {
				s.mu.Lock()
				s.err = err
				s.mu.Unlock()
				s.log.Println("error listening on remote host:", err)
				time.Sleep(1 * time.Second) // TODO: make backoff a config option...
				continue
			}

			s.mu.Lock()
			s.err = nil
			s.log.Println("forwarding connections to:", s._config.Address)
			s.mu.Unlock()

			for {
				connectionAddress, err := serviceClient.Recv()
				if err != nil {
					if status.Code(err) == codes.Canceled {
						return
					}
					s.mu.Lock()
					s.err = err
					s.mu.Unlock()
					s.log.Println("error receiving connection address:", err)
					time.Sleep(1 * time.Second)
					continue outer
				}
				if connectionAddress.RedirectHostPort != "" {
					s.mu.Lock()
					s.serverAddress = connectionAddress.RedirectHostPort
					s.mu.Unlock()
					continue outer
				}

				go s.connect(ctx, remoteHost, connectionAddress)
			}
		}
	}()
}

func (s *service) connect(ctx context.Context, remoteHost grpcapi.RemoteHostClient, connectionAddress *grpcapi.ConnectionAddress) {
	s.wg.Add(1)
	defer s.wg.Done()

	// create a new context for the connection...
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	grpcConnection, err := remoteHost.AcceptConnection(ctx)
	if err != nil {
		if status.Code(err) == codes.Canceled {
			return
		}
		s.log.Printf("error opening tunnel: %v", err)
		return
	}

	err = grpcConnection.Send(&grpcapi.ConnectionAddressAndChunk{
		Address: connectionAddress,
	})
	if err != nil {
		if status.Code(err) == codes.Canceled {
			return
		}
		s.log.Printf("error sending initial tunnel frame: %v", err)
		return
	}

	s.mu.RLock()
	hostPort := s._config.Address
	s.mu.RUnlock()

	conn1, err := net.Dial("tcp", hostPort)
	if err != nil {
		s.log.Printf("could not dial local service: %v", err)
		_ = grpcConnection.CloseSend()
		return
	}
	var isClosed int32 = 0
	defer func() {
		atomic.StoreInt32(&isClosed, 1)
		conn1.Close()
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
	//log.Printf("tunnel #%d closed", connectionAddress.ConnectionId)
}

func wrapStream(ctx context.Context, c grpcapi.RemoteHost_AcceptConnectionClient, cancel context.CancelFunc) utils.Conn {
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
