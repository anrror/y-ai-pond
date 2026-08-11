# y-ai-pond Kubernetes 部署指南

> 基于 Helm 3 的 y-ai-pond 智慧水产养殖管理平台 K8s 部署方案。

---

## 目录

- [1. 前置条件](#1-前置条件)
- [2. 安装](#2-安装)
- [3. 验证](#3-验证)
- [4. 配置说明](#4-配置说明)
- [5. 卸载](#5-卸载)
- [6. 故障排查](#6-故障排查)

---

## 1. 前置条件

| 组件 | 最低版本 | 说明 |
|------|---------|------|
| Kubernetes 集群 | 1.25+ | 推荐 1.27+ |
| Helm | 3.12+ | Helm v3 必须 |
| kubectl | 1.25+ | 与集群版本匹配 |
| Ingress Controller | nginx-ingress 1.9+ | 用于外部访问（可选） |
| 持久化存储 | CSI/PV 供应商 | 至少支持 ReadWriteOnce |

**资源预估**（默认配置）：

| 组件 | CPU 请求 | 内存请求 | 存储 |
|------|---------|---------|------|
| Server (×2) | 500m | 512Mi | — |
| EMQX | 500m | 512Mi | 10Gi ×2 |
| InfluxDB 3 | 500m | 512Mi | 50Gi |
| PostgreSQL | 500m | 512Mi | 20Gi |
| Redis | 100m | 128Mi | 5Gi |
| Gotenberg | 250m | 256Mi | — |
| **合计** | **~2.9 核** | **~3.0Gi** | **~100Gi** |

---

## 2. 安装

### 2.1 快速安装（开发/测试环境）

```bash
# 安装 chart（默认 values）
helm install y-ai-pond ./k8s/

# 或者指定命名空间
helm install y-ai-pond ./k8s/ -n y-ai-pond --create-namespace
```

### 2.2 生产环境安装

```bash
# 1. 编辑生产环境配置
#    修改 k8s/values-prod.yaml 中的:
#    - ingress.host → 你的实际域名
#    - secrets.* → 替换为真实密钥
#    - persistence.size → 按需调整

# 2. 通过命令行注入 secrets（推荐方式，避免 secret 写入文件）
helm install y-ai-pond ./k8s/ \
  -f k8s/values-prod.yaml \
  --set secrets.jwtSecret="$(openssl rand -hex 32)" \
  --set secrets.postgresPassword="$(openssl rand -hex 16)" \
  --set secrets.influxdbToken="$(openssl rand -hex 32)" \
  --set secrets.influxdbAdminPassword="$(openssl rand -hex 16)" \
  -n y-ai-pond --create-namespace
```

### 2.3 自定义配置

```bash
# 创建你自己的 values 文件
cp k8s/values-prod.yaml k8s/values-custom.yaml
# 编辑 values-custom.yaml
helm install y-ai-pond ./k8s/ -f k8s/values-custom.yaml
```

### 2.4 升级

```bash
helm upgrade y-ai-pond ./k8s/ -f k8s/values-custom.yaml

# 查看历史版本
helm history y-ai-pond

# 回滚到上一版本
helm rollback y-ai-pond
```

---

## 3. 验证

### 3.1 检查 Pod 状态

```bash
# 等待所有 Pod 就绪
kubectl get pods -l app.kubernetes.io/part-of=y-ai-pond -w

# 预期输出: 所有 Pod STATUS = Running, READY = 1/1 (或 N/N)
```

### 3.2 端口转发验证

```bash
# 转发 Server 端口到本地
kubectl port-forward svc/y-ai-pond-server 8080:8080

# 另一个终端验证健康检查
curl -f http://localhost:8080/health
# 预期输出: {"status":"ok"}  (HTTP 200)
```

### 3.3 Helm 集成测试

```bash
# 运行 helm test
helm test y-ai-pond

# 预期输出:
# NAME: y-ai-pond
# LAST DEPLOYED: ...
# TEST SUITE:     y-ai-pond-test-connection
# Phase:          Succeeded
```

### 3.4 Ingress 验证（如果启用）

```bash
# 获取 Ingress 地址
kubectl get ingress y-ai-pond-server

# 通过域名访问
curl -f https://<ingress-host>/health
```

---

## 4. 配置说明

### 4.1 values.yaml 关键参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `server.replicas` | 2 | Server 副本数 |
| `server.image.tag` | `0.1.0` | Server 镜像版本 |
| `hpa.enabled` | true | 是否启用 HPA 弹性伸缩 |
| `hpa.maxReplicas` | 10 | HPA 最大副本数 |
| `hpa.targetCPUUtilizationPercentage` | 70 | CPU 阈值 (%) |
| `ingress.enabled` | true | 是否创建 Ingress |
| `ingress.host` | `y-ai-pond.example.com` | 外部访问域名 |
| `ingress.tls.enabled` | false | 是否启用 TLS |
| `emqx.replicas` | 1 | EMQX 副本数（集群需 ≥2） |
| `gotenberg.enabled` | true | 是否启用文档转换服务 |

### 4.2 Secrets 管理

**重要：** 默认 values.yaml 中的 secrets 值仅供开发测试。生产环境必须覆盖：

```bash
# 方式 1: 命令行注入（推荐，不留痕迹）
helm install y-ai-pond ./k8s/ \
  --set secrets.jwtSecret="YOUR_REAL_SECRET" \
  --set secrets.postgresPassword="YOUR_REAL_PASSWORD" \
  --set secrets.influxdbToken="YOUR_REAL_TOKEN" \
  --set secrets.influxdbAdminPassword="YOUR_REAL_ADMIN_PASSWORD"

# 方式 2: 使用外部 Secret（先创建再引用）
kubectl create secret generic y-ai-pond-secret \
  --from-literal=JWT_SECRET="..." \
  --from-literal=POSTGRES_PASSWORD="..." \
  --from-literal=INFLUXDB_TOKEN="..." \
  --from-literal=INFLUXDB_ADMIN_PASSWORD="..."
# 然后修改 values.yaml 中 secrets 引用方式

# 方式 3: Sealed Secrets / Vault（企业推荐）
```

**不要在 Helm template 中硬编码真实 secrets。** 所有模板通过 `.Values.secrets.*` 引用。

> **注意**：secret.yaml 使用 Helm `required` 函数校验。`values.yaml` 中提供了开发测试默认值（非空），可直接安装。若使用 `values-prod.yaml` 且将 secrets 留空，helm install/upgrade 会报错并提示必须设置相关字段——这是预期行为，确保生产环境不会以空密钥部署。

### 4.3 持久化存储

所有有状态组件默认使用 `volumeClaimTemplates`（StatefulSet）或独立的 PVC：

| 组件 | 默认大小 | values 路径 |
|------|---------|------------|
| EMQX 数据 + 日志 | 10Gi + 5Gi | `emqx.persistence.size` |
| InfluxDB 3 | 50Gi | `influxdb3.persistence.size` |
| PostgreSQL | 20Gi | `postgresql.persistence.size` |
| Redis | 5Gi | `redis.persistence.size` |

**扩容持久卷**：取决于你的 StorageClass 是否支持 `allowVolumeExpansion: true`。

### 4.4 服务间通信

所有服务通过 Kubernetes Service 名称互通 — 不要在配置中硬编码 IP：

| 服务 | K8s 内部域名 | 端口 |
|------|-------------|------|
| Server | `y-ai-pond-server` | 8080 |
| EMQX | `y-ai-pond-emqx` | 1883 (MQTT), 18083 (Dashboard) |
| InfluxDB 3 | `y-ai-pond-influxdb3` | 8086 |
| PostgreSQL | `y-ai-pond-postgresql` | 5432 |
| Redis | `y-ai-pond-redis` | 6379 |
| Gotenberg | `y-ai-pond-gotenberg` | 3000 |

> **重要**：服务域名前缀 `y-ai-pond-` 来自 Helm release 名称。若使用不同的 release 名安装（如 `helm install my-pond ./k8s/`），所有服务域名将变为 `my-pond-<component>`。此时必须同步修改 `values.yaml` 中 `config.*` 段的服务地址（mqtt.brokerUrl、database.postgresDsn、database.influxdb.url、database.redisAddr），使其匹配实际 Service 名称。

---

## 5. 卸载

```bash
# 删除 Helm release（保留 PV/PVC）
helm uninstall y-ai-pond

# 同时删除 PVC（数据将丢失！）
kubectl delete pvc -l app.kubernetes.io/part-of=y-ai-pond

# 删除命名空间
kubectl delete namespace y-ai-pond
```

---

## 6. 故障排查

### 6.1 Pod 状态 Pending — PVC 未绑定

```bash
kubectl get pvc -l app.kubernetes.io/part-of=y-ai-pond
kubectl describe pvc <pvc-name>
```

**常见原因：**

1. **StorageClass 未设置默认值** — 检查集群是否有默认 StorageClass：
   ```bash
   kubectl get storageclass
   # 列名中有 (default) 标记即为默认 SC
   ```

2. **PV 供应失败** — CSI 驱动未安装或配置错误：
   ```bash
   kubectl get events -n y-ai-pond --sort-by='.lastTimestamp' | grep -i provision
   ```

3. **存储配额不足** — 检查后端存储剩余空间。

**解决方案：**
- 创建匹配的 PV 手动绑定
- 在 `values.yaml` 中通过 `persistence.storageClass` 指定特定 StorageClass
- 如果集群无持久存储，暂时设置 `persistence.enabled: false`（数据重启丢失）

### 6.2 Pod 状态 ImagePullBackOff

```bash
kubectl describe pod <pod-name> | grep -A 5 "Events"
```

**常见原因：**

1. **镜像未构建** — Server 镜像 `y-ai-pond:0.1.0` 需要先构建并推送到集群可访问的镜像仓库：
   ```bash
   # 本地构建
   docker build -t y-ai-pond:0.1.0 .
   # 推送到私有仓库
   docker tag y-ai-pond:0.1.0 <registry>/y-ai-pond:0.1.0
   docker push <registry>/y-ai-pond:0.1.0
   # 更新 values.yaml 的 server.image.repository
   ```

2. **镜像仓库认证失败** — 配置 imagePullSecrets：
   ```bash
   kubectl create secret docker-registry my-registry-secret \
     --docker-server=<registry> \
     --docker-username=<user> \
     --docker-password=<password>
   # 在 values.yaml 的 global.imagePullSecrets 中引用
   ```

3. **网络隔离** — 集群无法访问外部镜像仓库（EMQX、InfluxDB 等公有镜像）

### 6.3 Pod 反复重启 (CrashLoopBackOff)

```bash
kubectl logs <pod-name> --previous
```

**常见原因：**

1. **ConfigMap/Secret 缺失或格式错误** — 检查挂载的配置文件是否存在：
   ```bash
   kubectl get configmap y-ai-pond-config -o yaml
   kubectl get secret y-ai-pond-secret -o yaml
   ```

2. **依赖服务未就绪** — Server 启动前需确保 EMQX、InfluxDB、PostgreSQL、Redis 均已 Running。查看启动顺序：
   ```bash
   kubectl get pods -l app.kubernetes.io/part-of=y-ai-pond
   ```

3. **资源不足** — 检查 Node 资源：
   ```bash
   kubectl top nodes
   kubectl describe pod <pod-name> | grep -A 5 "State"
   ```

### 6.4 Ingress 无法访问

```bash
kubectl get ingress y-ai-pond-server
kubectl describe ingress y-ai-pond-server
```

**检查清单：**
- Ingress Controller 是否已部署（`kubectl get pods -n ingress-nginx`）
- DNS 是否将域名解析到 Ingress Controller 的外部 IP
- TLS 证书 Secret 是否存在（如果启用 TLS）
- 防火墙/安全组是否开放 80/443 端口

### 6.5 EMQX 集群不工作

EMQX 集群模式需要至少 2 个副本，且使用 headless Service 进行节点发现：

```bash
# 检查 StatefulSet
kubectl get statefulset y-ai-pond-emqx

# 检查 headless Service DNS
kubectl run -it --rm debug --image=busybox -- nslookup y-ai-pond-emqx-headless
```

### 6.6 HPA 不工作

```bash
kubectl get hpa y-ai-pond-server
kubectl describe hpa y-ai-pond-server
```

**检查：**
- Metrics Server 是否已部署（`kubectl get pods -n kube-system | grep metrics-server`）
- Pod 是否有资源 requests 设置（HPA 依赖 requests 计算利用率）
- CPU 负载是否确实超过了阈值

---

## 附录 A: 文件清单

```
k8s/
├── Chart.yaml                      # Helm chart 元数据
├── values.yaml                     # 默认配置值
├── values-prod.yaml                # 生产环境覆盖配置
├── README.md                       # 本文件
└── templates/
    ├── _helpers.tpl                # 模板助手函数
    ├── configmap.yaml              # Server 配置文件 (ConfigMap)
    ├── secret.yaml                 # 密钥管理 (Secret)
    ├── serviceaccount.yaml         # ServiceAccount
    ├── server-deployment.yaml      # Server Deployment
    ├── server-service.yaml         # Server Service
    ├── server-ingress.yaml         # Ingress (外部访问)
    ├── server-hpa.yaml             # HPA (弹性伸缩)
    ├── emqx-statefulset.yaml       # EMQX StatefulSet
    ├── emqx-service.yaml           # EMQX Service (headless + ClusterIP)
    ├── influxdb3-statefulset.yaml  # InfluxDB 3 StatefulSet
    ├── influxdb3-service.yaml      # InfluxDB 3 Service
    ├── postgresql-statefulset.yaml # PostgreSQL StatefulSet
    ├── postgresql-service.yaml     # PostgreSQL Service
    ├── redis-deployment.yaml       # Redis Deployment
    ├── redis-service.yaml          # Redis Service
    ├── redis-pvc.yaml              # Redis PVC
    ├── gotenberg.yaml              # Gotenberg Deployment + Service
    └── tests/
        └── test-connection.yaml    # Helm 集成测试
```

## 附录 B: 从 Docker Compose 迁移

如果你已有 Docker Compose 部署，迁移到 K8s 时需注意：

| Docker Compose | Kubernetes |
|---------------|-----------|
| `container_name` | StatefulSet/Deployment 名称 |
| `depends_on` + `condition: service_healthy` | 探针 (liveness/readiness) + initContainers |
| `volumes: - named_volume:/path` | PVC / volumeClaimTemplates |
| `environment:` (内联) | ConfigMap + Secret |
| `ports: - "8080:8080"` (host) | Ingress + Service (ClusterIP) |
| `healthcheck:` | livenessProbe / readinessProbe |
| `restart: unless-stopped` | Pod restartPolicy (默认 Always) |

---

*本文档由 y-ai-pond 项目维护。*
