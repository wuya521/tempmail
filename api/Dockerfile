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

# 国内构建时 apk 拉 dl-cdn.alpinelinux.org 常极慢（数分钟～超时）；替换为阿里云镜像可明显加速。
# 若在境外构建且镜像不可用，可改为官方源：删除下行 sed，仅保留 apk 命令。
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk --no-cache add ca-certificates tzdata
ENV TZ=Asia/Shanghai

COPY --from=builder /build/api-server /usr/local/bin/api-server

EXPOSE 8080

CMD ["api-server"]
