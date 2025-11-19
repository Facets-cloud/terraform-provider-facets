HOSTNAME=registry.terraform.io
NAMESPACE=Facets-cloud
NAME=facets
BINARY=terraform-provider-${NAME}
VERSION=99.0.0-local
OS_ARCH=$(shell go env GOOS)_$(shell go env GOARCH)

default: install

build:
	go build -o ${BINARY}

install: build
	mkdir -p ~/.terraform.d/plugins/${HOSTNAME}/${NAMESPACE}/${NAME}/${VERSION}/${OS_ARCH}
	mv ${BINARY} ~/.terraform.d/plugins/${HOSTNAME}/${NAMESPACE}/${NAME}/${VERSION}/${OS_ARCH}/terraform-provider-facets_v${VERSION}

test:
	go test -v ./...

clean:
	rm -rf .terraform .terraform.lock.hcl terraform.tfstate terraform.tfstate.backup

.PHONY: build install test clean
