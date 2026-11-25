# Stage 1: Build frontend
FROM node:20-alpine AS frontend-builder
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci
COPY frontend/ ./
RUN npm run build

# Build the offline VietQR helper separately so its runtime dependencies are small.
FROM node:22-alpine AS vietqr-builder
WORKDIR /app/vietqr-node
COPY backend/cloudifyorders/vietqr-node/package.json backend/cloudifyorders/vietqr-node/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --omit=dev --ignore-scripts
COPY backend/cloudifyorders/vietqr-node/generate.mjs ./

# Build backend
FROM golang:1.25-alpine AS backend-builder
WORKDIR /app/backend
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY backend/ ./
ARG VERSION=v1
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o /cqa-server .

# Production image
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata nodejs
WORKDIR /app

COPY --from=backend-builder /cqa-server /app/cqa-server
COPY --from=frontend-builder /app/frontend/dist /app/static
COPY --from=vietqr-builder /app/vietqr-node /app/vietqr-node

RUN mkdir -p /var/lib/cqa/files

EXPOSE 8080

ENTRYPOINT ["/app/cqa-server"]
