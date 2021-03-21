package client

import (
	"context"
	"github.com/chirino/grpc-rpf/internal/pkg/grpcapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"io/ioutil"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	ImporterAddress string
	grpcapi.TLSConfig
	Log      *log.Logger
	Services map[string]ServiceConfig
}

type ServiceConfig struct {
	Address string
}

type Client struct {
	config     Config
	ctx        context.Context
	cancel     func()
	wg         sync.WaitGroup
	log        *log.Logger
	services   map[string]*service
	remoteHost grpcapi.RemoteHostClient
	mu         sync.Mutex
}

func New(config Config) (*Client, error) {
	c := &Client{
		log:      config.Log,
		config:   config,
		services: map[string]*service{},
	}
	if c.log == nil {
		c.log = log.New(ioutil.Discard, "", 0)
	}
	return c, nil
}

func (client *Client) Start() error {

	client.ctx, client.cancel = context.WithCancel(context.Background())
	client.log.Println("connecting to GRPC server at:", client.config.ImporterAddress)
	opts, err := grpcapi.NewDialOptions(client.config.TLSConfig)
	if err != nil {
		return err
	}

	c, err := grpc.Dial(client.config.ImporterAddress, opts...)
	if err != nil {
		return err
	}

	client.remoteHost = grpcapi.NewRemoteHostClient(c)
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
			s = &service{
				client:  client,
				name:    name,
				_config: config,
				log:     log.New(client.log.Writer(), client.log.Prefix()+"service '"+name+"': ", client.log.Flags()),
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

type service struct {
	name   string
	client *Client
	wg     sync.WaitGroup
	cancel context.CancelFunc

	// mu guards access to the variables that start with _
	mu      sync.RWMutex
	_config ServiceConfig
	log     *log.Logger
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
		}()

	outer:
		for {

			select {
			case <-ctx.Done():
				return
			default:
			}

			// Listen on remote server port
			//log.Printf("opening listener for service %s -> %s", serviceName, hostPort)
			serviceClient, err := s.client.remoteHost.Listen(ctx, &grpcapi.ServiceAddress{
				ServiceName: s.name,
			})
			if err != nil {
				s.log.Println("error listening on remote host:", err)
				time.Sleep(1 * time.Second) // TODO: make backoff a config option...
				continue
			}

			s.mu.RLock()
			s.log.Println("forwarding connections to:", s._config.Address)
			s.mu.RUnlock()

			for {
				connectionAddress, err := serviceClient.Recv()
				if err != nil {
					if status.Code(err) == codes.Canceled {
						return
					}
					s.log.Println("error receiving connection address:", err)
					time.Sleep(1 * time.Second)
					continue outer
				}

				go s.connect(ctx, connectionAddress)
			}
		}
	}()
}

func (s *service) connect(ctx context.Context, connectionAddress *grpcapi.ConnectionAddress) {
	s.wg.Add(1)
	defer s.wg.Done()

	// create a new context for the connection...
	ctx, closeTunnel := context.WithCancel(ctx)
	defer closeTunnel()

	grpcConnection, err := s.client.remoteHost.AcceptConnection(ctx)
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

	conn, err := net.Dial("tcp", hostPort)
	if err != nil {
		s.log.Printf("could not dial local service: %v", err)
		_ = grpcConnection.CloseSend()
		return
	}
	var isClosed int32 = 0
	defer func() {
		atomic.StoreInt32(&isClosed, 1)
		conn.Close()
	}()

	// start go routine to pump bytes from grpc to the socket...
	s.wg.Add(1)
	go func() {
		defer func() {
			s.wg.Done()
			closeTunnel()
		}()
		for {
			msg, err := grpcConnection.Recv()
			if err != nil {
				if err == io.EOF {
					return
				}
				if status.Code(err) == codes.Canceled {
					return
				}
				if atomic.LoadInt32(&isClosed) == 1 {
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
				s.log.Printf("error writing to connection: short write")
				return
			}
		}
	}()

	// start go routine to pump bytes from the socket to grpc...
	s.wg.Add(1)
	go func() {
		defer func() {
			s.wg.Done()
			closeTunnel()
		}()
		data := make([]byte, 1024*4)
		for {
			n, err := conn.Read(data)
			if err != nil {
				if err == io.EOF {
					_ = grpcConnection.CloseSend()
					return
				}
				if atomic.LoadInt32(&isClosed) == 1 {
					return
				}
				s.log.Printf("error reading connection: %v", err)
				return
			}
			if n > 0 {
				err = grpcConnection.Send(&grpcapi.ConnectionAddressAndChunk{
					Data: data[0:n],
				})
				if err != nil {
					if atomic.LoadInt32(&isClosed) == 1 {
						return
					}
					if status.Code(err) == codes.Canceled {
						return
					}
					s.log.Printf("error writing tunnel frame: %v", err)
					return
				}
			}
		}
	}()

	select {
	case <-ctx.Done():
	}
	//log.Printf("tunnel #%d closed", connectionAddress.ConnectionId)
}
