FROM golang:1.22 AS builder
WORKDIR /workspace
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=$GOPROXY
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /manager ./cmd/manager
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /probe ./cmd/probe

FROM alpine:3.20
WORKDIR /
RUN adduser -D -u 65532 nonroot \
    && apk add --no-cache ca-certificates
COPY --from=builder /manager /manager
COPY --from=builder /probe /probe
USER 65532:65532
ENTRYPOINT ["/manager"]
