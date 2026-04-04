# ============================================================
# Go API 多阶段构建 - 最小化镜像
# ============================================================
FROM golang:1.23-alpine AS builder

# 国内服务器 Docker 构建时拉取 Go 模块（与服务器本地 GOPROXY 对齐，避免 git 工作区长期「已修改」）
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.google.cn

WORKDIR /build
COPY . .
RUN go mod tidy && go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/api-server .

# ==================== 运行阶段 ====================
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata
ENV TZ=Asia/Shanghai

COPY --from=builder /build/api-server /usr/local/bin/api-server

EXPOSE 8080

CMD ["api-server"]
