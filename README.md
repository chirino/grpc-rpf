# `svcteleporter` : A Kubernetes Service Teleporter.

[![CircleCI](https://circleci.com/gh/chirino/svcteleporter.svg?style=svg)](https://circleci.com/gh/chirino/svcteleporter)

If you have a firewalled service that you need to make available in your Kubernetes or Openshift cluster, you can use `svcteleporter` to securely expose it in the cluster without having setup a VPN or poke holes on the firewall protecting the private network where your service is located.   

## How it works

`svcteleporter` runs a an `exporter` process in your on premise network to establish an outbound ssh over secure web sockets (wss) connection to an importer pod that runs in your publicly accessible kubernetes cluster.  The wss connection uses mutual tls authentication so to ensure that only your kubernetes cluster can access the exported service.  The importer pods exposes:

1. a public kubernetes service the ssh over wss connection
2. a private kubernetes service at that is a proxy to the on premise service.

**Figure #1**

              Firewall
                  |                       
    +----------+  |    grpc/http2        +----------+     
    | exporter | ----------------------> | importer |     
    +----------+  |                      +----------+     
          |       |                            ^          
          v       |                            |          
    +----------+  |                      +----------+     
    | service  |  |                      |  client  |     
    +----------+  |                      +----------+     
                  |                                       


## Installing

Browse the [releases page](https://github.com/chirino/svcteleporter/releases), extract the appropriate executable
for your platform, and install it to your `PATH`.

    $ svcteleporter create 

# Usage

First you need to create configuration files for the `importer` and `exporter` processes.  You can use the `svcteleporter create` command for that.  

The main thing you need to figure out is a host name that your importer process will be available at.  In my following example, I'll be running the importer on an OpenShift cluster at `b6ff.rh-idev.openshiftapps.com`. I'll run the importer on the `svcteleporter-importer-ws-svcteleporter.b6ff.rh-idev.openshiftapps.com` host.  I want to have Kube service named asf listening on port 8080 forward traffic the service apache.org:80 that the exporter can access.

    $ svcteleporter create svcteleporter-importer-ws-svcteleporter.b6ff.rh-idev.openshiftapps.com asf:8080,apache.org:80
    wrote:  standalone-importer.yaml
    wrote:  standalone-exporter.yaml
    wrote:  openshift-importer.yaml
    wrote:  openshift-exporter.yaml
      
    These files contain secrets.  Please be careful sharing them.
    $  

You can then use the `oc create -f openshift-importer.yaml` to create the importer deployment on your openshift cluster.  You can also use `oc create -f openshift-exporter.yaml` to create the exporter.  If you don't have an openshift cluster, you can manually run the importer and exporter processes like so:
    
    $ svcteleporter importer standalone-importer.yaml
    $ svcteleporter exporter standalone-exporter.yaml

## Installing From Source

Requires [Go 1.12+](https://golang.org/dl/).  To fetch the latest sources and install into your system:

    go get -u github.com/chirino/svcteleporter

## Buidling From Source

Use git to clone this repo:

    git clone https://github.com/chirino/svcteleporter
    cd svcteleporter

Then you can build it using:

| Platform | Command to run |
| -------- | -------------- |
| Windows  | `mage`         |
| Other    | `./mage`       |
