package main

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ----- 三明治攻击测试 -----

func TestSandwich_BasicProfitCorrectness(t *testing.T) {
	// 池子：10000 tokenA / 10000000 tokenB，手续费 30 bps
	sim := NewSandwichSimulator(
		big.NewInt(10000),
		big.NewInt(10000000),
		30,
	)

	result := sim.SimulateAttack(
		big.NewInt(1000), // 受害者投入 1000
		big.NewInt(500),  // 攻击者投入 500
	)

	// 受害者在被攻击后获得的输出应该少于无攻击时
	if result.VictimPriceWith.Cmp(result.VictimPriceWithout) >= 0 {
		t.Errorf("被攻击后的输出应该更少: without=%s, with=%s",
			result.VictimPriceWithout.String(), result.VictimPriceWith.String())
	}

	// 受害者损失应为正
	if result.VictimLoss.Sign() <= 0 {
		t.Errorf("受害者损失应为正: got %s", result.VictimLoss.String())
	}

	// 攻击者利润应为正
	if result.AttackerProfit.Sign() <= 0 {
		t.Errorf("攻击者利润应为正: got %s", result.AttackerProfit.String())
	}

	// 受害者损失 = 无攻击输出 - 被攻击输出
	expectedLoss := new(big.Int).Sub(result.VictimPriceWithout, result.VictimPriceWith)
	if result.VictimLoss.Cmp(expectedLoss) != 0 {
		t.Errorf("受害者损失计算不正确: got %s, want %s",
			result.VictimLoss.String(), expectedLoss.String())
	}

	// 价格影响应为正
	if result.PriceImpactBps == 0 {
		t.Error("价格影响应该大于 0")
	}

	t.Logf("三明治攻击结果: %s", result.String())
}

func TestSandwich_LargerVictimMoreProfit(t *testing.T) {
	sim := NewSandwichSimulator(
		big.NewInt(10000),
		big.NewInt(10000000),
		30,
	)

	attackerAmount := big.NewInt(500)

	// 受害者金额增大，攻击者利润应该增大
	var lastProfit *big.Int
	victimAmounts := []int64{100, 500, 1000, 2000}

	for _, vAmt := range victimAmounts {
		result := sim.SimulateAttack(big.NewInt(vAmt), attackerAmount)

		if lastProfit != nil && result.AttackerProfit.Cmp(lastProfit) <= 0 {
			t.Errorf("受害者金额增大时攻击者利润应增大: victim=%d profit=%s, prev=%s",
				vAmt, result.AttackerProfit.String(), lastProfit.String())
		}
		lastProfit = new(big.Int).Set(result.AttackerProfit)
	}
}

func TestSandwich_DeeperPoolLessProfit(t *testing.T) {
	attackerAmount := big.NewInt(500)
	victimAmount := big.NewInt(1000)

	// 池子越深，攻击者利润应该越低
	var lastProfit *big.Int
	depths := []int64{1000, 5000, 10000, 50000}

	for _, depth := range depths {
		sim := NewSandwichSimulator(
			big.NewInt(depth),
			new(big.Int).Mul(big.NewInt(depth), big.NewInt(1000)),
			30,
		)
		result := sim.SimulateAttack(victimAmount, attackerAmount)

		if lastProfit != nil && result.AttackerProfit.Cmp(lastProfit) >= 0 {
			t.Errorf("池子越深利润应越低: depth=%d profit=%s, prev=%s",
				depth, result.AttackerProfit.String(), lastProfit.String())
		}
		lastProfit = new(big.Int).Set(result.AttackerProfit)
	}
}

func TestSandwich_ZeroFeeRate(t *testing.T) {
	// 无手续费场景，攻击者应该有更高利润
	simWithFee := NewSandwichSimulator(big.NewInt(10000), big.NewInt(10000000), 30)
	simNoFee := NewSandwichSimulator(big.NewInt(10000), big.NewInt(10000000), 0)

	resultWithFee := simWithFee.SimulateAttack(big.NewInt(1000), big.NewInt(500))
	resultNoFee := simNoFee.SimulateAttack(big.NewInt(1000), big.NewInt(500))

	// 无手续费时攻击者利润应该更高（不需要支付两次手续费）
	if resultNoFee.AttackerProfit.Cmp(resultWithFee.AttackerProfit) <= 0 {
		t.Errorf("无手续费时利润应更高: no_fee=%s, with_fee=%s",
			resultNoFee.AttackerProfit.String(), resultWithFee.AttackerProfit.String())
	}
}

// ----- 贿赂服务测试 -----

func TestBribeService_NormalSend(t *testing.T) {
	ctx := context.Background()
	txData := []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04}

	services := []dexwallet.BribeService{
		NewNextBlockService(),
		NewTemporalService(),
		NewZeroSlotService(),
		NewBlockRazorService(),
		NewBlockRushService(),
	}

	for _, svc := range services {
		// 将失败率设为 0 以确保测试确定性
		switch s := svc.(type) {
		case *NextBlockService:
			s.SetFailRate(0)
		case *TemporalService:
			s.SetFailRate(0)
		case *ZeroSlotService:
			s.SetFailRate(0)
		case *BlockRazorService:
			s.SetFailRate(0)
		case *BlockRushService:
			s.SetFailRate(0)
		}

		hash, err := svc.Send(ctx, txData, big.NewInt(10000))
		if err != nil {
			t.Errorf("%s: 发送失败: %v", svc.Name(), err)
			continue
		}
		if hash == "" {
			t.Errorf("%s: 返回了空的交易哈希", svc.Name())
		}

		// 验证推荐费
		fee, err := svc.GetRecommendedFee(ctx)
		if err != nil {
			t.Errorf("%s: 获取推荐费失败: %v", svc.Name(), err)
		}
		if fee.Sign() <= 0 {
			t.Errorf("%s: 推荐费应为正: got %s", svc.Name(), fee.String())
		}

		t.Logf("%s: hash=%s, fee=%s", svc.Name(), hash, fee.String())
	}
}

func TestBribeService_FailureDegradation(t *testing.T) {
	ctx := context.Background()
	txData := []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04}

	// 创建一个 100% 失败的服务
	svc := NewNextBlockService()
	svc.SetFailRate(1.0)

	_, err := svc.Send(ctx, txData, nil)
	if err == nil {
		t.Error("100% 失败率的服务应该返回错误")
	}

	// 恢复后应该正常
	svc.SetFailRate(0)
	hash, err := svc.Send(ctx, txData, nil)
	if err != nil {
		t.Errorf("恢复后发送失败: %v", err)
	}
	if hash == "" {
		t.Error("恢复后应该返回有效哈希")
	}
}

func TestBribeService_ContextTimeout(t *testing.T) {
	// 设置一个非常短的超时
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	txData := []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04}

	svc := NewNextBlockService()
	svc.SetLatency(1 * time.Second) // 延迟远大于超时

	_, err := svc.Send(ctx, txData, nil)
	if err == nil {
		t.Error("超时场景应该返回错误")
	}
}

func TestAntiMEV_NormalSend(t *testing.T) {
	ctx := context.Background()
	txData := []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04}

	antiMEV := NewAntiMEVRPC("flashbots_protect")
	antiMEV.SetFailRate(0)

	hash, err := antiMEV.Send(ctx, txData, nil)
	if err != nil {
		t.Errorf("Anti-MEV 发送失败: %v", err)
	}
	if hash == "" {
		t.Error("应该返回有效哈希")
	}

	// Anti-MEV 推荐费应该为 0
	fee, _ := antiMEV.GetRecommendedFee(ctx)
	if fee.Sign() != 0 {
		t.Errorf("Anti-MEV 推荐费应为 0: got %s", fee.String())
	}
}

func TestBribeService_ImplementsInterface(t *testing.T) {
	// 编译时接口检查
	var _ dexwallet.BribeService = (*NextBlockService)(nil)
	var _ dexwallet.BribeService = (*TemporalService)(nil)
	var _ dexwallet.BribeService = (*ZeroSlotService)(nil)
	var _ dexwallet.BribeService = (*BlockRazorService)(nil)
	var _ dexwallet.BribeService = (*BlockRushService)(nil)
	var _ dexwallet.BribeService = (*AntiMEVRPC)(nil)
}

// ----- 优先费推荐测试 -----

func TestPriorityFee_PercentileCorrectness(t *testing.T) {
	r := NewPriorityFeeRecommender(100)

	// 添加 10 个有序样本: 100, 200, 300, ..., 1000
	for i := 1; i <= 10; i++ {
		r.AddSample(uint64(i * 100))
	}

	// 25th percentile: index = 10 * 25 / 100 = 2 -> sorted[2] = 300
	low := r.Recommend("low")
	if low != 300 {
		t.Errorf("25th percentile 应为 300: got %d", low)
	}

	// 50th percentile: index = 10 * 50 / 100 = 5 -> sorted[5] = 600
	medium := r.Recommend("medium")
	if medium != 600 {
		t.Errorf("50th percentile 应为 600: got %d", medium)
	}

	// 75th percentile: index = 10 * 75 / 100 = 7 -> sorted[7] = 800
	high := r.Recommend("high")
	if high != 800 {
		t.Errorf("75th percentile 应为 800: got %d", high)
	}

	t.Logf("样本 100-1000: low=%d, medium=%d, high=%d", low, medium, high)
}

func TestPriorityFee_EmptySamples(t *testing.T) {
	r := NewPriorityFeeRecommender(50)

	fee := r.Recommend("medium")
	if fee != 0 {
		t.Errorf("空样本应返回 0: got %d", fee)
	}
}

func TestPriorityFee_OutlierResistance(t *testing.T) {
	r := NewPriorityFeeRecommender(100)

	// 9 个正常值 + 1 个极端离群值
	normalFees := []uint64{500, 600, 700, 800, 900, 1000, 1100, 1200, 1300}
	for _, f := range normalFees {
		r.AddSample(f)
	}
	r.AddSample(100000) // 离群值

	// 50th percentile 不应被离群值严重影响
	medium := r.Recommend("medium")

	// medium 应该接近正常范围的中位数（约 900），而不是被离群值拉高
	if medium > 2000 {
		t.Errorf("中位数不应被离群值严重影响: got %d (期望接近 900)", medium)
	}

	t.Logf("含离群值的 50th percentile: %d", medium)
}

func TestPriorityFee_WindowOverflow(t *testing.T) {
	maxSamples := 5
	r := NewPriorityFeeRecommender(maxSamples)

	// 添加超过窗口大小的样本
	for i := 1; i <= 10; i++ {
		r.AddSample(uint64(i * 100))
	}

	// 应该只保留最新的 5 个: 600, 700, 800, 900, 1000
	count := r.SampleCount()
	if count != maxSamples {
		t.Errorf("样本数应为 %d: got %d", maxSamples, count)
	}

	// 最小值应该是 600（不是 100）
	low := r.Recommend("low")
	if low < 600 {
		t.Errorf("窗口溢出后最小值应 >= 600: got %d", low)
	}
}

func TestPriorityFee_UnknownLevel(t *testing.T) {
	r := NewPriorityFeeRecommender(50)
	r.AddSamples([]uint64{100, 200, 300, 400, 500})

	// 未知级别应该默认使用 50th percentile
	fee := r.Recommend("unknown")
	medium := r.Recommend("medium")

	if fee != medium {
		t.Errorf("未知级别应等于 medium: got %d, medium=%d", fee, medium)
	}
}

// ----- RBF 加速测试 -----

func TestRBF_BasicAcceleration(t *testing.T) {
	accelerator := NewRBFAccelerator(30000) // 最大 3.0x

	originalGas := uint64(20_000000000) // 20 Gwei
	tx := BuildMockTx(originalGas, []byte("test_payload"))

	// 1.3x 加速
	newTx, err := accelerator.Accelerate(tx, 13000)
	if err != nil {
		t.Fatalf("加速失败: %v", err)
	}

	newGas := ExtractGasPrice(newTx)
	expectedGas := originalGas * 13000 / 10000 // 26 Gwei

	if newGas != expectedGas {
		t.Errorf("新 Gas 不正确: got %d, want %d", newGas, expectedGas)
	}

	// 验证负载未被修改
	if len(newTx) != len(tx) {
		t.Errorf("交易长度不应改变: got %d, want %d", len(newTx), len(tx))
	}

	t.Logf("加速结果: %d -> %d Gwei (1.3x)", originalGas/1_000000000, newGas/1_000000000)
}

func TestRBF_GasMultiplierRange(t *testing.T) {
	accelerator := NewRBFAccelerator(20000) // 最大 2.0x

	tx := BuildMockTx(20_000000000, []byte("test"))

	// 乘数太低
	_, err := accelerator.Accelerate(tx, 10000) // 1.0x
	if err == nil {
		t.Error("1.0x 乘数应该被拒绝（低于最小值 1.1x）")
	}

	// 乘数太高
	_, err = accelerator.Accelerate(tx, 25000) // 2.5x > 2.0x
	if err == nil {
		t.Error("2.5x 乘数应该被拒绝（超过最大值 2.0x）")
	}

	// 合法范围
	_, err = accelerator.Accelerate(tx, 15000) // 1.5x
	if err != nil {
		t.Errorf("1.5x 乘数应该被接受: %v", err)
	}
}

func TestRBF_ShortTransaction(t *testing.T) {
	accelerator := NewRBFAccelerator(20000)

	// 交易数据太短
	_, err := accelerator.Accelerate([]byte{0x01, 0x02}, 13000)
	if err == nil {
		t.Error("短交易数据应该返回错误")
	}
}

func TestRBF_ZeroOriginalGas(t *testing.T) {
	accelerator := NewRBFAccelerator(20000)

	tx := BuildMockTx(0, []byte("test"))

	_, err := accelerator.Accelerate(tx, 13000)
	if err == nil {
		t.Error("原始 Gas 为 0 应该返回错误")
	}
}

func TestRBF_ProgressiveAcceleration(t *testing.T) {
	accelerator := NewRBFAccelerator(30000) // 最大 3.0x

	tx := BuildMockTx(10_000000000, []byte("test")) // 10 Gwei

	// 逐步加速: 1.3x -> 1.5x -> 2.0x
	multipliers := []int64{13000, 15000, 20000}
	var lastGas uint64

	for _, mul := range multipliers {
		newTx, err := accelerator.Accelerate(tx, mul)
		if err != nil {
			t.Fatalf("乘数 %d 加速失败: %v", mul, err)
		}

		newGas := ExtractGasPrice(newTx)
		if lastGas > 0 && newGas <= lastGas {
			t.Errorf("逐步加速的 Gas 应该递增: %d -> %d", lastGas, newGas)
		}
		lastGas = newGas

		// 以新交易作为下一轮的输入
		tx = newTx
	}

	t.Logf("最终 Gas: %d Gwei (从 10 Gwei 开始)", lastGas/1_000000000)
}

func TestRBF_AccelerateResult(t *testing.T) {
	accelerator := NewRBFAccelerator(20000)

	tx := BuildMockTx(15_000000000, []byte("test"))

	_, result, err := accelerator.AccelerateResult(tx, 15000)
	if err != nil {
		t.Fatalf("AccelerateResult 失败: %v", err)
	}

	if result.OriginalGas != 15_000000000 {
		t.Errorf("原始 Gas 不正确: got %d", result.OriginalGas)
	}

	expectedNewGas := uint64(15_000000000 * 15000 / 10000)
	if result.NewGas != expectedNewGas {
		t.Errorf("新 Gas 不正确: got %d, want %d", result.NewGas, expectedNewGas)
	}

	if result.Multiplier != 15000 {
		t.Errorf("乘数不正确: got %d, want 15000", result.Multiplier)
	}
}

// ----- 辅助函数 -----

func TestBuildAndExtractMockTx(t *testing.T) {
	gasPrice := uint64(25_000000000) // 25 Gwei
	payload := []byte("hello world")

	tx := BuildMockTx(gasPrice, payload)

	extracted := ExtractGasPrice(tx)
	if extracted != gasPrice {
		t.Errorf("提取的 Gas Price 不匹配: got %d, want %d", extracted, gasPrice)
	}

	// 验证负载部分
	if len(tx) != 8+len(payload) {
		t.Errorf("交易长度不正确: got %d, want %d", len(tx), 8+len(payload))
	}
}
