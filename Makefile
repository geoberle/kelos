.PHONY: test build fmt fmt-check lint lint-fix image chart
GOLANGCI = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.8.0
GOIMPORTS = go run golang.org/x/tools/cmd/goimports@v0.39.0

test:
	go test -race ./...
build:
	go build -o bin/orchestrator ./cmd/orchestrator
fmt:
	$(GOIMPORTS) -local github.com/geoberle/kelos -w cmd internal
	$(GOLANGCI) fmt
fmt-check:
	@test -z "$$($(GOIMPORTS) -local github.com/geoberle/kelos -l cmd internal)"
	$(GOLANGCI) fmt --diff > /tmp/kelos-format.diff
	@test ! -s /tmp/kelos-format.diff
lint:
	$(GOLANGCI) run
lint-fix:
	$(GOLANGCI) run --fix
image:
	docker buildx build --platform linux/amd64,linux/arm64 .
chart:
	helm lint charts/orchestrator
	helm template admission charts/orchestrator --set 'configuration.repositories[0]=geoberle/kelos' >/dev/null
