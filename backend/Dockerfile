FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
RUN go mod verify && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
FROM alpine:3.20
RUN adduser -D -u 10001 app && apk add --no-cache wget tzdata
COPY --from=build /out/server /usr/local/bin/server
USER app
ENTRYPOINT ["server"]
CMD ["api"]
