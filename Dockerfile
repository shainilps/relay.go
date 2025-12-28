FROM golang:1.25-alpine AS builder

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -ldflags="-s -w" -o relay ./main.go

FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/relay .

COPY config.yaml .

EXPOSE 8080

CMD ["./relay"]
