FROM --platform=$BUILDPLATFORM golang:1.25.5 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /orchestrator ./cmd/orchestrator
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /orchestrator /orchestrator
USER 65532:65532
ENTRYPOINT ["/orchestrator"]
