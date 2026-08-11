# y-ai-pond · 边缘端部署指南（Edge Setup）

> **版本**: v1.0 | **日期**: 2026-08-11 | **作者**: y-ai-pond 项目组
> **目标**: 按步骤完成 RK3588 边缘端部署（刷写 + 交叉编译 + 摄像头 + 传感器校准 + MQTT 配置）

---

## 1. 硬件清单

| 组件 | 型号 | 规格 | 数量 |
|------|------|------|------|
| 主控 | RK3588 | 8 核 ARM, 6 TOPS NPU, 16GB RAM | 1 |
| 摄像头 | IMX415 + IR-CUT | 4K 30fps, MIPI CSI-2 | 1-2 |
| 传感器节点 | ESP32-S3 | Xtensa LX7, WiFi/BLE, 16MB Flash | 1+ |
| pH 探头 | DFRobot SEN0169 | 0-14, ±0.11 | 1 |
| 溶解氧探头 | DFRobot SEN0237-A | 0-20 mg/L, ±0.22 mg/L | 1 |
| 温度探头 | DS18B20 | -55~125°C, ±0.08°C | 1 |
| 氨氮探头 | Analog NH3 | 0-10 mg/L, ±0.014 mg/L | 1 |
| 浊度探头 | DFRobot SEN0189 | 0-3000 NTU, ±1.7 NTU | 1 |
| ADC | ADS1115 ×3 | 16-bit, I2C, 860 SPS | 3 |
| I2C 多路复用器 | TCA9548A | 8 通道 | 1 |
| 电机驱动 | IRLZ44N MOSFET ×3 | N-Channel, 55V/47A | 3 |

---

## 2. RK3588 系统刷写

### 2.1 准备刷写工具

1. 下载 **RK3588 Ubuntu 22.04 镜像**（官方或第三方，如 Radxa / FriendlyElec 提供的镜像）。
2. 下载 **RKDevTool**（Windows）或 **upgrade_tool**（Linux）刷写工具。
3. 准备一张 **microSD 卡**（≥ 16GB）或使用 eMMC。

### 2.2 刷写步骤

**方式一：microSD 卡刷写（推荐，便于恢复）**

```bash
# Linux 下使用 dd 写入镜像
sudo dd if=rk3588-ubuntu-22.04.img of=/dev/sdX bs=4M status=progress
sync
```

**方式二：eMMC 刷写（RKDevTool，Windows）**

1. 将 RK3588 进入 **MaskROM 模式**（按住 Recovery 键上电）。
2. 打开 RKDevTool，加载镜像文件。
3. 点击"执行"开始刷写。

### 2.3 首次启动配置

```bash
# 连接显示器/串口，进入系统后
sudo apt update && sudo apt upgrade -y

# 安装基础工具
sudo apt install -y git curl build-essential

# 安装 Go 1.25（ARM64）
wget https://go.dev/dl/go1.25.0.linux-arm64.tar.gz
sudo tar -C /usr/local -xzf go1.25.0.linux-arm64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc
go version   # 期望 go1.25.0 linux/arm64
```

### 2.4 安装 RKNN SDK

RK3588 的 NPU 推理需要 RKNN SDK 与 RKNN Runtime：

```bash
# 安装 RKNN Runtime（ARM64）
# 从 Rockchip 官方仓库下载 rknn-toolkit2 的 runtime 包
sudo dpkg -i rknn-runtime_*.deb

# 验证 NPU 可用
ls /dev/rknpu   # 期望存在 rknpu 设备节点
```

> **注意**：YOLOv8n 模型需通过 `tools/convert_yolo.py` 转换为 RKNN 格式（INT8 量化）后部署到 RK3588 NPU。

---

## 3. Go 交叉编译

在开发机（x86_64）上交叉编译边缘端控制器为 ARM64 二进制。

### 3.1 设置交叉编译环境变量

```bash
# Linux/macOS
export GOOS=linux
export GOARCH=arm64
export CGO_ENABLED=0   # 纯 Go，无 cgo，避免交叉编译复杂性

# Windows PowerShell
$env:GOOS = "linux"
$env:GOARCH = "arm64"
$env:CGO_ENABLED = "0"
```

### 3.2 交叉编译

```bash
# 编译边缘端控制器
go build -o bin/edge-controller-arm64 ./cmd/edge/

# 编译云端服务（可选，若需在 ARM 上运行）
go build -o bin/server-arm64 ./cmd/server/
```

### 3.3 部署到 RK3588

```bash
# 通过 scp 拷贝二进制到 RK3588
scp bin/edge-controller-arm64 user@rk3588-ip:/opt/y-ai-pond/

# 在 RK3588 上赋予执行权限
ssh user@rk3588-ip
chmod +x /opt/y-ai-pond/edge-controller-arm64
```

### 3.4 配置 systemd 服务（可选）

创建 `/etc/systemd/system/y-ai-pond-edge.service`：

```ini
[Unit]
Description=y-ai-pond Edge Controller
After=network.target

[Service]
ExecStart=/opt/y-ai-pond/edge-controller-arm64
Restart=always
RestartSec=5
Environment=POND_CONFIG=/opt/y-ai-pond/config.yaml

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable y-ai-pond-edge
sudo systemctl start y-ai-pond-edge
```

---

## 4. 摄像头接线（IMX415 MIPI CSI-2）

### 4.1 硬件接线

IMX415 摄像头通过 **MIPI CSI-2** 接口连接到 RK3588：

| IMX415 引脚 | RK3588 接口 | 说明 |
|------------|-------------|------|
| MIPI CSI-2 数据 | CSI-2 RX 通道 | 4-lane MIPI |
| I2C SCL | I2C 时钟 | 摄像头配置 |
| I2C SDA | I2C 数据 | 摄像头配置 |
| MCLK | 主时钟 | 24MHz |
| 3.3V | 电源 | 摄像头供电 |
| GND | 地 | 接地 |

> **注意**：接线前务必断电。MIPI 接口方向性敏感，请参考 RK3588 开发板原理图确认引脚定义。

### 4.2 驱动验证

```bash
# 检查摄像头设备节点
ls /dev/video*

# 使用 v4l2 验证摄像头
v4l2-ctl --list-devices
v4l2-ctl -d /dev/video0 --list-formats-ext
```

### 4.3 图像采集测试

```bash
# 采集一帧图像
v4l2-ctl -d /dev/video0 --set-fmt-video=width=3840,height=2160,pixelformat=NV12 --stream-mmap --stream-count=1 --stream-to=frame.raw

# 使用 GStreamer 预览（边缘控制器内部使用 GStreamer 预处理）
gst-launch-1.0 v4l2src device=/dev/video0 ! video/x-raw,width=3840,height=2160 ! videoconvert ! autovideosink
```

---

## 5. 传感器校准

### 5.1 pH 探头校准（3-point 法）

pH 探头（SEN0169）需每周校准，使用标准缓冲液 4.0 / 7.0 / 10.0：

1. **准备**：3 杯标准缓冲液（pH 4.0、7.0、10.0），温度稳定在 25°C。
2. **清洗**：用去离子水冲洗探头，吸干。
3. **校准**：
   - 将探头浸入 pH 7.0 缓冲液，等待读数稳定，记录 ADC 值。
   - 浸入 pH 4.0 缓冲液，记录 ADC 值。
   - 浸入 pH 10.0 缓冲液，记录 ADC 值。
4. **计算**：三点拟合线性关系 `pH = a × ADC + b`，更新 `sensor_config` 表中的 `calibration_coeffs`。
5. **验证**：用已知 pH 的样品验证，误差应在 ±0.11 内。

### 5.2 溶解氧探头校准（饱和空气法）

溶解氧探头（SEN0237-A）需每周校准，使用饱和空气法：

1. **准备**：将探头置于饱和空气环境（如湿毛巾包裹，暴露于空气中）30 分钟。
2. **记录**：读取当前温度下的饱和溶解氧值（查表，如 25°C 时约 8.26 mg/L）。
3. **校准**：将探头读数调整为饱和值，更新校准系数。
4. **验证**：将探头放入已知 DO 的水样中验证，误差应在 ±0.22 mg/L 内。

### 5.3 温度探头校准

温度探头（DS18B20）每月校准，使用标准数字温度计对比：

1. 将 DS18B20 与标准温度计放入同一水样。
2. 等待读数稳定，对比两者差值。
3. 若偏差超过 ±0.08°C，调整校准偏移。

### 5.4 氨氮探头校准

氨氮探头（Analog NH3）每周校准，使用标准液：

1. 准备已知浓度的氨氮标准液（如 0.1 / 0.5 / 1.0 mg/L）。
2. 依次浸入，记录 ADC 值。
3. 拟合线性关系，更新校准系数。

### 5.5 浊度探头校准

浊度探头（SEN0189）每月校准，使用 Formazin 标准液：

1. 准备已知浊度的 Formazin 标准液（如 0 / 100 / 800 NTU）。
2. 依次浸入，记录 ADC 值。
3. 拟合线性关系，更新校准系数。

### 5.6 校准数据存储

校准系数存储在 PostgreSQL `sensor_config` 表的 `calibration_coeffs`（JSONB）字段：

```json
{
  "ph": { "a": 0.018, "b": -1.2 },
  "do": { "offset": 0.15 },
  "temp": { "offset": 0.05 },
  "nh3": { "a": 0.002, "b": 0.01 },
  "turbidity": { "a": 0.5, "b": -10 }
}
```

---

## 6. MQTT 配置

### 6.1 云端 EMQX Broker 配置

`docker-compose.yml` 中的 EMQX 服务：

```yaml
emqx:
  image: emqx/emqx:5.8
  ports:
    - "1883:1883"    # MQTT
    - "8083:8083"    # WebSocket MQTT
    - "18083:18083"  # Dashboard
  environment:
    EMQX_NAME: y-ai-pond-broker
    EMQX_ALLOW_ANONYMOUS: "true"
```

### 6.2 边缘端 MQTT 客户端配置

边缘端 `config.yaml` 中的 MQTT 配置：

```yaml
mqtt:
  broker_url: "tcp://<cloud-ip>:1883"   # 云端 EMQX 地址
  client_id: "y-ai-pond-edge-001"
  keepalive: 20        # 秒
  session_expiry: 3600 # 秒（1h）
```

### 6.3 MQTT 主题订阅/发布

边缘端发布（上报）：

| 主题 | 载荷 | QoS |
|------|------|-----|
| `pond/v1/{farm_id}/{pond_id}/sensor/water/ph` | float32 | 0 |
| `pond/v1/{farm_id}/{pond_id}/sensor/water/do` | float32 | 0 |
| `pond/v1/{farm_id}/{pond_id}/sensor/water/temperature` | float32 | 0 |
| `pond/v1/{farm_id}/{pond_id}/sensor/water/nh3` | float32 | 0 |
| `pond/v1/{farm_id}/{pond_id}/sensor/water/turbidity` | float32 | 0 |
| `pond/v1/{farm_id}/{pond_id}/sensor/water/level` | float32 | 0 |
| `pond/v1/{farm_id}/{pond_id}/camera/inference` | Protobuf | 0 |
| `pond/v1/{farm_id}/{pond_id}/control/feeding/status` | JSON | 1 |
| `pond/v1/{farm_id}/{pond_id}/device/status` | JSON | 1 |
| `pond/v1/{farm_id}/{pond_id}/device/alarm` | JSON | 1 |

边缘端订阅（接收指令）：

| 主题 | 载荷 | QoS |
|------|------|-----|
| `cloud/{farm_id}/{pond_id}/cmd/feeding/start` | JSON | 1 |
| `cloud/{farm_id}/{pond_id}/cmd/feeding/stop` | JSON | 1 |
| `cloud/{farm_id}/{pond_id}/cmd/aerator/on` | JSON | 1 |
| `cloud/{farm_id}/{pond_id}/cmd/aerator/off` | JSON | 1 |
| `cloud/{farm_id}/{pond_id}/config/fuzzy/update` | JSON | 2 |
| `cloud/{farm_id}/{pond_id}/config/sensor/interval` | JSON | 2 |
| `cloud/{farm_id}/{pond_id}/model/update` | JSON | 2 |

### 6.4 MQTT TLS 配置（生产环境）

生产环境启用 TLS 加密：

```yaml
mqtt:
  broker_url: "tls://<cloud-ip>:8883"   # TLS 端口
  # 客户端证书配置（X.509）
```

EMQX 侧需配置 TLS 证书（CA + 服务端证书），并关闭匿名访问。

### 6.5 验证 MQTT 连接

```bash
# 使用 mosquitto 客户端验证
mosquitto_sub -h <cloud-ip> -p 1883 -t "pond/v1/+/+/sensor/#" -v

# 发布测试数据
mosquitto_pub -h <cloud-ip> -p 1883 -t "pond/v1/frm_001/pnd_001/sensor/water/ph" -m "7.4"
```

---

## 7. 边缘端启动验证

### 7.1 启动边缘控制器

```bash
# 在 RK3588 上
cd /opt/y-ai-pond
./edge-controller-arm64
```

### 7.2 验证项

| 验证项 | 方法 | 期望结果 |
|--------|------|---------|
| 摄像头 | `v4l2-ctl --list-devices` | 识别 IMX415 |
| NPU | `ls /dev/rknpu` | 设备节点存在 |
| 传感器 | 边缘控制器日志 | 各传感器读数正常 |
| MQTT | `mosquitto_sub` | 收到传感器数据 |
| 云端 | 云端 `/health` | 组件健康 |

---

## 8. 故障排查

### 8.1 摄像头无图像

- 检查 MIPI 接线方向与引脚定义。
- 确认驱动已加载：`dmesg | grep -i camera`。
- 确认设备节点存在：`ls /dev/video*`。

### 8.2 NPU 推理失败

- 确认 RKNN Runtime 已安装：`dpkg -l | grep rknn`。
- 确认模型为 RKNN 格式（INT8 量化）。
- 检查 `/dev/rknpu` 设备节点。

### 8.3 MQTT 连接失败

- 确认云端 EMQX 已启动：`docker compose ps`。
- 确认 `broker_url` 地址与端口正确。
- 检查网络连通性：`ping <cloud-ip>`、`nc -zv <cloud-ip> 1883`。

### 8.4 传感器读数异常

- 检查传感器接线与供电。
- 重新校准传感器。
- 检查 `sensor_config` 表中的校准系数。

---

*本文档由 y-ai-pond 项目维护。技术细节以 `.omo/plans/y-ai-pond.md` 与 `doc/` 下文档为准。*
