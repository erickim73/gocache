# start from official Go image
FROM golang:1.25.3-alpine AS builder

# install git
RUN apk add --no-cache git

# sets working directory for all subsequent instructions
WORKDIR /app

# copy go.mod and go.sum before copying rest of source code
COPY go.mod go.sum ./

# download all dependencies listed in go.mod into module cache
RUN go mod download

# copy rest of source code into container
COPY . .

# compile binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o gocache ./cmd/server

# --- runtime ---
# start fresh from minimal Alpine image
FROM alpine:3.21

# install two small utilities
RUN apk add --no-cache ca-certificates wget

# create a dedicated directory for persistence files
RUN mkdir -p /data
WORKDIR /data

# copy compiled binary from stage 1 into image
COPY --from=builder /app/gocache /usr/local/bin/gocache

# document that container listens on these ports at runtime
EXPOSE 7000 8000 9090

# command that runs when container starts
CMD ["gocache"]