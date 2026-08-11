# y-ai-pond · 开发者指南（Developer Guide）

> **版本**: v1.0 | **日期**: 2026-08-11 | **作者**: y-ai-pond 项目组
> **目标**: 让新开发者在 30 分钟内启动本地开发环境

---

## 1. 前置依赖

| 依赖 | 版本 | 用途 |
|------|------|------|
| **Go** | 1.25+ | 编译运行（go.mod 使用 `go 1.25.0` 指令，GOTOOLCHAIN=auto 可自动下载） |
| **Docker** | 20.10+ | 启动 EMQX / InfluxDB 3 / PostgreSQL / Redis 容器 |
| **buf** | 最新 | Protobuf 代码生成（若未安装可用 protoc + protoc-gen-go 替代） |
| **golangci-lint** | v2.x | 静态检查（可选，但 CI 强制） |

### 1.1 验证安装

```powershell
go version        # 期望 go1.25.x
docker --version  # 期望 20.10+
buf --version     # 期望 v1.x
```

---

## 2. 克隆与依赖配置

### 2.1 克隆项目

```powershell
git clone <repo-url> y-ai-pond
cd y-ai-pond
```

### 2.2 依赖同级目录 y-ai-agent-base

本项目依赖 `github.com/anrror/y-ai-agent-base`，该模块位于同级目录 `../y-ai-agent-base`。本地开发需通过 `go mod edit -replace` 指向它：

```powershell
# 在项目根目录执行
go mod edit -replace github.com/anrror/y-ai-agent-base=../y-ai-agent-base
go mod tidy
```

> **注意**：`y-ai-agent-base` 必须与 `y-ai-pond` 位于同一父目录下（即 `../y-ai-agent-base`）。若目录结构不同，请调整 replace 路径。

### 2.3 配置

项目使用 `config/config.yaml` 作为默认配置。首次开发可复制示例配置：

```powershell
Copy-Item config/config.yaml config/config.local.yaml
# 按需修改 config.local.yaml 中的端口、数据库连接等
```

配置加载优先级：`--config` 命令行参数 > `POND_CONFIG` 环境变量 > `config/config.yaml`。

```powershell
# 方式一：命令行参数
go run ./cmd/server/ -config config/config.local.yaml

# 方式二：环境变量
$env:POND_CONFIG = "config/config.local.yaml"
go run ./cmd/server/
```

---

## 3. 构建

### 3.1 使用 Makefile（推荐）

```powershell
make build    # go build ./...
```

### 3.2 直接使用 go

```powershell
# 构建云端服务
go build ./cmd/server/

# 构建边缘端控制器
go build ./cmd/edge/

# 构建数据库迁移工具
go build ./cmd/migrate/
```

---

## 4. 启动依赖服务（Docker Compose）

`docker-compose.yml` 一键启动全部依赖服务：

```powershell
docker compose up -d
```

启动的服务：

| 服务 | 镜像 | 端口 | 用途 |
|------|------|------|------|
| EMQX | emqx/emqx:5.8 | 1883 (MQTT), 8083 (WS), 18083 (Dashboard) | MQTT Broker |
| InfluxDB 3 | influxdb:3.0 | 8086, 8181 | 时序数据库 |
| PostgreSQL | postgres:16-alpine | 5432 | 业务数据库 |
| Redis | redis:7-alpine | 6379 | 缓存/消息队列 |

验证服务健康：

```powershell
docker compose ps
```

> **注意**：需要 Docker。若本机无 Docker，可用 mock 单元测试替代容器集成测试（见第 8 节）。

---

## 5. 运行

### 5.1 启动云端服务

```powershell
# 启动云端服务（默认 :8080）
go run ./cmd/server/

# 验证健康检查
curl http://localhost:8080/health
```

期望响应：

```json
{
  "status": "ok",
  "timestamp": "2026-08-11T08:00:00Z",
  "uptime_s": 5,
  "checks": {
    "influxdb": { "status": "ok", "latency": "1.2ms" },
    "postgres": { "status": "ok", "latency": "0.8ms" },
    "redis": { "status": "ok", "latency": "0.5ms" }
  }
}
```

### 5.2 启动边缘端控制器

```powershell
go run ./cmd/edge/
```

> 边缘端控制器需要真实硬件（RK3588 + 传感器）。无硬件时使用 mock 模式（见第 7 节）。

### 5.3 数据库迁移

```powershell
go run ./cmd/migrate/
```

---

## 6. Protobuf 代码生成

项目使用 `buf` 管理 Protobuf 定义（`proto/` 目录）并生成 Go 代码（`pkg/proto/`）。

### 6.1 使用 buf（推荐）

```powershell
# 生成 Protobuf 代码
buf generate
```

### 6.2 使用 protoc 替代

若未安装 buf，可用 protoc + protoc-gen-go 替代：

```powershell
# 安装 protoc-gen-go
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

# 生成代码（示例，具体命令以 proto/ 目录结构为准）
protoc --go_out=. --go_opt=paths=source_relative proto/*.proto
```

---

## 7. 硬件模拟（Mock Sensors）

无真实硬件时，可通过 mock 传感器驱动进行开发与测试。`pkg/edge/hal/` 提供 `MockSensor` / `MockActuator`，支持单元测试与本地开发。

### 7.1 Mock 传感器驱动

`pkg/edge/hal/` 定义接口：

```go
type Sensor interface {
    Read() float64
    Calibrate()
    Status() Health
}

type Actuator interface {
    On()
    Off()
    SetSpeed(pct)
    Status() Health
}
```

`MockSensor` / `MockActuator` 实现上述接口，返回预设值，无需真实硬件。

### 7.2 使用 Mock 运行边缘控制器

在 `cmd/edge/main.go` 中，通过配置切换真实 HAL 与 Mock HAL。开发环境可设置环境变量或配置项启用 mock 模式：

```powershell
# 启用 mock 传感器模式（示例，具体配置项以代码为准）
$env:EDGE_MOCK = "true"
go run ./cmd/edge/
```

### 7.3 模拟 MQTT 数据

无边缘设备时，可用 `mosquitto_pub` 或 MQTT 客户端向 EMQX 发布模拟传感器数据：

```powershell
# 发布模拟 pH 数据
mosquitto_pub -h localhost -p 1883 -t "pond/v1/frm_001/pnd_001/sensor/water/ph" -m "7.4"

# 发布模拟 DO 数据
mosquitto_pub -h localhost -p 1883 -t "pond/v1/frm_001/pnd_001/sensor/water/do" -m "6.2"
```

> 注意：实际生产环境传感器数据使用 Protobuf 编码（QoS 0）。上述示例为调试用途的简化 JSON/文本格式。

---

## 8. 测试指南

### 8.1 运行单元测试

```powershell
# 使用 Makefile
make test    # go test -race -shuffle=on -count=1 ./...

# 或直接使用 go
go test ./...
```

### 8.2 运行指定包测试

```powershell
# 运行 handler 包测试
go test ./internal/handler/

# 运行 middleware 包测试
go test ./internal/middleware/

# 运行 store 包测试
go test ./pkg/store/
```

### 8.3 测试覆盖

项目采用 TDD 开发模式，核心包均有完整单元测试：

| 包 | 测试文件 | 覆盖内容 |
|----|---------|---------|
| `internal/handler` | handler_test.go, farm_test.go, device_test.go, sensor_test.go, feeding_test.go, alert_test.go, dashboard_test.go, dt_test.go, recommend_test.go, stream_test.go | 各端点 handler 逻辑 |
| `internal/middleware` | auth_test.go | JWT 认证、RBAC、FarmScope |
| `pkg/store` | influx_test.go, postgres_test.go, redis_test.go | 数据库层 |
| `pkg/mqtt` | client_test.go | MQTT 客户端 |
| `pkg/cloud/*` | 各包 *_test.go | AI 引擎、告警、推荐等 |
| `pkg/dt/*` | 各包 *_test.go | 数字孪生 |

### 8.4 无 Docker 时的测试策略

若本机无 Docker，可用 mock 单元测试替代容器集成测试：

- `store.PgxPool` 可用 `pgxmock` 模拟。
- `store.InfluxWriter` 可用 fake 实现模拟。
- `pkg/mqtt` 可用 mock broker 测试。

---

## 9. 静态检查（golangci-lint）

```powershell
# 使用 Makefile
make lint    # golangci-lint run ./...

# 或直接使用
golangci-lint run ./...
```

项目强制零 lint 问题。常见规则：

- `errcheck`：所有错误必须处理。
- `no-shadowing`：禁止变量遮蔽。
- `ST1003`：命名风格。
- `gosec`：安全扫描（G404 禁止非测试代码使用 math/rand，G304 文件操作前必须校验路径）。

---

## 10. 常用开发命令速查

```powershell
# 构建
make build

# 测试
make test

# 静态检查
make lint

# 生成 Protobuf
make proto

# 构建 Docker 镜像
make docker-build

# 启动 Docker Compose
make docker-up

# 清理构建产物
make clean
```

---

## 11. 故障排查

### 11.1 健康检查返回 degraded

```powershell
curl http://localhost:8080/health
```

检查 `checks` 字段中哪个组件为 `down`，并确认对应容器已启动：

```powershell
docker compose ps
docker compose logs <service>
```

### 11.2 PostgreSQL 连接失败

确认 `config/config.yaml` 中 `database.postgres_dsn` 与 `docker-compose.yml` 一致：

```yaml
database:
  postgres_dsn: "postgres://pond:pond@localhost:5432/y-ai-pond?sslmode=disable"
```

### 11.3 MQTT 连接失败

确认 EMQX 容器已启动，且 `config/config.yaml` 中 `mqtt.broker_url` 正确：

```yaml
mqtt:
  broker_url: "tcp://localhost:1883"
```

### 11.4 go mod tidy 报错

确认 `y-ai-agent-base` 位于 `../y-ai-agent-base`，且 replace 指令已添加：

```powershell
go mod edit -replace github.com/anrror/y-ai-agent-base=../y-ai-agent-base
go mod tidy
```

### 11.5 buf generate 报错

确认已安装 buf，且 `proto/` 目录存在：

```powershell
buf --version
buf generate
```

---

## 12. 下一步

- 查看 [API 文档](api.md) 了解全部端点。
- 查看 [系统架构](architecture.md) 了解组件交互。
- 查看 [边缘端部署指南](edge-setup.md) 了解 RK3588 部署。

---

*本文档由 y-ai-pond 项目维护。技术细节以 `.omo/plans/y-ai-pond.md` 与 `doc/` 下文档为准。*
