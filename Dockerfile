# syntax=docker/dockerfile:1
ARG GO_VERSION=1.23

FROM golang:${GO_VERSION}-alpine AS builder
RUN apk add --no-cache ca-certificates git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/agentsdk-http ./examples/03-http

FROM alpine:3.20
RUN addgroup -S agent && adduser -S agent -G agent \
    && apk add --no-cache ca-certificates wget \
    && mkdir -p /var/agentsdk && chown -R agent:agent /var/agentsdk
WORKDIR /app
ENV TMPDIR=/var/agentsdk \
    ANTHROPIC_API_KEY="" \
    AGENTSDK_HTTP_ADDR=":8080" \
    AGENTSDK_MODEL="claude-3-5-sonnet-20241022"
COPY --from=builder /out/agentsdk-http /usr/local/bin/agentsdk-http
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["sh","-c","ADDR=${AGENTSDK_HTTP_ADDR:-:8080}; PORT=${ADDR##*:}; [ -z \"$PORT\" ] && PORT=8080; wget -qO- http://127.0.0.1:${PORT}/health || exit 1"]
USER agent
ENTRYPOINT ["/usr/local/bin/agentsdk-http"]
