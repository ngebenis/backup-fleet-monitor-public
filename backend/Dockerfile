FROM golang:1.23-alpine@sha256:383395b794dffa5b53012a212365d40c8e37109a626ca30d6151c8348d380b5f AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
RUN go mod verify && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
RUN adduser -D -u 10001 app && apk add --no-cache wget tzdata
COPY --from=build /out/server /usr/local/bin/server
USER app
ENTRYPOINT ["server"]
CMD ["api"]
