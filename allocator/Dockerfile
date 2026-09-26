# syntax=docker/dockerfile:1

# --- build ----------------------------------------------------------------
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY buddy ./buddy
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/allocator .

# --- runtime --------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/allocator /usr/local/bin/allocator
ENV ADDR=":8080"
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/allocator"]
