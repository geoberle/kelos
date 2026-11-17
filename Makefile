.PHONY: verify test build fmt fmt-check lint lint-fix image chart
GOLANGCI = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.8.0
GOIMPORTS = go run golang.org/x/tools/cmd/goimports@v0.39.0

# The race runtime cannot start on every host, such as a Raspberry Pi 5.
RACE = $(shell go test -race -count=1 -run '^$$' errors >/dev/null 2>&1 && echo -race)

verify: fmt-check lint test build chart
test:
	go test $(RACE) ./...
build:
	go build -o bin/orchestrator ./cmd/orchestrator
fmt:
	$(GOIMPORTS) -local github.com/hearthctl/kelos-orchestrator -w cmd internal
	$(GOLANGCI) fmt
fmt-check:
	@test -z "$$($(GOIMPORTS) -local github.com/hearthctl/kelos-orchestrator -l cmd internal)"
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
	helm template admission charts/orchestrator --set 'configuration.repositories[0]=hearthctl/kelos-orchestrator' >/dev/null
