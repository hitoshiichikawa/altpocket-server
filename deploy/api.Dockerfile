FROM golang:1.22-bookworm AS build
WORKDIR /app
COPY . .
RUN go mod download
RUN GIT_SHA=$(git rev-parse --short=12 HEAD 2>/dev/null || echo dev) && \
    go build -ldflags "-X altpocket/internal/ui.BuildRevision=${GIT_SHA}" -o /out/api ./cmd/api

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/api /app/api
COPY templates /app/templates
COPY static /app/static
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/api"]
