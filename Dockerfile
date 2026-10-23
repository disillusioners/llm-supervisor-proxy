# Stage 1: Build frontend
FROM node:22-alpine AS frontend-builder

WORKDIR /app/pkg/ui/frontend

COPY pkg/ui/frontend/package*.json ./
RUN npm ci

COPY pkg/ui/frontend/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.26-alpine AS builder

WORKDIR /app

# APP_VERSION is injected by CI: the docker-ci workflow passes
# github.run_number as this build-arg, so every CI build gets a fresh,
# monotonic version for the UI badge. Local builds without the arg fall
# back to "dev" — there is NO committed VERSION file to bump.
ARG APP_VERSION=dev

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Copy frontend build output (needed for embedding)
COPY --from=frontend-builder /app/pkg/ui/static ./pkg/ui/static

# Build static binary (modernc.org/sqlite is pure Go, no CGO needed)
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X main.Version=${APP_VERSION} -s -w" \
    -o llm-supervisor-proxy ./cmd/main.go

# Stage 3: Minimal runtime image
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/llm-supervisor-proxy /usr/local/bin/llm-supervisor-proxy

# Create directory for SQLite database (if used)
RUN mkdir -p /data && chmod 777 /data

EXPOSE 4321

ENV PORT=4321

ENTRYPOINT ["llm-supervisor-proxy"]
