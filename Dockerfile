FROM node:22-alpine AS ui-builder
WORKDIR /app/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ .
RUN npx vite build

FROM golang:1.26-alpine AS go-builder
ARG VERSION=dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui-builder /app/internal/api/dist/ internal/api/dist/
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o /machmqtt-dashboard ./cmd/machmqtt-dashboard

FROM alpine:3.21
RUN adduser -D -u 1000 app && mkdir -p /data && chown app:app /data
COPY --from=go-builder /machmqtt-dashboard /usr/local/bin/machmqtt-dashboard
USER app
ENTRYPOINT ["machmqtt-dashboard"]
CMD ["-config", "/etc/machmqtt-dashboard/config.yaml"]
