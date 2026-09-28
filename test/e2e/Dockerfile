FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/mikan-node ./cmd/mikan-node && \
    CGO_ENABLED=0 go build -o /out/target ./test/e2e/target

FROM alpine:3.22
COPY --from=build /out/ /usr/local/bin/
