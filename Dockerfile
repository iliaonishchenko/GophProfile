FROM golang:1.26.5-alpine AS builder

WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/migrate ./cmd/migrate

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 app
WORKDIR /app
COPY --from=builder /out/server /app/server
COPY --from=builder /out/worker /app/worker
COPY --from=builder /out/migrate /app/migrate
COPY --from=builder /src/web /app/web
USER app
EXPOSE 8080
CMD ["/app/server"]
