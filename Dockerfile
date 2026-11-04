# Multi-stage build: static Go binaries on a distroless base.
FROM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pusaq ./cmd/pusaq \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mockprovider ./cmd/mockprovider

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS pusaq
COPY --from=build /out/pusaq /pusaq
EXPOSE 8080
ENTRYPOINT ["/pusaq"]

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS mockprovider
COPY --from=build /out/mockprovider /mockprovider
EXPOSE 9090
ENTRYPOINT ["/mockprovider"]
