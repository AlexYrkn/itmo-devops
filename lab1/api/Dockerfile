FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod ./
COPY *.go ./

RUN CGO_ENABLED=0 go build -o /out/main ./

FROM alpine:3.20

WORKDIR /app
COPY --from=builder /out/main .


EXPOSE 8080

CMD ["./main"]
