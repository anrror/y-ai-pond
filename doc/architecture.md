# y-ai-pond · 系统架构图（System Architecture）

> **版本**: v1.0 | **日期**: 2026-08-11 | **作者**: y-ai-pond 项目组
> **技术栈**: Go 1.25 + y-ai-agent-base + InfluxDB 3 + PostgreSQL + Redis + EMQX

---

## 1. 三态架构总览

y-ai-pond 采用**三态架构**：边缘端（TIER 1）负责毫秒级实时控制，云端平台（TIER 2）负责数据分析与 AI 策略优化，数字孪生层（TIER 3）负责大型基地水体仿真与极端天气推演。

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        TIER 3: 数字孪生层 (Digital Twin)                      │
│                    【大型基地高阶应用 — 可选部署】                               │
│                                                                             │
│  ┌──────────────────┐  ┌──────────────────────┐  ┌──────────────────────┐  │
│  │ ST-GNN 水体建模   │  │ 物理信息融合模拟器    │  │ 策略推演引擎           │  │
│  │ 架构: D-TGCN      │  │ PI-GNN: 质量守恒约束  │  │ 输入: SSP245/370/585 │  │
│  │ 输入: 多站点水质   │  │ 水温: 能量平衡模型    │  │      极端天气场景     │  │
│  │ 输出: 多步水质预测 │  │ 生长: FishMet生物能学 │  │ 输出: 最优投喂策略     │  │
│  │ 推理: Go onnxer   │  │                      │  │      风险评估报告     │  │
│  └──────────────────┘  └──────────────────────┘  └──────────────────────┘  │
│                                                                             │
│  Tech: PyTorch → ONNX → onnxer (Go) │ gonum │ Graph-WaveNet │ ST-GPINN     │
└─────────────────────────────────────────────────────────────────────────────┘
                                       │
                                       │ HTTP/gRPC
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        TIER 2: 云端平台 (Cloud Platform)                      │
│                              Go + y-ai-agent-base                             │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                      MQTT Gateway (paho.golang/autopaho)             │    │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐               │    │
│  │  │ 设备数据接入   │  │ 指令下发      │  │ 固件OTA      │               │    │
│  │  │ QoS 0/1      │  │ QoS 1        │  │ QoS 2        │               │    │
│  │  │ Protobuf解码  │  │ JSON→Protobuf │  │ 分片传输      │               │    │
│  │  └──────────────┘  └──────────────┘  └──────────────┘               │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                    │                                        │
│  ┌─────────────────────────────────┼──────────────────────────────────┐     │
│  │                         Data Pipeline                               │     │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐              │     │
│  │  │ 实时写入       │  │ 聚合窗口      │  │ 告警引擎      │              │     │
│  │  │ InfluxDB 3    │  │ 1m/5m/1h/1d  │  │ 阈值+异常检测  │              │     │
│  │  │ 批量5K-10K    │  │ 物化视图      │  │ Webhook/SSE   │              │     │
│  │  └──────────────┘  └──────────────┘  └──────────────┘              │     │
│  └─────────────────────────────────────────────────────────────────────┘     │
│                                    │                                        │
│  ┌─────────────────────────────────┼──────────────────────────────────┐     │
│  │                    AI Engine (ONNX Runtime Go)                       │     │
│  │  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐  │     │
│  │  │ 时序预测           │  │ RL投喂优化        │  │ 生长模型           │  │     │
│  │  │ go-forecaster     │  │ DDPG/SAC         │  │ VBGM +            │  │     │
│  │  │ + goarima(SARIMAX)│  │ 多目标奖励:        │  │ Bioenergetic 4.0  │  │     │
│  │  │ 预测: DO/pH/Temp  │  │ FCR×水质×能耗     │  │ gonum RK4积分      │  │     │
│  │  │       /NH3        │  │ Go onnxer推理     │  │ 105+物种参数库    │  │     │
│  │  └──────────────────┘  └──────────────────┘  └──────────────────┘  │     │
│  └─────────────────────────────────────────────────────────────────────┘     │
│                                    │                                        │
│  ┌─────────────────────────────────┼──────────────────────────────────┐     │
│  │                         Storage Layer                                │     │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐              │     │
│  │  │ InfluxDB 3    │  │ PostgreSQL    │  │ Redis         │              │     │
│  │  │ 时序传感器数据 │  │ + TimescaleDB │  │ ·缓存(设备影子)│              │     │
│  │  │ 保留: 90d热    │  │ ·业务数据     │  │ ·消息队列     │              │     │
│  │  │     365d冷    │  │ ·设备注册表   │  │ ·投喂决策缓存  │              │     │
│  │  └──────────────┘  └──────────────┘  └──────────────┘              │     │
│  └─────────────────────────────────────────────────────────────────────┘     │
│  Framework: Go 1.25 │ Gin │ y-ai-agent-base │ Viper │ slog                  │
└─────────────────────────────────────────────────────────────────────────────┘
                                       │
                                       │ MQTT v5 (EMQX Broker)
                                       │
┌─────────────────────────────────────────────────────────────────────────────┐
│                        TIER 1: 边缘端 (Edge Device)                           │
│                            RK3588 / Jetson Orin Nano                          │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                     Edge Controller (Go Binary)                       │    │
│  │  ┌──────────────────────┐    ┌──────────────────────────────────┐   │    │
│  │  │   YOLOv8n Pipeline    │    │   Fuzzy-PID Controller            │   │    │
│  │  │  PT → ONNX → RKNN     │    │  Mamdani模糊推理器 (Go)           │   │    │
│  │  │ (RK3588 NPU, INT8)   │    │  输入: 密度/尺寸/摄食/DO/温度/NH3 │   │    │
│  │  │ 或 PT → ONNX → TensorRT│   │  输出: PWM占空比 → 电机转速      │   │    │
│  │  │ 后处理: 计数/体长/行为 │    │  安全互锁(硬件级, 不可云端覆盖)   │   │    │
│  │  └──────────────────────┘    └──────────────────────────────────┘   │    │
│  │  ┌──────────────────────┐    ┌──────────────────────────────────┐   │    │
│  │  │   MQTT Client         │    │   本地数据缓冲                    │   │    │
│  │  │   paho.golang/autopaho│    │   SQLite (7天离线数据)            │   │    │
│  │  │   上报: Protobuf      │    │   断网自动切换本地推理             │   │    │
│  │  │   指令: JSON          │    │   恢复后数据补传                  │   │    │
│  │  └──────────────────────┘    └──────────────────────────────────┘   │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                    │                                        │
│  ┌─────────────────────────────────┼──────────────────────────────────┐     │
│  │                    Sensor Nodes (ESP32-S3)                           │     │
│  │  ADS1115 16-bit ADC ×3 (TCA9548A mux):                              │     │
│  │  · pH (SEN0169)    · 溶解氧 (SEN0237-A)  · 氨氮 (analog)             │     │
│  │  · 浊度 (SEN0189)  · 水温 (DS18B20)      · 水位 (HC-SR04)            │     │
│  │  IRLZ44N MOSFET ×3: 增氧泵/加药泵/循环泵控制                          │     │
│  └─────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 数据流

### 2.1 传感器数据上行流（边缘 → 云端）

```
ESP32-S3 传感器节点
  │  RS-485 (Modbus RTU)
  ▼
RK3588 边缘控制器
  │  采集: pH/DO/Temp/NH3/Turbidity/水位
  │  编码: Protobuf (proto/sensor.proto)
  │  发布: pond/v1/{farm_id}/{pond_id}/sensor/water/{type}  (QoS 0)
  ▼
EMQX Broker (MQTT v5)
  │  订阅: pond/v1/+/+/sensor/#
  ▼
MQTT Gateway (internal/mqtt/gateway.go)
  │  Protobuf 解码 → 数据校验 (pH 0-14, DO 0-20, Temp 0-50)
  │  非法数据丢弃并告警
  ├──▶ InfluxDB 3 (sensor_data measurement, 批量写入 5K-10K pts)
  └──▶ PostgreSQL (设备自动注册 device_registry)
  │
  ▼
实时推送 (pkg/cloud/realtime)
  │  SSE: /api/v1/stream/sensors?pond_id=X
  │  WebSocket: /ws/dashboard
  ▼
Web Dashboard (前端)
```

### 2.2 摄像头推理数据流（边缘视觉）

```
IMX415 摄像头 (MIPI CSI-2, 4K 30fps)
  │  图像帧
  ▼
YOLOv8n Pipeline (RK3588 NPU, ~100 FPS)
  │  输出: 鱼群计数 / 体长估计 / 摄食行为
  │  发布: pond/v1/{farm_id}/{pond_id}/camera/inference (Protobuf, QoS 0)
  ▼
MQTT Gateway
  └──▶ InfluxDB 3 (camera_inference measurement)
```

### 2.3 云端控制指令下行流（云端 → 边缘）

```
Web Dashboard / API
  │  POST /api/v1/recommend/feeding (AI 建议)
  ▼
AI 推荐引擎 (pkg/cloud/recommend)
  │  融合: 时序预测 + 生长模型 + RL 策略
  │  输出: {feeding_rate, expected_growth, risk_level, confidence}
  ▼
MQTT Gateway
  │  发布: cloud/{farm_id}/{pond_id}/cmd/feeding/start (JSON, QoS 1)
  ▼
EMQX Broker
  ▼
RK3588 边缘控制器
  │  解析 JSON 指令 → 模糊 PID 控制器
  ▼
投喂电机 (PWM)
```

### 2.4 数字孪生数据流（TIER 3）

```
历史传感器数据 (InfluxDB)
  │
  ▼
ST-GNN 水体建模 (pkg/dt/gnn)
  │  输入: 多站点 pH/DO/Temp/NH3/Turbidity + 气象
  │  输出: 多步水质预测 (1h/6h/24h)
  ▼
物理信息融合模拟器 (pkg/dt)
  │  PI-GNN 质量守恒 / 水温能量平衡 / 水动力 PINN-SWE
  ▼
策略推演引擎 (pkg/dt/scenario)
  │  场景: heatwave / storm_flood / cold_snap
  │  RL 策略搜索 (DDPG 在仿真环境探索)
  │  输出: 最优投喂策略 + 风险评估
  ▼
可视化 API (internal/handler/dt.go)
  │  GET /api/v1/dt/pond/:id/state
  │  GET /api/v1/dt/pond/:id/trajectory
  │  GET /api/v1/dt/compare
  │  GET /api/v1/dt/pond/:id/anomaly
  ▼
Web Dashboard (数字孪生可视化)
```

---

## 3. 组件交互

### 3.1 云端 HTTP 层（internal/handler + internal/middleware）

```
HTTP 请求
  │
  ▼
┌─────────────────────────────────────────────────────────────┐
│ 中间件链 (internal/middleware)                               │
│  AuthRequired → 解析 Bearer JWT → 注入 Claims 到 Gin Context │
│  FarmScope    → 校验 farm_id 归属 → 跨农场返回 403           │
│  RequireWrite → 拦截 viewer 的 POST/PUT/DELETE → 返回 403    │
└─────────────────────────────────────────────────────────────┘
  │
  ▼
┌─────────────────────────────────────────────────────────────┐
│ Handler (internal/handler)                                   │
│  farm.go      → 农场 CRUD (PostgreSQL)                       │
│  device.go    → 设备 CRUD (PostgreSQL)                       │
│  sensor.go    → 传感器最新/历史 (InfluxDB)                   │
│  feeding.go   → 投喂日志 (PostgreSQL)                        │
│  alert.go     → 告警列表 (PostgreSQL)                        │
│  dashboard.go → 仪表盘汇总 (PostgreSQL)                      │
│  recommend.go → AI 推荐 (pkg/cloud/recommend)                │
│  dt.go        → 数字孪生 (pkg/dt/visual)                     │
│  stream.go    → SSE/WebSocket (pkg/cloud/realtime)           │
└─────────────────────────────────────────────────────────────┘
  │
  ├──▶ store.PgxPool (PostgreSQL, 参数化查询)
  ├──▶ store.InfluxWriter (InfluxDB 3, 时序查询)
  ├──▶ auth.AuthService (JWT 签发/解析)
  ├──▶ recommend.RecommendEngine (AI 推荐)
  ├──▶ visual.Visualizer (数字孪生)
  └──▶ realtime.Hub (SSE/WebSocket 房间)
```

### 3.2 MQTT Gateway 交互（internal/mqtt + pkg/mqtt）

```
pkg/mqtt.Client (paho.golang/autopaho 封装)
  │  Connect: tcp://localhost:1883, KeepAlive 20s, SessionExpiry 3600s
  │  重连: 指数退避 1s → 30s
  │  OnConnectionUp: 自动重新订阅已注册主题
  │
  ├── PublishTelemetry(topic, payload)  → QoS 0 (传感器/摄像头)
  ├── PublishCommand(topic, payload)    → QoS 1 (控制指令)
  ├── Subscribe(topic, qos)             → 注册订阅
  │
  ▼
internal/mqtt/gateway.go
  │  订阅: pond/v1/+/+/sensor/#
  │        pond/v1/+/+/camera/#
  │        pond/v1/+/+/control/#
  │        pond/v1/+/+/device/#
  │  消息处理: 每个消息派发到独立 goroutine (不阻塞读循环)
  │  Protobuf 解码 → 数据校验 → 写入 InfluxDB + PostgreSQL
```

### 3.3 实时推送交互（pkg/cloud/realtime）

```
realtime.Hub (房间模式)
  │  房间: SensorRoom(farmID, pondID) / AlertRoom(farmID)
  │  心跳: keepalive 15s
  │
  ├── SSE 传感器流: GET /api/v1/stream/sensors?token=&pond_id=
  │     → 订阅 SensorRoom(farmID, pondID)
  │     → 推送实时传感器数据 (text/event-stream)
  │
  ├── SSE 告警流: GET /api/v1/stream/alerts?token=
  │     → 订阅所有授权农场的 AlertRoom
  │     → 推送实时告警事件
  │
  └── WebSocket: GET /ws/dashboard?token=
        → 订阅所有授权农场的仪表盘房间
        → 客户端可发送 JSON 控制命令
```

### 3.4 AI 推荐引擎交互（pkg/cloud/recommend）

```
POST /api/v1/recommend/feeding
  │  body: {pond_id, do_mg_l, temp_c, nh3_mg_l, fish_weight_g, fcr, species, stocking_density}
  ▼
RecommendEngine.RecommendFeeding(state, ...)
  │  6 步流水线:
  │  ① RL 基础投喂率 (pkg/cloud/rl, ONNX 推理)
  │  ② 时序预测趋势调整 (pkg/cloud/forecast)
  │  ③ 生长模型投影 (pkg/cloud/growth)
  │  ④ 状态异常检测
  │  ⑤ 风险评估
  │  ⑥ 投喂率钳制 + 人工确认标记
  │  降级: 任一子引擎为 nil 时回退规则逻辑，置信度降低
  ▼
响应: {feeding_rate, expected_growth_g_per_day, risk_level, confidence, actions, reason, requires_manual_review}
```

### 3.5 数字孪生引擎交互（pkg/dt）

```
GET /api/v1/dt/pond/:id/state
  → Visualizer.State(pondID) → VirtualState {temperature_c, do_mg_l, turbidity_ntu, nh3_mg_l}

GET /api/v1/dt/pond/:id/trajectory?scenario=&offset=&limit=
  → Visualizer.TrajectoryByName(pondID, scenario, offset, limit) → Trajectory

GET /api/v1/dt/compare?scenarios=a,b
  → Visualizer.Compare(names) → []CompareResult (并行运行多场景)

GET /api/v1/dt/pond/:id/anomaly?do_mg_l=&temp_c=&turbidity_ntu=&nh3_mg_l=
  → Visualizer.Anomaly(pondID, PhysicalState) → AnomalyReport
  → 阈值: do 1.0, temperature_c 2.0, turbidity 10.0, nh3 0.2
```

---

## 4. 存储层设计

### 4.1 InfluxDB 3（时序数据）

| Measurement | Tags | Fields | Retention |
|------------|------|--------|-----------|
| `sensor_data` | farm_id, pond_id, sensor_type | ph, do, temp, nh3, turbidity, water_level | 90d hot / 365d cold |
| `camera_inference` | farm_id, pond_id | fish_count, sizes[], behavior_score, texture_energy | 30d |
| `feeding_decisions` | farm_id, pond_id | fuzzy_inputs, output_speed, output_duration, rules_fired | 365d |
| `device_health` | farm_id, pond_id, device_id | cpu, mem, temp, uptime, npu_usage | 90d |

### 4.2 PostgreSQL（业务数据）

| 表名 | 核心字段 | 用途 |
|------|---------|------|
| `farms` | id, name, location, area_m2, species, created_at | 农场注册 |
| `ponds` | id, farm_id, name, area_m2, depth_m, fish_count, created_at | 池塘管理 |
| `devices` | id, farm_id, pond_id, type, status, firmware_version, last_heartbeat | 设备注册 |
| `device_shadow` | device_id, reported(JSONB), desired(JSONB), delta(JSONB), updated_at | 设备影子 |
| `feeding_logs` | id, pond_id, speed, duration, decision_json, created_at | 投喂审计 |
| `alerts` | id, farm_id, pond_id, level, type, message, status, created_at, resolved_at | 告警记录 |
| `users` | id, username, password_hash, role, farm_ids[], created_at | 用户管理 |
| `model_registry` | id, type, version, onnx_path, metrics(JSONB), status, created_at | 模型版本管理 |
| `growth_records` | id, pond_id, sample_date, avg_weight_g, avg_length_mm, sample_count | 生长测量 |
| `sensor_config` | pond_id, sensor_type, calibration_coeffs(JSONB), sample_interval_s | 传感器校准 |

### 4.3 Redis

- 设备影子缓存（reported/desired/delta）
- 告警去重（60s 窗口）
- 投喂决策缓存
- 消息队列

---

## 5. MQTT 主题结构

```
pond/v1/{farm_id}/{pond_id}/
├── sensor/
│   ├── water/ph               ← float32, QoS 0
│   ├── water/do               ← float32 (mg/L)
│   ├── water/temperature      ← float32 (°C)
│   ├── water/nh3              ← float32 (mg/L)
│   ├── water/turbidity        ← float32 (NTU)
│   └── water/level            ← float32 (cm)
│
├── camera/
│   ├── inference              ← Protobuf (count, sizes[], behavior)
│   └── snapshot               ← JPEG (关键帧, QoS 0)
│
├── control/
│   ├── feeding/status         ← {speed_rpm, duration_ms, remaining_g}
│   └── feeding/decision       ← {fuzzy_inputs, rules_fired, output}
│
├── device/
│   ├── status                 ← {cpu, mem, temp, uptime} (heartbeat 30s)
│   └── alarm                  ← {level, code, message}

cloud/{farm_id}/{pond_id}/
├── cmd/
│   ├── feeding/start          ← {speed, duration} JSON
│   ├── feeding/stop           ← {} JSON
│   ├── aerator/on             ← {} JSON
│   └── aerator/off            ← {} JSON
│
├── config/
│   ├── fuzzy/update           ← {rules[], membership_fns[]}
│   └── sensor/interval        ← {ph: 30s, do: 10s, temp: 60s}
│
└── model/
    └── update                 ← {version, url, sha256} → OTA download
```

### QoS 策略矩阵

| 数据类型 | QoS | 理由 |
|---------|-----|------|
| 传感器遥测（pH/DO/Temp） | 0 | 可容忍丢包；下次读数覆盖上次 |
| 摄像头推理结果 | 0 | 高频发送；单帧丢失不影响决策 |
| 投喂状态/决策日志 | 1 | 至少一次送达；审计需要 |
| 设备心跳/告警 | 1 | 至少一次送达；接收端去重 |
| 云端控制指令 | 1 | 至少一次送达；电机指令幂等 |
| 配置/固件更新 | 2 | 必须精确送达一次 |

---

## 6. 安全设计

### 6.1 认证与授权

- **JWT 认证**：HS256，Claims 含 `user_id`、`role`（admin/operator/viewer）、`farm_ids[]`。
- **RBAC 中间件**：`AuthRequired` → `FarmScope` → `RequireWrite`。
- **租户隔离**：跨 farm 查询返回 403，viewer 角色不能写设备。

### 6.2 传输安全

- **MQTT TLS**：X.509 证书加密。
- **HTTPS API**：生产环境不使用 HTTP。
- **API 限流**：令牌桶，100 req/min/IP，超限返回 429。

### 6.3 数据安全

- **参数化查询**：抵御 SQL 注入。
- **Protobuf 输入校验**：range/type check。
- **密钥管理**：不用明文存储密钥，Dockerfile 不含 secrets。

### 6.4 硬件安全互锁（不可覆盖）

- DO < 4.0 → 增氧泵强制 ON。
- 投喂电机过流 → 强制 STOP。
- 急停按钮 → 全部执行机构断电（纯硬件回路）。
- 云端指令无法覆盖硬件互锁。

---

## 7. 目录结构（代码架构）

```
y-ai-pond/
├── cmd/
│   ├── server/          # 云端 HTTP 服务入口
│   ├── edge/            # 边缘端控制器入口
│   └── migrate/         # 数据库迁移入口
├── internal/
│   ├── config/          # 服务端配置派生 (Viper)
│   ├── handler/         # HTTP Handler (farm/device/sensor/feeding/alert/dashboard/dt/recommend/stream)
│   ├── middleware/       # HTTP 中间件 (auth.go: JWT + RBAC + FarmScope)
│   ├── mqtt/            # 云端 MQTT Gateway
│   ├── router/          # 路由注册
│   └── server/          # 服务器生命周期 (health/metrics)
├── pkg/
│   ├── proto/           # Protobuf 生成代码 (buf)
│   ├── mqtt/            # MQTT Client 封装 (autopaho)
│   ├── store/           # 数据库层 (influx.go / postgres.go / redis.go)
│   ├── auth/            # JWT 认证服务
│   ├── edge/            # 边缘端核心 (detector/texture/fuzzypid/controller/safety/hal)
│   ├── cloud/           # 云端核心 (alert/shadow/scheduler/realtime/forecast/growth/rl/recommend/modelmgr/report)
│   └── dt/              # 数字孪生 (engine/gnn/scenario/visual)
├── proto/               # Protobuf 定义文件
├── python/              # Python 离线训练 (不部署生产)
├── tools/               # 开发工具脚本
├── config/              # 配置文件
├── doc/                 # 文档
├── k8s/                 # K8s Helm Chart
├── Dockerfile
├── docker-compose.yml
├── Makefile
└── go.mod
```

---

*本文档由 y-ai-pond 项目维护。技术细节以 `.omo/plans/y-ai-pond.md` 与 `doc/` 下文档为准。*
