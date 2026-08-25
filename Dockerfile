# Multi-stage build: static Go binaries on a distroless base.
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pusaq ./cmd/pusaq \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mockprovider ./cmd/mockprovider

FROM gcr.io/distroless/static-debian12:nonroot AS pusaq
COPY --from=build /out/pusaq /pusaq
EXPOSE 8080
ENTRYPOINT ["/pusaq"]

FROM gcr.io/distroless/static-debian12:nonroot AS mockprovider
COPY --from=build /out/mockprovider /mockprovider
EXPOSE 9090
ENTRYPOINT ["/mockprovider"]
