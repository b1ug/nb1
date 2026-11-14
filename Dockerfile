FROM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS build

ENV CGO_ENABLED=1 GOWORK=off GOTOOLCHAIN=local
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -mod=readonly -buildvcs=false -trimpath -o /out/nb1 .

FROM debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc

RUN groupadd --gid 65532 nb1 \
    && useradd --uid 65532 --gid 65532 --home-dir /var/lib/nb1 \
       --create-home --shell /usr/sbin/nologin nb1 \
    && chmod 0700 /var/lib/nb1
COPY --from=build /out/nb1 /usr/local/bin/nb1
USER 65532:65532
ENV HOME=/var/lib/nb1 XDG_CACHE_HOME=/var/lib/nb1/cache XDG_CONFIG_HOME=/var/lib/nb1/config
WORKDIR /var/lib/nb1
ENTRYPOINT ["/usr/local/bin/nb1"]
CMD ["--help"]
