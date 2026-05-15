# 08-production-architecture: 生产级架构特性

## 模块概述

本模块实现生产级 DEX 钱包系统中的三大架构特性：限流与背压、多链配置管理、监控告警。这些特性在功能层面之上，构成了系统可靠运行的基础设施层。

在生产环境中，功能正确只是起点。系统需要在高并发下保持稳定（限流与背压）、支持快速扩展到新链（多链配置）、在异常发生时及时通知运维团队（监控告警）。

## 核心设计

### 限流与背压

链上操作（RPC 调用、Swap 交易）有天然的速率限制：
1. RPC 节点有 TPS 限制（免费节点通常 25-50 TPS，付费节点 200-1000 TPS）
2. 链本身的吞吐限制（Solana ~4000 TPS，BSC ~100 TPS，Ethereum ~30 TPS）
3. DEX 聚合器 API 有调用频率限制

令牌桶算法（Token Bucket）是最适合这个场景的限流方案：
- 允许短时间内的突发流量（桶中有积攒的令牌）
- 长期平均速率严格受控（填充速率固定）
- 实现简单，CPU 开销极低

背压机制通过有界队列实现：当 Swap 请求积压超过阈值时，新请求被拒绝或排队等待，避免系统过载导致雪崩。

### 多链配置管理

生产系统需要同时支持 Solana、BSC、Ethereum、Base 等多条链，每条链有不同的：
- 限流参数（TPS、Worker 数）
- DEX 启用/禁用列表
- 灰度发布比例（新 DEX 逐步放量）

关键设计：
- 基于 `coinset.ChainConfig` 扩展出 `ChainRuntime`，包含运行时可变参数
- 支持热更新：无需重启服务即可调整配置
- 新增 EVM 链只需添加一组配置，零代码修改

### 监控告警

基于 `dexwallet.Monitor` 和 `alarm.Manager` 构建完整的监控服务：
- 定期收集所有链的监控指标（Swap 成功率、报价失败率、区块停滞）
- 指标异常时通过 `alarm.Manager` 发送告警
- 告警通道可插拔（Console/钉钉/Lark/PagerDuty）

### 配置管理

所有配置参数化，通过 `ChainRuntime` 统一管理。生产中配置来源通常是：
- 启动配置：配置文件（YAML/TOML）或环境变量
- 运行时配置：配置中心（Apollo/Nacos）推送或 API 热更新
- 紧急配置：通过告警回调自动降级（如禁用异常 DEX）

## 文件结构

```
08-production-architecture/
  README.md              -- 模块概述（本文件）
  goals.md               -- 学习目标与自检问题
  notes.md               -- 深度技术笔记
  demo/
    rate_limiter.go      -- 令牌桶限流器 + Worker 池 + SwapQueue
    chain_config.go      -- 多链配置管理与热更新
    monitor_demo.go      -- 监控服务与告警演示
    tx_state_machine.go  -- 交易状态机（8 态 + ValidTransitions + RejectCode 错误码）
    stuck_tx.go          -- Stuck 交易检测器（超时扫描 + 重试/丢弃生命周期）
    nonce_manager.go     -- EVM 地址级 Nonce 管理（per-address mutex + 本地追踪 + 链上同步）
    main.go              -- 端到端演示程序
    production_test.go   -- 测试
```

## 与生产系统的差距

| 维度 | 本 demo | 生产系统 |
|------|---------|----------|
| 限流算法 | 单机令牌桶 | 分布式限流（Redis + Lua 脚本）+ 多级限流 |
| Worker 池 | 固定大小信号量 | 动态伸缩 + 优先级队列 + 任务超时取消 |
| 配置管理 | 内存中手动更新 | 配置中心（Apollo/Nacos）+ 版本管理 + 灰度推送 |
| 监控指标 | slog 日志 + 内存计数器 | Prometheus + Grafana + 多维度聚合 |
| 告警通道 | Console 输出 | 钉钉/Lark/PagerDuty + 告警分级 + 抑制/合并 |
| 熔断降级 | 手动禁用 DEX | Hystrix/Sentinel 自动熔断 + 自动恢复探测 |
| 灰度发布 | 百分比随机 | 按用户/地址/交易对维度灰度 + A/B 测试 |
| 链扩展 | 代码中添加配置 | 配置中心动态注册 + 自动发现 |
| 压测 | 简单并发测试 | 全链路压测 + 混沌工程（故障注入） |
| 可观测性 | 基础日志 | 分布式追踪（Jaeger/Zipkin）+ 链路拓扑图 |
