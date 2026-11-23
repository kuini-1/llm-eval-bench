FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY assets ./assets
# modernc.org/sqlite is pure Go, so the binary needs no C toolchain or libc.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/evalbench ./cmd/evalbench

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
 && adduser -D -u 10001 app \
 && mkdir -p /data && chown app:app /data
COPY --from=build /out/evalbench /usr/local/bin/evalbench
COPY datasets /app/datasets
COPY deploy/bench.docker.yaml /etc/evalbench/bench.yaml
WORKDIR /app
USER app
ENV EVALBENCH_CONFIG=/etc/evalbench/bench.yaml
VOLUME ["/data"]
EXPOSE 8097
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8097/api/health || exit 1
ENTRYPOINT ["evalbench"]
CMD ["serve", "-addr", "0.0.0.0:8097"]
