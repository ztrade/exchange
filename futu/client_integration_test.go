//go:build integration

package futu

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
	integrationEnabledEnv = "FUTU_INTEGRATION"
	tradeEnabledEnv       = "FUTU_ENABLE_TRADE_TESTS"
	orderEnabledEnv       = "FUTU_ENABLE_ORDER_TESTS"
	cancelAllConfirmEnv   = "FUTU_ENABLE_CANCEL_ALL_TEST"
	historyEnabledEnv     = "FUTU_ENABLE_HISTORY_TEST"
	candleWatchEnabledEnv = "FUTU_ENABLE_CANDLE_WATCH"
)

func TestFutuIntegrationPublicREST(t *testing.T) {
	requireFutuIntegration(t)

	client := newIntegrationClient(t, false)

	info := client.Info()
	if info.Value != "futu" || info.KLineLimit.Limit <= 0 {
		t.Fatalf("unexpected exchange info: %#v", info)
	}

	symbols, err := client.Symbols()
	if err != nil {
		t.Fatalf("Symbols: %v", err)
	}
	if len(symbols) == 0 {
		t.Fatalf("Symbols returned nothing; configure FUTU_SYMBOLS/FUTU_PLATES or a valid FUTU_MARKET")
	}
	for _, symbol := range symbols {
		if symbol.Symbol == "" || symbol.Exchange != "futu" {
			t.Fatalf("invalid symbol: %#v", symbol)
		}
	}
	t.Logf("received %d symbols; first=%s", len(symbols), symbols[0].Symbol)

	symbol := integrationSymbol()
	end := time.Now()
	start := end.Add(-30 * time.Minute)
	candles, err := client.GetKline(symbol, "1m", start, end)
	if err != nil {
		t.Fatalf("GetKline(%s): %v", symbol, err)
	}
	if len(candles) == 0 {
		t.Fatalf("GetKline returned no closed candles; is the market open for %s?", symbol)
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
}

func TestFutuIntegrationHistoryKL(t *testing.T) {
	requireFutuIntegration(t)
	if !integrationBool(historyEnabledEnv) {
		t.Skip("set FUTU_ENABLE_HISTORY_TEST=1; this test consumes the online history K-line quota")
	}

	client := newIntegrationClient(t, false)
	symbol := integrationSymbol()
	end := time.Now()
	start := end.AddDate(0, 0, -30)
	candles, err := client.GetKline(symbol, "1d", start, end)
	if err != nil {
		t.Fatalf("GetKline history(%s): %v", symbol, err)
	}
	if len(candles) == 0 {
		t.Fatalf("GetKline history returned no daily candles for %s", symbol)
	}
	for i, item := range candles {
		if item.Open <= 0 || item.Close <= 0 || item.High < item.Low {
			t.Fatalf("invalid candle at %d: %#v", i, item)
		}
		if i > 0 && item.Start <= candles[i-1].Start {
			t.Fatalf("candles are not strictly ascending at %d: %d <= %d", i, item.Start, candles[i-1].Start)
		}
	}
	t.Logf("received %d daily candles; first=%s last=%s", len(candles), candles[0].Time(), candles[len(candles)-1].Time())
}

func TestFutuIntegrationStartStop(t *testing.T) {
	requireFutuIntegration(t)

	client := newIntegrationClient(t, false)
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

func TestFutuIntegrationWatchMarketData(t *testing.T) {
	requireFutuIntegration(t)

	client := newIntegrationClient(t, false)
	if err := client.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	symbol := integrationSymbol()
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

	timeout := time.NewTimer(integrationDuration("FUTU_WATCH_TIMEOUT", 30*time.Second))
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

func TestFutuIntegrationWatchCandle(t *testing.T) {
	requireFutuIntegration(t)
	if !integrationBool(candleWatchEnabledEnv) {
		t.Skip("set FUTU_ENABLE_CANDLE_WATCH=1; this test waits for a 1m candle rollover")
	}

	client := newIntegrationClient(t, false)
	if err := client.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	candleCh := make(chan *Candle, 1)
	if err := client.Watch(exchange.WatchCandle(integrationSymbol(), "1m"), func(value interface{}) {
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
	case <-time.After(integrationDuration("FUTU_CANDLE_WATCH_TIMEOUT", 90*time.Second)):
		t.Fatal("timed out waiting for a closed 1m candle; is the market open?")
	}
}

func TestFutuIntegrationAccount(t *testing.T) {
	requireFutuTrade(t)

	client := newIntegrationClient(t, true)
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
	case <-time.After(integrationDuration("FUTU_ACCOUNT_TIMEOUT", 20*time.Second)):
		t.Fatal("timed out waiting for balance data; check FUTU_TRD_ENV/FUTU_ACC_ID")
	}

	select {
	case position := <-positionCh:
		t.Logf("position=%#v", position)
	case <-time.After(3 * time.Second):
		t.Log("no non-zero position was returned; this is valid for an empty account")
	}
}

func TestFutuIntegrationOrderLifecycle(t *testing.T) {
	requireFutuOrder(t)

	client := newIntegrationClient(t, true)
	if err := client.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	symbol := integrationSymbol()
	orderCh := make(chan *Order, 32)
	if err := client.Watch(exchange.WatchParam{Type: exchange.WatchTypeTrade}, func(value interface{}) {
		order, ok := value.(*Order)
		if !ok {
			return
		}
		select {
		case orderCh <- order:
		default:
		}
	}); err != nil {
		t.Fatalf("Watch order events: %v", err)
	}

	referencePrice := integrationReferencePrice(t, client, symbol)
	price := referencePrice * integrationOrderPriceFactor(t)
	amount := integrationOrderAmount(t, symbol)
	created, err := client.ProcessOrder(TradeAction{
		Action: OpenLong | Limit,
		Amount: amount,
		Price:  price,
		Time:   time.Now(),
		Symbol: symbol,
	})
	if err != nil {
		t.Fatalf("ProcessOrder: %v", err)
	}
	t.Logf("created order=%#v (reference price=%v)", created, referencePrice)
	if created.OrderID == "" {
		t.Fatal("ProcessOrder returned an empty order ID")
	}

	eventTimeout := time.NewTimer(integrationDuration("FUTU_ORDER_EVENT_TIMEOUT", 30*time.Second))
	defer eventTimeout.Stop()
	if order := waitOrderEvent(t, orderCh, created.OrderID, "SUBMITTED", eventTimeout); order == nil {
		t.Fatal("timed out waiting for the order push; check account push subscription")
	} else {
		t.Logf("order push=%#v", order)
	}

	canceled, err := client.CancelOrder(created)
	if err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	t.Logf("canceled order=%#v", canceled)
	if order := waitOrderEvent(t, orderCh, created.OrderID, OrderStatusCanceled, eventTimeout); order == nil {
		t.Fatal("timed out waiting for the cancel push")
	} else {
		t.Logf("cancel push=%#v", order)
	}
}

func TestFutuIntegrationCancelAllOrders(t *testing.T) {
	requireFutuOrder(t)
	if !integrationBool(cancelAllConfirmEnv) {
		t.Skip("set FUTU_ENABLE_CANCEL_ALL_TEST=YES_I_UNDERSTAND; this cancels every active order of the account")
	}

	client := newIntegrationClient(t, true)
	if err := client.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	symbol := integrationSymbol()
	referencePrice := integrationReferencePrice(t, client, symbol)
	price := referencePrice * integrationOrderPriceFactor(t)
	amount := integrationOrderAmount(t, symbol)

	for i := 0; i < 2; i++ {
		created, err := client.ProcessOrder(TradeAction{
			Action: OpenLong | Limit,
			Amount: amount,
			Price:  price,
			Time:   time.Now(),
			Symbol: symbol,
		})
		if err != nil {
			t.Fatalf("ProcessOrder %d: %v", i, err)
		}
		t.Logf("created order[%d]=%#v", i, created)
	}

	orders, err := client.CancelAllOrders()
	if err != nil {
		t.Fatalf("CancelAllOrders: %v", err)
	}
	if len(orders) == 0 {
		t.Fatal("CancelAllOrders returned no orders")
	}
	for _, order := range orders {
		if order.Status != OrderStatusCanceled {
			t.Fatalf("order not canceled: %#v", order)
		}
		t.Logf("canceled=%#v", order)
	}
}

func newIntegrationClient(t *testing.T, private bool) *Client {
	t.Helper()
	config := FutuConfig{
		Addr:        integrationString("FUTU_ADDR", ":11111"),
		Timeout:     integrationDuration("FUTU_TIMEOUT", 15*time.Second),
		Market:      integrationString("FUTU_MARKET", "HK"),
		Symbols:     integrationList("FUTU_SYMBOLS"),
		Plates:      integrationList("FUTU_PLATES"),
		KLineLimit:  int(integrationInt64(t, "FUTU_KLINE_LIMIT", 1000)),
		Currency:    integrationString("FUTU_CURRENCY", ""),
		Resolutions: integrationString("FUTU_RESOLUTIONS", ""),
	}
	if private {
		config.TrdEnv = integrationString("FUTU_TRD_ENV", "simulate")
		config.AccID = uint64(integrationInt64(t, "FUTU_ACC_ID", 0))
		config.PwdMD5 = os.Getenv("FUTU_PWD_MD5")
		config.SecurityFirm = int32(integrationInt64(t, "FUTU_SECURITY_FIRM", 0))
		config.UnlockTrade = integrationBool("FUTU_UNLOCK_TRADE")
	}
	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient: %v (is FutuOpenD running at %s?)", err, config.Addr)
	}
	t.Cleanup(func() {
		if err := client.Stop(); err != nil {
			t.Logf("Stop cleanup failed: %v", err)
		}
	})
	return client
}

func requireFutuIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("Futu integration test skipped in short mode")
	}
	if !integrationBool(integrationEnabledEnv) {
		t.Skip("set FUTU_INTEGRATION=1 and run with -tags=integration")
	}
}

func requireFutuTrade(t *testing.T) {
	t.Helper()
	requireFutuIntegration(t)
	if !integrationBool(tradeEnabledEnv) {
		t.Skip("set FUTU_ENABLE_TRADE_TESTS=1 to enable account tests")
	}
}

func requireFutuOrder(t *testing.T) {
	t.Helper()
	requireFutuTrade(t)
	if !integrationBool(orderEnabledEnv) {
		t.Skip("set FUTU_ENABLE_ORDER_TESTS=1; prefer a simulation account (FUTU_TRD_ENV=simulate)")
	}
}

func waitOrderEvent(t *testing.T, ch chan *Order, orderID, wantStatus string, timer *time.Timer) *Order {
	t.Helper()
	for {
		select {
		case order := <-ch:
			if order.OrderID == orderID {
				t.Logf("order event: status=%s filled=%v", order.Status, order.Filled)
				if wantStatus == "SUBMITTED" && order.Status != "UNKNOWN" && order.Status != "" {
					return order
				}
				if order.Status == wantStatus {
					return order
				}
			}
		case <-timer.C:
			return nil
		}
	}
}

func integrationReferencePrice(t *testing.T, client *Client, symbol string) float64 {
	t.Helper()
	end := time.Now()
	for _, bin := range []string{"1m", "1d"} {
		start := end.Add(-15 * time.Minute)
		if bin == "1d" {
			start = end.AddDate(0, 0, -5)
		}
		candles, err := client.GetKline(symbol, bin, start, end)
		if err != nil {
			continue
		}
		if len(candles) > 0 && candles[len(candles)-1].Close > 0 {
			return candles[len(candles)-1].Close
		}
	}
	t.Fatal("no valid candle available for reference price; is the market open?")
	return 0
}

func integrationOrderPriceFactor(t *testing.T) float64 {
	t.Helper()
	factor := integrationFloat(t, "FUTU_ORDER_PRICE_FACTOR", 0.5)
	if factor <= 0 || factor >= 0.9 {
		t.Fatalf("FUTU_ORDER_PRICE_FACTOR must be > 0 and < 0.9, got %v", factor)
	}
	return factor
}

func integrationOrderAmount(t *testing.T, symbol string) float64 {
	t.Helper()
	if value := strings.TrimSpace(os.Getenv("FUTU_ORDER_AMOUNT")); value != "" {
		return integrationRequiredFloat(t, "FUTU_ORDER_AMOUNT")
	}
	if strings.HasPrefix(strings.ToUpper(symbol), "US.") {
		return 1
	}
	return 100
}

func integrationSymbol() string {
	if value := strings.TrimSpace(os.Getenv("FUTU_SYMBOL")); value != "" {
		return value
	}
	switch strings.ToUpper(integrationString("FUTU_MARKET", "HK")) {
	case "US":
		return "US.AAPL"
	case "SH":
		return "SH.600519"
	case "SZ":
		return "SZ.000001"
	default:
		return "HK.00700"
	}
}

func integrationList(name string) []string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	list := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			list = append(list, part)
		}
	}
	return list
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

func Example_futuIntegrationEnvironment() {
	fmt.Println("FUTU_INTEGRATION=1 go test -tags=integration -run TestFutuIntegrationPublicREST -v ./futu")
	// Output:
	// FUTU_INTEGRATION=1 go test -tags=integration -run TestFutuIntegrationPublicREST -v ./futu
}
