# Multi-stage build: static Go binaries on a distroless base.
FROM golang:1.27-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
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
