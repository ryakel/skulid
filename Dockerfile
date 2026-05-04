# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG APP_VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.appVersion=${APP_VERSION}" \
    -o /out/skulid ./cmd/skulid

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/skulid /skulid
USER nonroot:nonroot
EXPOSE 8567
ENTRYPOINT ["/skulid"]
