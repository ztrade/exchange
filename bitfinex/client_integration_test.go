//go:build integration

package bitfinex

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ztrade/exchange"
	. "github.com/ztrade/trademodel"
)

const (
	integrationEnabledEnv = "BITFINEX_INTEGRATION"
	orderEnabledEnv       = "BITFINEX_ENABLE_ORDER_TESTS"
	cancelAllConfirmEnv   = "BITFINEX_ENABLE_CANCEL_ALL_TEST"
)

func TestBitfinexIntegrationPublicREST(t *testing.T) {
	requireBitfinexIntegration(t)

	for _, kind := range []Kind{KindSpot, KindFutures} {
		t.Run(string(kind), func(t *testing.T) {
			client := newIntegrationClient(t, kind, false)

			info := client.Info()
			if info.Value != "bitfinex_"+string(kind) || info.KLineLimit.Limit <= 0 {
				t.Fatalf("unexpected exchange info: %#v", info)
			}

			symbols, err := client.Symbols()
			if err != nil {
				t.Fatalf("Symbols: %v", err)
			}
			if len(symbols) == 0 {
				t.Fatalf("Symbols returned no %s symbols for currency %s", kind, client.currency)
			}
			for _, symbol := range symbols {
				if !client.matchesKind(symbol.Symbol) {
					t.Fatalf("Symbols returned a symbol for the wrong market kind: %#v", symbol)
				}
			}
			t.Logf("received %d %s symbols; first=%s", len(symbols), kind, symbols[0].Symbol)

			end := time.Now()
			start := end.Add(-30 * time.Minute)
			candles, err := client.GetKline(integrationSymbol(kind), "1m", start, end)
			if err != nil {
				t.Fatalf("GetKline: %v", err)
			}
			if len(candles) == 0 {
				t.Fatal("GetKline returned no closed candles")
			}
			for i, item := range candles {
				if item.Open <= 0 || item.High <= 0 || item.Low <= 0 || item.Close <= 0 || item.Volume < 0 {
					t.Fatalf("invalid candle at %d: %#v", i, item)
				}
				if i > 0 && item.Start <= candles[i-1].Start {
					t.Fatalf("candles are not strictly ascending at %d: %d <= %d", i, item.Start, candles[i-1].Start)
				}
			}
			t.Logf("received %d candles; last=%s", len(candles), candles[len(candles)-1])
		})
	}
}

func TestBitfinexIntegrationStartStop(t *testing.T) {
	requireBitfinexIntegration(t)

	client := newIntegrationClient(t, integrationKind("BITFINEX_PUBLIC_KIND", KindSpot), false)
	if err := client.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(2 * time.Second)
	if err := client.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := client.Start(); err == nil {
		t.Fatal("Start succeeded after Stop; stopped clients must not be reusable")
	}
}

func TestBitfinexIntegrationWatchMarketData(t *testing.T) {
	requireBitfinexIntegration(t)

	kind := integrationKind("BITFINEX_PUBLIC_KIND", KindSpot)
	client := newIntegrationClient(t, kind, false)
	if err := client.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	symbol := integrationSymbol(kind)
	depthCh := make(chan *Depth, 1)
	tradeCh := make(chan *Trade, 1)

	if err := client.Watch(exchange.WatchParam{
		Type:  exchange.WatchTypeDepth,
		Param: map[string]string{"symbol": symbol},
	}, func(value interface{}) {
		depth, ok := value.(*Depth)
		if !ok {
			return
		}
		select {
		case depthCh <- depth:
		default:
		}
	}); err != nil {
		t.Fatalf("Watch depth: %v", err)
	}
	if err := client.Watch(exchange.WatchParam{
		Type:  exchange.WatchTypeTradeMarket,
		Param: map[string]string{"symbol": symbol},
	}, func(value interface{}) {
		trade, ok := value.(*Trade)
		if !ok {
			return
		}
		select {
		case tradeCh <- trade:
		default:
		}
	}); err != nil {
		t.Fatalf("Watch market trades: %v", err)
	}

	timeout := time.NewTimer(integrationDuration("BITFINEX_WATCH_TIMEOUT", 30*time.Second))
	defer timeout.Stop()
	var depth *Depth
	var marketTrade *Trade
	for depth == nil || marketTrade == nil {
		select {
		case depth = <-depthCh:
			if len(depth.Buys) == 0 || len(depth.Sells) == 0 {
				t.Fatalf("invalid depth snapshot: %#v", depth)
			}
		case marketTrade = <-tradeCh:
			if marketTrade.ID == "" || marketTrade.Price <= 0 || marketTrade.Amount <= 0 {
				t.Fatalf("invalid market trade: %#v", marketTrade)
			}
		case <-timeout.C:
			t.Fatalf("timed out waiting for market data: depth=%t trade=%t", depth != nil, marketTrade != nil)
		}
	}
	t.Logf("depth best bid=%v best ask=%v", depth.Buys[0], depth.Sells[0])
	t.Logf("market trade=%#v", marketTrade)
}

func TestBitfinexIntegrationWatchCandle(t *testing.T) {
	requireBitfinexIntegration(t)
	if !integrationBool("BITFINEX_ENABLE_CANDLE_WATCH") {
		t.Skip("set BITFINEX_ENABLE_CANDLE_WATCH=1; this test waits for a 1m candle rollover")
	}

	kind := integrationKind("BITFINEX_PUBLIC_KIND", KindSpot)
	client := newIntegrationClient(t, kind, false)
	if err := client.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	candleCh := make(chan *Candle, 1)
	if err := client.Watch(exchange.WatchCandle(integrationSymbol(kind), "1m"), func(value interface{}) {
		item, ok := value.(*Candle)
		if !ok {
			return
		}
		select {
		case candleCh <- item:
		default:
		}
	}); err != nil {
		t.Fatalf("Watch candle: %v", err)
	}

	select {
	case item := <-candleCh:
		if item.Open <= 0 || item.High <= 0 || item.Low <= 0 || item.Close <= 0 {
			t.Fatalf("invalid candle: %#v", item)
		}
		t.Logf("closed candle=%s", item)
	case <-time.After(integrationDuration("BITFINEX_CANDLE_WATCH_TIMEOUT", 90*time.Second)):
		t.Fatal("timed out waiting for a closed 1m candle")
	}
}

func TestBitfinexIntegrationAccount(t *testing.T) {
	requireBitfinexIntegration(t)
	requireBitfinexCredentials(t)

	kind := integrationKind("BITFINEX_PRIVATE_KIND", KindSpot)
	client := newIntegrationClient(t, kind, true)
	if err := client.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	balanceCh := make(chan *Balance, 2)
	positionCh := make(chan *Position, 8)

	if err := client.Watch(exchange.WatchParam{Type: exchange.WatchTypeBalance}, func(value interface{}) {
		balance, ok := value.(*Balance)
		if !ok {
			return
		}
		select {
		case balanceCh <- balance:
		default:
		}
	}); err != nil {
		t.Fatalf("Watch balance: %v", err)
	}
	if err := client.Watch(exchange.WatchParam{Type: exchange.WatchTypePosition}, func(value interface{}) {
		position, ok := value.(*Position)
		if !ok {
			return
		}
		select {
		case positionCh <- position:
		default:
		}
	}); err != nil {
		t.Fatalf("Watch position: %v", err)
	}

	select {
	case balance := <-balanceCh:
		if balance.Currency == "" {
			t.Fatalf("invalid balance: %#v", balance)
		}
		t.Logf("balance=%#v", balance)
	case <-time.After(integrationDuration("BITFINEX_ACCOUNT_TIMEOUT", 20*time.Second)):
		t.Fatal("timed out waiting for balance data; check BITFINEX_CURRENCY and API permissions")
	}

	select {
	case position := <-positionCh:
		t.Logf("position=%#v", position)
	case <-time.After(3 * time.Second):
		t.Log("no non-zero position was returned; this is valid for an empty account")
	}
}

func TestBitfinexIntegrationOrderLifecycle(t *testing.T) {
	requireOrderIntegration(t)

	kind := integrationKind("BITFINEX_ORDER_KIND", KindSpot)
	client := newIntegrationClient(t, kind, true)
	orderEventCh := make(chan *Order, 8)
	if err := client.Watch(exchange.WatchParam{Type: exchange.WatchTypeTrade}, func(value interface{}) {
		order, ok := value.(*Order)
		if !ok {
			return
		}
		select {
		case orderEventCh <- order:
		default:
		}
	}); err != nil {
		t.Fatalf("Watch order events: %v", err)
	}
	// Authentication happens asynchronously after the websocket opens.
	time.Sleep(integrationDuration("BITFINEX_AUTH_WAIT", 3*time.Second))
	amount := integrationRequiredFloat(t, "BITFINEX_ORDER_AMOUNT")
	price := integrationReferencePrice(t, client, integrationSymbol(kind)) * integrationOrderPriceFactor(t)

	created, err := client.ProcessOrder(TradeAction{
		Action: Limit | OpenLong,
		Amount: amount,
		Price:  price,
		Time:   time.Now(),
		Symbol: integrationSymbol(kind),
	})
	if err != nil {
		t.Fatalf("ProcessOrder: %v", err)
	}
	cancelled := false
	t.Cleanup(func() {
		if !cancelled {
			if _, cleanupErr := client.CancelOrder(created); cleanupErr != nil {
				t.Logf("cleanup CancelOrder failed: %v", cleanupErr)
			}
		}
	})
	t.Logf("created order=%#v", created)
	if created.OrderID == "" {
		t.Fatal("ProcessOrder returned an empty order ID")
	}

	canceledOrder, err := client.CancelOrder(created)
	if err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	cancelled = true
	if canceledOrder.OrderID != created.OrderID || canceledOrder.Status != OrderStatusCanceled {
		t.Fatalf("unexpected canceled order: %#v", canceledOrder)
	}
	t.Logf("canceled order=%#v", canceledOrder)

	eventTimeout := time.NewTimer(integrationDuration("BITFINEX_ORDER_EVENT_TIMEOUT", 20*time.Second))
	defer eventTimeout.Stop()
	for {
		select {
		case event := <-orderEventCh:
			t.Logf("order websocket event=%#v", event)
			if event.OrderID == created.OrderID {
				return
			}
		case <-eventTimeout.C:
			t.Fatalf("timed out waiting for websocket order event for %s", created.OrderID)
		}
	}
}

func TestBitfinexIntegrationCancelAllOrders(t *testing.T) {
	requireOrderIntegration(t)
	if os.Getenv(cancelAllConfirmEnv) != "YES_I_UNDERSTAND" {
		t.Skip("set BITFINEX_ENABLE_CANCEL_ALL_TEST=YES_I_UNDERSTAND; this cancels every active order of the selected kind")
	}

	kind := integrationKind("BITFINEX_ORDER_KIND", KindSpot)
	client := newIntegrationClient(t, kind, true)
	amount := integrationRequiredFloat(t, "BITFINEX_ORDER_AMOUNT")
	referencePrice := integrationReferencePrice(t, client, integrationSymbol(kind))
	factor := integrationOrderPriceFactor(t)
	created := make([]*Order, 0, 2)
	for _, multiplier := range []float64{factor, factor * 0.9} {
		order, err := client.ProcessOrder(TradeAction{
			Action: Limit | OpenLong,
			Amount: amount,
			Price:  referencePrice * multiplier,
			Time:   time.Now(),
			Symbol: integrationSymbol(kind),
		})
		if err != nil {
			for _, existing := range created {
				_, _ = client.CancelOrder(existing)
			}
			t.Fatalf("ProcessOrder: %v", err)
		}
		created = append(created, order)
	}
	t.Cleanup(func() {
		for _, order := range created {
			_, _ = client.CancelOrder(order)
		}
	})

	canceled, err := client.CancelAllOrders()
	if err != nil {
		t.Fatalf("CancelAllOrders: %v", err)
	}
	returned := make(map[string]bool, len(canceled))
	for _, order := range canceled {
		returned[order.OrderID] = true
		t.Logf("canceled order=%#v", order)
	}
	for _, order := range created {
		if !returned[order.OrderID] {
			t.Fatalf("CancelAllOrders did not return created order %s", order.OrderID)
		}
	}
}

func newIntegrationClient(t *testing.T, kind Kind, private bool) *Client {
	t.Helper()
	config := BitfinexConfig{
		Kind:     string(kind),
		Currency: integrationString("BITFINEX_CURRENCY", "USDT"),
		Timeout:  integrationDuration("BITFINEX_TIMEOUT", 30*time.Second),
		Leverage: integrationInt64(t, "BITFINEX_LEVERAGE", 0),
		RESTURL:  os.Getenv("BITFINEX_REST_URL"),
		WSURL:    os.Getenv("BITFINEX_WS_URL"),
		IsTest:   integrationBool("BITFINEX_IS_TEST"),
	}
	if private {
		config.Key = os.Getenv("BITFINEX_API_KEY")
		config.Secret = os.Getenv("BITFINEX_API_SECRET")
	}
	client, err := NewClient(config, os.Getenv("BITFINEX_PROXY"), kind)
	if err != nil {
		t.Fatalf("NewClient(%s): %v", kind, err)
	}
	t.Cleanup(func() {
		if err := client.Stop(); err != nil {
			t.Logf("Stop cleanup failed: %v", err)
		}
	})
	return client
}

func requireBitfinexIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("Bitfinex integration test skipped in short mode")
	}
	if !integrationBool(integrationEnabledEnv) {
		t.Skip("set BITFINEX_INTEGRATION=1 and run with -tags=integration")
	}
}

func requireBitfinexCredentials(t *testing.T) {
	t.Helper()
	if os.Getenv("BITFINEX_API_KEY") == "" || os.Getenv("BITFINEX_API_SECRET") == "" {
		t.Skip("set BITFINEX_API_KEY and BITFINEX_API_SECRET")
	}
}

func requireOrderIntegration(t *testing.T) {
	t.Helper()
	requireBitfinexIntegration(t)
	requireBitfinexCredentials(t)
	if !integrationBool(orderEnabledEnv) {
		t.Skip("set BITFINEX_ENABLE_ORDER_TESTS=1; use a Bitfinex paper-trading account")
	}
	if os.Getenv("BITFINEX_ORDER_AMOUNT") == "" {
		t.Skip("set BITFINEX_ORDER_AMOUNT explicitly; no default is used for safety")
	}
}

func integrationReferencePrice(t *testing.T, client *Client, symbol string) float64 {
	t.Helper()
	end := time.Now()
	candles, err := client.GetKline(symbol, "1m", end.Add(-15*time.Minute), end)
	if err != nil {
		t.Fatalf("GetKline for reference price: %v", err)
	}
	if len(candles) == 0 || candles[len(candles)-1].Close <= 0 {
		t.Fatal("no valid candle available for reference price")
	}
	return candles[len(candles)-1].Close
}

func integrationOrderPriceFactor(t *testing.T) float64 {
	t.Helper()
	factor := integrationFloat(t, "BITFINEX_ORDER_PRICE_FACTOR", 0.5)
	if factor <= 0 || factor >= 0.9 {
		t.Fatalf("BITFINEX_ORDER_PRICE_FACTOR must be > 0 and < 0.9, got %v", factor)
	}
	return factor
}

func integrationSymbol(kind Kind) string {
	if kind == KindFutures {
		return integrationString("BITFINEX_FUTURES_SYMBOL", "BTCUSDT")
	}
	return integrationString("BITFINEX_SPOT_SYMBOL", "BTCUSDT")
}

func integrationKind(name string, fallback Kind) Kind {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch value {
	case "futures", "future", "derivatives", "derivative", "contract", "swap":
		return KindFutures
	case "spot":
		return KindSpot
	default:
		return fallback
	}
}

func integrationString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func integrationBool(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return value == "1" || value == "true" || value == "yes"
}

func integrationDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}

func integrationRequiredFloat(t *testing.T, name string) float64 {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 {
		t.Fatalf("%s must be a positive number, got %q", name, value)
	}
	return parsed
}

func integrationFloat(t *testing.T, name string, fallback float64) float64 {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		t.Fatalf("parse %s=%q: %v", name, value, err)
	}
	return parsed
}

func integrationInt64(t *testing.T, name string, fallback int64) int64 {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("parse %s=%q: %v", name, value, err)
	}
	return parsed
}

func Example_bitfinexIntegrationEnvironment() {
	fmt.Println("BITFINEX_INTEGRATION=1 go test -tags=integration -run TestBitfinexIntegrationPublicREST -v ./bitfinex")
	// Output:
	// BITFINEX_INTEGRATION=1 go test -tags=integration -run TestBitfinexIntegrationPublicREST -v ./bitfinex
}
