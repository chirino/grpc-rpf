#!bin/bash
mkdir generated
cd generated
rm *

openssl req \
    -newkey rsa:2048 \
    -nodes \
    -days 3650 \
    -x509 \
    -keyout ca.key \
    -out ca.pem \
    -subj "/CN=Root CA"

openssl req \
    -newkey rsa:2048 \
    -nodes \
    -keyout server.key \
    -out server.csr \
    -subj "/CN=localhost"
openssl x509 \
    -req \
    -days 3650 \
    -sha256 \
    -CA ca.pem \
    -CAkey ca.key \
    -CAcreateserial \
    -in server.csr \
    -out server.pem \
    -extfile <(echo subjectAltName = DNS:localhost,IP:127.0.0.1)


openssl x509 -in server.pem -text -noout

openssl req \
    -newkey rsa:2048 \
    -nodes \
    -keyout client.key \
    -out client.csr \
    -subj "/CN=client"

openssl x509 \
    -req \
    -days 3650 \
    -sha256 \
    -CA ca.pem \
    -CAkey ca.key \
    -CAcreateserial \
    -in client.csr \
    -out client.pem

