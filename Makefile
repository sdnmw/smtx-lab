SHELL := /bin/bash

IMG ?= docker.io/lammw12/smtx-lab-operator:v0.1.0
AGENT_IMG ?= docker.io/lammw12/smtx-lab-agent:v0.1.0

.PHONY: test
test:
	go test ./...

.PHONY: fmt
fmt:
	gofmt -w api cmd internal

.PHONY: build
build:
	go build ./cmd/manager
	go build ./cmd/agent
	go build ./cmd/probe

.PHONY: docker-build
docker-build:
	docker build -t $(IMG) -f Dockerfile .

.PHONY: docker-build-agent
docker-build-agent:
	docker build -t $(AGENT_IMG) -f Dockerfile.agent .

.PHONY: manifests
manifests:
	@echo "CRD manifests are maintained under config/crd/bases."

.PHONY: install
install:
	kubectl apply -k config/crd/bases

.PHONY: deploy
deploy:
	kubectl apply -k config/default

.PHONY: e2e-kind
e2e-kind:
	bash scripts/e2e-kind.sh
