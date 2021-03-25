package grpcapi

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/testdata"
	"io/ioutil"
)

type TLSConfig struct {
	Insecure bool
	CAFile   string
	CertFile string
	KeyFile  string
}

func NewDialOptions(config TLSConfig) ([]grpc.DialOption, error) {
	opts := []grpc.DialOption{}
	if config.Insecure {
		opts = append(opts, grpc.WithInsecure())
	} else {
		tlsConfig, err := newTLSConfig(config)
		if err != nil {
			return opts, err
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	}
	return opts, nil
}

func NewServerOptions(config TLSConfig) ([]grpc.ServerOption, error) {
	var opts []grpc.ServerOption
	if !config.Insecure {
		if config.CertFile == "" {
			config.CertFile = testdata.Path("server.crt")
		}
		if config.KeyFile == "" {
			config.KeyFile = testdata.Path("server.key")
		}

		tlsConfig, err := newTLSConfig(config)
		if err != nil {
			return opts, err
		}
		opts = []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsConfig))}
	}
	return opts, nil
}

func newTLSConfig(config TLSConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{},
	}
	if config.CertFile != "" && config.KeyFile != "" {
		certificate, err := tls.LoadX509KeyPair(
			config.CertFile,
			config.KeyFile,
		)
		if err != nil {
			return nil, fmt.Errorf("could not load certificate key pair (%s, %s): %v", config.CertFile, config.KeyFile, err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}

	if config.CAFile != "" {
		certPool := x509.NewCertPool()
		bs, err := ioutil.ReadFile(config.CAFile)
		if err != nil {
			return nil, fmt.Errorf("could not load ca certs: %v", err)
		}
		ok := certPool.AppendCertsFromPEM(bs)
		if !ok {
			return nil, fmt.Errorf("failed to append ca certs: %v", err)
		}
		tlsConfig.RootCAs = certPool
		tlsConfig.ClientCAs = certPool
	}
	return tlsConfig, nil
}
