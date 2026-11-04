FROM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
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
