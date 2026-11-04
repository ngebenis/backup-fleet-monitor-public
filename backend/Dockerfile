FROM golang:1.23-alpine@sha256:383395b794dffa5b53012a212365d40c8e37109a626ca30d6151c8348d380b5f AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
RUN go mod verify && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
RUN adduser -D -u 10001 app && apk add --no-cache wget tzdata
COPY --from=build /out/server /usr/local/bin/server
USER app
ENTRYPOINT ["server"]
CMD ["api"]
