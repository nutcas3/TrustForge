FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .


RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /bin/trustforge-api ./cmd/api

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /bin/guest_agent ./cmd/guest_agent

FROM alpine:3.21


RUN apk add --no-cache e2fsprogs util-linux

# Create the jailer user
RUN addgroup -g 1001 trustforge && \
    adduser -u 1001 -G trustforge -s /bin/sh -D trustforge

RUN mkdir -p \
    /var/run/trustforge/sockets \
    /var/lib/trustforge/images \
    /var/lib/trustforge/tasks \
    /var/lib/trustforge/snapshots && \
    chown -R trustforge:trustforge /var/run/trustforge /var/lib/trustforge

COPY --from=builder /bin/trustforge-api /usr/local/bin/trustforge-api
COPY --from=builder /bin/guest_agent /var/lib/trustforge/guest_agent
COPY config.yaml /etc/trustforge/config.yaml

EXPOSE 8080 50051

USER trustforge

ENTRYPOINT ["/usr/local/bin/trustforge-api"]
CMD ["--config", "/etc/trustforge/config.yaml"]
