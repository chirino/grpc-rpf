
# Build to with goreleaser
build:
	goreleaser --snapshot --rm-dist
.PHONY: build

###########################################################################################
# DB related targets
###########################################################################################
POSTGRES_CONTAINER=grpc-rpf
POSTGRES_DB=grpc-rpf
POSTGRES_PORT=5432
POSTGRES_USER=grpc-rpf

db/setup:
	@docker run \
	  --name=${POSTGRES_CONTAINER} \
	  -e POSTGRES_DB="${POSTGRES_DB}" \
	  -p "${POSTGRES_PORT}":5432 \
	  -e POSTGRES_USER="${POSTGRES_USER}" \
	  -e POSTGRES_PASSWORD="${POSTGRES_PASSWORD}" \
	  -d postgres:13
.PHONY: db/setup

db/teardown:
	docker stop ${POSTGRES_CONTAINER}
	docker rm ${POSTGRES_CONTAINER}
.PHONY: db/teardown

db/sql:
	@docker exec -u $(shell id -u) -it ${POSTGRES_CONTAINER} /bin/bash -c 'PGPASSWORD=${POSTGRES_PASSWORD} psql -d ${POSTGRES_DB} -U ${POSTGRES_USER}'
.PHONY: db/login
