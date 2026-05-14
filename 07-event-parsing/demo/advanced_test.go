package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/yys9517/onchain-dex-lab/internal/coinset"
	"github.com/yys9517/onchain-dex-lab/internal/dexwallet"
)

// ============================================================================
// Helper: build Solana tx with inner instructions
// ============================================================================

func buildSolanaTxWithInner(t *testing.T, txHash string, block uint64,
	insts []MockInstruction, inner []MockInnerInstruction,
	sender string, success bool) []byte {
	t.Helper()
	s := success
	tx := MockRawTransaction{
		TxHash:            txHash,
		Block:             block,
		Timestamp:         time.Now().Unix(),
		ChainID:           string(coinset.ChainSolana),
		Instructions:      insts,
		InnerInstructions: inner,
		Sender:            sender,
		Success:           &s,
	}
	data, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// ============================================================================
// 1. FlattenInstructions Tests
// ============================================================================

func TestFlattenNoInner(t *testing.T) {
	topLevel := []MockInstruction{
		{ProgramID: RaydiumAMMProgramID, Data: []byte(`{"pool":"p1"}`)},
		{ProgramID: TokenProgramID, Data: []byte(`{"mint":"m1"}`)},
		{ProgramID: PumpFunProgramID, Data: []byte(`{"mint":"m2"}`)},
	}

	flat := FlattenInstructions(topLevel, nil)

	if len(flat) != 3 {
		t.Fatalf("expected 3 flat instructions, got %d", len(flat))
	}
	for i, fi := range flat {
		if fi.IsInner {
			t.Errorf("[%d] expected IsInner=false, got true", i)
		}
		if fi.ParentProgramID != "" {
			t.Errorf("[%d] expected empty ParentProgramID, got %q", i, fi.ParentProgramID)
		}
		if fi.ProgramID != topLevel[i].ProgramID {
			t.Errorf("[%d] expected ProgramID=%s, got %s", i, topLevel[i].ProgramID, fi.ProgramID)
		}
		if fi.Index != i {
			t.Errorf("[%d] expected Index=%d, got %d", i, i, fi.Index)
		}
	}
}

func TestFlattenWithInner(t *testing.T) {
	jupiterProgramID := "JUP4Fb2cqiRUcaTHdrPC8h2gNsA2ETXiPDD33WcGuJB"

	topLevel := []MockInstruction{
		{ProgramID: jupiterProgramID, Data: []byte(`{"route":"multi-hop"}`)},
	}
	inner := []MockInnerInstruction{
		{
			InstructionIndex: 0,
			Instructions: []MockInstruction{
				{ProgramID: RaydiumAMMProgramID, Data: []byte(`{"pool":"raydium_pool"}`)},
				{ProgramID: TokenProgramID, Data: []byte(`{"mint":"usdc"}`)},
				{ProgramID: "whirLbMiicVdio4qvUfM5KAg6Ct8VwpYzGff3uctyCc", Data: []byte(`{"pool":"orca_pool"}`)},
			},
		},
	}

	flat := FlattenInstructions(topLevel, inner)

	if len(flat) != 4 {
		t.Fatalf("expected 4 flat instructions, got %d", len(flat))
	}

	// [0] Jupiter top-level
	if flat[0].IsInner {
		t.Error("[0] expected IsInner=false")
	}
	if flat[0].ProgramID != jupiterProgramID {
		t.Errorf("[0] expected ProgramID=%s, got %s", jupiterProgramID, flat[0].ProgramID)
	}

	// [1] Raydium swap (inner, parent=Jupiter)
	if !flat[1].IsInner {
		t.Error("[1] expected IsInner=true")
	}
	if flat[1].ParentProgramID != jupiterProgramID {
		t.Errorf("[1] expected ParentProgramID=%s, got %s", jupiterProgramID, flat[1].ParentProgramID)
	}
	if flat[1].ProgramID != RaydiumAMMProgramID {
		t.Errorf("[1] expected ProgramID=%s, got %s", RaydiumAMMProgramID, flat[1].ProgramID)
	}

	// [2] Token transfer (inner, parent=Jupiter)
	if !flat[2].IsInner {
		t.Error("[2] expected IsInner=true")
	}
	if flat[2].ParentProgramID != jupiterProgramID {
		t.Errorf("[2] expected ParentProgramID=%s, got %s", jupiterProgramID, flat[2].ParentProgramID)
	}
	if flat[2].ProgramID != TokenProgramID {
		t.Errorf("[2] expected ProgramID=%s, got %s", TokenProgramID, flat[2].ProgramID)
	}

	// [3] Orca swap (inner, parent=Jupiter)
	if !flat[3].IsInner {
		t.Error("[3] expected IsInner=true")
	}
	if flat[3].ParentProgramID != jupiterProgramID {
		t.Errorf("[3] expected ParentProgramID=%s, got %s", jupiterProgramID, flat[3].ParentProgramID)
	}
}

func TestFlattenMultiTopWithInner(t *testing.T) {
	top0 := MockInstruction{ProgramID: "ProgramA", Data: []byte(`{"a":1}`)}
	top1 := MockInstruction{ProgramID: "ProgramB", Data: []byte(`{"b":2}`)}

	topLevel := []MockInstruction{top0, top1}
	inner := []MockInnerInstruction{
		{
			InstructionIndex: 0,
			Instructions: []MockInstruction{
				{ProgramID: "InnerC", Data: []byte(`{"c":3}`)},
				{ProgramID: "InnerD", Data: []byte(`{"d":4}`)},
			},
		},
		// No inner instructions for top1
	}

	flat := FlattenInstructions(topLevel, inner)

	// Expected order: top0, inner0-0, inner0-1, top1
	if len(flat) != 4 {
		t.Fatalf("expected 4 flat instructions, got %d", len(flat))
	}

	// top0
	if flat[0].ProgramID != "ProgramA" || flat[0].IsInner {
		t.Errorf("[0] expected top-level ProgramA, got ProgramID=%s IsInner=%v", flat[0].ProgramID, flat[0].IsInner)
	}
	// inner0-0
	if flat[1].ProgramID != "InnerC" || !flat[1].IsInner {
		t.Errorf("[1] expected inner InnerC, got ProgramID=%s IsInner=%v", flat[1].ProgramID, flat[1].IsInner)
	}
	if flat[1].ParentProgramID != "ProgramA" {
		t.Errorf("[1] expected ParentProgramID=ProgramA, got %s", flat[1].ParentProgramID)
	}
	// inner0-1
	if flat[2].ProgramID != "InnerD" || !flat[2].IsInner {
		t.Errorf("[2] expected inner InnerD, got ProgramID=%s IsInner=%v", flat[2].ProgramID, flat[2].IsInner)
	}
	// top1
	if flat[3].ProgramID != "ProgramB" || flat[3].IsInner {
		t.Errorf("[3] expected top-level ProgramB, got ProgramID=%s IsInner=%v", flat[3].ProgramID, flat[3].IsInner)
	}
}

// ============================================================================
// 2. BalanceDiffCalculator Tests
// ============================================================================

func TestBalanceDiffCalculate(t *testing.T) {
	calc := NewBalanceDiffCalculator()

	pre := []MockTokenBalance{
		{AccountIndex: 0, Owner: "Alice", Mint: "SOL", Amount: "1000000000"},
	}
	post := []MockTokenBalance{
		{AccountIndex: 0, Owner: "Alice", Mint: "SOL", Amount: "900000000"},
	}

	diffs := calc.Calculate(pre, post)

	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}

	d := diffs[0]
	if d.Owner != "Alice" {
		t.Errorf("expected owner Alice, got %s", d.Owner)
	}
	if d.Mint != "SOL" {
		t.Errorf("expected mint SOL, got %s", d.Mint)
	}

	expectedChange := big.NewInt(-100000000)
	if d.Change.Cmp(expectedChange) != 0 {
		t.Errorf("expected change %s, got %s", expectedChange, d.Change)
	}
}

func TestBalanceDiffValidateMatch(t *testing.T) {
	calc := NewBalanceDiffCalculator()

	// Alice lost 100 SOL (account 0) and gained 150 USDC (account 1)
	pre := []MockTokenBalance{
		{AccountIndex: 0, Owner: "Alice", Mint: "SOL", Amount: "200"},
		{AccountIndex: 1, Owner: "Alice", Mint: "USDC", Amount: "50"},
	}
	post := []MockTokenBalance{
		{AccountIndex: 0, Owner: "Alice", Mint: "SOL", Amount: "100"},
		{AccountIndex: 1, Owner: "Alice", Mint: "USDC", Amount: "200"},
	}

	diffs := calc.Calculate(pre, post)

	event := &dexwallet.ChainEvent{
		Type:      dexwallet.EventSwap,
		Sender:    "Alice",
		TokenIn:   "SOL",
		TokenOut:  "USDC",
		AmountIn:  big.NewInt(100),
		AmountOut: big.NewInt(150),
	}

	validation := calc.Validate(diffs, event)

	if !validation.IsValid {
		t.Errorf("expected IsValid=true, got false. Discrepancy: %s", validation.Discrepancy)
	}
}

func TestBalanceDiffValidateMismatch(t *testing.T) {
	calc := NewBalanceDiffCalculator()

	// Alice lost 100 SOL and gained 150 USDC
	pre := []MockTokenBalance{
		{AccountIndex: 0, Owner: "Alice", Mint: "SOL", Amount: "200"},
		{AccountIndex: 1, Owner: "Alice", Mint: "USDC", Amount: "50"},
	}
	post := []MockTokenBalance{
		{AccountIndex: 0, Owner: "Alice", Mint: "SOL", Amount: "100"},
		{AccountIndex: 1, Owner: "Alice", Mint: "USDC", Amount: "200"},
	}

	diffs := calc.Calculate(pre, post)

	// Event claims different amounts than the actual balance diff
	event := &dexwallet.ChainEvent{
		Type:      dexwallet.EventSwap,
		Sender:    "Alice",
		TokenIn:   "SOL",
		TokenOut:  "USDC",
		AmountIn:  big.NewInt(999), // mismatch: actual diff is 100
		AmountOut: big.NewInt(150),
	}

	validation := calc.Validate(diffs, event)

	if validation.IsValid {
		t.Error("expected IsValid=false for mismatched amounts")
	}
	if validation.Discrepancy == "" {
		t.Error("expected non-empty Discrepancy explanation")
	}
}

// ============================================================================
// 3. TxClassifier Tests
// ============================================================================

func newTestClassifier(addressMap map[string]AddressType) *TxClassifier {
	return NewTxClassifier(func(addr string) AddressType {
		if t, ok := addressMap[addr]; ok {
			return t
		}
		return AddressTypeUnknown
	})
}

func TestClassifySwapEvent(t *testing.T) {
	classifier := newTestClassifier(nil)

	events := []dexwallet.ChainEvent{
		{
			Type:   dexwallet.EventSwap,
			Sender: "anyone",
		},
	}

	classified := classifier.Classify(events)

	if classified[0].TxType != dexwallet.TxTypeSwap {
		t.Errorf("expected TxTypeSwap, got %s", classified[0].TxType)
	}
}

func TestClassifyInbound(t *testing.T) {
	// Unknown -> User = inbound
	classifier := newTestClassifier(map[string]AddressType{
		"userAddr": AddressTypeUser,
	})

	events := []dexwallet.ChainEvent{
		{
			Type:     dexwallet.EventTransfer,
			Sender:   "externalAddr",
			TokenIn:  "externalAddr",
			TokenOut: "userAddr",
		},
	}

	classified := classifier.Classify(events)

	if classified[0].TxType != dexwallet.TxTypeInbound {
		t.Errorf("expected TxTypeInbound, got %s", classified[0].TxType)
	}
}

func TestClassifyOutbound(t *testing.T) {
	// User -> Unknown = outbound
	classifier := newTestClassifier(map[string]AddressType{
		"userAddr": AddressTypeUser,
	})

	events := []dexwallet.ChainEvent{
		{
			Type:     dexwallet.EventTransfer,
			Sender:   "userAddr",
			TokenIn:  "userAddr",
			TokenOut: "externalAddr",
		},
	}

	classified := classifier.Classify(events)

	if classified[0].TxType != dexwallet.TxTypeOutbound {
		t.Errorf("expected TxTypeOutbound, got %s", classified[0].TxType)
	}
}

func TestClassifyInternal(t *testing.T) {
	// User -> User = internal
	classifier := newTestClassifier(map[string]AddressType{
		"userA": AddressTypeUser,
		"userB": AddressTypeUser,
	})

	events := []dexwallet.ChainEvent{
		{
			Type:     dexwallet.EventTransfer,
			Sender:   "userA",
			TokenIn:  "userA",
			TokenOut: "userB",
		},
	}

	classified := classifier.Classify(events)

	if classified[0].TxType != dexwallet.TxTypeInternal {
		t.Errorf("expected TxTypeInternal, got %s", classified[0].TxType)
	}
}

func TestClassifySystem(t *testing.T) {
	// System -> anything = system
	classifier := newTestClassifier(map[string]AddressType{
		"systemAddr": AddressTypeSystem,
		"userAddr":   AddressTypeUser,
	})

	events := []dexwallet.ChainEvent{
		{
			Type:     dexwallet.EventTransfer,
			Sender:   "systemAddr",
			TokenIn:  "systemAddr",
			TokenOut: "userAddr",
		},
	}

	classified := classifier.Classify(events)

	if classified[0].TxType != dexwallet.TxTypeSystem {
		t.Errorf("expected TxTypeSystem, got %s", classified[0].TxType)
	}
}

func TestClassifyBatch(t *testing.T) {
	classifier := newTestClassifier(map[string]AddressType{
		"userA":      AddressTypeUser,
		"userB":      AddressTypeUser,
		"systemAddr": AddressTypeSystem,
	})

	events := []dexwallet.ChainEvent{
		// swap
		{Type: dexwallet.EventSwap, Sender: "userA"},
		// inbound: Unknown -> User
		{Type: dexwallet.EventTransfer, TokenIn: "externalAddr", TokenOut: "userA"},
		// outbound: User -> Unknown
		{Type: dexwallet.EventTransfer, TokenIn: "userA", TokenOut: "externalAddr"},
		// internal: User -> User
		{Type: dexwallet.EventTransfer, TokenIn: "userA", TokenOut: "userB"},
		// system
		{Type: dexwallet.EventTransfer, TokenIn: "systemAddr", TokenOut: "userA"},
	}

	grouped := classifier.ClassifyBatch(events)

	if len(grouped[dexwallet.TxTypeSwap]) != 1 {
		t.Errorf("expected 1 swap, got %d", len(grouped[dexwallet.TxTypeSwap]))
	}
	if len(grouped[dexwallet.TxTypeInbound]) != 1 {
		t.Errorf("expected 1 inbound, got %d", len(grouped[dexwallet.TxTypeInbound]))
	}
	if len(grouped[dexwallet.TxTypeOutbound]) != 1 {
		t.Errorf("expected 1 outbound, got %d", len(grouped[dexwallet.TxTypeOutbound]))
	}
	if len(grouped[dexwallet.TxTypeInternal]) != 1 {
		t.Errorf("expected 1 internal, got %d", len(grouped[dexwallet.TxTypeInternal]))
	}
	if len(grouped[dexwallet.TxTypeSystem]) != 1 {
		t.Errorf("expected 1 system, got %d", len(grouped[dexwallet.TxTypeSystem]))
	}
}

// ============================================================================
// 4. AddressFilter Tests
// ============================================================================

func TestAddressFilterContains(t *testing.T) {
	f := NewAddressFilter()

	f.Add("addr1")
	f.Add("addr2")

	if !f.Contains("addr1") {
		t.Error("expected Contains(addr1)=true")
	}
	if !f.Contains("addr2") {
		t.Error("expected Contains(addr2)=true")
	}
	if f.Contains("addr3") {
		t.Error("expected Contains(addr3)=false")
	}
	if f.Contains("") {
		t.Error("expected Contains('')=false")
	}
}

func TestAddressFilterShouldProcess(t *testing.T) {
	f := NewAddressFilter()
	f.Add("myWallet")

	// Transaction with Sender matching
	tx1 := &MockRawTransaction{
		TxHash: "tx1",
		Sender: "myWallet",
	}
	if !f.ShouldProcess(tx1) {
		t.Error("expected ShouldProcess=true when Sender matches")
	}

	// Transaction with no matching address
	tx2 := &MockRawTransaction{
		TxHash: "tx2",
		Sender: "unknown",
	}
	if f.ShouldProcess(tx2) {
		t.Error("expected ShouldProcess=false when no address matches")
	}

	// Transaction matching via PostTokenBalances owner
	tx3 := &MockRawTransaction{
		TxHash: "tx3",
		Sender: "other",
		PostTokenBalances: []MockTokenBalance{
			{AccountIndex: 0, Owner: "myWallet", Mint: "SOL", Amount: "100"},
		},
	}
	if !f.ShouldProcess(tx3) {
		t.Error("expected ShouldProcess=true when PostTokenBalances owner matches")
	}
}

func TestAddressFilterStats(t *testing.T) {
	f := NewAddressFilter()
	f.Add("myAddr")

	// Check a matching tx
	f.ShouldProcess(&MockRawTransaction{Sender: "myAddr"})
	// Check a non-matching tx
	f.ShouldProcess(&MockRawTransaction{Sender: "other"})
	// Check another non-matching tx
	f.ShouldProcess(&MockRawTransaction{Sender: "another"})

	checked, filtered := f.Stats()
	if checked != 3 {
		t.Errorf("expected checked=3, got %d", checked)
	}
	if filtered != 2 {
		t.Errorf("expected filtered=2, got %d", filtered)
	}
}

// ============================================================================
// 5. Pipeline Tests
// ============================================================================

func TestPipelineFullFlow(t *testing.T) {
	af := NewAddressFilter()
	af.Add("Alice")

	pipeline := NewPipeline().
		AddStage(NewFailedTxFilterStage()).
		AddStage(NewBalanceValidationStage(nil, nil)).
		AddStage(NewClassificationStage(func(addr string) AddressType {
			if addr == "Alice" {
				return AddressTypeUser
			}
			return AddressTypeUnknown
		})).
		AddStage(NewAddressFilterStage(af)).
		AddStage(NewDeduplicationStage())

	events := []dexwallet.ChainEvent{
		{
			Type:    dexwallet.EventSwap,
			TxHash:  "tx1",
			Sender:  "Alice",
			Pool:    "pool1",
			Success: true,
		},
		{
			Type:    dexwallet.EventSwap,
			TxHash:  "tx2",
			Sender:  "Alice",
			Pool:    "pool2",
			Success: false, // should be filtered by FailedTxFilter
		},
	}

	result, stats, err := pipeline.Run(context.Background(), events)
	if err != nil {
		t.Fatalf("pipeline run failed: %v", err)
	}

	if stats.EventsIn != 2 {
		t.Errorf("expected EventsIn=2, got %d", stats.EventsIn)
	}
	if stats.StagesRun != 5 {
		t.Errorf("expected StagesRun=5, got %d", stats.StagesRun)
	}
	// After FailedTxFilter: only tx1 remains (Success=true)
	// After AddressFilter: tx1 has Sender=Alice which is in filter
	if len(result) != 1 {
		t.Fatalf("expected 1 event after pipeline, got %d", len(result))
	}
	if result[0].TxHash != "tx1" {
		t.Errorf("expected surviving event tx1, got %s", result[0].TxHash)
	}
	if result[0].TxType != dexwallet.TxTypeSwap {
		t.Errorf("expected TxTypeSwap after classification, got %s", result[0].TxType)
	}
}

// failingStage is a stage that always returns an error.
type failingStage struct {
	name     string
	required bool
}

func (s *failingStage) Name() string   { return s.name }
func (s *failingStage) Required() bool { return s.required }
func (s *failingStage) Process(_ context.Context, events []dexwallet.ChainEvent) ([]dexwallet.ChainEvent, error) {
	return nil, fmt.Errorf("stage %s deliberately failed", s.name)
}

func TestPipelineRequiredStageFail(t *testing.T) {
	pipeline := NewPipeline().
		AddStage(NewFailedTxFilterStage()).
		AddStage(&failingStage{name: "RequiredFail", required: true}).
		AddStage(NewDeduplicationStage())

	events := []dexwallet.ChainEvent{
		{Type: dexwallet.EventSwap, TxHash: "tx1", Success: true},
	}

	_, stats, err := pipeline.Run(context.Background(), events)
	if err == nil {
		t.Fatal("expected error from required stage failure")
	}
	if stats.StagesFailed != 1 {
		t.Errorf("expected StagesFailed=1, got %d", stats.StagesFailed)
	}
}

func TestPipelineOptionalStageFail(t *testing.T) {
	pipeline := NewPipeline().
		AddStage(&failingStage{name: "OptionalFail", required: false}).
		AddStage(NewDeduplicationStage())

	events := []dexwallet.ChainEvent{
		{Type: dexwallet.EventSwap, TxHash: "tx1", Pool: "pool1"},
	}

	result, stats, err := pipeline.Run(context.Background(), events)
	if err != nil {
		t.Fatalf("expected no error for optional stage failure, got: %v", err)
	}
	if stats.StagesFailed != 1 {
		t.Errorf("expected StagesFailed=1, got %d", stats.StagesFailed)
	}
	// Events should still flow through
	if len(result) != 1 {
		t.Errorf("expected 1 event to pass through, got %d", len(result))
	}
}

// ============================================================================
// 6. UniswapV3 Parser Tests
// ============================================================================

func TestUniV3SwapToken0ToToken1(t *testing.T) {
	handler := NewUniV3SwapHandler()

	// amount0 > 0 (pool receives token0), amount1 < 0 (pool pays token1)
	// User perspective: pays token0, receives token1
	data, err := json.Marshal(UniV3SwapData{
		Pair:    "0xPoolV3",
		Token0:  "WETH",
		Token1:  "USDC",
		Amount0: "1000000000000000000",  // +1 WETH (pool receives)
		Amount1: "-3500000000",          // -3500 USDC (pool pays)
	})
	if err != nil {
		t.Fatal(err)
	}

	event, err := handler(context.Background(), data)
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if event.TokenIn != "WETH" {
		t.Errorf("expected TokenIn=WETH, got %s", event.TokenIn)
	}
	if event.TokenOut != "USDC" {
		t.Errorf("expected TokenOut=USDC, got %s", event.TokenOut)
	}

	expectedIn, _ := new(big.Int).SetString("1000000000000000000", 10)
	if event.AmountIn.Cmp(expectedIn) != 0 {
		t.Errorf("expected AmountIn=%s, got %s", expectedIn, event.AmountIn)
	}

	expectedOut := big.NewInt(3500000000)
	if event.AmountOut.Cmp(expectedOut) != 0 {
		t.Errorf("expected AmountOut=%s, got %s", expectedOut, event.AmountOut)
	}

	if event.Type != dexwallet.EventSwap {
		t.Errorf("expected type swap, got %s", event.Type)
	}
	if event.DexID != dexwallet.DexUniswapV3 {
		t.Errorf("expected DexUniswapV3, got %s", event.DexID)
	}
}

func TestUniV3SwapToken1ToToken0(t *testing.T) {
	handler := NewUniV3SwapHandler()

	// amount0 < 0 (pool pays token0), amount1 > 0 (pool receives token1)
	// User perspective: pays token1, receives token0
	data, err := json.Marshal(UniV3SwapData{
		Pair:    "0xPoolV3",
		Token0:  "WETH",
		Token1:  "USDC",
		Amount0: "-1000000000000000000", // -1 WETH (pool pays)
		Amount1: "3500000000",            // +3500 USDC (pool receives)
	})
	if err != nil {
		t.Fatal(err)
	}

	event, err := handler(context.Background(), data)
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if event.TokenIn != "USDC" {
		t.Errorf("expected TokenIn=USDC, got %s", event.TokenIn)
	}
	if event.TokenOut != "WETH" {
		t.Errorf("expected TokenOut=WETH, got %s", event.TokenOut)
	}

	expectedIn := big.NewInt(3500000000)
	if event.AmountIn.Cmp(expectedIn) != 0 {
		t.Errorf("expected AmountIn=%s, got %s", expectedIn, event.AmountIn)
	}

	expectedOut, _ := new(big.Int).SetString("1000000000000000000", 10)
	if event.AmountOut.Cmp(expectedOut) != 0 {
		t.Errorf("expected AmountOut=%s, got %s", expectedOut, event.AmountOut)
	}
}

func TestUniV3SwapInvalidPattern(t *testing.T) {
	handler := NewUniV3SwapHandler()

	// Both positive -> error
	data, err := json.Marshal(UniV3SwapData{
		Pair:    "0xPoolV3",
		Token0:  "WETH",
		Token1:  "USDC",
		Amount0: "100",
		Amount1: "200",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = handler(context.Background(), data)
	if err == nil {
		t.Fatal("expected error for both-positive amounts pattern")
	}
}

// ============================================================================
// 7. InnerInstructions Integration with Parse
// ============================================================================

func TestParseWithInnerInstructions(t *testing.T) {
	registry := NewParserRegistry(coinset.ChainSolana)
	registry.Register(RaydiumAMMProgramID, NewRaydiumSwapHandler())

	jupiterProgramID := "JUP4Fb2cqiRUcaTHdrPC8h2gNsA2ETXiPDD33WcGuJB"

	raydiumSwapJSON := mustMarshal(t, RaydiumSwapData{
		Pool:      "RaydiumPool1",
		TokenIn:   "SOL",
		TokenOut:  "USDC",
		AmountIn:  "1000000000",
		AmountOut: "150000000",
	})

	rawTx := buildSolanaTxWithInner(t,
		"tx_jupiter_inner",
		500,
		[]MockInstruction{
			{ProgramID: jupiterProgramID, Data: []byte(`{"route":"SOL-USDC"}`)},
		},
		[]MockInnerInstruction{
			{
				InstructionIndex: 0,
				Instructions: []MockInstruction{
					{ProgramID: RaydiumAMMProgramID, Data: raydiumSwapJSON},
				},
			},
		},
		"UserWallet123",
		true,
	)

	events, err := registry.Parse(context.Background(), rawTx)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	// Jupiter top-level has no handler registered, so only the inner Raydium swap should parse
	if len(events) != 1 {
		t.Fatalf("expected 1 event (inner Raydium swap), got %d", len(events))
	}

	e := events[0]
	if e.Type != dexwallet.EventSwap {
		t.Errorf("expected EventSwap, got %s", e.Type)
	}
	if e.DexID != dexwallet.DexRaydiumAMM {
		t.Errorf("expected DexRaydiumAMM, got %s", e.DexID)
	}
	if !e.IsInnerInst {
		t.Error("expected IsInnerInst=true for CPI-produced event")
	}
	if e.ParentProgramID != jupiterProgramID {
		t.Errorf("expected ParentProgramID=%s, got %s", jupiterProgramID, e.ParentProgramID)
	}
	if e.ProgramID != RaydiumAMMProgramID {
		t.Errorf("expected ProgramID=%s, got %s", RaydiumAMMProgramID, e.ProgramID)
	}
	if e.Sender != "UserWallet123" {
		t.Errorf("expected Sender=UserWallet123, got %s", e.Sender)
	}
	if e.AmountIn.Cmp(big.NewInt(1000000000)) != 0 {
		t.Errorf("expected AmountIn=1000000000, got %s", e.AmountIn)
	}
	if e.AmountOut.Cmp(big.NewInt(150000000)) != 0 {
		t.Errorf("expected AmountOut=150000000, got %s", e.AmountOut)
	}
}
