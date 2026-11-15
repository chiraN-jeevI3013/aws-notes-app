# ---- Stage 1: build ----
# A full Go toolchain image (~800 MB). Used only to compile; it is thrown away afterwards.
FROM golang:1.27.4 AS build
WORKDIR /src

# Copy dependency files first and download modules. Docker caches each step (layer),
# so as long as go.mod/go.sum don't change, this slow step is skipped on rebuilds.
COPY go.mod go.sum ./
RUN go mod download

# Now copy the source and compile a static binary (CGO off = no C libraries needed at runtime).
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/server ./cmd/server

# ---- Stage 2: run ----
# Distroless: just the binary plus CA certs and timezone data. No shell, no package manager,
# so a much smaller attack surface. The :nonroot tag runs as user 65532, not root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/server"]
