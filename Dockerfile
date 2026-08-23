FROM golang:1.21 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o sentinel ./cmd/sentinel

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /root/
COPY --from=builder /app/sentinel .
COPY --from=builder /app/config.yaml ./config.yaml
ENV TZ=Asia/Shanghai
EXPOSE 8080
CMD ["./sentinel"]
