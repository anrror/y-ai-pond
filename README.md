# y-ai-pond · 智慧水产养殖管理平台

> **版本**: v1.0 | **定位**: 三态（边缘 + 云端 + 数字孪生）智慧水产养殖管理平台
> **技术栈**: Go 1.25 + y-ai-agent-base + InfluxDB 3 + PostgreSQL + Redis + EMQX
> **AI 模型**: YOLOv8n（边缘视觉）+ Fuzzy-PID（投喂控制）+ Prophet/ARIMA（时序预测）+ VBGM/Bioenergetic（生长模型）+ DDPG（强化学习）+ ST-GNN（数字孪生）

---

## 目录

- [1. 项目概述](#1-项目概述)
- [2. 三态架构总览](#2-三态架构总览)
- [3. 边缘端（TIER 1）](#3-边缘端tier-1)
  - [3.1 边缘控制器（Edge Controller）](#31-边缘控制器edge-controller)
  - [3.2 YOLOv8n 视觉推理流水线](#32-yolov8n-视觉推理流水线)
  - [3.3 水面纹理提取与摄食强度评估](#33-水面纹理提取与摄食强度评估)
  - [3.4 Mamdani 模糊 PID 投喂控制器](#34-mamdani-模糊-pid-投喂控制器)
  - [3.5 硬件安全互锁](#35-硬件安全互锁)
  - [3.6 设备驱动抽象层（HAL）](#36-设备驱动抽象层hal)
  - [3.7 本地数据缓冲与离线降级](#37-本地数据缓冲与离线降级)
- [4. 传感器节点层（ESP32-S3）](#4-传感器节点层esp32-s3)
- [5. 云端平台（TIER 2）](#5-云端平台tier-2)
  - [5.1 MQTT Gateway 数据接入](#51-mqtt-gateway-数据接入)
  - [5.2 数据管道与存储层](#52-数据管道与存储层)
  - [5.3 时序预测引擎](#53-时序预测引擎)
  - [5.4 生长模型引擎](#54-生长模型引擎)
  - [5.5 RL 投喂策略优化](#55-rl-投喂策略优化)
  - [5.6 AI 推荐引擎](#56-ai-推荐引擎)
  - [5.7 实时告警引擎](#57-实时告警引擎)
  - [5.8 设备影子与远程配置](#58-设备影子与远程配置)
  - [5.9 集群调度与实时推送](#59-集群调度与实时推送)
  - [5.10 模型管理与报表](#510-模型管理与报表)
- [6. 数字孪生层（TIER 3）](#6-数字孪生层tier-3)
- [7. MQTT 主题结构](#7-mqtt-主题结构)
- [8. 技术栈总览](#8-技术栈总览)
- [9. 硬件方案](#9-硬件方案)
- [10. AI 模型管线](#10-ai-模型管线)
- [11. 数据存储设计](#11-数据存储设计)
- [12. 安全设计](#12-安全设计)
- [13. 目录结构说明](#13-目录结构说明)
- [14. 快速开始（开发环境）](#14-快速开始开发环境)
- [15. 部署方式](#15-部署方式)
- [16. 文档索引](#16-文档索引)
- [17. 贡献指南](#17-贡献指南)

---

## 1. 项目概述

y-ai-pond 是面向水产养殖基地的**三态智能管理平台**，覆盖边缘端（设备本地智能控制）、云端平台（数据分析与 AI 策略优化）、数字孪生（大型基地水体仿真与极端天气推演）三层架构。

**核心价值**：将 YOLOv8n 视觉识别、模糊 PID 控制、时序预测、强化学习、时空图神经网络等 AI 技术深度融入投喂管理全流程，实现从"经验投喂"到"数据驱动的精准投喂"的产业升级。

**一句话概括**：边缘端部署在 RK3588 上，通过 YOLOv8n 实时识别鱼群行为、结合模糊 PID 控制毫秒级精准投喂；云端基于 Go 构建时序预测与强化学习模型，优化长期投喂策略；大型基地可启用数字孪生，通过时空 GNN 模拟水体环境与极端天气下的投喂策略推演。

### 1.1 为什么采用三态架构

传统水产养殖管理方案通常只有"数据采集 + 简单展示"两层，存在三个根本问题：

1. **带宽与延迟**：所有视频帧、传感器数据都上传云端，弱网环境下决策延迟高、带宽成本大。
2. **决策滞后**：投喂决策依赖人工经验或简单定时器，无法根据鱼群实时摄食行为调整。
3. **缺乏前瞻**：无法预测水质恶化趋势、无法模拟极端天气对养殖的影响。

三态架构把"毫秒级实时控制"下沉到边缘端，"长期策略优化"放在云端，"前瞻性推演"交给数字孪生，各层各司其职，协同解决上述问题。

### 1.2 明确不做的事（Scope 边界）

- 不做水下 VR 巡检或声呐探测（v1 范围外）。
- 不绑定单一硬件厂商（支持 RK3588 / Jetson 双平台，ONNX 中间格式）。
- 不在生产环境运行 Python 后端（仅离线训练使用 Python）。
- 不做移动 App（Web PWA 先行，Phase 2 实现完整前端）。

---

## 2. 三态架构总览

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        TIER 3: 数字孪生层 (Digital Twin)                      │
│                    【大型基地高阶应用 — 可选部署】                               │
│                                                                             │
│  ┌──────────────────┐  ┌──────────────────────┐  ┌──────────────────────┐  │
│  │ ST-GNN 水体建模   │  │ 物理信息融合模拟器    │  │ 策略推演引擎           │  │
│  │                  │  │                      │  │                      │  │
│  │ 架构: D-TGCN      │  │ PI-GNN: 质量守恒约束  │  │ 输入: SSP245/370/585 │  │
│  │ 输入: 多站点水质   │  │ 水温: 能量平衡模型    │  │      极端天气场景     │  │
│  │       pH/DO/Temp  │  │ 水动力: PINN-SWE替代  │  │                      │  │
│  │ 输出: 多步水质预测 │  │ 生长: FishMet生物能学 │  │ 输出: 最优投喂策略     │  │
│  │                  │  │                      │  │      风险评估报告     │  │
│  │ 训练: Python→ONNX │  │ 推理: Go onnxer       │  │                      │  │
│  │ 推理: Go onnxer   │  │                      │  │ DDPG RL 策略搜索      │  │
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
│  │  │ (Prophet风格)     │  │ 多目标奖励:        │  │ Bioenergetic 4.0  │  │     │
│  │  │ + goarima(SARIMAX)│  │ FCR×水质×能耗     │  │ gonum RK4积分      │  │     │
│  │  │ 预测目标:         │  │ Python训练→ONNX   │  │ 105+物种参数库    │  │     │
│  │  │ DO/pH/Temp/NH3   │  │ Go onnxer推理     │  │ 温度-生长耦合     │  │     │
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
│  │  │ 查询: SQL     │  │ ·用户/权限    │  │              │              │     │
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
│  │  │ (RK3588 NPU, INT8)   │    │  ┌────────────────────────────┐  │   │    │
│  │  │ 或 PT → ONNX → TensorRT│   │  │ 模糊化 → 规则匹配 → 去模糊  │  │   │    │
│  │  │ (Jetson GPU, FP16)   │    │  │ PID分量: Kp·e + Ki·∫e +    │  │   │    │
│  │  │ 后处理:               │    │  │          Kd·de/dt          │  │   │    │
│  │  │ · 鱼群计数 (NMS)     │    │  │ 输出: PWM占空比 → 电机转速  │  │   │    │
│  │  │ · 体长估计 (像素→mm) │    │  └────────────────────────────┘  │   │    │
│  │  │ · 行为纹理 (光流法)  │    │  安全互锁(硬件级, 不可云端覆盖):  │   │    │
│  │  │ 性能: ~100 FPS       │    │  · DO < 4.0 → 增氧泵强制ON       │   │    │
│  │  │ 延迟: <10ms          │    │  · 投喂电机过热 → 强制停止        │   │    │
│  │  └──────────────────────┘    │  · 急停按钮 → 全部执行机构断电    │   │    │
│  │  ┌──────────────────────┐    └──────────────────────────────────┘   │    │
│  │  │   MQTT Client         │    ┌──────────────────────────────────┐   │    │
│  │  │   paho.golang/autopaho│    │   本地数据缓冲                    │   │    │
│  │  │   上报: Protobuf      │    │   SQLite (7天离线数据)            │   │    │
│  │  │   指令: JSON          │    │   断网自动切换本地推理             │   │    │
│  │  └──────────────────────┘    │   恢复后数据补传                  │   │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                    │                                        │
│  ┌─────────────────────────────────┼──────────────────────────────────┐     │
│  │                    Sensor Nodes (ESP32-S3)                           │     │
│  │  ADS1115 16-bit ADC ×3 (TCA9548A mux):                              │     │
│  │  · pH (DFRobot SEN0169)    · 溶解氧 (SEN0237-A)                     │     │
│  │  · 氨氮 (analog)           · 浊度 (SEN0189)                          │     │
│  │  DS18B20 1-Wire ×1: 水温                                            │     │
│  │  IRLZ44N MOSFET ×3: 增氧泵/加药泵/循环泵控制                          │     │
│  │  HC-SR04 ×1: 水位监测                                               │     │
│  │  Ra-02 SX1278: LoRa备用通信                                          │     │
│  └─────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 2.1 核心业务流程（投喂决策，边缘端毫秒级实时响应）

```
                                ┌─────────────┐
                                │  摄像头采集   │
                                │ (水面+水下)   │
                                └──────┬──────┘
                                       │ 图像帧 (30 FPS)
                                       ▼
                          ┌─────────────────────────┐
                          │    YOLOv8n ONNX 推理      │
                          │  (RK3588 NPU, ~10ms)     │
                          │  输出:                    │
                          │  · 鱼群数量 count         │
                          │  · 鱼体长度 size_mm       │
                          │  · 摄食行为 behavior      │
                          └────────────┬────────────┘
                                       │
              ┌────────────────────────┼────────────────────────┐
              │                        │                        │
              ▼                        ▼                        ▼
    ┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
    │  水面纹理提取     │    │  水质传感器数据   │    │  历史投喂记录     │
    │  (摄食强度评估)   │    │  pH/DO/Temp/NH3  │    │  (本地缓存)       │
    └────────┬────────┘    └────────┬────────┘    └────────┬────────┘
             │                      │                      │
             └──────────────────────┼──────────────────────┘
                                    │
                                    ▼
                    ┌───────────────────────────────┐
                    │   Fuzzy-PID 投喂控制器          │
                    │  输入变量:                      │
                    │  ① 鱼群密度 (count/area)        │
                    │  ② 鱼体尺寸 (avg_size)          │
                    │  ③ 摄食强度 (behavior_texture)  │
                    │  ④ 溶解氧 (DO mg/L)             │
                    │  ⑤ 水温 (temp °C)               │
                    │  ⑥ 氨氮 (NH3 mg/L)             │
                    │  模糊规则库 (25+ rules):         │
                    │  IF density=HIGH AND DO=NORMAL │
                    │     THEN feeding=INCREASE     │
                    │  IF appetite=LOW AND NH3=HIGH │
                    │     THEN feeding=STOP         │
                    │  输出: feeding_speed_rpm,       │
                    │       feeding_duration_ms      │
                    └──────────────┬────────────────┘
                                   │
                    ┌──────────────┴──────────────┐
                    │                             │
                    ▼                             ▼
          ┌─────────────────┐          ┌─────────────────┐
          │  投喂电机执行     │          │  MQTT → 云端上报  │
          │  (GPIO/PWM, <5ms) │          │  推理+控制+传感器 │
          └─────────────────┘          └─────────────────┘
```

### 2.2 云端 AI 策略优化流程（非实时）

```
历史数据积累 (天数/周数)
  └─ 传感器时序数据 · 投喂日志 · 生长测量 · 环境数据 · 告警记录
        │
        ▼
   数据管道 (清洗 · 聚合 · 特征工程)
        │
   ┌────┼────────────┐
   ▼    ▼            ▼
时序预测  RL策略优化   生长建模
·DO趋势  ·投喂率优化  ·VBGM预测
·温度变化 ·FCR最小化  ·出塘时间
·氨氮累积 ·能耗平衡   ·生物能学
Prophet+ARIMA  DDPG/SAC  Bioenergetic 4.0
   └────┼────────────┘
        ▼
   AI 推荐引擎 (融合预测+RL+生长)
   └─ 投喂建议(含置信度+风险评估)
        ▼
   MQTT → 边缘端参数更新 (投喂参数/模糊规则/传感器间隔)
```

---

## 3. 边缘端（TIER 1）

### 3.1 边缘控制器（Edge Controller）

**作用**：边缘端是整套系统的"大脑"，负责毫秒级实时控制。它运行在 RK3588（或 Jetson Orin Nano）上，是一个独立的 Go 二进制程序，不依赖云端即可完成投喂决策。

**实现**：主控制循环以约 100Hz 频率运行，串联以下环节：摄像头帧 → YOLOv8n 推理 → 纹理提取 → 传感器读数 → Fuzzy-PID → PWM 输出。控制器通过 `pkg/edge/controller/` 包实现，采用模块编排模式（参考 y-ai-agent-base 的 module orchestrator 模式），各子模块（detector、texture、fuzzypid、safety、hal）通过接口解耦。

**好处**：
- **毫秒级响应**：投喂决策完全在本地完成，不依赖网络往返，电机执行延迟 < 5ms。
- **断网可用**：MQTT 断开时自动切换本地推理，SQLite 缓冲 7 天离线数据，恢复后补传。
- **故障降级**：摄像头故障时降级为纯传感器控制模式；NPU 故障时回退 CPU 推理（慢但可用）。

### 3.2 YOLOv8n 视觉推理流水线

**作用**：实时识别鱼群，输出鱼群数量、鱼体长度、摄食行为，是投喂决策的视觉输入。

**实现**（`pkg/edge/detector/`）：
- 模型转换流水线：`PT → ONNX → RKNN`（RK3588 NPU，INT8 量化）或 `PT → ONNX → TensorRT`（Jetson GPU，FP16）。转换脚本为 `tools/convert_yolo.py`。
- Go 端通过 onnxer（零 CGO 的 Go ONNX Runtime）封装推理，输出 `Detection{fishes: [{bbox, class, confidence}], count, avg_size_px}`。
- 后处理：NMS（IoU=0.45）去重 → 鱼群计数 → 体长估计（像素→mm，基于相机标定）→ 行为分类（聚集/散开/抢食）。
- 图像预处理（GStreamer）：4K → 640×640 缩放（YOLOv8n 标准输入）、BGR→RGB、归一化 [0,1]、去畸变。

**性能**：RK3588 NPU 上约 100 FPS，单帧延迟 < 10ms（目标 < 15ms）。

**好处**：
- **ONNX 中间格式**：一套模型同时支持 RK3588 和 Jetson 双平台，不绑定单一硬件厂商，规避供应链风险。
- **INT8 量化**：相比 FP32 仅损失 < 0.3 mAP，但推理速度提升数倍，功耗更低。
- **边缘推理**：视频帧不上传云端，带宽敏感度最低，隐私性更好。

### 3.3 水面纹理提取与摄食强度评估

**作用**：评估鱼群摄食强度（None/Weak/Medium/Strong 四级），作为模糊 PID 的重要输入，判断"该不该喂、喂多快"。

**实现**（`pkg/edge/texture/`）：
- 连续帧间光流计算（Farnebäck dense optical flow 或帧差法简化版），计算水面纹理能量 `E = Σ(∇I)² / area`。
- 结合 YOLOv8n 输出的行为分类（聚集/散开/抢食），融合为摄食强度分数 `S_t ∈ [0,1]`。
- 参考 YOLO11-PEGA 的三特征融合：`splash_frequency + mean_bbox_area + texture_energy` → 阈值分级。
- 分级阈值：E < 0.1 静止，0.1-0.3 轻度，0.3-0.6 中度，> 0.6 剧烈。

**好处**：
- **CPU 友好**：使用帧差法/简化光流，不需要 GPU 加速，适配边缘端算力。
- **不阻塞推理管线**：纹理提取与 YOLOv8n 推理解耦，各自独立运行。
- **抗反光**：即使水面反光也能正确分级，不会崩溃。

### 3.4 Mamdani 模糊 PID 投喂控制器

**作用**：综合视觉、传感器、历史数据，输出投喂电机的 PWM 占空比（0-100% → 电机转速）和投喂时长，是投喂决策的核心。

**实现**（`pkg/edge/fuzzypid/`）：
- **模糊化（Fuzzifier）**：三角/梯形隶属函数，5 级（VL/L/M/H/VH），对 6 个输入变量（鱼群密度、鱼体尺寸、摄食强度、溶解氧、水温、氨氮）归一化。
- **规则库（RuleBase）**：25+ 条 Mamdani 规则，例如 `IF density=HIGH AND DO=NORMAL THEN feeding=INCREASE`、`IF appetite=LOW AND NH3=HIGH THEN feeding=STOP`。
- **推理引擎（InferenceEngine）**：Mamdani min-max 推理。
- **去模糊（Defuzzifier）**：重心法（COG）。
- **PID 增量式输出**：`Δu = Kp·e(k) + Ki·Σe + Kd·(e(k)-e(k-1))`，输出 PWM 占空比。

**好处**：
- **已验证的精度提升**：模糊 PID 混合控制相比纯 PID 降低 66.5% MSE，能更好地处理水质与摄食行为的非线性、时变关系。
- **规则可解释**：25+ 条规则可读、可调，养殖专家能理解并微调，而非黑盒。
- **毫秒级响应**：纯 Go 实现，无 Python 运行时，投喂指令毫秒级执行。

### 3.5 硬件安全互锁

**作用**：保障养殖安全与设备安全，是**硬件级、不可被云端覆盖**的兜底机制。

**实现**（`pkg/edge/safety/`）：
- DO < 4.0 mg/L → 增氧泵强制 ON（无论模糊控制器输出如何）。
- 投喂电机电流 > 阈值 → 强制 STOP（过流保护）。
- 急停 GPIO 拉低 → 全部执行机构断电（纯硬件回路，不依赖 MCU/软件）。
- 水温 > 38°C → 投喂暂停。
- 单次加药 ≤ 15mL，两次加药间隔 ≥ 600s，每小时加药 ≤ 40mL。
- 互锁规则优先级高于模糊控制器输出。

**好处**：
- **不可覆盖**：云端指令无法覆盖硬件互锁，即使云端被攻破或误操作，硬件仍能保护鱼塘。
- **不依赖网络**：安全互锁不依赖 MQTT 连接，断网时依然生效。
- **fail-safe**：急停回路采用常闭（NC）触点，电源故障时继电器线圈断电，全部执行器自动断电。

### 3.6 设备驱动抽象层（HAL）

**作用**：统一封装传感器与执行器的硬件访问，屏蔽底层差异，让上层控制器与具体硬件解耦。

**实现**（`pkg/edge/hal/`）：
- 定义接口：`Sensor{Read() → float64, Calibrate(), Status() → Health}`、`Actuator{On(), Off(), SetSpeed(pct), Status() → Health}`。
- 传感器驱动：pH（SEN0169）、DO（SEN0237-A）、温度（DS18B20）、氨氮（analog）、浊度（SEN0189）。
- 执行器驱动：投喂电机（PWM）、增氧泵（GPIO）、循环泵（GPIO）。
- 硬件访问通过 periph.io（纯 Go，无 cgo），ADS1115 ADC 通过 I2C 读取。

**好处**：
- **纯 Go 无 cgo**：避免 cgo 交叉编译的复杂性，RK3588/Jetson 均可轻松交叉编译。
- **可测试**：MockSensor/MockActuator 支持单元测试，无需真实硬件。
- **健康监测**：传感器断线时 `Status() → STATUS_ERROR`，主循环跳过该传感器并告警。

### 3.7 本地数据缓冲与离线降级

**作用**：保证弱网/断网环境下系统持续可用，数据不丢失。

**实现**：SQLite 本地缓冲 7 天离线数据；断网自动切换本地推理；恢复后数据补传。MQTT 客户端采用指数退避重连（1s→30s），KeepAlive 20s，SessionExpiry 3600s，OnConnectionUp 自动重新订阅。

**好处**：MQTT 弱网环境是水产养殖的常态（偏远鱼塘、4G 信号差），本地缓冲 + 补传机制保证数据完整性和决策连续性。

---

## 4. 传感器节点层（ESP32-S3）

**作用**：部署在池塘现场的采集节点，负责水质参数的采集与执行机构的本地控制，通过 RS-485（Modbus RTU）与 RK3588 主控通信。

**实现**：
- **主控**：ESP32-S3-WROOM-1-N16R8（Xtensa LX7 双核 @ 240MHz，16MB Flash，8MB PSRAM）。
- **ADC 采集**：ADS1115 16-bit ADC ×3，通过 TCA9548A I2C 多路复用器扩展通道。量程 ±4.096V，采样率 860 SPS，中值滤波 + EMA（α=0.3）平滑。
- **传感器**：pH（SEN0169，±0.11）、溶解氧（SEN0237-A，±0.22 mg/L）、温度（DS18B20，±0.08°C）、氨氮（analog，±0.014 mg/L）、浊度（SEN0189，±1.7 NTU）。
- **执行机构**：IRLZ44N MOSFET ×3 控制增氧泵/加药泵/循环泵。
- **水位监测**：HC-SR04 超声波。
- **备用通信**：Ra-02 SX1278 LoRa 模块（433MHz，视距 3-5km）。
- **固件级安全互锁**：DO < 4.0 → 增氧泵强制 ON；单次加药 ≤ 15mL；两次加药间隔 ≥ 600s；每小时加药 ≤ 40mL。

**为什么用 ADS1115 而不用 ESP32 内置 ADC**：ESP32 内置 ADC 仅 12-bit（4096 级）且非线性严重；ADS1115 是 16-bit（65536 级），线性度 < 0.01%。传感器精度要求 pH ±0.11，16-bit 足够而 12-bit 不够。

**好处**：
- **分布式部署**：传感器浮筒可部署在池塘中心、投喂区等多个位置，通过 RS-485 总线（最长 1200m）汇聚到主控。
- **低成本**：ESP32-S3 节点成本低，可大规模部署。
- **本地安全互锁**：即使与主控通信中断，节点固件仍能执行安全互锁。

---

## 5. 云端平台（TIER 2）

### 5.1 MQTT Gateway 数据接入

**作用**：云端与边缘端之间的数据枢纽，负责设备数据接入、指令下发、固件 OTA。

**实现**（`internal/mqtt/gateway.go`）：
- 订阅主题：`pond/v1/+/+/sensor/#`、`pond/v1/+/+/camera/#`、`pond/v1/+/+/control/#`、`pond/v1/+/+/device/#`。
- Protobuf 解码 → 数据校验（范围检查：pH 0-14，DO 0-20，Temp 0-50）→ 写入 InfluxDB（传感器数据）+ PostgreSQL（投喂日志/设备状态）。
- 设备自动注册：首次连接 → device_registry 表 INSERT。
- QoS 处理：sensor QoS 0，control/status QoS 1，cmd QoS 1。
- 基于 paho.golang/autopaho（MQTT v5 原生库）。

**好处**：
- **异步非阻塞**：message handler 用 goroutine 处理，不阻塞主循环。
- **数据校验**：非法数据（如 pH=15）直接丢弃并告警，防止脏数据污染数据库。
- **自动注册**：设备即插即用，无需人工预注册。

### 5.2 数据管道与存储层

**作用**：存储海量时序数据与业务数据，支撑查询、聚合、告警与 AI 训练。

**实现**（`pkg/store/`）：
- **InfluxDB 3**：时序传感器数据，批量写入（5K-10K points/batch），保留策略 90d 热 / 365d 冷，SQL 查询，聚合窗口 1m/5m/1h/1d 物化视图。
- **PostgreSQL + TimescaleDB**：业务数据（farms、ponds、devices、users、feeding_logs、alerts、model_registry、growth_records、sensor_config）。
- **Redis**：设备影子缓存、告警去重、投喂决策缓存、消息队列。

**好处**：
- **分层存储**：时序数据（InfluxDB）与业务数据（PostgreSQL）分离，各用所长，避免单一数据库类型无法兼顾时序写入与事务一致性。
- **高吞吐**：InfluxDB 3 批量写入 > 10K pts/s，查询 < 50ms。
- **生态复用**：PostgreSQL 复用 y-ai-agent-base 的 migrations 模式，降低学习成本。

### 5.3 时序预测引擎

**作用**：预测未来水质趋势（DO/pH/Temp/NH3），为投喂决策和告警提供前瞻性依据。

**实现**（`pkg/cloud/forecast/`）：
- 集成 go-forecaster（Prophet 风格：趋势 + 周期 + 节假日，适合 DO 日周期、温度季节性）和 albertyw/goarima（SARIMAX：季节 ARIMA + 外生变量，适合短期精细预测）。
- 预测目标：DO（1h/6h/24h）、pH（1h/6h）、Temperature（6h/24h）、NH3（1h/6h）。
- 输出置信区间（80%/95%）。
- 模型漂移检测：周期性重训练，预测误差趋势超阈值触发重训练建议。

**好处**：
- **双模型互补**：Prophet 擅长捕捉长期趋势与季节性，SARIMAX 擅长短期精细预测，两者结合覆盖不同预测时域。
- **置信区间**：给养殖人员提供不确定性信息，避免盲目信任单点预测。
- **数据门槛**：对 < 7 天数据拒绝训练，避免过拟合。

### 5.4 生长模型引擎

**作用**：预测鱼体生长（体重增长 g/day、FCR 饲料转化率、出塘时间），支撑投喂策略优化与成本分析。

**实现**（`pkg/cloud/growth/`）：
- **VBGM**（von Bertalanffy）：`L(t) = L∞ × (1 - e^(-k(t-t0)))`，预测体长增长。
- **Bioenergetic 4.0**（Deslauriers 2017）：`C = R + (F+U) + G`，能量守恒模型，41+ 参数/物种。
- 温度-生长耦合：`R(T) = R_max × f_R(T) × ACT`。
- 使用 gonum RK4（diff/fd）数值积分。
- 105+ 物种参数库（CSV 配置）。

**好处**：
- **物理可解释**：基于能量守恒的生物能学模型，比纯数据拟合更可靠、可解释。
- **温度耦合**：显式建模温度对代谢的影响，高温时维持代谢增加、净生长降低。
- **多物种支持**：105+ 物种参数库，不假设所有物种使用同一组参数。

### 5.5 RL 投喂策略优化

**作用**：通过强化学习优化长期投喂策略，目标是最小化 FCR（饲料转化率）、稳定水质、降低能耗。

**实现**（`python/rl/` + `pkg/cloud/rl/`）：
- **离线训练**（Python，不部署生产）：`rl/feeding_env.py` 定义投喂环境 Gym（state=[DO, temp, NH3, fish_weight, FCR]，action=[feeding_rate]）；`rl/train_ddpg.py` 用 Stable-Baselines3 DDPG 训练 → 导出 ONNX。
- **Go 推理**：`pkg/cloud/rl/` 通过 onnxer 加载 ONNX 策略模型，State → Action 推理 < 1ms。
- **多目标奖励函数**：FCR 改善 × 0.4 + 水质稳定 × 0.3 + 能耗降低 × 0.3。

**好处**：
- **零 CGO 生产依赖**：Python 仅用于离线训练，生产环境 Go 通过 onnxer 推理，避免引入 Python 运行时。
- **多目标优化**：奖励函数显式权衡 FCR、水质、能耗，而非单一目标。
- **安全边界**：RL 策略不覆盖模糊控制器的安全互锁，保障安全。

### 5.6 AI 推荐引擎

**作用**：融合时序预测 + 生长模型 + RL 策略，生成结构化的投喂建议与异常响应建议。

**实现**（`pkg/cloud/recommend/`）：
- 组合 T20（预测）+ T21（生长）+ T22（RL）输出 → 生成投喂建议 `{feeding_rate, expected_growth, risk_level, confidence}`。
- 异常响应：DO 下降预测 → 建议提前开启增氧；生长滞后预测 → 建议调整投喂/密度。
- 建议 API：`POST /api/v1/recommend/feeding`、`GET /api/v1/recommend/daily`。

**好处**：
- **辅助而非替代**：建议不自动执行，保留养殖人员最终决策权。
- **置信度标记**：低置信度（< 0.7）时标记"需要人工确认"，避免误导。
- **模型未就绪降级**：AI 模型未就绪时回退基础规则，保证可用性。

### 5.7 实时告警引擎

**作用**：实时监测水质异常，及时通知养殖人员，防止损失。

**实现**（`pkg/cloud/alert/`）：
- **阈值告警**：pH < 6.5 或 > 8.5、DO < 4.0、Temp > 35、NH3 > 0.5 → 告警生成。
- **级别**：CRITICAL / WARNING / INFO。
- **通知渠道**：Webhook / SSE / 日志。
- **告警去重**：Redis，60s 窗口内同类型只发一次。
- **告警升级**：WARNING 持续 30min → CRITICAL。
- **异常检测**：STL 分解残差 > 3σ（wenta/timeseries-go）。

**好处**：
- **防告警风暴**：Redis 去重 + 限流（1/s），避免重复告警淹没。
- **异常检测**：阈值之外还能发现趋势性异常（STL 残差超 3σ）。
- **降级可用**：Redis 不可用时降级为内存去重。

### 5.8 设备影子与远程配置

**作用**：实现设备状态的双向同步（reported ↔ desired → delta），支持远程配置下发与 OTA 固件升级。

**实现**（`pkg/cloud/shadow/`）：
- 设备影子：reported（设备上报属性）↔ desired（云端期望属性）→ delta（差异自动下发）。
- `POST /api/v1/devices/:id/shadow` → 更新 desired → MQTT 下发 config/update 主题。
- 设备上报 reported → 更新 Redis 缓存 + PostgreSQL。
- OTA 固件升级：`POST /api/v1/devices/:id/ota` → 分片传输固件 → MQTT model/update 主题。

**好处**：
- **状态一致性**：影子模式保证云端与设备状态最终一致，delta 自动下发差异。
- **安全边界**：desired 不覆盖硬件安全互锁参数；不下发未签名固件。
- **OTA 不阻塞**：固件升级过程不阻塞设备正常功能。

### 5.9 集群调度与实时推送

**作用**：管理多设备集群，批量下发指令；向 Web 前端实时推送传感器数据与告警。

**实现**（`pkg/cloud/scheduler/` + `pkg/cloud/realtime/`）：
- **集群调度**：设备分组（farm→pond→device tree）、集群健康检查（心跳超时 120s → 离线告警）、负载均衡（多池塘批量指令 fan-out MQTT）、设备轮值（增氧泵交替工作）。调度 API：`POST /api/v1/scheduler/batch-feeding`、`POST /api/v1/scheduler/batch-aeration`。
- **实时推送**：SSE 端点 `GET /api/v1/stream/sensors?pond_id=X`、`GET /api/v1/stream/alerts`；WebSocket 端点 `/ws/dashboard`。房间模式按 farm_id/pond_id 分组，心跳 keepalive 15s。

**好处**：
- **批量高效**：一次 API 调用批量下发到多个池塘，避免逐台操作。
- **故障隔离**：单设备离线只标记跳过，不影响其他设备。
- **房间隔离**：SSE/WS 按 farm 隔离，未授权 farm 数据不泄露。

### 5.10 模型管理与报表

**作用**：管理 AI 模型版本、A/B 测试、漂移检测；生成日报/周报/月报并导出。

**实现**（`pkg/cloud/modelmgr/` + `pkg/cloud/report/`）：
- **模型注册表**（PostgreSQL model_registry）：model_id、type（forecast/growth/rl/dt）、version、onnx_path、metrics（RMSE/R²）、status（active/staging/archived）。
- **A/B 测试**：生产模型 vs 候选模型 → 指标对比 → 自动升级。
- **漂移检测**：预测误差趋势超阈值 → 触发重训练建议。
- **报表**：日报（水质概要+投喂量+告警）、周报（生长趋势+预测+建议）、月报（成本分析+FCR+出塘预测）。导出格式 JSON/CSV/PDF（gotenberg）。

**好处**：
- **版本可控**：模型可回滚、可对比，不直接覆盖活跃模型文件。
- **异步报表**：大型报表用 job queue 异步生成，不阻塞 API handler。
- **数据限制**：导出限制 10K 行，防止超大响应。

---

## 6. 数字孪生层（TIER 3）

**作用**：面向大型基地的高阶应用，通过时空图神经网络（ST-GNN）模拟水体环境，推演极端天气下的投喂策略，输出风险评估报告。

**实现**（`pkg/dt/`）：
- **ST-GNN 水体建模**（`pkg/dt/gnn/`）：架构 D-TGCN 或 AEST-GNN（有向图 + 动态邻接矩阵 + GRU 门控）。GNN 节点 = 池塘监测站，边 = 水流方向/管道连通。输入特征：pH/DO/Temp/NH3/Turbidity + 气象（气温/气压/降雨）。输出多步水质预测（1h/6h/24h）。Python 离线训练（`python/dt/train_stgnn.py`）→ ONNX → Go onnxer 推理。
- **物理信息融合模拟器**：PI-GNN 质量守恒约束、水温能量平衡模型、水动力 PINN-SWE 替代、生长 FishMet 生物能学。
- **策略推演引擎**（`pkg/dt/scenario/`）：极端天气场景库（SSP585 高温热浪 +4°C 持续 7 天、暴雨洪水降雨×3、寒潮 -10°C 48h）。ScenarioRunner 加载场景 → 仿真引擎 Step × N → RL 策略搜索（DDPG 在仿真环境探索）→ 输出最优策略 + 风险评估。
- **仿真引擎接口**：`Simulator{Init→State, Step→(State, Reward), Reset→State}`、`WaterBodyModel{Step→WaterQuality}`、`FishGrowthModel{Step→Growth}`、`SimulationEngine{Run→Trajectory}`。
- **可视化 API**（`internal/handler/dt.go`）：`GET /api/v1/dt/pond/:id/state`、`/trajectory`、`/compare`、`/anomaly`。
- **μDT 边缘部署**：边缘在 MQTT 断线时独立运行本地简化仿真（LocalDTMicro），恢复后同步。

**好处**：
- **物理先验**：D-TGCN 有向图编码水流方向物理先验（R²=0.884），比纯数据模型更准。
- **动态邻接矩阵**：流量变化时邻接矩阵更新，适应水体连通性变化。
- **仿真安全**：RL 策略只在仿真环境探索，不在生产池塘直接测试，零风险。
- **前瞻决策**：极端天气来临前即可推演最优投喂策略，而非事后补救。

---

## 7. MQTT 主题结构

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

## 8. 技术栈总览

| 层级 | 编程语言 | 核心框架/库 | AI 模型 |
|------|---------|-----------|--------|
| Web Dashboard | TypeScript | Next.js 15 + shadcn/ui（Phase 2） | — |
| 云端 API | Go 1.25 | Gin + y-ai-agent-base + Viper + slog | — |
| 云端 AI | Go（推理）/ Python（训练） | onnxer + go-forecaster + goarima + gonum | Prophet/ARIMA/VBGM/Bioenergetic/RL-DDPG |
| 数字孪生 | Go（推理）/ Python（训练） | onnxer + gonum | ST-GNN（D-TGCN/AEST-GNN） |
| 边缘控制器 | Go | periph.io + autopaho | YOLOv8n（RKNN/TensorRT） |
| 数据库 | — | InfluxDB 3 + PostgreSQL + Redis | — |
| 消息中间件 | — | EMQX（MQTT v5） | — |
| 部署 | — | Docker Compose / K8s Helm | — |

### 关键技术决策

| 决策 | 选择 | 依据 |
|------|------|------|
| 边缘 AI 平台 | RK3588（首选）/ Jetson Orin Nano（备选） | RK3588 ~100 FPS @ 6-10W，性价比最优 |
| YOLOv8n 部署 | ONNX → RKNN（INT8）/ TensorRT（FP16） | 平台原生加速，< 0.3 mAP 损失 |
| 投喂控制 | Mamdani 模糊 + PID 混合 | 已验证 66.5% MSE 降低 vs 纯 PID |
| MQTT 协议 | v5 + Protobuf + EMQX | v5 原生会话保持，Protobuf 压缩 3-5× |
| 时序 DB | InfluxDB 3（实时）+ PostgreSQL（业务） | 无需单一 DB 类型；ecosystem 复用 |
| ML 推理 | Python 训练 → ONNX 导出 → onnxer Go 推理 | 零 CGO，零 Python 生产依赖 |
| 数字孪生 GNN | D-TGCN（有向图）+ 动态邻接矩阵 | 水流方向物理先验，R²=0.884 |
| 前端框架 | Next.js 15 + shadcn/ui | 与 y-ai-gc 生态一致，Phase 2 |
| 部署方案 | Docker Compose（单机）/ Helm（K8s） | 分级部署，平滑迁移 |

---

## 9. 硬件方案

### 9.1 边缘端硬件选型

| 组件 | 型号 | 规格 | 备注 |
|------|------|------|------|
| 主控 | RK3588 | 8 核 ARM, 6 TOPS NPU, 16GB RAM | 首选，性价比最优 |
| 备选主控 | Jetson Orin Nano | 6 核 ARM, 40 TOPS GPU, 8GB RAM | NVIDIA 生态 |
| 传感器节点 | ESP32-S3 | Xtensa LX7, WiFi/BLE, 16MB Flash | pH/DO/Temp/NH3/Turbidity |
| 摄像头 | IMX415 + IR-CUT | 4K 30fps, MIPI CSI-2 | 水面 + 水下 |
| ADC | ADS1115 ×3 | 16-bit, I2C, 860 SPS | 通过 TCA9548A mux 扩展 |
| 温度探头 | DS18B20 | 1-Wire, -55~125°C, ±0.5°C | 防水封装 |
| 电机驱动 | IRLZ44N MOSFET ×3 | N-Channel, 55V/47A | 投喂/增氧/循环泵 |

### 9.2 传感器精度矩阵

| 参数 | 型号 | 量程 | 精度 (RMSE) | 校准周期 |
|------|------|------|------------|---------|
| pH | DFRobot SEN0169 | 0-14 | ±0.11 | 每周 3-point (4/7/10) |
| 溶解氧 | DFRobot SEN0237-A | 0-20 mg/L | ±0.22 mg/L | 每周饱和空气法 |
| 温度 | DS18B20 | -55~125°C | ±0.08°C | 每月数字温度计对比 |
| 氨氮 | Analog NH3 | 0-10 mg/L | ±0.014 mg/L | 每周标准液 |
| 浊度 | DFRobot SEN0189 | 0-3000 NTU | ±1.7 NTU | 每月 Formazin 标准液 |

### 9.3 成本分级

| 配置级别 | 单鱼塘成本 | 适用场景 |
|---------|-----------|---------|
| 基础版 | ¥3,858 | 水质监测 + 手动投喂，1 个传感器节点，单摄像头 |
| 标准版（推荐） | ¥5,852 | 全自动投喂控制，2 个传感器节点，双摄像头 |
| 专业版 | ¥24,337 | 标准版 + 氨氮 + 气象站 + 太阳能备电 + 双机热备 |
| 批量（10+） | ¥4,500~5,000/塘 | 传感器批量折扣 ~15% |

### 9.4 专业版双机热备

专业版采用两台 RK3588 工业整机，通过专用心跳线（UART）相互监控。主机每 100ms 发送 heartbeat，备机 3 次心跳超时后自动接管 GPIO 总线，切换为 Active 并发送 `ROLE_SWITCH` 事件。切换过程投喂控制不中断（< 50ms failover）。

**为什么专业版贵**：不是"快一点"，而是"不能停"。工业探头（雷磁/哈希同级）输出 RS-485 数字信号，内部完成 24-bit ADC + 温度补偿 + 线性化，可计量可追溯；荧光法溶氧仪免维护（3 年寿命 vs 极谱法 12-18 月换膜）；防爆配电箱（Ex d IIC T6）满足饲料粉尘环境合规要求；UPS + 太阳能保证无限续航。

---

## 10. AI 模型管线

### 10.1 统一训练-部署流水线

```
┌─────────────────────────────────────────────────────────────┐
│                 Python 离线训练环境                          │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐        │
│  │YOLOv8n   │ │时序预测   │ │RL-DDPG   │ │ST-GNN    │        │
│  │微调       │ │Prophet   │ │训练      │ │训练      │        │
│  └────┬─────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘        │
│       └────────────┴────────────┴────────────┘               │
│                         │                                    │
│                   Export ONNX                               │
└──────────────────────────┼──────────────────────────────────┘
                           │
┌─────────────────────────────────────────────────────────────┐
│                  Go 生产推理环境                              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ onnxer (Go ONNX Runtime, 零 CGO)                     │   │
│  │ model_registry (PostgreSQL)                          │   │
│  │ ┌──────────┐┌──────────┐┌──────────┐┌──────────┐     │   │
│  │ │Edge:     ││Cloud:    ││Cloud:    ││DT:       │     │   │
│  │ │YOLOv8n   ││Forecast  ││RL-DDPG   ││ST-GNN    │     │   │
│  │ │.rknn     ││.onnx     ││.onnx     ││.onnx     │     │   │
│  │ │RK3588    ││onnxer    ││onnxer    ││onnxer    │     │   │
│  │ └──────────┘└──────────┘└──────────┘└──────────┘     │   │
│  └──────────────────────────────────────────────────────┘   │
│  A/B Testing: active vs. staging → metrics → auto-promote   │
└─────────────────────────────────────────────────────────────┘
```

### 10.2 各模型管线细节

| 模型 | 训练（Python 离线） | 导出 | 推理（Go） | 关键指标 |
|------|-------------------|------|-----------|---------|
| YOLOv8n 检测器 | 微调（FeedFishDatas 7.4K images） | PT→ONNX→RKNN/TensorRT | RKNN NPU / TensorRT GPU | ~100 FPS，<15ms/帧 |
| Mamdani 模糊 PID | 规则人工设计 | — | 纯 Go 推理器 | 5 级隶属函数，25+ 规则，COG 去模糊，增量式 PID |
| 时序预测 | Prophet 风格 + SARIMAX | ONNX | go-forecaster + goarima | 24h 预测，置信区间 80%/95% |
| 生长模型 | VBGM + Bioenergetic 4.0 | — | gonum RK4 积分 | 105+ 物种参数库，误差 < 15% |
| RL 投喂 | Stable-Baselines3 DDPG | ONNX | onnxer | 多目标奖励，推理 < 1ms |
| ST-GNN | D-TGCN/AEST-GNN | ONNX | onnxer | 动态邻接矩阵，50 节点 < 50ms |

---

## 11. 数据存储设计

### 11.1 InfluxDB 3（时序）

| Measurement | Tags | Fields | Retention |
|------------|------|--------|-----------|
| `sensor_data` | farm_id, pond_id, sensor_type | ph, do, temp, nh3, turbidity, water_level | 90d hot / 365d cold |
| `camera_inference` | farm_id, pond_id | fish_count, sizes[], behavior_score, texture_energy | 30d |
| `feeding_decisions` | farm_id, pond_id | fuzzy_inputs, output_speed, output_duration, rules_fired | 365d |
| `device_health` | farm_id, pond_id, device_id | cpu, mem, temp, uptime, npu_usage | 90d |

### 11.2 PostgreSQL（业务数据）

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

### 11.3 Redis

- 设备影子缓存（reported/desired/delta）
- 告警去重（60s 窗口）
- 投喂决策缓存
- 消息队列

**分层存储的好处**：时序数据写入 InfluxDB（高吞吐、压缩、保留策略），业务数据写入 PostgreSQL（事务、关系、审计），热数据缓存到 Redis（低延迟）。三者各司其职，避免单一数据库在时序写入与事务一致性之间妥协。

---

## 12. 安全设计

### 12.1 认证与授权

- **JWT 认证**：HS256，Claims 含 user_id、role（admin/operator/viewer）、farm_ids[]（多农场权限）。
- **RBAC 中间件**：AuthRequired → FarmScope（检查 farm_id 归属）→ RoleRequired。
- **租户隔离**：跨 farm 查询返回 403，viewer 角色不能写设备。

### 12.2 传输安全

- **MQTT TLS**：X.509 证书加密。
- **HTTPS API**：生产环境不使用 HTTP。
- **API 限流**：令牌桶，100 req/min/IP，超限返回 429。

### 12.3 数据安全

- **参数化查询**：抵御 SQL 注入。
- **Protobuf 输入校验**：range/type check。
- **密钥管理**：不用明文存储密钥，Dockerfile 不含 secrets。

### 12.4 硬件安全互锁（不可覆盖）

- DO < 4.0 → 增氧泵强制 ON。
- 投喂电机过流 → 强制 STOP。
- 急停按钮 → 全部执行机构断电（纯硬件回路）。
- 云端指令无法覆盖硬件互锁。

### 12.5 安全测试

- fuzz testing：Protobuf 畸形消息、MQTT 非法 topic。
- gosec 加入 CI，零高危。
- 弱密码被 gosec 标记 HIGH。

---

## 13. 目录结构说明

```
y-ai-pond/
├── cmd/
│   ├── server/          # 云端 HTTP 服务入口
│   │   └── main.go
│   ├── edge/            # 边缘端控制器入口
│   │   └── main.go
│   └── migrate/         # 数据库迁移入口
│       └── main.go
├── internal/
│   ├── config/          # 服务端配置派生
│   ├── handler/         # HTTP Handler
│   │   ├── farm.go
│   │   ├── device.go
│   │   ├── sensor.go
│   │   ├── feeding.go
│   │   ├── alert.go
│   │   ├── dashboard.go
│   │   ├── dt.go        # 数字孪生 API
│   │   ├── recommend.go
│   │   └── stream.go    # SSE/WebSocket 实时推送
│   ├── middleware/       # HTTP 中间件
│   │   ├── auth.go      # JWT + RBAC + FarmScope
│   │   └── ratelimit.go
│   ├── mqtt/            # 云端 MQTT Gateway
│   │   └── gateway.go
│   └── server/          # 服务器生命周期 + 路由注册
├── pkg/
│   ├── proto/           # Protobuf 生成代码 (buf)
│   │   ├── sensor.pb.go
│   │   ├── camera.pb.go
│   │   └── control.pb.go
│   ├── mqtt/            # MQTT Client 封装
│   │   └── client.go
│   ├── store/           # 数据库层
│   │   ├── influx.go    # InfluxDB 3
│   │   ├── postgres.go  # PostgreSQL
│   │   ├── redis.go     # Redis
│   │   └── migrations/  # SQL 迁移
│   ├── edge/            # 边缘端核心
│   │   ├── detector/    # YOLOv8n 推理
│   │   ├── texture/     # 纹理提取 & 摄食强度
│   │   ├── fuzzypid/    # Mamdani Fuzzy-PID
│   │   ├── controller/  # 主控制循环
│   │   ├── safety/      # 安全互锁
│   │   └── hal/         # 传感器/执行器 HAL
│   ├── cloud/           # 云端核心
│   │   ├── alert/       # 告警引擎
│   │   ├── shadow/      # 设备影子
│   │   ├── scheduler/   # 集群调度
│   │   ├── realtime/    # SSE/WebSocket
│   │   ├── forecast/    # 时序预测
│   │   ├── growth/      # 生长模型
│   │   ├── rl/          # RL 推理 (ONNX)
│   │   ├── recommend/   # AI 推荐引擎
│   │   ├── modelmgr/    # 模型注册表
│   │   ├── report/      # 报表生成
│   │   └── twin/        # 水体物理仿真
│   └── dt/              # 数字孪生
│       ├── gnn/         # ST-GNN 推理
│       ├── micro/       # μDT 边缘微仿真
│       ├── scenario/    # 场景推演
│       └── visual/      # 可视化
├── proto/               # Protobuf 定义文件
│   ├── sensor.proto
│   ├── camera.proto
│   └── control.proto
├── python/              # Python 离线训练 (不部署生产)
│   ├── rl/              # RL 训练
│   │   ├── feeding_env.py
│   │   └── train_ddpg.py
│   └── dt/              # GNN 训练
│       └── train_stgnn.py
├── tools/               # 开发工具脚本
│   └── convert_yolo.py  # YOLOv8n PT→ONNX→RKNN/TensorRT
├── config/              # 配置文件
│   ├── config.yaml
│   ├── config.docker.yaml
│   └── config.edge.docker.yaml
├── integration/         # 集成测试
├── benchmark/           # 性能测试
├── security/            # 安全测试
├── doc/                 # 文档
├── k8s/                 # K8s Helm Chart
├── Dockerfile
├── Dockerfile.edge
├── docker-compose.yml
├── docker-compose.edge.yml
├── Makefile
├── go.mod
└── go.sum
```

---

## 14. 快速开始（开发环境）

### 14.1 前置依赖

- **Go 1.25**（go.mod 使用 `go 1.25.0` 指令，GOTOOLCHAIN=auto 可自动下载）。
- **Docker**（用于 EMQX、InfluxDB 3、PostgreSQL、Redis 容器）。
- **buf**（Protobuf 代码生成；若未安装可用 protoc + protoc-gen-go 替代）。

### 14.2 依赖同级目录 y-ai-agent-base

本项目依赖 `github.com/anrror/y-ai-agent-base`，该模块位于同级目录 `../y-ai-agent-base`。本地开发需通过 `go mod edit -replace` 指向它：

```powershell
# 在项目根目录执行
go mod edit -replace github.com/anrror/y-ai-agent-base=../y-ai-agent-base
go mod tidy
```

### 14.3 构建

```powershell
# 构建云端服务
go build ./cmd/server/

# 构建边缘端控制器
go build ./cmd/edge/
```

### 14.4 启动依赖服务（Docker）

```powershell
# 启动 EMQX + InfluxDB 3 + PostgreSQL + Redis
docker compose up -d
```

> 注意：需要 Docker。若本机无 Docker，可用 mock 单元测试替代容器集成测试（计划已列出 mock broker 测试作为主要验收方式）。

### 14.5 运行

```powershell
# 启动云端服务（:8080）
go run ./cmd/server/

# 验证健康检查
curl http://localhost:8080/health

# 启动边缘端控制器
go run ./cmd/edge/
```

### 14.6 常用开发命令

```powershell
# 生成 Protobuf 代码
buf generate

# 运行测试
go test ./...

# 静态检查
golangci-lint run ./...
```

---

## 15. 部署方式

### 15.1 Docker Compose（单机，推荐）

`docker-compose.yml` 一键启动全部服务：Go server + EMQX + InfluxDB 3 + PostgreSQL+TimescaleDB + Redis + Gotenberg（可选 ClickHouse）。所有服务带 HEALTHCHECK，`depends_on` 保证启动顺序。

```powershell
docker compose up -d
docker compose exec server curl /health   # 验证 200
docker compose down                        # 优雅关闭
```

边缘端独立部署：`docker compose -f docker-compose.edge.yml up`（edge controller + local MQTT）。

### 15.2 Kubernetes（多基地，可选）

`k8s/` 目录提供 Helm Chart：server（Deployment + Service + Ingress）、emqx（StatefulSet）、influxdb3（StatefulSet + PVC）、postgresql（StatefulSet + PVC）、redis（Deployment）。支持 HPA 自动伸缩（CPU > 70% → scale）、Readiness/Liveness Probe、资源限制。

```bash
helm install y-ai-pond ./k8s/
kubectl port-forward svc/server 8080:8080
helm test y-ai-pond
```

### 15.3 边缘端部署

RK3588 刷写 Ubuntu 22.04 + RKNN SDK，Go 边缘控制器交叉编译（ARM64）部署到 eMMC，ESP32-S3 烧写传感器采集固件。详见 `doc/edge-setup.md`。

---

## 16. 文档索引

| 文档 | 位置 | 内容 |
|------|------|------|
| 产品设计图 | `doc/01-产品设计图.md` | 产品定位、系统全景、用户旅程、功能矩阵 |
| 技术架构图 | `doc/02-技术架构图.md` | 三态架构、MQTT 主题、数据流、代码架构、Schema |
| 可执行方案 | `doc/03-可执行方案.md` | 执行波次、任务分解、依赖矩阵、技术决策、风险 |
| 硬件设计说明书 | `doc/04-硬件设计说明书.md` | 硬件架构、选型、接线、校准、BOM、装配 |
| 边缘端成本清单 | `doc/05-边缘端成本清单.md` | 三级配置成本、批量折扣、运行成本、竞品对比 |
| 专业版硬件配置清单 | `doc/06-边缘端专业版硬件配置清单.md` | 专业版逐项清单、双机热备、工业级差异解读 |
| API 接口文档 | `doc/api.md` | OpenAPI 3.0.3 完整规范，27 个端点 |
| 技术架构专题 | `doc/architecture.md` | 架构决策记录、数据流、部署拓扑 |
| 开发者指南 | `doc/developer-guide.md` | 开发环境搭建、代码规范、测试方法、调试技巧 |
| 边缘端部署指南 | `doc/edge-setup.md` | RK3588 刷写、交叉编译、传感器接线、故障排查 |
| 用户手册 | `doc/user-guide.md` | 农场管理、设备注册、投喂配置、告警查看、FAQ |
| 运维指南 | `doc/ops-guide.md` | 监控指标、备份恢复、扩容指南、故障排查 |
| 模型训练指南 | `doc/model-training-guide.md` | YOLOv8n/RL/GNN 训练、ONNX 导出、模型注册上线 |
| 硬件测试计划 | `doc/hardware-test-plan.md` | RK3588/Jetson/ESP32 适配测试、7 天稳定性测试 |
| 数字孪生架构 | `doc/dt-architecture.md` | μDT 分布式架构、边缘部署、性能评估 |
| 工作规划 | `.omo/plans/y-ai-pond.md` | 40 个任务的完整实现细节（权威素材） |

---

## 17. 贡献指南

### 17.1 开发环境搭建

```powershell
# 1. 克隆仓库
git clone https://github.com/anrror/y-ai-pond.git
cd y-ai-pond

# 2. 配置同级依赖（如未自动 clone y-ai-agent-base）
go mod edit -replace github.com/anrror/y-ai-agent-base=../y-ai-agent-base
go mod tidy

# 3. 安装开发工具
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest  # 静态检查
go install github.com/bufbuild/buf/cmd/buf@latest                      # Protobuf 代码生成

# 4. 验证环境
make ci       # 构建 + 测试 + lint + vet 全部通过
make proto    # Protobuf 代码生成
```

### 17.2 代码规范

参见 [AGENTS.md](./AGENTS.md) 完整规范。核心要求：

- **Go 1.25**：强制使用 `go 1.25.0` 工具链。
- **零 `any`**：不使用 `interface{}` 或 `any`，所有类型显式声明。
- **库代码无 panic**：`pkg/` 下公开库函数错误必须通过返回值传播。
- **无全局状态**：所有组件通过构造函数注入依赖。
- **接口驱动**：编译时断言 `var _ Interface = (*Impl)(nil)`。
- **零 CGO**：推理通过 onnxer（纯 Go ONNX Runtime）完成。
- **golangci-lint 零错误**：提交前必须运行 `make lint` 并确保零问题。
- **结构化日志**：使用 `log/slog`，包含 key-value 对。

### 17.3 提交规范

使用 **Conventional Commits** 格式：

```
type(scope): description
```

| 类型 | 说明 | 示例 |
|------|------|------|
| `feat` | 新功能 | `feat(edge): add YOLOv8n NPU inference pipeline` |
| `fix` | Bug 修复 | `fix(mqtt): handle broker disconnect reconnect loop` |
| `docs` | 文档更新 | `docs: add API reference for sensor endpoints` |
| `test` | 测试相关 | `test(cloud): add integration test for forecast engine` |
| `security` | 安全加固 | `security: fix JWT token validation bypass` |
| `perf` | 性能优化 | `perf(store): batch insert InfluxDB 10K points` |
| `refactor` | 重构 | `refactor(handler): extract common pagination logic` |
| `deploy` | 部署配置 | `deploy(k8s): add HPA autoscaling` |
| `ci` | CI/CD 配置 | `ci: add security scan to GitHub Actions` |

**scope** 按模块划分：`edge` / `cloud` / `dt` / `mqtt` / `store` / `api` / `k8s` 等。

- **一次提交一个任务**：不攒多个任务在一个 commit 中。
- **提交信息中英文均可**，优先英文。

### 17.4 测试要求

- **TDD**：先写测试、看到失败、再写实现。
- **竞态检测**：`go test -race` 必须通过。
- **Mock 优先**：使用 `MockSensor`、`MockActuator`、`pgxmock` 替代真实硬件/DB。
- **集成测试**：`integration/` 目录使用 mochi-mqtt 内存 broker，不依赖 Docker。
- **关键路径必须覆盖**：覆盖率不强制 100%，但核心业务逻辑必须有测试。

### 17.5 PR 流程

1. Fork 仓库，创建 feature 分支（命名：`feat/short-description` 或 `fix/issue-number`）。
2. 实现功能 + 编写测试 + 确保 `make ci` 全部通过。
3. 提交 PR，在描述中说明：
   - 改动内容与动机
   - 测试结果（`go test -race ./...` 输出摘要）
   - 破坏性变更说明（如有）
4. 至少一位 reviewer 批准后合并到 `main`。
5. 合并后 GitHub Actions 自动触发 CI 流水线。

### 17.6 设计原则

- **零 Python 生产依赖**：Python 仅用于离线训练（`python/` 目录），生产环境 Go onnxer 推理。
- **边缘端独立运行**：MQTT 断开时边缘端仍可本地推理 + SQLite 缓冲，云端不可用时系统不崩溃。
- **硬件安全互锁不可覆盖**：DO < 4.0 → 增氧泵强制 ON；急停按钮 → 全部断电。云端指令无法覆盖。
- **Protobuf 序列化**：边缘-云端数据交换统一使用 Protobuf（MQTT payload）。
- **buf 代码生成**：不手动编辑 `*.pb.go` 文件。

---

## 附录：关键性能指标

| 指标 | 目标 | 测量方法 |
|------|------|---------|
| YOLOv8n 推理延迟 | < 15ms（RK3588 NPU） | `benchmark/` |
| 投喂控制响应 | < 5ms（PWM 输出） | 示波器 GPIO toggle |
| MQTT 端到端延迟 | < 200ms | `mosquitto_sub` timestamp diff |
| 云端 API p95 | < 200ms（1000 req/s） | `wrk` / `hey` |
| InfluxDB 写入吞吐 | > 10K pts/s | `influxdb3-go` batch benchmark |
| 边缘离线持续 | > 7 天（SQLite local） | 离线测试 |
| ST-GNN 推理（50 节点） | < 50ms | `onnxer` benchmark |
| 传感器精度 MAPE | < 5% | 校准仪器对比 |

---

*本文档由 y-ai-pond 项目维护。技术细节以 `.omo/plans/y-ai-pond.md` 与 `doc/` 下文档为准。*
