FROM golang:1.24-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/vorax ./cmd/server

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S vorax \
    && adduser -S -G vorax vorax \
    && mkdir /data \
    && chown vorax:vorax /data

COPY --from=build /out/vorax /usr/local/bin/vorax

USER vorax
ENV LISTEN_ADDR=0.0.0.0:8080
ENV SIGNING_KEY_FILE=/data/signing.key

EXPOSE 8080
ENTRYPOINT ["vorax"]
