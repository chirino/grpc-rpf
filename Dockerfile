FROM alpine:3.12
ENTRYPOINT ["grpc-rpf"]
COPY grpc-rpf /usr/local/bin/grpc-rpf
EXPOSE 8080
