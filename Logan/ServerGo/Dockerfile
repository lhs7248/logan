ARG GO_VERSION=1.27.0
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -tags stdjson -trimpath -o /out/logan-server .

FROM alpine:3.22.1
RUN apk add --no-cache ca-certificates && addgroup -g 10001 logan && adduser -D -H -u 10001 -G logan logan \
    && mkdir -p /var/lib/logan-tmp /run/logan-secrets \
    && chown 10001:10001 /var/lib/logan-tmp && chmod 0700 /var/lib/logan-tmp
COPY --from=build /out/logan-server /usr/local/bin/logan-server
USER 10001:10001
ENV LOGAN_HTTP_ADDRESS=:8080 LOGAN_NATIVE_TEMP_DIR=/var/lib/logan-tmp
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/logan-server"]
