# syntax=docker/dockerfile:1.7

FROM node:24-alpine AS frontend
WORKDIR /src/web
COPY web/package*.json ./
RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X github.com/kayungou/BatchManagementofCloudServerAccounts/internal/buildinfo.Version=${VERSION} -X github.com/kayungou/BatchManagementofCloudServerAccounts/internal/buildinfo.Commit=${COMMIT} -X github.com/kayungou/BatchManagementofCloudServerAccounts/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o /out/cloudmanager ./cmd/cloudmanager

FROM alpine:3.24 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 cloudmanager \
    && adduser -S -D -H -u 10001 -G cloudmanager cloudmanager
WORKDIR /app
COPY --from=backend --chown=cloudmanager:cloudmanager /out/cloudmanager /app/cloudmanager
COPY --from=frontend --chown=cloudmanager:cloudmanager /src/web/dist /app/web/dist

ENV APP_ENV=production \
    FRONTEND_DIR=/app/web/dist \
    RUN_WORKER=false \
    DEV_EXPOSE_TOKENS=false

USER cloudmanager
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=15s --retries=5 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/app/cloudmanager"]
CMD ["serve"]
