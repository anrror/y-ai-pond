# y-ai-pond · API 文档（OpenAPI 3.0）

> **版本**: v1.0 | **日期**: 2026-08-11 | **作者**: y-ai-pond 项目组
> **Base URL**: `http://localhost:8080`（生产环境为 HTTPS）
> **协议**: OpenAPI 3.0.3 | **数据格式**: JSON（SSE/WebSocket 除外）

---

## 1. 概述

本文档是 y-ai-pond 云端平台（TIER 2）HTTP API 的完整规范，覆盖 `internal/handler/handler.go` 中注册的全部端点。所有端点均与代码一一对应，未包含任何代码中不存在的虚构端点。

### 1.1 认证方式

平台采用 **JWT（HS256）** 认证，分为两种传递方式：

| 方式 | 适用端点 | 传递位置 |
|------|---------|---------|
| **Bearer Header** | `/api/v1/*` 全部 REST 端点 | `Authorization: Bearer <token>` |
| **Query Token** | SSE（`/api/v1/stream/*`）与 WebSocket（`/ws/dashboard`） | `?token=<token>` |

> **为什么 SSE/WS 用 query token**：浏览器 `EventSource` 和 `WebSocket` 无法自定义请求头，只能通过 URL 查询参数携带 JWT。这些端点不经过中间件链，由 handler 内部手动解析并校验 token。

### 1.2 角色与权限（RBAC）

JWT Claims 包含 `role`（`admin` / `operator` / `viewer`）与 `farm_ids[]`（可访问的农场列表）。

| 角色 | 读操作 | 写操作（POST/PUT/DELETE） |
|------|-------|--------------------------|
| `admin` | ✅ | ✅ |
| `operator` | ✅ | ✅ |
| `viewer` | ✅ | ❌（返回 403） |

- **写保护**：所有 POST/PUT/DELETE 端点均挂载 `middleware.RequireWrite()`，`viewer` 角色调用返回 403。
- **租户隔离**：`middleware.FarmScope` 校验请求中的 `farm_id`（路径参数或查询参数）是否属于当前用户的 `farm_ids[]`，跨农场访问返回 403。

### 1.3 通用响应格式

**错误响应**（统一 JSON 结构）：

```json
{ "error": "错误描述" }
```

| HTTP 状态码 | 含义 |
|------------|------|
| `200` | 成功 |
| `201` | 创建成功 |
| `400` | 请求参数错误（缺参、格式错误、非法窗口等） |
| `401` | 未认证（缺失/无效 token） |
| `403` | 无权限（viewer 写操作、跨农场访问） |
| `404` | 资源不存在 |
| `429` | 触发限流（100 req/min/IP） |
| `500` | 服务器内部错误 |
| `503` | 依赖引擎未就绪（推荐引擎 / 数字孪生引擎） |

### 1.4 分页

列表端点（farms、devices、feeding/logs、alerts）支持统一分页参数：

| 参数 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `page` | int | `1` | 页码（从 1 开始） |
| `page_size` | int | `20` | 每页条数，最大 `100` |

---

## 2. 端点总览

| 方法 | 路径 | 认证 | 写权限 | 说明 |
|------|------|------|--------|------|
| GET | `/health` | 无 | - | 健康检查 |
| GET | `/metrics` | 无 | - | Prometheus 指标 |
| GET | `/api/v1/farms` | Bearer | - | 农场列表 |
| POST | `/api/v1/farms` | Bearer | ✅ | 创建农场 |
| GET | `/api/v1/farms/:id` | Bearer | - | 农场详情 |
| PUT | `/api/v1/farms/:id` | Bearer | ✅ | 更新农场 |
| DELETE | `/api/v1/farms/:id` | Bearer | ✅ | 删除农场 |
| GET | `/api/v1/devices` | Bearer | - | 设备列表 |
| POST | `/api/v1/devices` | Bearer | ✅ | 创建设备 |
| GET | `/api/v1/devices/:id` | Bearer | - | 设备详情 |
| PUT | `/api/v1/devices/:id` | Bearer | ✅ | 更新设备 |
| DELETE | `/api/v1/devices/:id` | Bearer | ✅ | 删除设备 |
| GET | `/api/v1/sensors/latest` | Bearer | - | 池塘最新传感器读数 |
| GET | `/api/v1/sensors/history` | Bearer | - | 池塘传感器历史聚合 |
| GET | `/api/v1/feeding/logs` | Bearer | - | 投喂日志列表 |
| GET | `/api/v1/alerts` | Bearer | - | 告警列表 |
| GET | `/api/v1/dashboard/summary` | Bearer | - | 仪表盘汇总指标 |
| POST | `/api/v1/recommend/feeding` | Bearer | ✅ | AI 投喂建议 |
| GET | `/api/v1/recommend/daily` | Bearer | - | 每日投喂计划 |
| GET | `/api/v1/dt/pond/:id/state` | Bearer | - | 数字孪生虚拟水体状态 |
| GET | `/api/v1/dt/pond/:id/trajectory` | Bearer | - | 数字孪生仿真轨迹 |
| GET | `/api/v1/dt/compare` | Bearer | - | 多场景对比 |
| GET | `/api/v1/dt/pond/:id/anomaly` | Bearer | - | 物理 vs 虚拟偏差检测 |
| GET | `/api/v1/stream/sensors` | Query | - | SSE 实时传感器流 |
| GET | `/api/v1/stream/alerts` | Query | - | SSE 实时告警流 |
| GET | `/ws/dashboard` | Query | - | WebSocket 仪表盘 |

---

## 3. OpenAPI 3.0 规范

```yaml
openapi: 3.0.3
info:
  title: y-ai-pond Cloud API
  description: |
    智慧水产养殖管理平台云端 API。覆盖农场/设备/传感器/投喂/告警/仪表盘/
    AI 推荐/数字孪生/实时推送。所有 /api/v1/* REST 端点使用 Bearer JWT 认证，
    SSE 与 WebSocket 端点使用 ?token= 查询参数认证。
  version: 1.0.0
  contact:
    name: y-ai-pond 项目组
servers:
  - url: http://localhost:8080
    description: 本地开发环境
  - url: https://api.example.com
    description: 生产环境（HTTPS）
tags:
  - name: System
    description: 系统健康与指标
  - name: Farms
    description: 农场管理
  - name: Devices
    description: 设备管理
  - name: Sensors
    description: 传感器数据
  - name: Feeding
    description: 投喂日志
  - name: Alerts
    description: 告警
  - name: Dashboard
    description: 仪表盘汇总
  - name: Recommend
    description: AI 推荐引擎
  - name: DigitalTwin
    description: 数字孪生
  - name: Streaming
    description: 实时推送（SSE / WebSocket）

paths:
  /health:
    get:
      tags: [System]
      summary: 健康检查
      description: 报告各组件（PostgreSQL、InfluxDB、Redis、MQTT）健康状态。
      responses:
        '200':
          description: 健康状态
          content:
            application/json:
              schema:
                type: object
                properties:
                  status:
                    type: string
                    enum: [ok, degraded]
                    description: 全部组件通过为 ok，否则 degraded
                  timestamp:
                    type: string
                    format: date-time
                  uptime_s:
                    type: integer
                    description: 服务运行秒数
                  checks:
                    type: object
                    additionalProperties:
                      type: object
                      properties:
                        status:
                          type: string
                          enum: [ok, down]
                        error:
                          type: string
                        latency:
                          type: string
                          description: 检查耗时，如 "1.2ms"

  /metrics:
    get:
      tags: [System]
      summary: Prometheus 指标
      description: 以 Prometheus text exposition 0.0.4 格式输出运行时与组件健康指标。
      responses:
        '200':
          description: Prometheus 文本指标
          content:
            text/plain:
              schema:
                type: string

  /api/v1/farms:
    get:
      tags: [Farms]
      summary: 农场列表
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/Page'
        - $ref: '#/components/parameters/PageSize'
      responses:
        '200':
          description: 农场列表
          content:
            application/json:
              schema:
                type: object
                required: [farms]
                properties:
                  farms:
                    type: array
                    items:
                      $ref: '#/components/schemas/Farm'
        '401':
          $ref: '#/components/responses/Unauthorized'
    post:
      tags: [Farms]
      summary: 创建农场
      description: 需要写权限（admin/operator）。
      security:
        - bearerAuth: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/FarmRequest'
      responses:
        '201':
          description: 创建成功
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Farm'
        '400':
          $ref: '#/components/responses/BadRequest'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'

  /api/v1/farms/{id}:
    get:
      tags: [Farms]
      summary: 农场详情
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/FarmId'
      responses:
        '200':
          description: 农场详情
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Farm'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'
        '404':
          $ref: '#/components/responses/NotFound'
    put:
      tags: [Farms]
      summary: 更新农场
      description: 需要写权限（admin/operator）。
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/FarmId'
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/FarmRequest'
      responses:
        '200':
          description: 更新成功
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Farm'
        '400':
          $ref: '#/components/responses/BadRequest'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'
        '404':
          $ref: '#/components/responses/NotFound'
    delete:
      tags: [Farms]
      summary: 删除农场
      description: 需要写权限（admin/operator）。
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/FarmId'
      responses:
        '200':
          description: 删除成功
          content:
            application/json:
              schema:
                type: object
                required: [deleted]
                properties:
                  deleted:
                    type: boolean
                    example: true
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'
        '404':
          $ref: '#/components/responses/NotFound'

  /api/v1/devices:
    get:
      tags: [Devices]
      summary: 设备列表
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/Page'
        - $ref: '#/components/parameters/PageSize'
        - name: farm_id
          in: query
          required: false
          schema:
            type: string
          description: 按农场过滤（同时受 FarmScope 租户隔离约束）
      responses:
        '200':
          description: 设备列表
          content:
            application/json:
              schema:
                type: object
                required: [devices]
                properties:
                  devices:
                    type: array
                    items:
                      $ref: '#/components/schemas/Device'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'
    post:
      tags: [Devices]
      summary: 创建设备
      description: 需要写权限（admin/operator）。
      security:
        - bearerAuth: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/DeviceRequest'
      responses:
        '201':
          description: 创建成功
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Device'
        '400':
          $ref: '#/components/responses/BadRequest'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'

  /api/v1/devices/{id}:
    get:
      tags: [Devices]
      summary: 设备详情
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/DeviceId'
      responses:
        '200':
          description: 设备详情
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Device'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '404':
          $ref: '#/components/responses/NotFound'
    put:
      tags: [Devices]
      summary: 更新设备
      description: 需要写权限（admin/operator）。
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/DeviceId'
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/DeviceRequest'
      responses:
        '200':
          description: 更新成功
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Device'
        '400':
          $ref: '#/components/responses/BadRequest'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'
        '404':
          $ref: '#/components/responses/NotFound'
    delete:
      tags: [Devices]
      summary: 删除设备
      description: 需要写权限（admin/operator）。
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/DeviceId'
      responses:
        '200':
          description: 删除成功
          content:
            application/json:
              schema:
                type: object
                required: [deleted]
                properties:
                  deleted:
                    type: boolean
                    example: true
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'
        '404':
          $ref: '#/components/responses/NotFound'

  /api/v1/sensors/latest:
    get:
      tags: [Sensors]
      summary: 池塘最新传感器读数
      description: 返回指定池塘最近 1 小时内每种传感器类型的最新读数。
      security:
        - bearerAuth: []
      parameters:
        - name: pond_id
          in: query
          required: true
          schema:
            type: string
          description: 池塘 ID
      responses:
        '200':
          description: 最新读数数组（按 sensor_type 升序）
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/SensorReading'
        '400':
          $ref: '#/components/responses/BadRequest'
        '401':
          $ref: '#/components/responses/Unauthorized'

  /api/v1/sensors/history:
    get:
      tags: [Sensors]
      summary: 池塘传感器历史聚合
      description: 按时间窗口聚合指定时间范围内的传感器读数（平均值）。
      security:
        - bearerAuth: []
      parameters:
        - name: pond_id
          in: query
          required: true
          schema:
            type: string
          description: 池塘 ID
        - name: from
          in: query
          required: true
          schema:
            type: string
            format: date-time
          description: 起始时间（RFC3339，如 2026-08-11T00:00:00Z）
        - name: to
          in: query
          required: true
          schema:
            type: string
            format: date-time
          description: 结束时间（RFC3339）
        - name: window
          in: query
          required: false
          schema:
            type: string
            enum: [1m, 5m, 1h, 1d]
            default: 5m
          description: 聚合窗口
      responses:
        '200':
          description: 历史聚合结果
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/HistoryResponse'
        '400':
          $ref: '#/components/responses/BadRequest'
        '401':
          $ref: '#/components/responses/Unauthorized'

  /api/v1/feeding/logs:
    get:
      tags: [Feeding]
      summary: 投喂日志列表
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/Page'
        - $ref: '#/components/parameters/PageSize'
        - name: pond_id
          in: query
          required: false
          schema:
            type: string
          description: 按池塘过滤
      responses:
        '200':
          description: 投喂日志列表
          content:
            application/json:
              schema:
                type: object
                required: [logs]
                properties:
                  logs:
                    type: array
                    items:
                      $ref: '#/components/schemas/FeedingLog'
        '401':
          $ref: '#/components/responses/Unauthorized'

  /api/v1/alerts:
    get:
      tags: [Alerts]
      summary: 告警列表
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/Page'
        - $ref: '#/components/parameters/PageSize'
        - name: farm_id
          in: query
          required: false
          schema:
            type: string
          description: 按农场过滤
        - name: pond_id
          in: query
          required: false
          schema:
            type: string
          description: 按池塘过滤
        - name: status
          in: query
          required: false
          schema:
            type: string
            enum: [open, resolved]
          description: 按状态过滤
      responses:
        '200':
          description: 告警列表
          content:
            application/json:
              schema:
                type: object
                required: [alerts]
                properties:
                  alerts:
                    type: array
                    items:
                      $ref: '#/components/schemas/Alert'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'

  /api/v1/dashboard/summary:
    get:
      tags: [Dashboard]
      summary: 仪表盘汇总指标
      description: 返回设备总数、在线设备数、今日投喂量、未处理告警数。
      security:
        - bearerAuth: []
      responses:
        '200':
          description: 汇总指标
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/DashboardSummary'
        '401':
          $ref: '#/components/responses/Unauthorized'

  /api/v1/recommend/feeding:
    post:
      tags: [Recommend]
      summary: AI 投喂建议
      description: |
        基于当前池塘状态生成 AI 投喂建议（仅建议，不自动执行）。
        需要写权限（admin/operator）。引擎未就绪时返回 503。
      security:
        - bearerAuth: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/RecommendFeedingRequest'
      responses:
        '200':
          description: 投喂建议
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/FeedingRecommendation'
        '400':
          $ref: '#/components/responses/BadRequest'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'
        '503':
          $ref: '#/components/responses/EngineUnavailable'

  /api/v1/recommend/daily:
    get:
      tags: [Recommend]
      summary: 每日投喂计划
      description: 生成指定池塘的每日投喂计划。引擎未就绪时返回 503。
      security:
        - bearerAuth: []
      parameters:
        - name: pond_id
          in: query
          required: true
          schema:
            type: string
          description: 池塘 ID
      responses:
        '200':
          description: 每日投喂计划
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/DailyRecommendation'
        '400':
          $ref: '#/components/responses/BadRequest'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '503':
          $ref: '#/components/responses/EngineUnavailable'

  /api/v1/dt/pond/{id}/state:
    get:
      tags: [DigitalTwin]
      summary: 数字孪生虚拟水体状态
      description: 返回指定池塘的当前虚拟水体状态。引擎未就绪时返回 503。
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/PondId'
      responses:
        '200':
          description: 虚拟水体状态
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/VirtualState'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '503':
          $ref: '#/components/responses/EngineUnavailable'

  /api/v1/dt/pond/{id}/trajectory:
    get:
      tags: [DigitalTwin]
      summary: 数字孪生仿真轨迹
      description: 返回指定池塘在指定场景下的分页仿真轨迹。
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/PondId'
        - name: scenario
          in: query
          required: true
          schema:
            type: string
            enum: [heatwave, storm_flood, cold_snap]
          description: 极端天气场景
        - name: offset
          in: query
          required: false
          schema:
            type: integer
            default: 0
          description: 轨迹偏移
        - name: limit
          in: query
          required: false
          schema:
            type: integer
          description: 返回点数上限
      responses:
        '200':
          description: 仿真轨迹
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Trajectory'
        '400':
          $ref: '#/components/responses/BadRequest'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '503':
          $ref: '#/components/responses/EngineUnavailable'

  /api/v1/dt/compare:
    get:
      tags: [DigitalTwin]
      summary: 多场景对比
      description: 并行运行多个场景并返回并排摘要。
      security:
        - bearerAuth: []
      parameters:
        - name: scenarios
          in: query
          required: true
          schema:
            type: string
          description: 逗号分隔的场景名，如 heatwave,storm_flood
      responses:
        '200':
          description: 场景对比结果
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/CompareResult'
        '400':
          $ref: '#/components/responses/BadRequest'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '503':
          $ref: '#/components/responses/EngineUnavailable'

  /api/v1/dt/pond/{id}/anomaly:
    get:
      tags: [DigitalTwin]
      summary: 物理 vs 虚拟偏差检测
      description: 将物理传感器读数与虚拟基线对比，报告超过阈值的偏差。
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/PondId'
        - name: do_mg_l
          in: query
          required: false
          schema:
            type: number
          description: 物理溶解氧读数（mg/L）
        - name: temp_c
          in: query
          required: false
          schema:
            type: number
          description: 物理水温读数（°C）
        - name: turbidity_ntu
          in: query
          required: false
          schema:
            type: number
          description: 物理浊度读数（NTU）
        - name: nh3_mg_l
          in: query
          required: false
          schema:
            type: number
          description: 物理氨氮读数（mg/L）
      responses:
        '200':
          description: 偏差检测报告
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/AnomalyReport'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '503':
          $ref: '#/components/responses/EngineUnavailable'

  /api/v1/stream/sensors:
    get:
      tags: [Streaming]
      summary: SSE 实时传感器流
      description: |
        通过 Server-Sent Events 实时推送指定池塘的传感器数据。
        认证使用 ?token= 查询参数（浏览器 EventSource 无法设置请求头）。
      security:
        - queryTokenAuth: []
      parameters:
        - name: token
          in: query
          required: true
          schema:
            type: string
          description: JWT 访问令牌
        - name: pond_id
          in: query
          required: true
          schema:
            type: string
          description: 池塘 ID
        - name: farm_id
          in: query
          required: false
          schema:
            type: string
          description: 农场 ID（可选；缺省时取用户第一个授权农场）
      responses:
        '200':
          description: SSE 事件流（text/event-stream）
          content:
            text/event-stream:
              schema:
                type: string
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'

  /api/v1/stream/alerts:
    get:
      tags: [Streaming]
      summary: SSE 实时告警流
      description: |
        通过 Server-Sent Events 实时推送当前用户所有授权农场的告警事件。
        认证使用 ?token= 查询参数。
      security:
        - queryTokenAuth: []
      parameters:
        - name: token
          in: query
          required: true
          schema:
            type: string
          description: JWT 访问令牌
      responses:
        '200':
          description: SSE 事件流（text/event-stream）
          content:
            text/event-stream:
              schema:
                type: string
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'

  /ws/dashboard:
    get:
      tags: [Streaming]
      summary: WebSocket 仪表盘
      description: |
        升级为 WebSocket，订阅当前用户所有授权农场的仪表盘房间。
        客户端可发送 JSON 控制命令。认证使用 ?token= 查询参数。
      security:
        - queryTokenAuth: []
      parameters:
        - name: token
          in: query
          required: true
          schema:
            type: string
          description: JWT 访问令牌
      responses:
        '101':
          description: WebSocket 升级成功
        '401':
          $ref: '#/components/responses/Unauthorized'
        '403':
          $ref: '#/components/responses/Forbidden'

components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT
      description: Authorization: Bearer <token>
    queryTokenAuth:
      type: apiKey
      in: query
      name: token
      description: SSE/WebSocket 端点通过 ?token= 传递 JWT

  parameters:
    Page:
      name: page
      in: query
      required: false
      schema:
        type: integer
        default: 1
      description: 页码（从 1 开始）
    PageSize:
      name: page_size
      in: query
      required: false
      schema:
        type: integer
        default: 20
        maximum: 100
      description: 每页条数（最大 100）
    FarmId:
      name: id
      in: path
      required: true
      schema:
        type: string
      description: 农场 ID
    DeviceId:
      name: id
      in: path
      required: true
      schema:
        type: string
      description: 设备 ID
    PondId:
      name: id
      in: path
      required: true
      schema:
        type: string
      description: 池塘 ID

  responses:
    BadRequest:
      description: 请求参数错误
      content:
        application/json:
          schema:
            type: object
            required: [error]
            properties:
              error:
                type: string
    Unauthorized:
      description: 未认证（缺失/无效 token）
      content:
        application/json:
          schema:
            type: object
            required: [error]
            properties:
              error:
                type: string
    Forbidden:
      description: 无权限（viewer 写操作或跨农场访问）
      content:
        application/json:
          schema:
            type: object
            required: [error]
            properties:
              error:
                type: string
    NotFound:
      description: 资源不存在
      content:
        application/json:
          schema:
            type: object
            required: [error]
            properties:
              error:
                type: string
    EngineUnavailable:
      description: 依赖引擎未就绪
      content:
        application/json:
          schema:
            type: object
            required: [error]
            properties:
              error:
                type: string

  schemas:
    Farm:
      type: object
      required: [id, name, created_at]
      properties:
        id:
          type: string
        name:
          type: string
        location:
          type: string
        area_m2:
          type: number
          format: double
        species:
          type: string
        created_at:
          type: string
          format: date-time

    FarmRequest:
      type: object
      required: [name]
      properties:
        name:
          type: string
          description: 农场名称（必填）
        location:
          type: string
          description: 地理位置
        area_m2:
          type: number
          format: double
          description: 面积（平方米）
        species:
          type: string
          description: 养殖物种

    Device:
      type: object
      required: [id, farm_id, type]
      properties:
        id:
          type: string
        farm_id:
          type: string
        pond_id:
          type: string
          description: 关联池塘（可为空字符串）
        type:
          type: string
          description: 设备类型
        status:
          type: string
          description: 设备状态（如 online/offline）
        firmware_version:
          type: string
        last_heartbeat:
          type: string
          format: date-time

    DeviceRequest:
      type: object
      required: [farm_id, type]
      properties:
        id:
          type: string
          description: 设备 ID（可选，缺省由服务端生成）
        farm_id:
          type: string
          description: 所属农场（必填）
        pond_id:
          type: string
          description: 关联池塘
        type:
          type: string
          description: 设备类型（必填）
        status:
          type: string
        firmware_version:
          type: string
        last_heartbeat:
          type: string
          format: date-time

    SensorReading:
      type: object
      required: [farm_id, pond_id, sensor_type, value, timestamp]
      properties:
        farm_id:
          type: string
        pond_id:
          type: string
        sensor_type:
          type: string
          description: 传感器类型（ph/do/temp/nh3/turbidity/water_level 等）
        value:
          type: number
          format: double
        timestamp:
          type: string
          format: date-time

    HistoryResponse:
      type: object
      required: [pond_id, window, points]
      properties:
        pond_id:
          type: string
        window:
          type: string
          description: 聚合窗口（1m/5m/1h/1d）
        points:
          type: array
          items:
            type: object
            required: [timestamp, values]
            properties:
              timestamp:
                type: string
                format: date-time
              values:
                type: object
                additionalProperties:
                  type: number
                  format: double
                description: 各传感器字段在该窗口的平均值

    FeedingLog:
      type: object
      required: [id, pond_id, speed, duration, created_at]
      properties:
        id:
          type: string
        pond_id:
          type: string
        speed:
          type: number
          format: double
          description: 投喂电机转速
        duration:
          type: integer
          description: 投喂时长（毫秒）
        decision_json:
          type: object
          description: 投喂决策详情（模糊 PID 输入/输出等）
        created_at:
          type: string
          format: date-time

    Alert:
      type: object
      required: [id, farm_id, level, type, message, status, created_at]
      properties:
        id:
          type: string
        farm_id:
          type: string
        pond_id:
          type: string
          nullable: true
        level:
          type: string
          enum: [CRITICAL, WARNING, INFO]
        type:
          type: string
        message:
          type: string
        status:
          type: string
          enum: [open, resolved]
        created_at:
          type: string
          format: date-time
        resolved_at:
          type: string
          format: date-time
          nullable: true

    DashboardSummary:
      type: object
      required: [total_devices, online_devices, today_feeding_amount, open_alerts]
      properties:
        total_devices:
          type: integer
          format: int64
        online_devices:
          type: integer
          format: int64
        today_feeding_amount:
          type: number
          format: double
          description: 今日投喂量（speed × duration 之和）
        open_alerts:
          type: integer
          format: int64

    RecommendFeedingRequest:
      type: object
      required: [pond_id]
      properties:
        pond_id:
          type: string
          description: 池塘 ID（必填）
        do_mg_l:
          type: number
          format: double
          description: 溶解氧（mg/L）
        temp_c:
          type: number
          format: double
          description: 水温（°C）
        nh3_mg_l:
          type: number
          format: double
          description: 氨氮（mg/L）
        fish_weight_g:
          type: number
          format: double
          description: 平均鱼重（g）
        fcr:
          type: number
          format: double
          description: 饲料转化率
        species:
          type: string
          description: 鱼种
        stocking_density:
          type: number
          format: double
          description: 放养密度（尾/m³）

    FeedingRecommendation:
      type: object
      required: [pond_id, feeding_rate, risk_level, confidence, actions, reason, requires_manual_review]
      properties:
        pond_id:
          type: string
        feeding_rate:
          type: number
          format: double
          description: 推荐投喂比例 [0, 1]
        expected_growth_g_per_day:
          type: number
          format: double
          description: 预计日增重（g）
        risk_level:
          type: string
          description: 风险等级
        confidence:
          type: number
          format: double
          description: 置信度 [0, 1]，低于 0.7 触发人工确认
        actions:
          type: array
          items:
            type: object
          description: 建议动作列表（按优先级排序）
        reason:
          type: string
          description: 建议逻辑说明
        requires_manual_review:
          type: boolean
          description: 置信度 < 0.7 时为 true

    DailyRecommendation:
      type: object
      required: [date, pond_id, feedings, summary]
      properties:
        date:
          type: string
          format: date
          description: 日期（YYYY-MM-DD）
        pond_id:
          type: string
        feedings:
          type: array
          items:
            $ref: '#/components/schemas/FeedingRecommendation'
        summary:
          type: string
          description: 每日概览

    VirtualState:
      type: object
      required: [pond_id, temperature_c, do_mg_l, turbidity_ntu, nh3_mg_l, updated_at]
      properties:
        pond_id:
          type: string
        temperature_c:
          type: number
          format: double
        do_mg_l:
          type: number
          format: double
        turbidity_ntu:
          type: number
          format: double
        nh3_mg_l:
          type: number
          format: double
        updated_at:
          type: string
          format: date-time

    Trajectory:
      type: object
      required: [pond_id, scenario, points, total]
      properties:
        pond_id:
          type: string
        scenario:
          type: string
        points:
          type: array
          items:
            type: object
            required: [step, temperature_c, do_mg_l, turbidity_ntu, nh3_mg_l]
            properties:
              step:
                type: integer
              temperature_c:
                type: number
                format: double
              do_mg_l:
                type: number
                format: double
              turbidity_ntu:
                type: number
                format: double
              nh3_mg_l:
                type: number
                format: double
        total:
          type: integer
          description: 轨迹总点数

    CompareResult:
      type: object
      required: [scenario, final_do_mg_l, final_temp_c, risk_level, feed_rate_adjust_pct]
      properties:
        scenario:
          type: string
        final_do_mg_l:
          type: number
          format: double
        final_temp_c:
          type: number
          format: double
        risk_level:
          type: string
        feed_rate_adjust_pct:
          type: integer
          description: 投喂率调整百分比

    AnomalyReport:
      type: object
      required: [pond_id, status, deviations, max_deviation]
      properties:
        pond_id:
          type: string
        status:
          type: string
          enum: [NORMAL, ANOMALY_DETECTED]
        deviations:
          type: array
          items:
            type: object
            required: [field, physical, virtual, deviation, threshold]
            properties:
              field:
                type: string
              physical:
                type: number
                format: double
              virtual:
                type: number
                format: double
              deviation:
                type: number
                format: double
              threshold:
                type: number
                format: double
        max_deviation:
          type: number
          format: double
```

---

## 4. curl 示例

以下示例假设服务运行在 `http://localhost:8080`，并已获取 JWT 令牌（`$TOKEN`）。

### 4.1 健康检查（无需认证）

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

### 4.2 Prometheus 指标（无需认证）

```bash
curl http://localhost:8080/metrics
```

### 4.3 农场管理

**创建农场**（需要写权限）：

```bash
curl -X POST http://localhost:8080/api/v1/farms \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"示范养殖基地","location":"广东湛江","area_m2":50000,"species":"罗非鱼"}'
```

**响应**（201）：

```json
{
  "id": "frm_001",
  "name": "示范养殖基地",
  "location": "广东湛江",
  "area_m2": 50000,
  "species": "罗非鱼",
  "created_at": "2026-08-11T08:00:00Z"
}
```

**农场列表**：

```bash
curl "http://localhost:8080/api/v1/farms?page=1&page_size=20" \
  -H "Authorization: Bearer $TOKEN"
```

**农场详情**：

```bash
curl http://localhost:8080/api/v1/farms/frm_001 \
  -H "Authorization: Bearer $TOKEN"
```

**更新农场**：

```bash
curl -X PUT http://localhost:8080/api/v1/farms/frm_001 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"示范养殖基地","location":"广东湛江","area_m2":60000,"species":"罗非鱼"}'
```

**删除农场**：

```bash
curl -X DELETE http://localhost:8080/api/v1/farms/frm_001 \
  -H "Authorization: Bearer $TOKEN"
```

**响应**：

```json
{ "deleted": true }
```

### 4.4 设备管理

**创建设备**：

```bash
curl -X POST http://localhost:8080/api/v1/devices \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"farm_id":"frm_001","pond_id":"pnd_001","type":"edge_controller","status":"online","firmware_version":"1.0.0"}'
```

**设备列表**（按农场过滤）：

```bash
curl "http://localhost:8080/api/v1/devices?farm_id=frm_001" \
  -H "Authorization: Bearer $TOKEN"
```

**设备详情**：

```bash
curl http://localhost:8080/api/v1/devices/dev_001 \
  -H "Authorization: Bearer $TOKEN"
```

**更新设备**：

```bash
curl -X PUT http://localhost:8080/api/v1/devices/dev_001 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"farm_id":"frm_001","pond_id":"pnd_001","type":"edge_controller","status":"offline","firmware_version":"1.0.1"}'
```

**删除设备**：

```bash
curl -X DELETE http://localhost:8080/api/v1/devices/dev_001 \
  -H "Authorization: Bearer $TOKEN"
```

### 4.5 传感器数据

**最新读数**：

```bash
curl "http://localhost:8080/api/v1/sensors/latest?pond_id=pnd_001" \
  -H "Authorization: Bearer $TOKEN"
```

**响应**：

```json
[
  { "farm_id": "frm_001", "pond_id": "pnd_001", "sensor_type": "do", "value": 6.2, "timestamp": "2026-08-11T08:00:00Z" },
  { "farm_id": "frm_001", "pond_id": "pnd_001", "sensor_type": "ph", "value": 7.4, "timestamp": "2026-08-11T08:00:00Z" },
  { "farm_id": "frm_001", "pond_id": "pnd_001", "sensor_type": "temp", "value": 26.5, "timestamp": "2026-08-11T08:00:00Z" }
]
```

**历史聚合**：

```bash
curl "http://localhost:8080/api/v1/sensors/history?pond_id=pnd_001&from=2026-08-11T00:00:00Z&to=2026-08-11T08:00:00Z&window=1h" \
  -H "Authorization: Bearer $TOKEN"
```

**响应**：

```json
{
  "pond_id": "pnd_001",
  "window": "1h",
  "points": [
    { "timestamp": "2026-08-11T00:00:00Z", "values": { "do": 6.1, "ph": 7.4, "temp": 26.3 } },
    { "timestamp": "2026-08-11T01:00:00Z", "values": { "do": 6.3, "ph": 7.5, "temp": 26.6 } }
  ]
}
```

### 4.6 投喂日志

```bash
curl "http://localhost:8080/api/v1/feeding/logs?pond_id=pnd_001" \
  -H "Authorization: Bearer $TOKEN"
```

### 4.7 告警列表

```bash
curl "http://localhost:8080/api/v1/alerts?farm_id=frm_001&status=open" \
  -H "Authorization: Bearer $TOKEN"
```

### 4.8 仪表盘汇总

```bash
curl http://localhost:8080/api/v1/dashboard/summary \
  -H "Authorization: Bearer $TOKEN"
```

**响应**：

```json
{
  "total_devices": 12,
  "online_devices": 10,
  "today_feeding_amount": 156.5,
  "open_alerts": 2
}
```

### 4.9 AI 投喂建议

```bash
curl -X POST http://localhost:8080/api/v1/recommend/feeding \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"pond_id":"pnd_001","do_mg_l":6.2,"temp_c":26.5,"nh3_mg_l":0.1,"fish_weight_g":350,"fcr":1.4,"species":"罗非鱼","stocking_density":20}'
```

**响应**：

```json
{
  "pond_id": "pnd_001",
  "feeding_rate": 0.8,
  "expected_growth_g_per_day": 3.2,
  "risk_level": "LOW",
  "confidence": 0.85,
  "actions": [],
  "reason": "水质正常，建议按 80% 投喂率投喂",
  "requires_manual_review": false
}
```

**每日投喂计划**：

```bash
curl "http://localhost:8080/api/v1/recommend/daily?pond_id=pnd_001" \
  -H "Authorization: Bearer $TOKEN"
```

### 4.10 数字孪生

**虚拟水体状态**：

```bash
curl http://localhost:8080/api/v1/dt/pond/pnd_001/state \
  -H "Authorization: Bearer $TOKEN"
```

**仿真轨迹**：

```bash
curl "http://localhost:8080/api/v1/dt/pond/pnd_001/trajectory?scenario=heatwave&offset=0&limit=100" \
  -H "Authorization: Bearer $TOKEN"
```

**多场景对比**：

```bash
curl "http://localhost:8080/api/v1/dt/compare?scenarios=heatwave,storm_flood" \
  -H "Authorization: Bearer $TOKEN"
```

**偏差检测**：

```bash
curl "http://localhost:8080/api/v1/dt/pond/pnd_001/anomaly?do_mg_l=5.0&temp_c=28.0&turbidity_ntu=25&nh3_mg_l=0.3" \
  -H "Authorization: Bearer $TOKEN"
```

### 4.11 实时推送（SSE / WebSocket）

**SSE 传感器流**（使用 `?token=` 认证）：

```bash
curl -N "http://localhost:8080/api/v1/stream/sensors?token=$TOKEN&pond_id=pnd_001"
```

**SSE 告警流**：

```bash
curl -N "http://localhost:8080/api/v1/stream/alerts?token=$TOKEN"
```

**WebSocket 仪表盘**（使用 `wscat` 或浏览器）：

```bash
wscat -c "ws://localhost:8080/ws/dashboard?token=$TOKEN"
```

---

## 5. 认证令牌获取

当前版本未暴露 HTTP 登录端点（`auth.TokenPair` 结构已定义，但未注册路由）。JWT 令牌由 `pkg/auth.AuthService` 签发，生产环境通过内部用户管理流程发放。开发环境可在测试代码或内部工具中调用 `auth.AuthService` 生成令牌。

---

*本文档由 y-ai-pond 项目维护。端点与 `internal/handler/handler.go` 的 `RegisterRoutes` 一一对应。*
