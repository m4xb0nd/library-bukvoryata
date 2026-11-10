FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go *.sql ./
RUN CGO_ENABLED=0 go build -o /library .

FROM alpine:3.20
WORKDIR /app
COPY --from=build /library /app/library
COPY templates /app/templates
COPY static /app/static
EXPOSE 8080
ENV LIBRARY_ADDR=:8080
CMD ["/app/library"]
