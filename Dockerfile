FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /pcke ./cmd/pcke

FROM gcr.io/distroless/static-debian12
COPY --from=builder /pcke /usr/local/bin/pcke
ENTRYPOINT ["pcke"]
