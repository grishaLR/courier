FROM golang:alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o courier ./cmd/server

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/courier .
COPY --from=builder /app/web/public/app_catalog.json ./data/app_catalog.json

EXPOSE 8080
ENV CATALOG_PATH=data/app_catalog.json
CMD ["./courier"]
