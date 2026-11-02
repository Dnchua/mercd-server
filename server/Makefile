GO ?= go
OUTDIR ?= ../output/rebuilt
.PHONY: help check build api
help:
	@echo 'check build api'
check:
	$(GO) vet ./...
	$(GO) test ./...
build:
	CGO_ENABLED=0 $(GO) build -trimpath -o $(OUTDIR)/mercd35 ./cmd/mercd35
	CGO_ENABLED=0 $(GO) build -trimpath -o $(OUTDIR)/mercdb ./cmd/mercdb
api:
	$(GO) run ./cmd/apidoc -service cn35 -out docs/api-cn35
