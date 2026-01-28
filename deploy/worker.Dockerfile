FROM golang:1.22-bookworm AS build
WORKDIR /app
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN go build -o /out/worker ./cmd/worker

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/worker /app/worker
USER nonroot:nonroot
ENTRYPOINT ["/app/worker"]
