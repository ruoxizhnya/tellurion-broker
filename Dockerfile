# syntax=docker/dockerfile:1
# tellurion-broker · 交易网关服务（ADR-036 G0；Mock 健康/版本面；真实 XTP 仍受 X6c 阻塞）
FROM golang:1.26.11-alpine AS builder

WORKDIR /src
RUN apk add --no-cache git

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath \
    -ldflags "-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildTime=${BUILD_TIME}" \
    -o /out/broker-gateway ./cmd/brokergateway

FROM alpine:3.19

WORKDIR /app
RUN apk add --no-cache ca-certificates curl tzdata && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone && \
    rm -rf /var/cache/apk/*

COPY --from=builder /out/broker-gateway /app/broker-gateway

EXPOSE 8091
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8091/healthz || exit 1

USER nobody
ENTRYPOINT ["/app/broker-gateway"]