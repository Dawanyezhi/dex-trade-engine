// 交易分类器 (TxClassifier)
// [dexwallet 通用层] -- 事件解析完成后，根据地址类型自动分类交易方向
//
// 这是钱包系统的核心业务逻辑之一。区块链上的原始交易没有"充值"/"提现"等语义，
// 只有"从地址 A 转到地址 B"的底层操作。钱包系统需要根据 A 和 B 的身份，
// 赋予交易业务含义：
//
//   - 如果 A 是外部地址、B 是我们的用户地址 → 这是一笔"充值"
//   - 如果 A 是我们的用户地址、B 是外部地址 → 这是一笔"提现"
//   - 如果 A 和 B 都是我们的地址 → 这是一笔"内部转账"（如归集、热钱包调拨）
//
// 对标生产中 irwallet 的 Classify 模块。
package main

import (
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ==================== 地址类型定义 ====================

// AddressType 地址相对于我们钱包系统的类型。
//
// 在钱包系统中，每个链上地址都有一个"身份"：
//   - 是我们管理的用户钱包？
//   - 是我们的内部热钱包/归集地址？
//   - 是系统程序地址（Token Program、System Program 等）？
//   - 还是完全不认识的外部地址？
//
// 这个身份决定了交易的分类方向。
type AddressType int

const (
	// AddressTypeUnknown 外部地址（不在我们系统中的地址）。
	// 可能是用户自己的外部钱包、交易所地址、其他项目地址等。
	AddressTypeUnknown AddressType = iota

	// AddressTypeUser 用户钱包地址。
	// 由我们的系统为用户生成和管理的充值/提现地址。
	// 例如：用户在我们平台的 SOL 充值地址、BSC 充值地址。
	AddressTypeUser

	// AddressTypeInternal 内部热钱包/归集地址。
	// 系统运营用的地址，包括：
	//   - 热钱包（Hot Wallet）：用于日常提现出款
	//   - 归集地址（Sweep Address）：用于将用户充值汇总到统一地址
	//   - 手续费代付地址（Fee Payer）：Solana 上为用户代付租金/交易费
	AddressTypeInternal

	// AddressTypeSystem 系统程序地址。
	// 链上的基础设施地址，不属于任何"人"，而是协议本身：
	//   - Solana: Token Program, System Program, Associated Token Program
	//   - EVM: 0x0 地址（mint/burn）, Router 合约地址
	AddressTypeSystem
)

// String 返回地址类型的可读字符串。
func (t AddressType) String() string {
	switch t {
	case AddressTypeUnknown:
		return "unknown"
	case AddressTypeUser:
		return "user"
	case AddressTypeInternal:
		return "internal"
	case AddressTypeSystem:
		return "system"
	default:
		return "invalid"
	}
}

// ==================== 交易分类器 ====================

// TxClassifier 交易分类器。
//
// 通过地址查询函数，将事件中的 Sender 和 Receiver 地址映射为 AddressType，
// 然后根据分类矩阵确定交易类型（TxType）。
//
// 设计说明：
//   - addressLookup 是一个函数而非接口，保持灵活性
//   - 生产中这个函数会查询数据库/缓存来判断地址身份
//   - 测试时可以用 map 模拟
type TxClassifier struct {
	// addressLookup 地址查询函数。
	// 输入一个链上地址，返回它在我们系统中的类型。
	// 生产中通常先查本地缓存（Bloom Filter 快速否定），再查数据库确认。
	addressLookup func(address string) AddressType
}

// NewTxClassifier 创建交易分类器。
//
// lookup 函数负责判断地址身份，分类器本身不关心判断逻辑的实现细节。
// 这种依赖注入的设计让分类器可以在不同环境下复用：
//   - 生产环境：lookup 查询 Redis 缓存 + MySQL 数据库
//   - 测试环境：lookup 使用内存 map
//   - 压测环境：lookup 使用 Bloom Filter + 降级策略
func NewTxClassifier(lookup func(string) AddressType) *TxClassifier {
	return &TxClassifier{
		addressLookup: lookup,
	}
}

// Classify 对事件列表进行分类，填充每个事件的 TxType 字段。
//
// 分类矩阵（对标 irwallet 的 Classify 实现）：
//
// ┌───────────┬───────────────────────────────────────────────────────────────┐
// │           │                        Receiver 类型                         │
// │           ├──────────┬──────────┬──────────────┬─────────────────────────┤
// │           │ Unknown  │ User     │ Internal     │ System                  │
// ├───────────┼──────────┼──────────┼──────────────┼─────────────────────────┤
// │ Sender    │          │          │              │                         │
// │ Unknown   │ unknown  │ inbound  │ inbound      │ system                  │
// │ User      │ outbound │ internal │ internal     │ system                  │
// │ Internal  │ outbound │ internal │ internal     │ system                  │
// │ System    │ system   │ system   │ system       │ system                  │
// └───────────┴──────────┴──────────┴──────────────┴─────────────────────────┘
//
// 矩阵解读：
//
// 1. inbound（充值）：外部地址 → 我们的地址（User 或 Internal）
//    - Unknown → User: 用户从外部钱包充值到我们分配的地址
//    - Unknown → Internal: 外部向我们的热钱包转账（如 OTC 入金）
//
// 2. outbound（提现）：我们的地址 → 外部地址
//    - User → Unknown: 用户提现到外部钱包（罕见，通常从热钱包出）
//    - Internal → Unknown: 热钱包出款（最常见的提现路径）
//
// 3. internal（内部转账）：我们的地址 → 我们的地址
//    - User → User: 平台内用户互转
//    - User → Internal: 用户地址归集到热钱包
//    - Internal → User: 热钱包给用户地址打手续费（Solana 租金）
//    - Internal → Internal: 热钱包之间调拨
//
// 4. system（系统交易）：涉及系统程序地址
//    - System 作为 Sender 或 Receiver 的所有交易
//    - 包括：账户创建/关闭（租金回收）、Token 铸造/销毁
//    - 这类交易通常不影响用户余额，但需要记录以便审计
//
// 5. unknown（未知）：双方都是外部地址
//    - Unknown → Unknown: 与我们无关的交易
//    - 正常情况下不应该出现（我们只监听相关交易）
//    - 如果出现，说明过滤逻辑可能有问题
//
// 6. swap（交换）：DEX 交易
//    - 无论 Sender/Receiver 是谁，只要事件类型是 Swap 就归类为 swap
//    - Swap 事件的 Sender/Receiver 可能是 Router 合约而非用户地址
//    - 需要单独处理，不能套用 Transfer 的分类矩阵
//
// 为什么这个分类很重要？
//   - 充值入账：只有 inbound 类型的交易才会增加用户在平台上的余额
//   - 提现扣款：只有 outbound 类型的交易才会减少用户余额
//   - 内部转账：不影响用户可见余额（对用户透明），但影响内部账本
//   - 系统交易：需要记录手续费支出，但不影响业务余额
//   - 错误分类会导致：重复入账（资金风险）、漏入账（用户投诉）、余额不一致（审计问题）
func (c *TxClassifier) Classify(events []dexwallet.ChainEvent) []dexwallet.ChainEvent {
	for i := range events {
		events[i].TxType = c.classifyOne(&events[i])
	}
	return events
}

// classifyOne 对单个事件进行分类。
func (c *TxClassifier) classifyOne(event *dexwallet.ChainEvent) dexwallet.TxType {
	// Swap 事件特殊处理：无论地址类型如何，统一归类为 swap。
	//
	// 原因：Swap 交易的 Sender/Receiver 在链上表现复杂——
	//   - Solana: Sender 可能是用户地址，但实际通过 Jupiter Router 中转
	//   - EVM: Sender 是用户，但 Token 的转移路径经过 Router 合约
	//   - Receiver 可能是池子地址、Router 地址，而非最终接收者
	// 因此不能用 Transfer 的分类矩阵来判断 Swap 的方向。
	if event.Type == dexwallet.EventSwap {
		return dexwallet.TxTypeSwap
	}

	// Transfer 事件：根据 Sender 和 Receiver 的地址类型，查分类矩阵。
	//
	// 字段映射说明（参见 solana_parsers.go 和 evm_parsers.go）：
	//   - event.Sender: 交易级别的发起者（由 ParserRegistry 填充）
	//   - event.TokenIn: Transfer 事件中的 From 地址（发送方）
	//   - event.TokenOut: Transfer 事件中的 To 地址（接收方）
	//
	// 对于 Transfer 分类，我们关注的是代币的实际流向（TokenIn → TokenOut），
	// 而非交易的发起者（Sender）。因为在 Solana 上，Sender 可能是手续费代付地址，
	// 真正的代币发送方在 TokenIn 字段中。
	sender := event.Sender
	receiver := event.TokenOut // Transfer 的接收方在 TokenOut 字段中

	// 优先使用 TokenIn 作为 sender（代币实际发送方），
	// 如果 TokenIn 为空则使用交易级 Sender。
	if event.TokenIn != "" {
		sender = event.TokenIn
	}

	// 查询地址类型
	senderType := c.lookupAddress(sender)
	receiverType := c.lookupAddress(receiver)

	// 应用分类矩阵
	return c.applyMatrix(senderType, receiverType)
}

// lookupAddress 查询地址类型，空地址返回 Unknown。
func (c *TxClassifier) lookupAddress(address string) AddressType {
	if address == "" {
		return AddressTypeUnknown
	}
	return c.addressLookup(address)
}

// applyMatrix 应用分类矩阵。
//
// 矩阵实现说明：
// System 优先级最高——只要任一方是 System，结果就是 system。
// 这是因为系统地址参与的交易本质上是协议层操作，不是资金流转。
func (c *TxClassifier) applyMatrix(senderType, receiverType AddressType) dexwallet.TxType {
	// 分类矩阵查表
	//
	// 实现方式：用二维数组直接映射，O(1) 查找。
	// 索引: [senderType][receiverType]
	//
	//                Unknown     User       Internal    System
	// Unknown    [0] unknown     inbound    inbound     system
	// User       [1] outbound   internal   internal    system
	// Internal   [2] outbound   internal   internal    system
	// System     [3] system     system     system      system
	matrix := [4][4]dexwallet.TxType{
		// Sender = Unknown
		{dexwallet.TxTypeUnknown, dexwallet.TxTypeInbound, dexwallet.TxTypeInbound, dexwallet.TxTypeSystem},
		// Sender = User
		{dexwallet.TxTypeOutbound, dexwallet.TxTypeInternal, dexwallet.TxTypeInternal, dexwallet.TxTypeSystem},
		// Sender = Internal
		{dexwallet.TxTypeOutbound, dexwallet.TxTypeInternal, dexwallet.TxTypeInternal, dexwallet.TxTypeSystem},
		// Sender = System
		{dexwallet.TxTypeSystem, dexwallet.TxTypeSystem, dexwallet.TxTypeSystem, dexwallet.TxTypeSystem},
	}

	// 安全检查：确保索引在范围内
	si := int(senderType)
	ri := int(receiverType)
	if si < 0 || si >= 4 || ri < 0 || ri >= 4 {
		return dexwallet.TxTypeUnknown
	}

	return matrix[si][ri]
}

// ClassifyBatch 批量分类事件，并按 TxType 分组返回。
//
// 使用场景：
//   - 区块同步完成后，将事件按类型分发到不同的处理管道
//   - inbound 事件 → 充值入账服务
//   - outbound 事件 → 提现确认服务
//   - swap 事件 → 交易监控服务
//   - internal 事件 → 内部账本服务
//   - system 事件 → 审计日志服务
//
// 这种分组设计让下游服务只需关注自己感兴趣的事件类型，
// 避免每个服务都要遍历全部事件并自行过滤。
func (c *TxClassifier) ClassifyBatch(events []dexwallet.ChainEvent) map[dexwallet.TxType][]dexwallet.ChainEvent {
	// 先分类
	classified := c.Classify(events)

	// 按类型分组
	result := make(map[dexwallet.TxType][]dexwallet.ChainEvent)
	for _, event := range classified {
		result[event.TxType] = append(result[event.TxType], event)
	}

	return result
}
