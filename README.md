# `rtsvc` : reverse tunnel service

[![CircleCI](https://circleci.com/gh/chirino/rtsvc.svg?style=svg)](https://circleci.com/gh/chirino/rtsvc)

If you have a firewalled service that you need to make available in to another publicly accessible network (like a Kubernates cluster), you can use `rtsvc` to securely expose it in that network without having setup a VPN or poke holes on the firewall protecting the private network where your service is located. 

## How it works

`rtsvc` runs a an `exporter` process in your on private network to establish an outbound grpc/http2 connection to an importer process that runs in your publicly accessible kubernetes cluster.  The importer process binds several ports:

- the grpc port that the exporter process will connect to.  This needs to be accessible from the 
- a port for each service exported by the exporter process that is imported by the importer process.

**Figure #1**

              Firewall
       Network A  |            |  Network B
                  |            |                                    
    +----------+  |    http2   |  +----------+     
    | exporter | ---------------> | importer |     
    +----------+  |            |  +----------+     
          |       |            |       ^          
          v       |            |       |
    +----------+  |            |  +----------+     
    | service  |  |            |  |  client  |     
    +----------+  |            |  +----------+     

## Installing

Browse the [releases page](https://github.com/chirino/rtsvc/releases), extract the appropriate executable
for your platform, and install it to your `PATH`.

    $ rtsvc create 

# Usage

First you need to create configuration files for the `importer` and `exporter` processes.  You can use the `rtsvc create` command for that.  

The main thing you need to figure out is a host name that your importer process will be available at.  In my following example, I'll be running the importer on an OpenShift cluster at `b6ff.rh-idev.openshiftapps.com`. I'll run the importer on the `rtsvc-importer-ws-svcteleprtsvc-idev.openshiftapps.com` host.  I want to have Kube service named asf listening on port 8080 forward traffic the service apache.org:80 that the exporter can access.

    $ rtsvc create svcteleprtsvcr-ws-svcteleporter.b6rtsvcenshiftapps.com asf:8080,apache.org:80
    wrote:  standalone-importer.yaml
    wrote:  standalone-exporter.yaml
    wrote:  openshift-importer.yaml
    wrote:  openshift-exporter.yaml
    
    These files contain secrets.  Please be careful sharing them.
    $  

You can then use the `oc create -f openshift-importer.yaml` to create the importer deployment on your openshift cluster.  You can also use `oc create -f openshift-exporter.yaml` to create the exporter.  If you don't have an openshift cluster, you can manually run the importer and exporter processes like so:

    $ rtsvc importer standalone-importer.yaml
    $ rtsvc exporter standalone-exporter.yaml

## Installing From Source

Requires [Go 1.12+](https://golang.org/dl/).  To fetch the latest sources and install into your system:

    go get -u github.com/chirino/rtsvc

## Buidling From Source

Use git to clone this repo:

    git clone https://github.com/chirino/rtsvc
    cd rtsvc

Then you can build it using:

| Platform | Command to run |
| -------- | -------------- |
| Windows  | `mage`         |
| Other    | `./mage`       |
