# Onchain-DEX-Lab 项目指令

## 语言
- 所有文档和注释使用简体中文
- 代码变量/函数名使用英文

## 技术约束
- Go 1.22+
- 金额处理：禁止 float64，全部使用 big.Int 或 decimal.Decimal
- 并发安全：所有共享数据结构必须有并发保护
- 错误处理：使用 `fmt.Errorf("xxx: %w", err)` 链式传递
- 日志：demo 代码使用 `log/slog`，cmd 使用 `logrus`
- 测试：所有 demo 使用内存 mock，不依赖外部服务

## 架构原则
- 三层抽象：dexwallet 通用层 → solana/evm 链特定层 → DEX 协议层
- internal/dexwallet/ 是项目灵魂，定义所有跨链通用接口
- internal/solana/ 和 internal/evm/ 只实现 dexwallet 定义的接口
- 每个模块的 notes.md 标注 [dexwallet 通用层] 或 [链特定层]

## 项目结构
- 编号目录（00-08）：每个模块有 README.md、goals.md、notes.md、demo/
- internal/dexwallet/：核心接口和通用实现
- internal/solana/：Solana 链特定实现
- internal/evm/：EVM 链特定实现
- cmd/simulate/：端到端演示
- interview-prep/：面试准备材料
