FROM golang:1.25-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o /tt-device-plugin ./cmd/tt-device-plugin/

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/OctopusET/k8s-device-plugin" \
      org.opencontainers.image.description="Kubernetes device plugin for Tenstorrent AI accelerators" \
      org.opencontainers.image.vendor="Tenstorrent Inc."
COPY --from=build /tt-device-plugin /tt-device-plugin
ENTRYPOINT ["/tt-device-plugin"]
