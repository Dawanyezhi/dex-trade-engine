// cmd/simulate 端到端全场景模拟
// 串联 8 个模块的核心功能，验证整体流程
package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	fmt.Println(`
╔══════════════════════════════════════════════════════════════╗
║         Onchain DEX Lab -- 端到端全场景模拟                  ║
╚══════════════════════════════════════════════════════════════╝`)

	scenarios := []struct {
		name    string
		desc    string
		fn      func() error
	}{
		{"场景 01：RPC 客户端", "多节点故障转移 / Solana & EVM RPC 封装", runScenario01},
		{"场景 02：DEX 协议解析", "AMM 价格计算 / CLMM Tick 机制 / Bonding Curve 定价", runScenario02},
		{"场景 03：Swap 交易引擎", "工厂模式选 DEX / Solana 指令组装 / EVM ABI 编码 / 交易模拟", runScenario03},
		{"场景 04：池子管理", "LRU 缓存 / 最优池选择 / 状态更新", runScenario04},
		{"场景 05：聚合路由", "并发报价 / 优先级排序 / 降级兜底 / 两跳路由", runScenario05},
		{"场景 06：MEV 防护", "三明治攻击模拟 / 贿赂服务 / 优先费推荐", runScenario06},
		{"场景 07：事件解析", "解析器注册 / Swap 事件解析 / Syncer 主循环", runScenario07},
		{"场景 08：生产级架构", "限流 / 多链配置 / 监控告警", runScenario08},
	}

	passed := 0
	failed := 0

	for _, s := range scenarios {
		fmt.Printf("\n【%s】\n", s.name)
		fmt.Printf("  %s\n", s.desc)

		start := time.Now()
		err := s.fn()
		elapsed := time.Since(start)

		if err != nil {
			fmt.Printf("  [FAIL] (%s) -- %v\n", formatDuration(elapsed), err)
			failed++
		} else {
			fmt.Printf("  [PASS] (%s)\n", formatDuration(elapsed))
			passed++
		}
	}

	fmt.Printf(`
╔══════════════════════════════════════════════════════════════╗
║  模拟结果：%d 通过 / %d 失败                                ║
╚══════════════════════════════════════════════════════════════╝
`, passed, failed)

	if failed > 0 {
		os.Exit(1)
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%.2f ms", float64(d.Microseconds())/1000.0)
	}
	return fmt.Sprintf("%.2f s", d.Seconds())
}
