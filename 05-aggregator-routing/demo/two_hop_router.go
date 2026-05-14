package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"sync"

	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================
// TwoHopRouter -- 两跳路由器
// ============================================================

// TwoHopRoute 两跳路由结果。
type TwoHopRoute struct {
	// 路由路径
	Hops []dexwallet.RouteHop

	// 总输出金额
	TotalOutput *big.Int

	// 使用的中间资产
	MiddleAsset string

	// 两跳的总价格影响（基点）
	TotalPriceImpact uint64

	// 两跳的总 Gas
	TotalGas *big.Int

	// 是否为直接路由（非两跳）
	IsDirect bool
}

// TwoHopRouter 两跳路由器。
// tokenA -> 中间资产(SOL/WETH/USDC) -> tokenB
// 当 tokenA 和 tokenB 之间没有直接交易池时，
// 通过高流动性的中间资产进行桥接。
type TwoHopRouter struct {
	aggregator   *dexwallet.BaseAggregator
	poolManager  dexwallet.PoolManager
	middleAssets []string // 中间资产地址列表
}

// NewTwoHopRouter 创建两跳路由器。
func NewTwoHopRouter(aggregator *dexwallet.BaseAggregator, poolManager dexwallet.PoolManager, middleAssets []string) *TwoHopRouter {
	return &TwoHopRouter{
		aggregator:   aggregator,
		poolManager:  poolManager,
		middleAssets: middleAssets,
	}
}

// FindBestRoute 寻找最优路由（直接路由 vs 两跳路由）。
func (r *TwoHopRouter) FindBestRoute(ctx context.Context, req dexwallet.SwapRequest) (*TwoHopRoute, error) {
	// 并发查找: 直接路由 + 每个中间资产的两跳路由
	type routeResult struct {
		route *TwoHopRoute
		err   error
	}

	totalCandidates := 1 + len(r.middleAssets) // 直接路由 + N 个两跳路由
	results := make(chan routeResult, totalCandidates)
	var wg sync.WaitGroup

	// 尝试直接路由
	wg.Add(1)
	go func() {
		defer wg.Done()
		route, err := r.findDirectRoute(ctx, req)
		results <- routeResult{route: route, err: err}
	}()

	// 尝试每个中间资产的两跳路由
	for _, middle := range r.middleAssets {
		// 跳过与 tokenIn 或 tokenOut 相同的中间资产
		if middle == req.FromToken.Address || middle == req.ToToken.Address {
			continue
		}
		wg.Add(1)
		go func(middleAsset string) {
			defer wg.Done()
			route, err := r.findTwoHopRoute(ctx, req, middleAsset)
			results <- routeResult{route: route, err: err}
		}(middle)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// 收集所有有效路由
	var routes []*TwoHopRoute
	for r := range results {
		if r.err != nil {
			slog.Debug("route search failed", "error", r.err)
			continue
		}
		if r.route != nil && r.route.TotalOutput != nil && r.route.TotalOutput.Sign() > 0 {
			routes = append(routes, r.route)
		}
	}

	if len(routes) == 0 {
		return nil, fmt.Errorf("no route found for %s -> %s (tried direct + %d middle assets)",
			req.FromToken.Symbol, req.ToToken.Symbol, len(r.middleAssets))
	}

	// 选择输出最大的路由
	best := routes[0]
	for _, route := range routes[1:] {
		if route.TotalOutput.Cmp(best.TotalOutput) > 0 {
			best = route
		}
	}

	routeType := "two-hop"
	if best.IsDirect {
		routeType = "direct"
	}
	slog.Info("best route found",
		"type", routeType,
		"middle_asset", best.MiddleAsset,
		"output", best.TotalOutput.String(),
		"hops", len(best.Hops),
	)

	return best, nil
}

// findDirectRoute 尝试直接路由。
func (r *TwoHopRouter) findDirectRoute(ctx context.Context, req dexwallet.SwapRequest) (*TwoHopRoute, error) {
	quote, err := r.aggregator.FindBestQuote(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("direct route: %w", err)
	}

	return &TwoHopRoute{
		Hops: []dexwallet.RouteHop{
			{
				DexID:     quote.DexID,
				Pool:      r.poolAddress(quote),
				TokenIn:   req.FromToken.Address,
				TokenOut:  req.ToToken.Address,
				AmountIn:  quote.InputAmount,
				AmountOut: quote.OutputAmount,
			},
		},
		TotalOutput:      quote.OutputAmount,
		MiddleAsset:      "", // 无中间资产
		TotalPriceImpact: quote.PriceImpact,
		TotalGas:         quote.EstimatedGas,
		IsDirect:         true,
	}, nil
}

// findTwoHopRoute 尝试两跳路由: tokenIn -> middle -> tokenOut。
func (r *TwoHopRouter) findTwoHopRoute(ctx context.Context, req dexwallet.SwapRequest, middleAsset string) (*TwoHopRoute, error) {
	// 第一跳: tokenIn -> middle
	hop1Req := dexwallet.SwapRequest{
		ChainID:   req.ChainID,
		Direction: req.Direction,
		FromToken: req.FromToken,
		ToToken: dexwallet.Token{
			Address:  middleAsset,
			Symbol:   "MIDDLE",
			Decimals: 9, // 默认精度
			ChainID:  req.ChainID,
		},
		Amount:      req.Amount,
		SlippageBps: req.SlippageBps,
		Sender:      req.Sender,
		Recipient:   req.Recipient,
	}

	quote1, err := r.aggregator.FindBestQuote(ctx, hop1Req)
	if err != nil {
		return nil, fmt.Errorf("two-hop first leg (%s -> %s): %w",
			req.FromToken.Symbol, middleAsset, err)
	}

	// 第二跳: middle -> tokenOut（使用第一跳的输出作为第二跳的输入）
	hop2Req := dexwallet.SwapRequest{
		ChainID:   req.ChainID,
		Direction: req.Direction,
		FromToken: dexwallet.Token{
			Address:  middleAsset,
			Symbol:   "MIDDLE",
			Decimals: 9,
			ChainID:  req.ChainID,
		},
		ToToken:     req.ToToken,
		Amount:      quote1.OutputAmount, // 第一跳的输出 = 第二跳的输入
		SlippageBps: req.SlippageBps,
		Sender:      req.Sender,
		Recipient:   req.Recipient,
	}

	quote2, err := r.aggregator.FindBestQuote(ctx, hop2Req)
	if err != nil {
		return nil, fmt.Errorf("two-hop second leg (%s -> %s): %w",
			middleAsset, req.ToToken.Symbol, err)
	}

	// 组合两跳结果
	totalGas := new(big.Int)
	if quote1.EstimatedGas != nil {
		totalGas.Add(totalGas, quote1.EstimatedGas)
	}
	if quote2.EstimatedGas != nil {
		totalGas.Add(totalGas, quote2.EstimatedGas)
	}

	return &TwoHopRoute{
		Hops: []dexwallet.RouteHop{
			{
				DexID:     quote1.DexID,
				Pool:      r.poolAddress(quote1),
				TokenIn:   req.FromToken.Address,
				TokenOut:  middleAsset,
				AmountIn:  quote1.InputAmount,
				AmountOut: quote1.OutputAmount,
			},
			{
				DexID:     quote2.DexID,
				Pool:      r.poolAddress(quote2),
				TokenIn:   middleAsset,
				TokenOut:  req.ToToken.Address,
				AmountIn:  quote2.InputAmount,
				AmountOut: quote2.OutputAmount,
			},
		},
		TotalOutput:      quote2.OutputAmount,
		MiddleAsset:      middleAsset,
		TotalPriceImpact: quote1.PriceImpact + quote2.PriceImpact,
		TotalGas:         totalGas,
		IsDirect:         false,
	}, nil
}

// poolAddress 从 Quote 中提取池子地址。
func (r *TwoHopRouter) poolAddress(quote *dexwallet.Quote) string {
	if quote.Pool != nil {
		return quote.Pool.Address
	}
	return "unknown"
}
