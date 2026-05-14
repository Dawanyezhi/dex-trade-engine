# 06-mev-protection: 学习目标与自检问题

## 知识点分层

### L1 -- 基础概念（必须掌握）

- MEV 是什么：验证者/搜索者通过控制交易排序提取的额外价值
- 三明治攻击的三步骤：前置交易（推高价格）-> 受害者交易 -> 后置交易（卖出获利）
- 为什么 DEX 交易容易被 MEV 攻击：交易在 mempool 中可见，且 AMM 价格可预测
- 滑点容忍度与 MEV 的关系：滑点越高，攻击者的利润空间越大
- 优先费/贿赂的基本概念：支付额外费用以获得更优的交易排序位置
- Solana 和 EVM 在交易排序上的区别：Solana Leader Schedule vs EVM MEV Auction

### L2 -- 核心实现（需要理解原理）

- 三明治攻击的利润计算
  - 攻击者前置交易如何改变池子储量
  - 受害者在被操纵后的池子中获得的输出为什么更少
  - 攻击者后置卖出时为什么能获利
  - 利润与池子深度、交易金额的关系
- 贿赂服务的多通道并发模式
  - BaseTxSender 的 goroutine 扇出（fan-out）设计
  - 为什么取「第一个成功」而不是「所有成功」
  - 某个通道失败时的降级策略
  - context 超时如何控制整体发送时间
- 优先费推荐算法
  - 百分位数（Percentile）的统计意义
  - 为什么用排序后取位而不是平均值
  - 样本窗口大小对推荐值的影响
- RBF（Replace-By-Fee）机制
  - 同 nonce 交易替换的条件（Gas 必须更高）
  - 为什么不能无限加 Gas（成本控制）
  - RBF 与交易确认的竞争关系

### L3 -- 进阶设计（生产中需要）

- Solana 贿赂服务的选择策略
  - 不同服务商的 Leader 覆盖率和延迟差异
  - 按地区选择最优通道（NextBlock 多地区 vs Temporal CDN）
  - 费率竞争：过低被忽略，过高浪费资金
  - 动态调整：根据历史成功率动态调整各通道的权重
- EVM Anti-MEV 方案对比
  - Flashbots Protect: 交易不进公开 mempool，但可能延迟更高
  - MEV Blocker: OFA 模式，搜索者竞价回馈用户
  - 私有 mempool 的信任模型：你信任谁不泄露你的交易
  - L2 Sequencer 排序：Base/Arbitrum 的天然优势与中心化风险
- 优先费的动态调整
  - 网络拥堵检测：近期区块的平均优先费是否快速上升
  - 紧急交易 vs 非紧急交易的费率策略差异
  - Solana getRecentPrioritizationFees RPC 的使用
  - EVM EIP-1559 baseFee 的预测算法
- RBF 高级策略
  - 指数退避：第 1 次 1.3x，第 2 次 1.5x，第 3 次 2.0x
  - 取消交易：发送同 nonce 的 0 value 交易替换原交易
  - Solana 的 transaction retransmission 与 EVM RBF 的本质区别

### L4 -- 架构视角（通盘理解）

- MEV 防护在 dexwallet 三层架构中的位置
  - dexwallet 层：BribeService 接口、BaseTxSender 多通道逻辑
  - solana 层：5 个贿赂服务的具体实现、Compute Unit Price 设置
  - evm 层：Anti-MEV RPC、RBF 逻辑、EIP-1559 参数
- MEV 防护与其他模块的协作
  - SwapBuilder 在构建交易时需要设置合理的滑点（过高被攻击，过低交易失败）
  - Aggregator 选路时需要考虑 MEV 风险（某些路径更容易被三明治）
  - Monitor 需要检测交易是否被三明治攻击（事后分析）
- MEV 的博弈论视角
  - 攻击者和防御者的军备竞赛
  - PBS（Proposer-Builder Separation）如何改变 MEV 格局
  - MEV 对 DeFi 用户体验的长期影响
  - 协议层面的 MEV 缓解方案（如 CoW Protocol、Chainlink CCIP）
- 不同链的 MEV 生态对比
  - Ethereum: 成熟的 MEV-Boost 生态，Flashbots 主导
  - Solana: Jito 验证者客户端，贿赂服务碎片化
  - BSC: 48 Club，MEV 保护较弱
  - L2: Sequencer 中心化排序，MEV 由 Sequencer 控制

## 自检问题

### L1 基础

1. 三明治攻击中，攻击者为什么要在受害者交易之后立刻卖出？如果攻击者不卖出会怎样？
2. 假设受害者设置了 100 bps（1%）的滑点容忍度，攻击者最多能提取多少利润（不考虑 Gas 成本）？
3. 为什么 Solana 需要贿赂服务而 EVM 链更依赖 Anti-MEV RPC？两条链的交易传播机制有什么本质区别？

### L2 核心

4. 在三明治攻击模拟中，如果池子流动性增大 10 倍，攻击者的利润会如何变化？请从 AMM 公式推导。
5. BaseTxSender 使用 `select` 取第一个成功结果，如果 5 个贿赂服务中 3 个返回了不同的 txHash，说明了什么？这是否表示交易被发送了多次？
6. 优先费推荐使用百分位而非平均值，如果样本中有一个极端离群值（比如某个区块的优先费是正常值的 100 倍），百分位和平均值的表现分别如何？
7. RBF 加速时 Gas 乘数设为 1.3x，但原交易的 Gas 已经很高了。此时 RBF 的成本收益分析应该如何做？

### L3 进阶

8. 如果你运行了一个月的贿赂服务，发现 NextBlock 的成功率是 85%、Temporal 是 70%、ZeroSlot 是 60%。你会如何调整发送策略？完全放弃低成功率的服务吗？
9. Flashbots Protect 保护了你的交易不被三明治攻击，但交易的确认时间从平均 12 秒变成了 24 秒。在什么场景下这个延迟是不可接受的？
10. Solana 的 getRecentPrioritizationFees 返回最近 150 个区块的数据。如果网络突然拥堵，历史数据可能严重低估当前需要的优先费。你会如何处理这种跳变？

### L4 架构

11. 如果要在 dexwallet 框架中新增一个 MEV 保护策略（比如交易拆分：将大额交易拆成多笔小额交易），应该在哪一层实现？需要修改哪些接口？
12. PBS（Proposer-Builder Separation）如何改变 MEV 的分配格局？对普通用户来说是好事还是坏事？
13. 假设 Solana 未来引入类似 EIP-1559 的 baseFee 机制，现有的贿赂服务生态会发生什么变化？BribeService 接口需要如何调整？
