# $BUILDPLATFORM = a máquina que builda (seu Mac, ARM).
# Assim o Go roda nativo e só GERA o binário pra arquitetura de destino —
# muito mais rápido do que emular um x86 inteiro pra compilar.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-s -w" -o /api ./cmd/api

FROM gcr.io/distroless/static-debian12
COPY --from=build /api /api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]
