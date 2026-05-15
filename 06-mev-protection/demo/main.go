package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

func main() {
	// 初始化结构化日志
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	slog.Info("== MEV 防护与交易优化演示 ==")
	fmt.Println()

	// ===== 场景 1: 三明治攻击模拟 =====
	fmt.Println("============================================================")
	fmt.Println("场景 1: 三明治攻击模拟")
	fmt.Println("============================================================")
	demoSandwichAttack()

	// ===== 场景 2: 贿赂服务多通道发送 =====
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("场景 2: 贿赂服务多通道发送")
	fmt.Println("============================================================")
	demoBribeServices()

	// ===== 场景 2.5: 贿赂服务健康管理 =====
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("场景 2.5: BribeServiceManager 健康管理")
	fmt.Println("============================================================")
	demoBribeServiceManager()

	// ===== 场景 3: 优先费推荐 =====
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("场景 3: 优先费推荐")
	fmt.Println("============================================================")
	demoPriorityFee()

	// ===== 场景 4: RBF 加速 =====
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("场景 4: RBF 交易加速")
	fmt.Println("============================================================")
	demoRBFAcceleration()

	// ===== 场景 5: EVM Gas Oracle =====
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("场景 5: EVM Gas Oracle (Gas 预测)")
	fmt.Println("============================================================")
	demoGasOracle()
}

// demoSandwichAttack 演示三明治攻击模拟。
func demoSandwichAttack() {
	// 池子：10000 tokenA / 10000000 tokenB，手续费 30 bps（0.3%）
	reserveA := big.NewInt(10000)
	reserveB := big.NewInt(10000000)
	feeRate := uint64(30)

	sim := NewSandwichSimulator(reserveA, reserveB, feeRate)

	fmt.Printf("\n池子初始状态: %s tokenA / %s tokenB\n", reserveA.String(), reserveB.String())
	fmt.Printf("手续费: %d bps (%.2f%%)\n", feeRate, float64(feeRate)/100.0)

	// --- 测试 1: 不同受害者金额，固定攻击者金额 ---
	fmt.Println("\n--- 固定攻击者投入 500 tokenA，不同受害者金额 ---")
	printMEVTableHeader("受害者投入", "无攻击输出", "被攻击输出", "受害者损失", "攻击者利润", "影响(bps)")

	attackerAmount := big.NewInt(500)
	victimAmounts := []int64{100, 500, 1000, 2000, 5000}

	for _, vAmt := range victimAmounts {
		result := sim.SimulateAttack(big.NewInt(vAmt), attackerAmount)
		fmt.Printf("| %-12d | %-14s | %-14s | %-14s | %-14s | %-10d |\n",
			vAmt,
			result.VictimPriceWithout.String(),
			result.VictimPriceWith.String(),
			result.VictimLoss.String(),
			result.AttackerProfit.String(),
			result.PriceImpactBps,
		)
	}
	printMEVTableFooter()

	// --- 测试 2: 不同攻击者金额，固定受害者金额 ---
	fmt.Println("\n--- 固定受害者投入 1000 tokenA，不同攻击者金额 ---")
	printMEVTableHeader("攻击者投入", "无攻击输出", "被攻击输出", "受害者损失", "攻击者利润", "影响(bps)")

	victimAmount := big.NewInt(1000)
	attackerAmounts := []int64{100, 300, 500, 1000, 2000}

	for _, aAmt := range attackerAmounts {
		result := sim.SimulateAttack(victimAmount, big.NewInt(aAmt))
		fmt.Printf("| %-12d | %-14s | %-14s | %-14s | %-14s | %-10d |\n",
			aAmt,
			result.VictimPriceWithout.String(),
			result.VictimPriceWith.String(),
			result.VictimLoss.String(),
			result.AttackerProfit.String(),
			result.PriceImpactBps,
		)
	}
	printMEVTableFooter()

	// --- 测试 3: 池子深度对攻击的影响 ---
	fmt.Println("\n--- 池子深度对攻击的影响（受害者 1000，攻击者 500）---")
	printMEVTableHeader("池子深度(A)", "受害者损失", "攻击者利润", "影响(bps)", "", "")

	depths := []int64{1000, 5000, 10000, 50000, 100000}
	for _, depth := range depths {
		deepSim := NewSandwichSimulator(
			big.NewInt(depth),
			new(big.Int).Mul(big.NewInt(depth), big.NewInt(1000)), // B = A * 1000
			feeRate,
		)
		result := deepSim.SimulateAttack(big.NewInt(1000), big.NewInt(500))
		fmt.Printf("| %-12d | %-14s | %-14s | %-14d | %-14s | %-10s |\n",
			depth,
			result.VictimLoss.String(),
			result.AttackerProfit.String(),
			result.PriceImpactBps,
			"",
			"",
		)
	}
	printMEVTableFooter()

	fmt.Println("\n结论:")
	fmt.Println("- 受害者交易金额越大，攻击者利润越高")
	fmt.Println("- 攻击者投入越多，前置交易推高价格越多，利润越大（但也有边际递减）")
	fmt.Println("- 池子越深，同样金额的攻击效果越弱")
}

// demoBribeServices 演示贿赂服务多通道发送。
func demoBribeServices() {
	ctx := context.Background()

	// 创建 5 个 Solana 贿赂服务
	nextBlock := NewNextBlockService()
	temporal := NewTemporalService()
	zeroSlot := NewZeroSlotService()
	blockRazor := NewBlockRazorService()
	blockRush := NewBlockRushService()

	services := []dexwallet.BribeService{nextBlock, temporal, zeroSlot, blockRazor, blockRush}

	// --- 展示各服务推荐费 ---
	fmt.Println("\n--- 各贿赂服务推荐费 ---")
	printMEVTableHeader("服务商", "推荐费(lamports)", "", "", "", "")
	for _, svc := range services {
		fee, _ := svc.GetRecommendedFee(ctx)
		fmt.Printf("| %-14s | %-18s | %-14s | %-14s | %-14s | %-10s |\n",
			svc.Name(), fee.String(), "", "", "", "")
	}
	printMEVTableFooter()

	// --- 多通道并发发送演示 ---
	fmt.Println("\n--- 多通道并发发送（模拟 3 次）---")
	txData := []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04}

	for round := 1; round <= 3; round++ {
		fmt.Printf("\n第 %d 次发送:\n", round)

		type sendResult struct {
			service string
			txHash  string
			err     error
			elapsed time.Duration
		}

		ch := make(chan sendResult, len(services))
		start := time.Now()

		for _, svc := range services {
			go func(s dexwallet.BribeService) {
				sendStart := time.Now()
				hash, err := s.Send(ctx, txData, nil)
				ch <- sendResult{
					service: s.Name(),
					txHash:  hash,
					err:     err,
					elapsed: time.Since(sendStart),
				}
			}(svc)
		}

		// 收集所有结果
		var firstSuccess *sendResult
		for i := 0; i < len(services); i++ {
			r := <-ch
			status := "成功"
			if r.err != nil {
				status = fmt.Sprintf("失败: %v", r.err)
			} else if firstSuccess == nil {
				firstSuccess = &r
			}
			fmt.Printf("  [%s] %s (耗时: %v)\n", r.service, status, r.elapsed.Round(time.Millisecond))
		}

		if firstSuccess != nil {
			fmt.Printf("  >> 最快成功通道: %s, txHash: %s, 总耗时: %v\n",
				firstSuccess.service, firstSuccess.txHash, time.Since(start).Round(time.Millisecond))
		} else {
			fmt.Printf("  >> 所有通道都失败!\n")
		}
	}

	// --- 故障降级演示 ---
	fmt.Println("\n--- 故障降级演示：3 个服务设为 100% 失败 ---")
	nextBlock.SetFailRate(1.0)
	temporal.SetFailRate(1.0)
	zeroSlot.SetFailRate(1.0)
	// blockRazor 和 blockRush 保持正常

	type sendResult struct {
		service string
		txHash  string
		err     error
	}
	ch := make(chan sendResult, len(services))
	for _, svc := range services {
		go func(s dexwallet.BribeService) {
			hash, err := s.Send(ctx, txData, nil)
			ch <- sendResult{service: s.Name(), txHash: hash, err: err}
		}(svc)
	}

	successCount := 0
	failCount := 0
	for i := 0; i < len(services); i++ {
		r := <-ch
		if r.err != nil {
			failCount++
		} else {
			successCount++
		}
	}
	fmt.Printf("结果: %d 个成功, %d 个失败（降级到存活的服务）\n", successCount, failCount)

	// 恢复
	nextBlock.SetFailRate(0.05)
	temporal.SetFailRate(0.08)
	zeroSlot.SetFailRate(0.10)

	// --- EVM Anti-MEV 演示 ---
	fmt.Println("\n--- EVM Anti-MEV RPC ---")
	antiMEV := NewAntiMEVRPC("flashbots_protect")
	fee, _ := antiMEV.GetRecommendedFee(ctx)
	fmt.Printf("服务: %s, 推荐费: %s（Anti-MEV 保护免费）\n", antiMEV.Name(), fee.String())

	hash, err := antiMEV.Send(ctx, txData, nil)
	if err != nil {
		fmt.Printf("发送失败: %v\n", err)
	} else {
		fmt.Printf("发送成功: %s\n", hash)
	}
}

// demoBribeServiceManager 演示贿赂服务健康管理。
func demoBribeServiceManager() {
	ctx := context.Background()

	// 创建 5 个服务
	svc1 := NewNextBlockService()
	svc2 := NewTemporalService()
	svc3 := NewZeroSlotService()
	svc4 := NewBlockRazorService()
	svc5 := NewBlockRushService()

	// 确定性测试：关闭所有随机失败
	svc1.SetFailRate(0)
	svc2.SetFailRate(0)
	svc3.SetFailRate(0)
	svc4.SetFailRate(0)
	svc5.SetFailRate(0)

	alertCount := 0
	config := BribeServiceManagerConfig{
		MaxConsecutiveFails: 2,
		CooldownDuration:   200 * time.Millisecond,
		SendTimeout:        5 * time.Second,
	}

	mgr := NewBribeServiceManager(
		[]dexwallet.BribeService{svc1, svc2, svc3, svc4, svc5},
		config,
		func() { alertCount++ },
	)

	txData := []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04}

	// --- 正常发送 ---
	fmt.Println("\n--- 正常发送（5 个服务全部健康）---")
	hash, err := mgr.Send(ctx, txData, big.NewInt(10000))
	if err != nil {
		fmt.Printf("发送失败: %v\n", err)
	} else {
		fmt.Printf("发送成功: %s\n", hash)
	}
	fmt.Printf("健康服务: %d/%d\n", mgr.HealthyCount(), mgr.TotalCount())

	// --- 模拟 3 个服务故障 ---
	fmt.Println("\n--- 模拟 3 个服务持续故障 ---")
	svc1.SetFailRate(1.0)
	svc2.SetFailRate(1.0)
	svc3.SetFailRate(1.0)

	// 发送多次让故障服务被标记为不健康
	for i := 0; i < 3; i++ {
		mgr.Send(ctx, txData, nil)
	}

	fmt.Printf("健康服务: %d/%d\n", mgr.HealthyCount(), mgr.TotalCount())
	fmt.Println("各服务状态:")
	for _, s := range mgr.Stats() {
		status := "健康"
		if !s.Healthy {
			status = "不健康"
		}
		fmt.Printf("  %s: %s (成功=%d, 失败=%d, 连续失败=%d)\n",
			s.Name, status, s.SuccessCount, s.FailCount, s.ConsecutiveFails)
	}

	// --- 冷却恢复演示 ---
	fmt.Println("\n--- 冷却恢复（200ms 后故障服务恢复）---")
	svc1.SetFailRate(0) // 先恢复
	svc2.SetFailRate(0)
	svc3.SetFailRate(0)

	time.Sleep(250 * time.Millisecond) // 等待冷却期

	hash, err = mgr.Send(ctx, txData, nil)
	if err != nil {
		fmt.Printf("发送失败: %v\n", err)
	} else {
		fmt.Printf("发送成功: %s\n", hash)
	}
	fmt.Printf("恢复后健康服务: %d/%d\n", mgr.HealthyCount(), mgr.TotalCount())

	// --- 全挂告警演示 ---
	fmt.Println("\n--- 全挂告警演示 ---")
	svc1.SetFailRate(1.0)
	svc2.SetFailRate(1.0)
	svc3.SetFailRate(1.0)
	svc4.SetFailRate(1.0)
	svc5.SetFailRate(1.0)

	alertCount = 0
	// 发送到全部不健康
	for i := 0; i < 3; i++ {
		mgr.Send(ctx, txData, nil)
	}
	fmt.Printf("告警触发次数: %d\n", alertCount)
	fmt.Printf("健康服务: %d/%d\n", mgr.HealthyCount(), mgr.TotalCount())

	fmt.Println("\n结论:")
	fmt.Println("- 并行广播跳过不健康服务，避免浪费资源")
	fmt.Println("- 冷却期后自动探测恢复，无需人工干预")
	fmt.Println("- 全部不可用时触发告警，接入监控系统")
}

// demoPriorityFee 演示优先费推荐。
func demoPriorityFee() {
	recommender := NewPriorityFeeRecommender(50)

	// 模拟最近 20 个区块的优先费数据
	// 正常区块的优先费在 500-2000 范围，偶尔有高峰
	sampleFees := []uint64{
		500, 800, 600, 1200, 900,
		700, 1500, 1100, 800, 2000,
		600, 900, 1300, 700, 1000,
		5000, // 一个离群值（套利机器人）
		800, 1100, 900, 1400,
	}

	recommender.AddSamples(sampleFees)

	fmt.Printf("\n样本数: %d\n", recommender.SampleCount())
	fmt.Printf("样本范围: 500 ~ 5000（包含一个离群值 5000）\n\n")

	// 三个级别的推荐
	fmt.Println("--- 优先费推荐 ---")
	levels := []struct {
		name string
		desc string
	}{
		{"low", "省钱优先（25th percentile）"},
		{"medium", "平衡选择（50th percentile）"},
		{"high", "速度优先（75th percentile）"},
	}

	printMEVTableHeader("级别", "推荐值", "说明", "", "", "")
	for _, l := range levels {
		fee := recommender.Recommend(l.name)
		fmt.Printf("| %-14s | %-18d | %-26s | %-14s | %-14s | %-10s |\n",
			l.name, fee, l.desc, "", "", "")
	}
	printMEVTableFooter()

	// 演示样本增长的影响
	fmt.Println("\n--- 网络拥堵模拟（加入高优先费样本）---")
	congestionFees := []uint64{
		3000, 4000, 3500, 5000, 6000,
		4500, 3800, 5500, 4200, 7000,
	}
	recommender.AddSamples(congestionFees)

	fmt.Printf("加入 %d 个拥堵期样本后:\n", len(congestionFees))
	printMEVTableHeader("级别", "推荐值(拥堵前)", "推荐值(拥堵后)", "", "", "")

	// 重新创建一个干净的推荐器对比
	cleanRecommender := NewPriorityFeeRecommender(50)
	cleanRecommender.AddSamples(sampleFees)

	for _, l := range levels {
		feeBefore := cleanRecommender.Recommend(l.name)
		feeAfter := recommender.Recommend(l.name)
		fmt.Printf("| %-14s | %-20d | %-22d | %-14s | %-14s | %-10s |\n",
			l.name, feeBefore, feeAfter, "", "", "")
	}
	printMEVTableFooter()

	fmt.Println("\n结论:")
	fmt.Println("- 百分位推荐对离群值有很好的鲁棒性")
	fmt.Println("- 网络拥堵时，推荐值自动上调")
	fmt.Println("- 窗口大小影响对网络变化的敏感度")
}

// demoRBFAcceleration 演示 RBF 交易加速。
func demoRBFAcceleration() {
	accelerator := NewRBFAccelerator(30000) // 最大 3.0x

	// 创建一笔模拟交易（Gas Price = 20 Gwei）
	originalGas := uint64(20_000000000) // 20 Gwei
	payload := []byte("swap(tokenA, tokenB, 1000)")
	originalTx := BuildMockTx(originalGas, payload)

	fmt.Printf("\n原始交易: Gas Price = %d Gwei\n", originalGas/1_000000000)

	// 不同级别的加速
	fmt.Println("\n--- 不同乘数的 RBF 加速 ---")
	printMEVTableHeader("乘数", "原Gas(Gwei)", "新Gas(Gwei)", "增幅", "", "")

	multipliers := []struct {
		value int64
		desc  string
	}{
		{11000, "1.1x（最低有效）"},
		{13000, "1.3x（推荐保守）"},
		{15000, "1.5x（推荐标准）"},
		{20000, "2.0x（紧急加速）"},
		{30000, "3.0x（极端情况）"},
	}

	for _, m := range multipliers {
		newTx, result, err := accelerator.AccelerateResult(originalTx, m.value)
		if err != nil {
			fmt.Printf("| %-14s | %-14d | %-14s | %-14s | %-14s | %-10s |\n",
				m.desc, originalGas/1_000000000, fmt.Sprintf("错误: %v", err), "", "", "")
			continue
		}

		_ = newTx // 实际使用中会将 newTx 发送到链上
		fmt.Printf("| %-14s | %-14d | %-14d | %-14s | %-14s | %-10s |\n",
			m.desc,
			result.OriginalGas/1_000000000,
			result.NewGas/1_000000000,
			fmt.Sprintf("+%d%%", (result.NewGas-result.OriginalGas)*100/result.OriginalGas),
			"", "")
	}
	printMEVTableFooter()

	// 演示错误情况
	fmt.Println("\n--- 错误情况演示 ---")

	// 乘数太低
	_, err := accelerator.Accelerate(originalTx, 10000) // 1.0x，不够
	fmt.Printf("乘数 1.0x: %v\n", err)

	// 乘数太高
	_, err = accelerator.Accelerate(originalTx, 50000) // 5.0x，超过限制
	fmt.Printf("乘数 5.0x: %v\n", err)

	// 交易数据太短
	_, err = accelerator.Accelerate([]byte{0x01, 0x02}, 13000)
	fmt.Printf("短交易数据: %v\n", err)

	// --- 逐步加速演示 ---
	fmt.Println("\n--- 逐步加速演示（模拟交易多次卡住）---")
	currentTx := originalTx
	steps := []int64{13000, 15000, 20000} // 1.3x -> 1.5x -> 2.0x
	for i, mul := range steps {
		currentGas := ExtractGasPrice(currentTx)
		newTx, err := accelerator.Accelerate(currentTx, mul)
		if err != nil {
			fmt.Printf("第 %d 次加速失败: %v\n", i+1, err)
			break
		}
		newGas := ExtractGasPrice(newTx)
		fmt.Printf("第 %d 次加速: %d Gwei -> %d Gwei (%.1fx)\n",
			i+1, currentGas/1_000000000, newGas/1_000000000, float64(mul)/10000.0)
		currentTx = newTx
	}

	fmt.Println("\n结论:")
	fmt.Println("- RBF 需要至少 10% 的 Gas 涨幅才能被节点接受")
	fmt.Println("- 建议从 1.3x 开始，逐步增加到 1.5x、2.0x")
	fmt.Println("- 设置最大乘数上限，避免 Gas 费失控")
}

// ----- 辅助函数 -----

// printMEVTableHeader 打印 MEV 演示的表格头。
func printMEVTableHeader(cols ...string) {
	fmt.Println(strings.Repeat("-", 100))
	fmt.Print("|")
	for _, col := range cols {
		fmt.Printf(" %-14s |", col)
	}
	fmt.Println()
	fmt.Println(strings.Repeat("-", 100))
}

// printMEVTableFooter 打印表格尾。
func printMEVTableFooter() {
	fmt.Println(strings.Repeat("-", 100))
}

// demoGasOracle 演示 EVM Gas 预测：UpdateBlock → EstimateBaseFee → RecommendPriorityFee → Trend。
func demoGasOracle() {
	oracle := NewGasOracle(20)

	fmt.Println("\n--- 初始化 GasOracle（历史窗口=20 个区块）---")

	// ---- 阶段 1: 注入稳定期区块数据 ----
	fmt.Println("\n--- 阶段 1: 注入稳定期区块数据（baseFee 约 30 Gwei）---")
	stableBaseFees := []uint64{
		30_000000000, 30_500000000, 29_800000000, 30_200000000, 30_100000000,
		29_900000000, 30_300000000, 30_000000000, 29_700000000, 30_400000000,
	}
	stableTips := []uint64{
		1_000000000, 1_500000000, 2_000000000, 1_200000000, 1_800000000,
	}

	for i, baseFee := range stableBaseFees {
		blockNum := uint64(18000000 + i)
		oracle.UpdateBlock(blockNum, baseFee, stableTips)
	}

	trend := oracle.Trend()
	estimatedBase := oracle.EstimateBaseFee()
	fmt.Printf("\n  当前趋势:     %s\n", trend)
	fmt.Printf("  当前 baseFee: %d wei (%.1f Gwei)\n",
		stableBaseFees[len(stableBaseFees)-1],
		float64(stableBaseFees[len(stableBaseFees)-1])/1e9)
	fmt.Printf("  预测 baseFee: %d wei (%.1f Gwei)\n", estimatedBase, float64(estimatedBase)/1e9)

	// 推荐 priorityFee
	fmt.Println("\n  PriorityFee 推荐:")
	for _, level := range []string{"low", "medium", "high"} {
		tip := oracle.RecommendPriorityFee(level)
		fmt.Printf("    %-8s: %d wei (%.1f Gwei)\n", level, tip, float64(tip)/1e9)
	}

	// 推荐 maxFeePerGas
	fmt.Println("\n  MaxFeePerGas 推荐 (= estimatedBaseFee*2 + tip):")
	for _, level := range []string{"low", "medium", "high"} {
		maxFee := oracle.RecommendMaxFee(level)
		fmt.Printf("    %-8s: %d wei (%.1f Gwei)\n", level, maxFee, float64(maxFee)/1e9)
	}

	// Gas 费用估算
	fmt.Println("\n  Swap 交易 Gas 费用估算 (gasLimit=200000):")
	for _, level := range []string{"low", "medium", "high"} {
		cost := oracle.EstimateGasCost(200000, level)
		costETH := float64(cost) / 1e18
		fmt.Printf("    %-8s: %d wei (%.6f ETH)\n", level, cost, costETH)
	}

	// ---- 阶段 2: 注入上升期区块数据（模拟网络拥堵）----
	fmt.Println("\n--- 阶段 2: 注入上升期区块数据（模拟网络拥堵）---")
	risingBaseFees := []uint64{
		32_000000000, 35_000000000, 38_000000000, 42_000000000, 46_000000000,
		50_000000000, 55_000000000, 60_000000000, 65_000000000, 70_000000000,
	}
	congestionTips := []uint64{
		3_000000000, 5_000000000, 8_000000000, 10_000000000, 15_000000000,
	}

	for i, baseFee := range risingBaseFees {
		blockNum := uint64(18000010 + i)
		oracle.UpdateBlock(blockNum, baseFee, congestionTips)
	}

	trend = oracle.Trend()
	estimatedBase = oracle.EstimateBaseFee()
	currentBase := risingBaseFees[len(risingBaseFees)-1]
	fmt.Printf("\n  当前趋势:     %s\n", trend)
	fmt.Printf("  当前 baseFee: %d wei (%.1f Gwei)\n", currentBase, float64(currentBase)/1e9)
	fmt.Printf("  预测 baseFee: %d wei (%.1f Gwei) -- 上涨 12.5%%\n",
		estimatedBase, float64(estimatedBase)/1e9)

	// ---- 阶段 3: 注入下降期区块数据（网络恢复）----
	fmt.Println("\n--- 阶段 3: 注入下降期区块数据（网络恢复）---")
	fallingBaseFees := []uint64{
		65_000000000, 58_000000000, 50_000000000, 44_000000000, 38_000000000,
		33_000000000, 30_000000000, 28_000000000, 27_000000000, 26_000000000,
	}

	for i, baseFee := range fallingBaseFees {
		blockNum := uint64(18000020 + i)
		oracle.UpdateBlock(blockNum, baseFee, stableTips)
	}

	trend = oracle.Trend()
	estimatedBase = oracle.EstimateBaseFee()
	currentBase = fallingBaseFees[len(fallingBaseFees)-1]
	fmt.Printf("\n  当前趋势:     %s\n", trend)
	fmt.Printf("  当前 baseFee: %d wei (%.1f Gwei)\n", currentBase, float64(currentBase)/1e9)
	fmt.Printf("  预测 baseFee: %d wei (%.1f Gwei) -- 下降 12.5%%\n",
		estimatedBase, float64(estimatedBase)/1e9)

	// ---- GasOracle 格式化输出 ----
	fmt.Printf("\n  Oracle 状态: %s\n", oracle.String())

	fmt.Println("\n结论:")
	fmt.Println("- EVM baseFee 每个区块动态调整（最大涨跌 12.5%）")
	fmt.Println("- GasOracle 根据历史趋势预测下一区块 baseFee")
	fmt.Println("- 拥堵时预测值自动上调，恢复时自动下调")
	fmt.Println("- maxFeePerGas = estimatedBase*2 + tip，留出安全余量")
}
