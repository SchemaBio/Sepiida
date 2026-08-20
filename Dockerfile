# syntax=docker/dockerfile:1.7

# ---- Sepiida Server build ----
FROM golang:1.25-alpine3.22 AS builder

ENV GOPROXY=https://mirrors.tencent.com/go/,direct

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tencent.com/g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates git

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd/server ./cmd/server
COPY internal/common ./internal/common
COPY internal/server ./internal/server

ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -buildid=" -o /out/sepiida-server ./cmd/server

FROM alpine:3.22

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tencent.com/g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates tzdata wget

ENV TZ=Asia/Shanghai
RUN adduser -D -u 1000 sepiida

WORKDIR /app
COPY --from=builder /out/sepiida-server /app/sepiida-server

RUN mkdir -p /etc/sepiida/keys && chown sepiida /etc/sepiida/keys

USER sepiida

EXPOSE 9090

LABEL org.opencontainers.image.title="Sepiida Server" \
      org.opencontainers.image.description="MiniWDL status collection server" \
      org.opencontainers.image.source="https://github.com/SchemaBio/Sepiida"

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -T 2 --spider http://127.0.0.1:9090/health || exit 1

STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/sepiida-server"]
