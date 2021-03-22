# `grpc-rpf` : GRPC Reverse Port Forward

[![CircleCI](https://circleci.com/gh/chirino/grpc-rpf.svg?style=svg)](https://circleci.com/gh/chirino/grpc-rpf)

If you have a firewalled service that you need to make available in to another publicly accessible network (like a Kubernates cluster), you can use `grpc-rpf` to expose it in that network without having setup a VPN or poke holes on the firewall protecting the private network where your service is located. 

## How it works

`grpc-rpf` runs a `client` process to establish an outbound grpc/http2 connection to a `server` process that runs in your publicly accessible network.  The `server` process binds several ports:

- the grpc port that the exporter process will connect to.  This needs to be accessible from the 
- a port for each service exported by the exporter process that is imported by the importer process.

**Figure #1**

              Firewall
       Network A  |            |  Network B
                  |            |                                    
    +----------+  |    grpc    |  +----------+     
    |  client  | ---------------> |  server  |     
    +----------+  |            |  +----------+     
          |       |            |       ^          
          v       |            |       |
    +----------+  |            |  +----------+     
    | service  |  |            |  |   app    |     
    +----------+  |            |  +----------+     

## Installing

Browse the [releases page](https://github.com/chirino/grpc-rpf/releases), extract the appropriate executable
for your platform, and install it to your `PATH`.

# Usage

TODO

## Installing From Source

Requires [Go 1.12+](https://golang.org/dl/).  To fetch the latest sources and install into your system:

    go get -u github.com/chirino/grpc-rpf

## Buidling From Source

    git clone https://github.com/chirino/grpc-rpf
    cd grpc-rpf
    go install
