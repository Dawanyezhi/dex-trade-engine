package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// 辅助函数 — 构建测试用的原始数据
// ============================================================

// makeRaydiumRawData 构建 Raydium AMM 的模拟原始数据。
func makeRaydiumRawData() []byte {
	raw := RaydiumPoolRawData{
		AMMID:        "58oQChx4yWmvKdwLLZzBi4ChoCc2fqCUWBkwMihLYQo2",
		BaseMint:     "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		QuoteMint:    "So11111111111111111111111111111111111111112",
		BaseVault:    "DQyrAcCrDXQ7NeoqGgDCZwBvWDcYmFCjSb9JtteuvPpz",
		QuoteVault:   "HLmqeL62xR1QoZ1HKKbXRrdN1p3phKpxRMb2VVopvBBz",
		BaseReserve:  "50000000000",    // 50000 USDC (6 decimals)
		QuoteReserve: "500000000000",   // 500 SOL (9 decimals)
		BaseDecimal:  6,
		QuoteDecimal: 9,
		BaseSymbol:   "USDC",
		QuoteSymbol:  "SOL",
		FeeRateBps:   25,
		OpenTime:     1700000000,
	}
	data, _ := json.Marshal(raw)
	return data
}

// makePumpFunRawData 构建 PumpFun 联合曲线的模拟原始数据。
func makePumpFunRawData(complete bool) []byte {
	raw := PumpFunPoolRawData{
		Mint:                 "7GCihgDB8fe6KNjn2MYtkzZcRjQy3t9GHdC8uHYmW2hr",
		BondingCurve:         "Fg6PaFpoGXkYsidMpWTK6W2BeZ7FEfcYkg476zPFsLnS",
		VirtualSOLReserves:   "30000000000000", // 30000 SOL (虚拟)
		VirtualTokenReserves: "1000000000000",  // 1000000 Token (虚拟)
		RealSOLReserves:      "5000000000",     // 5 SOL (真实锁定)
		RealTokenReserves:    "800000000000",   // 800000 Token (真实)
		Complete:             complete,
		TokenSymbol:          "MEME",
		TokenDecimal:         6,
	}
	data, _ := json.Marshal(raw)
	return data
}

// makeUniV2RawData 构建 Uniswap V2 的模拟原始数据。
func makeUniV2RawData() []byte {
	raw := UniV2PoolRawData{
		Pair:     "0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc",
		Token0:   "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
		Token1:   "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2",
		Symbol0:  "USDC",
		Symbol1:  "WETH",
		Decimal0: 6,
		Decimal1: 18,
		Reserve0: "50000000000",             // 50000 USDC
		Reserve1: "25000000000000000000",    // 25 WETH
		Fee:      30,
	}
	data, _ := json.Marshal(raw)
	return data
}

// ============================================================
// TestPoolParserRouter_RaydiumParse — Raydium 解析测试
// ============================================================

func TestPoolParserRouter_RaydiumParse(t *testing.T) {
	router := NewPoolParserRouter()
	router.Register(NewRaydiumPoolParser())

	ctx := context.Background()
	rawData := makeRaydiumRawData()

	pool, err := router.Parse(ctx, raydiumProgramID, rawData)
	if err != nil {
		t.Fatalf("parse raydium pool failed: %v", err)
	}

	// 验证基本字段
	if pool.Address != "58oQChx4yWmvKdwLLZzBi4ChoCc2fqCUWBkwMihLYQo2" {
		t.Errorf("expected address 58oQChx4yWmvKdwLLZzBi4ChoCc2fqCUWBkwMihLYQo2, got %s", pool.Address)
	}
	if pool.DexID != dexwallet.DexRaydiumAMM {
		t.Errorf("expected dex_id %s, got %s", dexwallet.DexRaydiumAMM, pool.DexID)
	}
	if pool.ProtocolType != dexwallet.ProtocolAMM {
		t.Errorf("expected protocol_type %s, got %s", dexwallet.ProtocolAMM, pool.ProtocolType)
	}
	if pool.BaseMint != "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v" {
		t.Errorf("unexpected base_mint: %s", pool.BaseMint)
	}
	if pool.QuoteMint != "So11111111111111111111111111111111111111112" {
		t.Errorf("unexpected quote_mint: %s", pool.QuoteMint)
	}
	if pool.BaseSymbol != "USDC" || pool.QuoteSymbol != "SOL" {
		t.Errorf("unexpected symbols: %s/%s", pool.BaseSymbol, pool.QuoteSymbol)
	}
	if pool.BaseDecimal != 6 || pool.QuoteDecimal != 9 {
		t.Errorf("unexpected decimals: %d/%d", pool.BaseDecimal, pool.QuoteDecimal)
	}
	if pool.FeeRate != 25 {
		t.Errorf("expected fee_rate 25, got %d", pool.FeeRate)
	}
	if pool.State != dexwallet.PoolStateActive {
		t.Errorf("expected state active, got %s", pool.State)
	}
	if pool.Liquidity == nil || pool.Liquidity.Sign() <= 0 {
		t.Error("expected positive liquidity")
	}

	// 验证 Extra 字段包含 vault 信息
	if pool.Extra == nil {
		t.Fatal("expected extra fields to be set")
	}
	if pool.Extra["base_vault"] != "DQyrAcCrDXQ7NeoqGgDCZwBvWDcYmFCjSb9JtteuvPpz" {
		t.Errorf("unexpected base_vault in extra: %v", pool.Extra["base_vault"])
	}
	if pool.Extra["quote_vault"] != "HLmqeL62xR1QoZ1HKKbXRrdN1p3phKpxRMb2VVopvBBz" {
		t.Errorf("unexpected quote_vault in extra: %v", pool.Extra["quote_vault"])
	}
}

// ============================================================
// TestPoolParserRouter_PumpFunParse — PumpFun 解析测试
// ============================================================

func TestPoolParserRouter_PumpFunParse(t *testing.T) {
	router := NewPoolParserRouter()
	router.Register(NewPumpFunPoolParser())

	ctx := context.Background()

	// 测试未毕业的池子
	t.Run("active_bonding_curve", func(t *testing.T) {
		rawData := makePumpFunRawData(false)
		pool, err := router.Parse(ctx, pumpFunProgramID, rawData)
		if err != nil {
			t.Fatalf("parse pumpfun pool failed: %v", err)
		}

		if pool.Address != "Fg6PaFpoGXkYsidMpWTK6W2BeZ7FEfcYkg476zPFsLnS" {
			t.Errorf("expected bonding_curve address, got %s", pool.Address)
		}
		if pool.DexID != dexwallet.DexPumpFun {
			t.Errorf("expected dex_id %s, got %s", dexwallet.DexPumpFun, pool.DexID)
		}
		if pool.ProtocolType != dexwallet.ProtocolBondingCurve {
			t.Errorf("expected protocol_type %s, got %s", dexwallet.ProtocolBondingCurve, pool.ProtocolType)
		}
		if pool.FeeRate != 100 {
			t.Errorf("expected fee_rate 100 (1%%), got %d", pool.FeeRate)
		}
		if pool.State != dexwallet.PoolStateActive {
			t.Errorf("expected state active for non-graduated pool, got %s", pool.State)
		}
		if pool.BaseSymbol != "MEME" || pool.QuoteSymbol != "SOL" {
			t.Errorf("unexpected symbols: %s/%s", pool.BaseSymbol, pool.QuoteSymbol)
		}

		// 验证 Extra 包含联合曲线特有字段
		if pool.Extra == nil {
			t.Fatal("expected extra fields")
		}
		if pool.Extra["complete"] != false {
			t.Errorf("expected complete=false, got %v", pool.Extra["complete"])
		}
		if pool.Extra["virtual_sol_reserves"] == nil {
			t.Error("expected virtual_sol_reserves in extra")
		}
	})

	// 测试已毕业的池子（complete=true → inactive）
	t.Run("graduated_bonding_curve", func(t *testing.T) {
		rawData := makePumpFunRawData(true)
		pool, err := router.Parse(ctx, pumpFunProgramID, rawData)
		if err != nil {
			t.Fatalf("parse graduated pumpfun pool failed: %v", err)
		}

		if pool.State != dexwallet.PoolStateInactive {
			t.Errorf("expected state inactive for graduated pool, got %s", pool.State)
		}
		if pool.Extra["complete"] != true {
			t.Errorf("expected complete=true, got %v", pool.Extra["complete"])
		}
	})
}

// ============================================================
// TestPoolParserRouter_UniswapV2Parse — Uniswap V2 解析测试
// ============================================================

func TestPoolParserRouter_UniswapV2Parse(t *testing.T) {
	router := NewPoolParserRouter()
	router.Register(NewUniswapV2PoolParser())

	ctx := context.Background()
	rawData := makeUniV2RawData()

	pool, err := router.Parse(ctx, uniswapV2FactoryAddress, rawData)
	if err != nil {
		t.Fatalf("parse uniswap v2 pool failed: %v", err)
	}

	if pool.Address != "0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc" {
		t.Errorf("unexpected pair address: %s", pool.Address)
	}
	if pool.DexID != dexwallet.DexUniswapV2 {
		t.Errorf("expected dex_id %s, got %s", dexwallet.DexUniswapV2, pool.DexID)
	}
	if pool.ProtocolType != dexwallet.ProtocolAMM {
		t.Errorf("expected protocol_type %s, got %s", dexwallet.ProtocolAMM, pool.ProtocolType)
	}
	if string(pool.ChainID) != "ethereum" {
		t.Errorf("expected chain_id ethereum, got %s", pool.ChainID)
	}
	if pool.FeeRate != 30 {
		t.Errorf("expected fee_rate 30, got %d", pool.FeeRate)
	}
	if pool.BaseSymbol != "USDC" || pool.QuoteSymbol != "WETH" {
		t.Errorf("unexpected symbols: %s/%s", pool.BaseSymbol, pool.QuoteSymbol)
	}
	if pool.BaseDecimal != 6 || pool.QuoteDecimal != 18 {
		t.Errorf("unexpected decimals: %d/%d", pool.BaseDecimal, pool.QuoteDecimal)
	}
	if pool.Liquidity == nil || pool.Liquidity.Sign() <= 0 {
		t.Error("expected positive liquidity")
	}

	// 验证 Extra 包含 factory 信息
	if pool.Extra == nil {
		t.Fatal("expected extra fields")
	}
	if pool.Extra["factory"] != uniswapV2FactoryAddress {
		t.Errorf("unexpected factory in extra: %v", pool.Extra["factory"])
	}
}

// ============================================================
// TestPoolParserRouter_UnknownOwner — 未知 owner 测试
// ============================================================

func TestPoolParserRouter_UnknownOwner(t *testing.T) {
	router := NewPoolParserRouter()
	router.Register(NewRaydiumPoolParser())

	ctx := context.Background()
	rawData := []byte(`{}`)

	// 使用一个未注册的 owner 地址
	unknownOwner := "11111111111111111111111111111111"
	_, err := router.Parse(ctx, unknownOwner, rawData)
	if err == nil {
		t.Fatal("expected error for unknown owner, got nil")
	}

	// 验证错误信息包含 owner 地址
	expectedMsg := "no parser registered for owner: " + unknownOwner
	if err.Error() != expectedMsg {
		t.Errorf("expected error message %q, got %q", expectedMsg, err.Error())
	}
}

// ============================================================
// TestPoolParserRouter_Registration — 注册逻辑测试
// ============================================================

func TestPoolParserRouter_Registration(t *testing.T) {
	router := NewPoolParserRouter()

	// 初始状态：0 个解析器
	if router.ParserCount() != 0 {
		t.Errorf("expected 0 parsers initially, got %d", router.ParserCount())
	}

	// 注册 3 个解析器
	router.Register(NewRaydiumPoolParser())
	router.Register(NewPumpFunPoolParser())
	router.Register(NewUniswapV2PoolParser())

	if router.ParserCount() != 3 {
		t.Errorf("expected 3 parsers after registration, got %d", router.ParserCount())
	}

	// 验证各解析器的 Owner 和 SupportedDex
	t.Run("raydium_parser_info", func(t *testing.T) {
		parser := NewRaydiumPoolParser()
		if parser.Owner() != raydiumProgramID {
			t.Errorf("unexpected raydium owner: %s", parser.Owner())
		}
		dexes := parser.SupportedDex()
		if len(dexes) != 1 || dexes[0] != dexwallet.DexRaydiumAMM {
			t.Errorf("unexpected raydium supported dex: %v", dexes)
		}
	})

	t.Run("pumpfun_parser_info", func(t *testing.T) {
		parser := NewPumpFunPoolParser()
		if parser.Owner() != pumpFunProgramID {
			t.Errorf("unexpected pumpfun owner: %s", parser.Owner())
		}
		dexes := parser.SupportedDex()
		if len(dexes) != 1 || dexes[0] != dexwallet.DexPumpFun {
			t.Errorf("unexpected pumpfun supported dex: %v", dexes)
		}
	})

	t.Run("uniswap_v2_parser_info", func(t *testing.T) {
		parser := NewUniswapV2PoolParser()
		if parser.Owner() != uniswapV2FactoryAddress {
			t.Errorf("unexpected uniswap v2 owner: %s", parser.Owner())
		}
		dexes := parser.SupportedDex()
		if len(dexes) != 1 || dexes[0] != dexwallet.DexUniswapV2 {
			t.Errorf("unexpected uniswap v2 supported dex: %v", dexes)
		}
	})

	// 重复注册同一个 owner 应覆盖
	t.Run("overwrite_registration", func(t *testing.T) {
		router2 := NewPoolParserRouter()
		router2.Register(NewRaydiumPoolParser())
		router2.Register(NewRaydiumPoolParser()) // 重复注册

		if router2.ParserCount() != 1 {
			t.Errorf("expected 1 parser after duplicate registration, got %d", router2.ParserCount())
		}
	})
}

// ============================================================
// TestPoolParserRouter_InvalidRawData — 无效数据测试
// ============================================================

func TestPoolParserRouter_InvalidRawData(t *testing.T) {
	router := NewPoolParserRouter()
	router.Register(NewRaydiumPoolParser())
	router.Register(NewPumpFunPoolParser())
	router.Register(NewUniswapV2PoolParser())

	ctx := context.Background()

	// 传入无效 JSON
	invalidData := []byte(`not-valid-json`)

	t.Run("raydium_invalid_json", func(t *testing.T) {
		_, err := router.Parse(ctx, raydiumProgramID, invalidData)
		if err == nil {
			t.Fatal("expected error for invalid json, got nil")
		}
	})

	t.Run("pumpfun_invalid_json", func(t *testing.T) {
		_, err := router.Parse(ctx, pumpFunProgramID, invalidData)
		if err == nil {
			t.Fatal("expected error for invalid json, got nil")
		}
	})

	t.Run("uniswap_v2_invalid_json", func(t *testing.T) {
		_, err := router.Parse(ctx, uniswapV2FactoryAddress, invalidData)
		if err == nil {
			t.Fatal("expected error for invalid json, got nil")
		}
	})
}

// ============================================================
// 接口兼容性测试
// ============================================================

func TestPoolParsersImplementInterface(t *testing.T) {
	var _ dexwallet.PoolParser = (*RaydiumPoolParser)(nil)
	var _ dexwallet.PoolParser = (*PumpFunPoolParser)(nil)
	var _ dexwallet.PoolParser = (*UniswapV2PoolParser)(nil)
}
