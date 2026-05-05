# syntax=docker/dockerfile:1.7

# Pin the builder to BUILDPLATFORM so Go cross-compiles natively for the
# requested target. This avoids QEMU emulation (which slows arm64 builds
# from ~30s to ~6min on amd64 runners).
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG APP_VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X main.appVersion=${APP_VERSION}" \
    -o /out/skulid ./cmd/skulid

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/skulid /skulid
USER nonroot:nonroot
EXPOSE 8567
ENTRYPOINT ["/skulid"]
