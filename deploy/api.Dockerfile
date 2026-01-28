FROM golang:1.22-bookworm AS build
WORKDIR /app
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
COPY templates ./templates
COPY static ./static
RUN go build -o /out/api ./cmd/api

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/api /app/api
COPY templates /app/templates
COPY static /app/static
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/api"]
