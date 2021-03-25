# `grpc-rpf` : GRPC Reverse Port Forward

![Build Status](https://github.com/chirino/grpc-rpf/actions/workflows/pr.yml/badge.svg?branch=main)

If you have a private service like a on premise database, that you need to make available in the cloud  
you can use `grpc-rpf` to expose it in that network without having setup a VPN or poke holes in the firewall.

## Overview

`grpc-rpf` can run in one of three modes:

* `server` - accepts GRPC/HTTP2 connections from the `importer` or `exporter` clients. It can optionally also accept
  connections to services that are exported to the `server`.
* `exporter` - Run this on the private network that does not allow inbound connections to the services your trying to
  make available in the cloud. It can export multiple local services to the `server`. It just needs to be able to make a
  https connection the `server`
* `importer` - If the app that wants to connect to your private service is also in a different private network from
  the `server`, you can run the `importer` accept connections to services that are exported to the `server`. It just
  needs to be able to make a https connection the `server`

**Figure #1**

              Firewall
       Network A  |        |   Network B    |      |    Network C
                  |        |                |      |              
    +----------+  |        |  +----------+  |      |   +----------+ 
    | exporter | ------:443-> |  server  | <-:443----- | importer | 
    +----------+  |        |  +----------+  |      |   +----------+ 
          |       |        |       :8080    |      |       :8080   
          v       |        |       ^        |      |       ^        
          :8080   |        |       |        |      |       |        
    +----------+  |        |  +----------+  |      |  +----------+  
    | service  |  |        |  |   app-b  |  |      |  |   app-c  |   
    +----------+  |        |  +----------+  |      |  +----------+    

Figure #1 shows a possible network deployment scenario. Both `app-b` and `app-c` are using `grpc-rpf` to be able to
connect to the `service` in Network A. Both Network A and C are behind NAT routers and can't accept connections.

## Installing

Browse the [releases page](https://github.com/chirino/grpc-rpf/releases), extract the appropriate executable for your
platform, and install it to your `PATH`.

# Usage

## Command Line

```shell
$ grpc-rpf
GRPC Remote Port Forward

Usage:
  grpc-rpf [command]

Available Commands:
  exporter    Runs the reverse port forward client to export services to the server.
  help        Help about any command
  importer    Runs the reverse port forward client to import services from the server.
  server      Runs the reverse port forward server.
  version     Print version information for this executable

Flags:
      --dev-mode   run in development mode. increases output verbosity.
  -h, --help       help for grpc-rpf

Use "grpc-rpf [command] --help" for more information about a command.
```

## Running the Server in Kubernetes

Running the sever in Kubernetes makes it easy to get a highly available, scalable, and secure `server` implementation up
and running.

note: right now only the OpenShift flavor of Kubernetes is supported.

You can use Helm chart in the `./helm` directory to install the server on Kubernetes:

```
$ helm install grpc-rpf ./helm --set ingress.hostname=rpf.example.com
```

Make sure you set the `ingress.hostname` option to a DNS name that points your Kube cluster.

The helm chart will run multiple `server` processes in a replica set and creates multiple external routes to each
server. You should configure your `importer` and `exporter` clients to connect to the host name you configured
in `ingress.hostname`. A Kub load balancer will spread clients out over all the `server` processes. If an `importer`
process connects to a
`server` to connect to service it's not hosting, it will send the `importer` process a redirect with the host name of
a `server` process that is hosting that service.

## Service Access Control

You must specify which `importer` and `exporter` clients can access which services. This is currently done adding a
record to a PostgreSQL table. When the helm chart is installed, it will provide you information how you can connect to
that database and a service record.1

example:

```shell
$ export PGPASSWORD=$(kubectl get secret --namespace hchirino-code grpc-rpf-postgresql -o jsonpath="{.data.postgresql-password}" | base64 --decode)
$ kubectl exec -it grpc-rpf-postgresql-0 -- bash -c "PGPASSWORD=${PGPASSWORD} psql -U grpc-rpf -d grpc-rpf"
```

Then you can run the following SQL to add a service named "myapp" that allows exporters with a token of "token1" and
importers with a token of "token2" to use the service:

```
INSERT INTO services (id, allowed_to_listen, allowed_to_connect) VALUES ('myapp', '{"token1"}', '{"token2"}');
```

## Connecting the `importer` and `exporter` processes

The `importer` and `exporter` processes use GRPC and a TLS connection to the server. The helm chart for the `server`
creates a self-signed certificate. We therefore need to download the CA certificate that signed the cert. This allows
the clients to trust the certificate.

Using kubectl to download the CA certificate:

```shell
$ kubectl get secret  grpc-rpf -o jsonpath="{.data['ca\.crt']}" | base64 --decode > ca.crt
```

### The `importer`

Running the importer will bind ports for each `--import` option that you configure. In the following example we are
making the 'myapp' and 'other' service available for connections on ports 8181 and 8182.

```shell
$ grpc-rpf importer --access-token token2 --ca-file ca.crt --server rpf.example.com:443 --import myapp=:8181 --import other=:8182
```

### The `exporter`

```shell
$ grpc-rpf exporter --access-token token1 --ca-file ca.crt --export myapp=coolapp.local:8080
```

## Development Info

### Installing From Source

Requires [Go 1.16+](https://golang.org/dl/). To fetch the latest sources and install into your system:

    go get -u github.com/chirino/grpc-rpf/cmd/grpc-rpf

### Building From Source

    git clone https://github.com/chirino/grpc-rpf
    cd grpc-rpf
    make
