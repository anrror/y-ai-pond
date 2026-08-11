# y-ai-pond · AI 模型训练指南（Model Training Guide）

> **版本**: v1.0 | **日期**: 2026-08-11 | **作者**: y-ai-pond 项目组
> **适用对象**: AI 工程师、模型训练人员
> **配套文档**: [用户手册](user-guide.md) | [运维指南](ops-guide.md) | [API 文档](api.md)

---

## 1. 训练环境准备

### 1.1 重要说明：Python 仅用于离线训练

> **关键原则**：y-ai-pond 的**生产环境不运行 Python 后端**。Python 仅用于**离线训练**，训练完成后将模型导出为 **ONNX** 格式，由 Go 端通过 `onnxer`（零 CGO 的 Go ONNX Runtime）加载推理。这保证了生产环境零 Python 运行时依赖。

### 1.2 训练环境

训练在**独立的离线环境**（开发机 / GPU 服务器）进行，不部署到生产：

| 依赖 | 用途 |
|------|------|
| Python 3.10+ | 训练脚本运行环境 |
| PyTorch | 深度学习框架 |
| ultralytics | YOLOv8n 微调 |
| stable-baselines3 | RL（DDPG）训练 |
| gymnasium | RL 环境定义 |
| onnx / onnxruntime | ONNX 导出与验证 |
| numpy | 数值计算 |

**安装依赖**：

```bash
# 安装通用依赖
pip install torch ultralytics onnx onnxruntime numpy

# 安装 RL 训练依赖
pip install stable-baselines3 gymnasium

# 安装 RKNN SDK（仅 RK3588 目标平台，Linux aarch64）
pip install rknn-toolkit2
```

### 1.3 训练脚本位置

| 脚本 | 路径 | 用途 |
|------|------|------|
| YOLOv8n 转换 | `tools/convert_yolo.py` | PT → ONNX → RKNN/TensorRT |
| RL 训练 | `python/rl/train_ddpg.py` | DDPG 投喂策略训练 → ONNX |
| GNN 训练 | `python/dt/train_stgnn.py` | ST-GNN 水质模型训练 → ONNX |

---

## 2. 数据准备

### 2.1 YOLOv8n 微调数据集

用于鱼群检测的微调数据集（参考 FeedFishDatas，约 7.4K 张图像）：

- **图像**：水面 + 水下摄像头拍摄的鱼群图像，覆盖不同光照、鱼群密度、水质条件。
- **标注**：每张图像标注鱼群边界框（bbox）与类别。
- **划分**：训练集 / 验证集 / 测试集（建议 8:1:1）。
- **格式**：YOLO 格式（`images/` + `labels/`，每张图对应一个 `.txt` 标注文件）。

**数据要求**：
- 覆盖不同鱼群密度（10 / 50 / 100 条鱼场景）。
- 覆盖不同光照与反光条件（抗反光是系统设计目标）。
- 标注质量直接影响模型精度，需人工校验。

### 2.2 时序数据（预测 / 生长模型）

用于时序预测与生长模型的数据来自系统积累的历史数据：

- **传感器时序数据**：pH / DO / Temp / NH3 / Turbidity / 水位，按时间戳记录。
- **投喂日志**：投喂时间、转速、时长、决策详情。
- **生长测量**：定期采样的鱼体平均体重、体长。
- **环境数据**：气温、气压、降雨（气象站）。

**数据要求**：
- 时序预测对 < 7 天数据拒绝训练，避免过拟合。
- 数据需清洗（去除异常值、缺失值处理）。
- 特征工程：聚合窗口（1m / 5m / 1h / 1d）。

### 2.3 RL 环境数据

RL 训练使用 `python/rl/feeding_env.py` 定义的投喂环境（Gym）：

- **状态（state）**：`[DO, Temp, NH3, FishWeight, FCR]`（5 维）。
- **动作（action）**：`[feeding_rate]`（投喂率）。
- **奖励（reward）**：多目标奖励 = FCR 改善 × 0.4 + 水质稳定 × 0.3 + 能耗降低 × 0.3。

RL 训练基于历史数据构建环境，模拟投喂决策的长期影响。

---

## 3. YOLOv8n 微调

### 3.1 微调流程

1. **准备数据集**：按第 2.1 节准备鱼群检测数据集。
2. **微调 YOLOv8n**：使用 ultralytics 在数据集上微调 YOLOv8n（nano 版本）。
3. **导出 ONNX**：使用 `tools/convert_yolo.py` 将微调模型转换为 ONNX。
4. **转换为平台格式**：RK3588 → RKNN（INT8），Jetson → TensorRT（FP16）。

### 3.2 模型转换（tools/convert_yolo.py）

`tools/convert_yolo.py` 实现完整的转换流水线：

```
PT (.pt) → ONNX (.onnx) → RKNN (.rknn)   [RK3588 NPU, INT8]
PT (.pt) → ONNX (.onnx) → TensorRT (.engine)  [Jetson GPU, FP16]
```

**导出 ONNX**：

```bash
python tools/convert_yolo.py --platform onnx --weights yolov8n.pt --output yolov8n.onnx
```

**转换为 RKNN（RK3588）**：

```bash
# 在 RK3588 开发板上执行（RKNN SDK 仅支持 Linux aarch64）
python tools/convert_yolo.py --platform rk3588 --weights yolov8n.onnx --output yolov8n.rknn --quantize --calib-dataset ./calib_images/
```

> **注意**：`--quantize` 启用 INT8 量化，需要 `--calib-dataset` 提供校准图像目录。INT8 量化相比 FP32 仅损失 < 0.3 mAP，但推理速度提升数倍。

**转换为 TensorRT（Jetson）**：

```bash
# 在 Jetson 上执行（trtexec 预装在 JetPack）
python tools/convert_yolo.py --platform jetson --weights yolov8n.onnx --output yolov8n_fp16.engine
```

### 3.3 部署到边缘端

转换后的模型部署到边缘端：

- **RK3588**：部署到 `/data/models/yolov8n.rknn`，Go 端使用 `pkg/edge/detector.NewRKNBackend()`。
- **Jetson**：部署到 `/data/models/yolov8n_fp16.engine`，Go 端使用 `pkg/edge/detector.NewTensorRTBackend()`。

**性能目标**：RK3588 NPU 上约 100 FPS，单帧延迟 < 15ms（目标 < 10ms）。

---

## 4. RL 训练（DDPG）

### 4.1 训练脚本

RL 投喂策略使用 `python/rl/train_ddpg.py` 训练 DDPG 策略，并导出 ONNX。

**快速训练（CI / 测试）**：

```bash
python python/rl/train_ddpg.py --timesteps 10000 --export-onnx model.onnx
```

**生产训练（完整预算）**：

```bash
python python/rl/train_ddpg.py --timesteps 200000 --export-onnx model.onnx --seed 42 --lr 1e-4
```

### 4.2 训练参数

| 参数 | 默认 | 说明 |
|------|------|------|
| `--timesteps` | 10000 | 训练步数（生产建议 200000） |
| `--seed` | 42 | 随机种子（可复现） |
| `--lr` | 1e-3 | 学习率 |
| `--buffer-size` | 100000 | 回放缓冲区大小 |
| `--batch-size` | 128 | 训练批次大小 |
| `--export-onnx` | 无 | 导出 ONNX 路径 |
| `--log-dir` | ./ddpg_logs | TensorBoard 日志目录 |

### 4.3 ONNX 导出

训练完成后，脚本将 DDPG actor 网络导出为 ONNX：

- **输入**：`[batch, 5]` float32（state = [DO, Temp, NH3, FishWeight, FCR]）。
- **输出**：`[batch, 1]` float32（feeding_rate）。
- **opset**：17（默认，最小 14）。

脚本会自动验证 ONNX 模型（结构检查 + 推理测试）。

### 4.4 Go 端推理

导出的 ONNX 模型由 Go 端 `pkg/cloud/rl/ONNXPolicy` 加载推理：

```go
policy := rl.NewONNXPolicy()
policy.LoadModel("model.onnx")
rate, _ := policy.Predict([]float64{7.5, 25.0, 0.1, 500.0, 1.5})
```

推理延迟 < 1ms。

---

## 5. GNN 训练（ST-GNN）

### 5.1 训练脚本

数字孪生的 ST-GNN 水质模型使用 `python/dt/train_stgnn.py` 训练，实现 D-TGCN 风格架构（有向图 + 动态邻接矩阵 + GRU 门控）。

**训练**：

```bash
python python/dt/train_stgnn.py --epochs 50 --nodes 6 --timesteps 720 --out models/stgnn.onnx
```

### 5.2 模型架构

- **输入特征**（每节点 8 维）：pH, DO, Temp, NH3, Turbidity, AirTemp, Pressure, Rainfall。
- **输出**：多步 DO 预测（1h / 6h / 24h 三个时域）。
- **动态邻接矩阵**：水流/管道连通性变化时，边权重动态更新（`FlowState` 物化）。
- **节点**：池塘监测站；**边**：水流方向/管道连通（上游 → 下游）。

### 5.3 ONNX 导出

训练完成后导出 ONNX：

- **输入**：`sensor_matrix`（batch, nodes, 8）+ `adjacency`（nodes, nodes）。
- **输出**：`predictions`（batch, nodes, 3）。
- **opset**：13。

### 5.4 Go 端推理

导出的 ONNX 模型由 Go 端 `pkg/dt/gnn` 加载推理。推理前会进行动态邻接矩阵预处理（将上游影响注入 DO 特征），50 节点推理 < 50ms。

---

## 6. ONNX 导出与验证

### 6.1 统一导出原则

所有模型（YOLOv8n / RL-DDPG / ST-GNN）统一导出为 **ONNX 中间格式**，再由 Go 端 `onnxer` 加载。这带来：

- **零 CGO**：Go 端无 CGO 依赖，交叉编译简单。
- **零 Python 生产依赖**：生产环境不需要 Python 运行时。
- **平台无关**：一套 ONNX 模型支持 RK3588 / Jetson 双平台。

### 6.2 ONNX 验证

每个训练脚本都内置 ONNX 验证：

- **结构检查**：`onnx.checker.check_model` 验证模型结构合法。
- **形状检查**：验证输入/输出张量形状符合预期。
- **推理测试**：用 `onnxruntime` 跑一次推理，验证输出范围合理。

**手动验证**：

```bash
# 使用 onnxruntime 验证
python -c "
import onnxruntime as ort
sess = ort.InferenceSession('model.onnx')
print('Inputs:', [i.name for i in sess.get_inputs()])
print('Outputs:', [o.name for o in sess.get_outputs()])
"
```

### 6.3 常见导出问题

| 问题 | 处理 |
|------|------|
| opset 版本过高 | 降低 opset（RK3588 要求 ≤ 17） |
| 动态 batch 不支持 | 边缘推理固定 batch=1 |
| 形状不匹配 | 核对输入/输出张量形状与 Go 端接口一致 |

---

## 7. 模型注册与上线

### 7.1 模型注册表（pkg/cloud/modelmgr）

训练并导出 ONNX 后，通过模型注册表（`pkg/cloud/modelmgr`）管理模型版本与生命周期。

**存储布局**：

```
<registry_root>/
  <model_name>/
    <version>/
      entry.json    # 注册条目（id, name, type, version, state, metadata）
      model.bin     # 模型二进制
```

**模型类型**：`forecast`（时序预测）/ `growth`（生长）/ `rl`（强化学习）/ `dt`（数字孪生）。

### 7.2 生命周期：upload → validate → activate → retire

模型状态流转：

```
uploaded → validated → active → retired
              ↑          │
              └──────────┘ (rollback 回滚)
```

| 状态 | 说明 |
|------|------|
| `uploaded` | 模型文件已上传到注册表，未验证 |
| `validated` | 通过构建范围检查（type / inputs / outputs 匹配） |
| `active` | 当前生产模型（每个 name 只有一个 active） |
| `retired` | 已退役（可删除） |

### 7.3 激活策略（3-gate）

`Activate` 操作强制执行 3 道激活门：

1. **评估指标门**：模型 `EvalMetrics` 中所有指标必须达到配置阈值（如 RMSE、R²）。
2. **回滚安全门**：若存在先前 active 模型，其文件必须仍可从磁盘检索（回滚路径可用）。
3. **安全门**：若 `RequireSafetyGate=true`，模型 `RuntimeRequirements` 必须包含 `safety_gate=true`。

任一门失败则激活失败（`ErrActivationGateFailed`）。

### 7.4 回滚

`Rollback(name, targetVersion)` 将指定版本重新激活，并退役当前 active 版本。回滚**绕过 3 道激活门**（目标版本此前已验证可信）。

### 7.5 删除策略

- **active 模型不可删除**（`ErrActiveModelCannotDelete`）。
- 必须先 `Retire` 或 `Rollback` 使模型退役，才能删除。
- 删除会移除磁盘上的模型文件。

### 7.6 上线流程建议

1. **训练**：在离线环境训练并导出 ONNX。
2. **验证**：本地用 onnxruntime 验证模型结构与推理。
3. **上传**：将模型上传到注册表（`uploaded` 状态）。
4. **校验**：执行 `Validate`，确认 type / inputs / outputs 匹配（`validated` 状态）。
5. **激活**：执行 `Activate`，通过 3 道门后成为 `active`。
6. **监控**：上线后监控推理指标与漂移（见第 8 节）。
7. **回滚**：若新模型表现异常，`Rollback` 到上一版本。

---

## 8. A/B 测试与漂移检测

### 8.1 A/B 测试

系统支持生产模型（active）与候选模型（staging）的 A/B 对比：

- 将候选模型上传并验证（`validated` 状态）。
- 对比候选模型与 active 模型的评估指标。
- 指标更优则自动升级（`Activate`）。

### 8.2 漂移检测

模型上线后需持续监控预测误差趋势：

- **时序预测**：预测误差趋势超阈值 → 触发重训练建议。
- **周期性重训练**：系统周期性重训练模型，保持预测精度。

### 8.3 重训练触发条件

| 触发条件 | 处理 |
|---------|------|
| 预测误差趋势超阈值 | 触发重训练建议 |
| 数据分布变化（季节/物种） | 重新准备数据集并微调 |
| 新物种 / 新场景 | 补充标注数据并重训 |

### 8.4 上线检查清单

1. 模型已通过 ONNX 验证。
2. 模型已上传并 `Validate` 通过。
3. 模型 `Activate` 通过 3 道门。
4. 生产推理正常（延迟、精度达标）。
5. 已配置漂移检测与重训练机制。

---

*本文档由 y-ai-pond 项目维护。训练脚本与 `python/` 目录、`tools/convert_yolo.py`、`pkg/cloud/modelmgr` 一一对应，技术细节以 `.omo/plans/y-ai-pond.md` 与 `doc/` 下文档为准。*
