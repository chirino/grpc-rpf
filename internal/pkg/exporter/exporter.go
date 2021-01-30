package exporter

import (
	"context"
	"github.com/chirino/hsvcproxy/internal/pkg/grpcapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	_log "log"
	"net"
	"os"
	"sync"
	"sync/atomic"
)

type Config struct {
	ImporterAddress string
	Services        map[string]string
	TLSConfig       grpcapi.TLSConfig
}

var log *_log.Logger

func init() {
	log = _log.New(os.Stderr, "exporter: ", 0)
}

func Serve(ctx context.Context, config Config) (func(), error) {

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)

	//log.Println("tls dialing:", config.ImporterAddress)
	opts, err := grpcapi.NewDialOptions(config.TLSConfig)
	if err != nil {
		return nil, err
	}

	c, err := grpc.Dial(config.ImporterAddress, opts...)
	if err != nil {
		return nil, err
	}
	remoteHost := grpcapi.NewRemoteHostClient(c)

	for serviceName, hostPort := range config.Services {
		hostPort := hostPort
		// Listen on remote server port
		//log.Printf("opening listener for service %s -> %s", serviceName, hostPort)
		serviceClient, err := remoteHost.Listen(ctx, &grpcapi.ServiceAddress{
			ServiceName: serviceName,
		})
		if err != nil {
			return nil, err
		}

		wg.Add(1)
		go func() {
			defer wg.Done()

			for ; ; {
				connectionAddress, err := serviceClient.Recv()
				if err != nil {
					if status.Code(err) == codes.Canceled {
						// log.Println("no longer accepting connections")
						return
					}
					log.Printf("error receiving connection address: %v", err)
				}

				wg.Add(1)
				go func() {
					defer wg.Done()
					connect(ctx, &wg, remoteHost, connectionAddress, hostPort)
				}()
			}
		}()
	}

	return func() {
		cancel()
		wg.Wait()
	}, nil
}

func connect(ctx context.Context, wg *sync.WaitGroup, remoteHost grpcapi.RemoteHostClient, connectionAddress *grpcapi.ConnectionAddress, hostPort string) {

	ctx, closeTunnel := context.WithCancel(ctx)
	grpcTunnel, err := remoteHost.AcceptConnection(ctx)
	if err != nil {
		if status.Code(err) == codes.Canceled {
			return
		}
		log.Printf("error opening tunnel: %v", err)
		return
	}
	defer closeTunnel()

	err = grpcTunnel.Send(&grpcapi.ConnectionAddressAndChunk{
		Address: connectionAddress,
	})
	if err != nil {
		if status.Code(err) == codes.Canceled {
			return
		}
		log.Printf("error sending initial tunnel frame: %v", err)
		return
	}

	conn, err := net.Dial("tcp", hostPort)
	if err != nil {
		log.Printf("could not dial local service: %v", err)
		_ = grpcTunnel.CloseSend()
		return
	}
	var isClosed int32 = 0
	defer func() {
		atomic.StoreInt32(&isClosed, 1)
		conn.Close()
	}()

	//log.Printf("tunnel #%d opened", connectionAddress.ConnectionId)
	wg.Add(1)
	go func() {
		defer wg.Done()

		defer func() {
			closeTunnel()
			//log.Printf("writer done")
		}()
		for {
			msg, err := grpcTunnel.Recv()
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
				log.Printf("error writing to connection: short write")
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		defer func() {
			closeTunnel()
			//log.Printf("reader done")
		}()
		data := make([]byte, 1024*4)
		for {
			n, err := conn.Read(data)
			if err != nil {
				if err == io.EOF {
					return
				}
				if atomic.LoadInt32(&isClosed) == 1 {
					return
				}
				log.Printf("error reading connection: %v", err)
				return
			}
			if n > 0 {
				err = grpcTunnel.Send(&grpcapi.ConnectionAddressAndChunk{
					Data: data[0:n],
				})
				if err != nil {
					if atomic.LoadInt32(&isClosed) == 1 {
						return
					}
					if status.Code(err) == codes.Canceled {
						return
					}
					log.Printf("error writting tunnel frame: %v", err)
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
