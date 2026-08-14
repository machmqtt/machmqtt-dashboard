FROM node:22-alpine@sha256:c610fcdfb1d5b4740dd70c284ed3cb16bb857e0f7166196e36a5501df7a3aa32 AS ui-builder
WORKDIR /app/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ .
RUN npm run build

FROM golang:1.26.6-alpine@sha256:af8d6740070b8906d12eae1c3e3ea0957fb63f492051ea05e354c38ef9fe88df AS go-builder
ARG VERSION=dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui-builder /app/internal/api/dist/ internal/api/dist/
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o /machmqtt-dashboard ./cmd/machmqtt-dashboard

FROM alpine:3.21@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d
RUN adduser -D -u 1000 app && mkdir -p /data && chown app:app /data
COPY --from=go-builder /machmqtt-dashboard /usr/local/bin/machmqtt-dashboard
USER app
ENTRYPOINT ["machmqtt-dashboard"]
CMD ["-config", "/etc/machmqtt-dashboard/config.yaml"]
