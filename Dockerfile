
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server && \
    CGO_ENABLED=0 go build -o /out/migrate ./cmd/migrate

FROM alpine:3.22
WORKDIR /app
COPY --from=build /out/server /app/server
COPY --from=build /out/migrate /app/migrate
ENV PORT=8080 DATABASE_PATH=/data/app.sqlite3
EXPOSE 8080
# 迁移已嵌入二进制，启动时自动应用；无需在镜像内安装 sqlite3。
CMD ["/bin/sh", "-c", "mkdir -p /data && exec /app/server"]
