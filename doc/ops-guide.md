# y-ai-pond · 运维指南（Operations Guide）

> **版本**: v1.0 | **日期**: 2026-08-11 | **作者**: y-ai-pond 项目组
> **适用对象**: 系统运维人员、值班工程师
> **配套文档**: [用户手册](user-guide.md) | [API 文档](api.md) | [开发者指南](developer-guide.md) | [边缘端部署指南](edge-setup.md)

---

## 1. 运维概览

y-ai-pond 生产环境由以下组件组成：

| 组件 | 用途 | 端口 |
|------|------|------|
| **Go Server** | 云端 HTTP API + MQTT Gateway | 8080 |
| **EMQX** | MQTT Broker（设备数据接入） | 1883 (MQTT), 8083 (WS), 18083 (Dashboard) |
| **InfluxDB 3** | 时序传感器数据 | 8086, 8181 |
| **PostgreSQL + TimescaleDB** | 业务数据（农场/设备/投喂/告警/模型） | 5432 |
| **Redis** | 缓存 / 告警去重 / 消息队列 | 6379 |
| **Gotenberg**（可选） | PDF 报表生成 | 3000 |

**部署方式**：
- **单机**：Docker Compose（`docker-compose.yml` 一键启动全部服务）。
- **多基地**：Kubernetes（`k8s/` Helm Chart，支持 HPA 自动伸缩）。

**核心运维原则**：
1. **数据是资产**：传感器时序数据与业务数据必须定期备份。
2. **监控先行**：先建立监控，再谈扩容与故障处理。
3. **安全互锁不可覆盖**：边缘端硬件安全互锁（DO < 4.0 强制增氧等）是硬件级兜底，云端无法覆盖，运维时不要试图绕过。

---

## 2. 监控指标

### 2.1 服务健康检查

云端服务提供健康检查端点（无需认证）：

```bash
curl http://localhost:8080/health
```

**响应**：

```json
{
  "status": "ok",
  "timestamp": "2026-08-11T08:00:00Z",
  "uptime_s": 3600,
  "checks": {
    "influxdb": { "status": "ok", "latency": "1.2ms" },
    "postgres": { "status": "ok", "latency": "0.8ms" },
    "redis": { "status": "ok", "latency": "0.5ms" }
  }
}
```

- `status: ok`：全部组件通过。
- `status: degraded`：至少一个组件异常，检查 `checks` 中哪个为 `down`。

> **建议**：将 `/health` 接入监控系统（如 Prometheus + Alertmanager），每 30 秒探测一次，`degraded` 即告警。

### 2.2 Prometheus 指标

云端服务暴露 Prometheus 格式指标（无需认证）：

```bash
curl http://localhost:8080/metrics
```

指标包含运行时与组件健康信息，可接入 Prometheus 采集并配置 Grafana 看板。

### 2.3 资源监控

| 组件 | 关键指标 | 告警阈值建议 |
|------|---------|-------------|
| Go Server | CPU / 内存 / goroutine | CPU > 70% 持续 5min，内存 > 80% |
| EMQX | 连接数 / 消息吞吐 / 订阅数 | 连接数接近上限 |
| InfluxDB 3 | 写入吞吐 / 查询延迟 / 磁盘 | 写入 > 10K pts/s，查询 > 50ms |
| PostgreSQL | 连接数 / 慢查询 / 磁盘 | 连接数 > 80%，慢查询 > 1s |
| Redis | 内存 / 命中率 / 连接数 | 内存 > 80% |
| 磁盘 | 各数据卷使用率 | > 80% |

### 2.4 业务指标

| 指标 | 含义 | 关注点 |
|------|------|--------|
| 在线设备数 | 当前心跳正常的设备 | 与设备总数对比，掉线率 |
| 今日投喂量 | 今日累计投喂量 | 异常突增/突减 |
| 未处理告警数 | 未处理告警 | 持续增长需排查 |
| 传感器数据完整率 | 接收点数 vs 理论点数 | 应 > 99% |

---

## 3. 日志查看

### 3.1 日志格式（slog）

云端服务使用 Go 标准库 `log/slog` 输出结构化日志。日志为键值对格式，便于机器解析与检索。

**日志级别**：

| 级别 | 用途 |
|------|------|
| `DEBUG` | 调试信息（生产环境通常关闭） |
| `INFO` | 常规运行信息 |
| `WARN` | 警告（如组件降级、重连） |
| `ERROR` | 错误（如数据库连接失败） |

### 3.2 查看容器日志

Docker Compose 部署下，使用 `docker compose logs` 查看各服务日志：

```bash
# 查看云端服务日志
docker compose logs server

# 实时跟踪
docker compose logs -f server

# 查看最近 100 行
docker compose logs --tail 100 server

# 查看其他组件
docker compose logs emqx
docker compose logs influxdb
docker compose logs postgres
docker compose logs redis
```

### 3.3 集中收集

生产环境建议将日志集中收集，便于检索与告警：

- **方案一（轻量）**：使用 `docker compose logs` + 日志轮转（`logrotate`）。
- **方案二（推荐）**：接入 ELK / Loki + Grafana，将各容器 stdout 采集到集中存储，按服务名与级别检索。

> **建议**：至少保留 30 天日志。ERROR 级别日志应接入告警。

---

## 4. 备份与恢复

### 4.1 备份策略总览

| 数据 | 存储 | 备份方式 | 频率建议 |
|------|------|---------|---------|
| 业务数据 | PostgreSQL | `pg_dump` | 每日全量 + 实时 WAL |
| 时序数据 | InfluxDB 3 | 官方备份工具 | 每日 |
| 缓存/队列 | Redis | `redis-cli SAVE` / `BGSAVE` | 每日（可容忍丢失） |
| 配置文件 | 文件系统 | 版本控制 / 手动拷贝 | 变更时 |
| AI 模型 | 文件系统（modelmgr） | 拷贝模型目录 | 模型变更时 |

### 4.2 PostgreSQL 备份与恢复

**备份**：

```bash
# 全量备份
docker compose exec postgres pg_dump -U pond -d y-ai-pond -F c -f /backup/y-ai-pond-$(date +%Y%m%d).dump

# 将备份文件拷贝到宿主机
docker compose cp postgres:/backup/y-ai-pond-20260811.dump ./backup/
```

**恢复**：

```bash
# 将备份文件拷入容器
docker compose cp ./backup/y-ai-pond-20260811.dump postgres:/backup/

# 恢复（先删除旧库或使用新库）
docker compose exec postgres pg_restore -U pond -d y-ai-pond --clean --if-exists /backup/y-ai-pond-20260811.dump
```

> **注意**：恢复前请确认目标库为空或已备份，`--clean` 会先删除现有对象。

### 4.3 InfluxDB 3 备份与恢复

InfluxDB 3 提供官方备份工具。请参考 InfluxDB 3 官方文档执行备份与恢复。

**通用原则**：
- 备份时序数据到独立存储（对象存储或异地磁盘）。
- 保留策略：90 天热数据 / 365 天冷数据，备份需覆盖完整保留周期。

### 4.4 Redis 备份与恢复

**备份**：

```bash
# 触发持久化
docker compose exec redis redis-cli SAVE

# 拷贝 RDB 文件
docker compose cp redis:/data/dump.rdb ./backup/redis-$(date +%Y%m%d).rdb
```

**恢复**：

```bash
# 将 RDB 文件拷入容器
docker compose cp ./backup/redis-20260811.rdb redis:/data/dump.rdb

# 重启 Redis 加载
docker compose restart redis
```

> **说明**：Redis 主要缓存设备影子、告警去重、投喂决策，丢失后系统会自动重建，可容忍短暂丢失。

### 4.5 配置文件备份

配置文件（`config/config.yaml` 等）建议纳入版本控制（Git），或定期拷贝到独立位置。恢复时直接覆盖并重启服务即可。

### 4.6 AI 模型备份

模型注册表（`pkg/cloud/modelmgr`）将模型存储在文件系统（默认 `models/` 目录）。备份该目录即可：

```bash
# 备份模型目录
cp -r models/ ./backup/models-$(date +%Y%m%d)/
```

> **注意**：模型是重要资产，建议与数据库分开备份，并保留历史版本以便回滚。

---

## 5. 扩容指南

### 5.1 垂直扩容（单机升级）

适用于单机 Docker Compose 部署，通过升级硬件资源提升性能：

| 瓶颈 | 扩容方式 |
|------|---------|
| CPU / 内存不足 | 升级服务器配置 |
| 磁盘不足 | 扩容数据卷，注意 InfluxDB 保留策略 |
| 网络带宽 | 升级带宽（边缘设备数据上传） |

**垂直扩容步骤**：
1. 备份数据（见第 4 节）。
2. 停机升级硬件。
3. 恢复数据，启动服务。
4. 验证 `/health` 与业务功能。

### 5.2 水平扩容（多实例）

适用于 Kubernetes 部署，通过增加实例提升吞吐。

**云端服务（无状态）**：
- 云端 Go Server 是无状态的，可水平扩展。
- 使用 HPA 自动伸缩：CPU > 70% 时自动扩容。

```bash
# 查看 HPA 状态
kubectl get hpa

# 手动扩容
kubectl scale deployment server --replicas=3
```

**有状态组件（不可随意水平扩展）**：
- **PostgreSQL**：主从复制或使用托管数据库服务。
- **InfluxDB 3**：按官方集群方案扩展。
- **Redis**：主从 + 哨兵或集群模式。
- **EMQX**：支持集群，多节点共享订阅。

> **注意**：水平扩容前请确认有状态组件已就绪，否则会导致数据不一致。

### 5.3 扩容检查清单

扩容后务必验证：
1. `/health` 返回 `ok`。
2. 设备能正常连接 MQTT 并上报数据。
3. 传感器数据能写入 InfluxDB。
4. 告警引擎正常工作。
5. 投喂指令能下发到边缘端。

---

## 6. 故障排查

### 6.1 服务启动失败

**现象**：`docker compose up -d` 后服务未启动或反复重启。

**排查步骤**：
1. 查看服务状态：`docker compose ps`。
2. 查看日志：`docker compose logs <service>`。
3. 检查依赖服务是否就绪（`depends_on` 保证启动顺序）。
4. 检查配置：`config/config.yaml` 中的数据库连接、MQTT 地址是否正确。

**常见原因**：
- PostgreSQL / InfluxDB / Redis 未启动或连接失败。
- 配置中的 DSN / URL 错误。
- 端口被占用。

### 6.2 MQTT 断连

**现象**：设备频繁掉线，或云端收不到设备数据。

**排查步骤**：
1. 检查 EMQX 状态：`docker compose ps emqx`。
2. 查看 EMQX 日志：`docker compose logs emqx`。
3. 检查网络连通性：`nc -zv <cloud-ip> 1883`。
4. 检查边缘端配置：`broker_url` 地址与端口。

**说明**：
- 边缘端 MQTT 客户端采用指数退避重连（1s → 30s），KeepAlive 20s，SessionExpiry 3600s。
- 弱网环境下断连是常态，边缘端会自动重连并补传数据（SQLite 缓冲 7 天）。
- 若持续断连，检查网络稳定性与 EMQX 连接数上限。

### 6.3 数据库连接失败

**现象**：云端服务报数据库连接错误，`/health` 返回 `degraded`。

**排查步骤**：
1. 确认数据库容器已启动：`docker compose ps postgres`。
2. 确认连接串正确：`config/config.yaml` 中 `database.postgres_dsn`。
3. 测试连接：`docker compose exec postgres psql -U pond -d y-ai-pond -c "SELECT 1"`。
4. 检查连接数是否耗尽：`SELECT count(*) FROM pg_stat_activity;`。

**常见原因**：
- 连接串错误（用户名/密码/库名）。
- 连接数耗尽（默认 100，可调大）。
- 磁盘满导致数据库无法写入。

### 6.4 告警风暴

**现象**：短时间内收到大量告警，告警列表刷屏。

**排查步骤**：
1. 确认系统已做告警去重（Redis，60 秒窗口内同类型只发一次）。
2. 检查 Redis 是否可用（Redis 不可用时降级为内存去重，仍能工作）。
3. 优先排查 CRITICAL 级别告警。
4. 检查是否传感器异常导致持续误报（如探头断线、校准漂移）。

**处理建议**：
- 若为传感器异常，先修复/校准传感器。
- 若为真实水质恶化，按告警处理流程采取增氧、调整投喂等措施。
- 若为系统误报，检查告警阈值配置。

### 6.5 边缘端故障

边缘端故障排查详见 [边缘端部署指南](edge-setup.md) 第 8 节，常见问题：

| 现象 | 排查 |
|------|------|
| 摄像头无图像 | 检查 MIPI 接线、驱动、设备节点 |
| NPU 推理失败 | 确认 RKNN Runtime 已装、模型为 RKNN 格式 |
| 传感器读数异常 | 检查接线、供电、重新校准 |
| 设备离线 | 检查供电、网络、心跳 |

---

## 7. 常见问题（FAQ）

### 7.1 如何确认系统健康？

```bash
curl http://localhost:8080/health
```

返回 `status: ok` 表示全部组件正常。

### 7.2 如何查看实时日志？

```bash
docker compose logs -f server
```

### 7.3 如何备份数据库？

见第 4.2 节，使用 `pg_dump` 每日全量备份。

### 7.4 如何恢复误删的数据？

使用备份文件执行 `pg_restore`（见第 4.2 节）。恢复前请确认备份时间点。

### 7.5 设备频繁掉线怎么办？

- 检查网络稳定性与 EMQX 连接数。
- 边缘端会自动重连并补传数据，无需人工干预。
- 若持续掉线，检查边缘端供电与网络设备。

### 7.6 磁盘空间不足怎么办？

- 检查 InfluxDB 保留策略（90 天热 / 365 天冷），清理过期数据。
- 清理 PostgreSQL 日志与 WAL。
- 扩容数据卷。

### 7.7 如何升级系统？

1. 备份数据（见第 4 节）。
2. 拉取新版本镜像：`docker compose pull`。
3. 重启服务：`docker compose up -d`。
4. 验证 `/health` 与业务功能。

### 7.8 如何回滚 AI 模型？

模型注册表支持回滚（见 [模型训练指南](model-training-guide.md) 第 7 节）。回滚会绕过激活策略，将指定版本重新激活。

---

*本文档由 y-ai-pond 项目维护。技术细节以 `.omo/plans/y-ai-pond.md` 与 `doc/` 下文档为准。*
