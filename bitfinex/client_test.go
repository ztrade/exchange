package bitfinex

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bitfinexcom/bitfinex-api-go/pkg/models/candle"
	bfxmodel "github.com/bitfinexcom/bitfinex-api-go/pkg/models/common"
	bfxorder "github.com/bitfinexcom/bitfinex-api-go/pkg/models/order"
	"github.com/ztrade/exchange"
	. "github.com/ztrade/trademodel"
)

func TestSymbolsSeparateSpotAndFutures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conf/pub:list:pair:exchange":
			_ = json.NewEncoder(w).Encode([]interface{}{[]string{
				"BTCUST",
				"ETHUSD",
				"DOGE:UST",
			}})
		case "/status/deriv":
			if got := r.URL.Query().Get("keys"); got != "ALL" {
				t.Fatalf("derivative status keys = %q, want ALL", got)
			}
			_ = json.NewEncoder(w).Encode([][]interface{}{
				derivativeStatusRaw("tBTCF0:USTF0"),
				derivativeStatusRaw("tETHF0:USDF0"),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	spot, err := NewClient(BitfinexConfig{Currency: "USDT", RESTURL: server.URL}, "", KindSpot)
	if err != nil {
		t.Fatalf("create spot client: %v", err)
	}
	defer spot.Stop()
	spotSymbols, err := spot.Symbols()
	if err != nil {
		t.Fatalf("spot symbols: %v", err)
	}
	if got, want := symbolNames(spotSymbols), []string{"tBTCUST", "tDOGE:UST"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("spot symbols = %v, want %v", got, want)
	}
	if spotSymbols[0].Type != SymbolTypeSpot {
		t.Fatalf("spot symbol type = %q", spotSymbols[0].Type)
	}

	futures, err := NewClient(BitfinexConfig{Currency: "UST", RESTURL: server.URL}, "", KindFutures)
	if err != nil {
		t.Fatalf("create futures client: %v", err)
	}
	defer futures.Stop()
	futuresSymbols, err := futures.Symbols()
	if err != nil {
		t.Fatalf("futures symbols: %v", err)
	}
	if got, want := symbolNames(futuresSymbols), []string{"tBTCF0:USTF0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("futures symbols = %v, want %v", got, want)
	}
	if futuresSymbols[0].Type != SymbolTypeFutures {
		t.Fatalf("futures symbol type = %q", futuresSymbols[0].Type)
	}
}

func TestGetKlineMapsResolutionAndSkipsOpenCandle(t *testing.T) {
	oldest := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/conf/pub:list:pair:exchange":
			_ = json.NewEncoder(w).Encode([]interface{}{[]string{"BTCUST"}})
		case strings.HasPrefix(r.URL.Path, "/candles/trade:1D:tBTCUST/HIST"):
			assertQuery(t, r.URL.Query(), "sort", "1")
			_ = json.NewEncoder(w).Encode([][]interface{}{
				{oldest.Add(24 * time.Hour).UnixMilli(), 11.0, 12.0, 13.0, 10.0, 3.0},
				{oldest.UnixMilli(), 10.0, 11.0, 12.0, 9.0, 2.0},
				{time.Now().Add(time.Hour).UnixMilli(), 12.0, 13.0, 14.0, 11.0, 4.0},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(BitfinexConfig{Currency: "UST", RESTURL: server.URL}, "", KindSpot)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Stop()
	items, err := client.GetKline("BTCUSDT", "1d", oldest, oldest.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("get kline: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("kline count = %d, want 2", len(items))
	}
	if items[0].Start != oldest.Unix() || items[1].Start != oldest.Add(24*time.Hour).Unix() {
		t.Fatalf("klines are not sorted: %d, %d", items[0].Start, items[1].Start)
	}
	if items[0].Turnover != 0 {
		t.Fatalf("turnover = %v, want 0 because Bitfinex candles do not provide quote volume", items[0].Turnover)
	}
}

func TestNewOrderRequestSeparatesSpotAndFutures(t *testing.T) {
	spot := &Client{kind: KindSpot, currency: "UST"}
	spot.cid.Store(10)
	spotOrder := spot.newOrderRequest(TradeAction{
		Action: Limit | OpenLong,
		Amount: 1.234567891,
		Price:  12345.678,
		Symbol: "BTCUSDT",
	})
	if spotOrder.Type != bfxmodel.OrderTypeExchangeLimit {
		t.Fatalf("spot order type = %q", spotOrder.Type)
	}
	if spotOrder.Symbol != "tBTCUST" || spotOrder.Amount != 1.23456789 || spotOrder.Price != 12346 {
		t.Fatalf("unexpected spot order: %#v", spotOrder)
	}
	if spotOrder.Close {
		t.Fatal("spot order must not carry close flag")
	}

	futures := &Client{kind: KindFutures, currency: "UST", cfg: BitfinexConfig{Leverage: 5}}
	futures.cid.Store(20)
	futuresOrder := futures.newOrderRequest(TradeAction{
		Action: Limit | CloseLong,
		Amount: 2,
		Price:  100_001.2,
		Symbol: "BTCUSDT",
	})
	if futuresOrder.Type != bfxmodel.OrderTypeLimit {
		t.Fatalf("futures order type = %q", futuresOrder.Type)
	}
	if futuresOrder.Symbol != "tBTCF0:USTF0" || futuresOrder.Amount != -2 {
		t.Fatalf("unexpected futures order: %#v", futuresOrder)
	}
	if !futuresOrder.Close || futuresOrder.Leverage != 5 {
		t.Fatalf("futures close/leverage not mapped: %#v", futuresOrder)
	}
}

func TestProcessOrderUsesAuthenticatedSpotOrderEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conf/pub:list:pair:exchange":
			_ = json.NewEncoder(w).Encode([]interface{}{[]string{"BTCUST"}})
		case "/auth/w/order/submit":
			if r.Header.Get("bfx-apikey") != "key" || r.Header.Get("bfx-signature") == "" {
				t.Fatal("authenticated order headers are missing")
			}
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode order body: %v", err)
			}
			if body["type"] != bfxmodel.OrderTypeExchangeLimit || body["symbol"] != "tBTCUST" || body["amount"] != "0.1" {
				t.Fatalf("unexpected order body: %#v", body)
			}
			orderRaw := []interface{}{
				int64(42), int64(0), int64(11), "tBTCUST", int64(1_700_000_000_000), int64(1_700_000_000_000),
				0.1, 0.1, "EXCHANGE LIMIT", nil, nil, nil, int64(0), "ACTIVE", nil, nil,
				100.0, 0.0, 0.0, 0.0, nil, nil, nil, false, false, int64(0), nil, nil, "API>BFX", nil, nil, nil,
			}
			_ = json.NewEncoder(w).Encode([]interface{}{
				int64(1_700_000_000_000), "on-req", nil, nil, orderRaw, nil, "SUCCESS", "submitted",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(BitfinexConfig{
		Key: "key", Secret: "secret", Currency: "UST", RESTURL: server.URL,
	}, "", KindSpot)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Stop()
	result, err := client.ProcessOrder(TradeAction{
		Action: Limit | OpenLong,
		Amount: 0.1,
		Price:  100,
		Symbol: "BTCUSDT",
	})
	if err != nil {
		t.Fatalf("process order: %v", err)
	}
	if result.OrderID != "42" || result.Symbol != "tBTCUST" || result.Status != "NEW" {
		t.Fatalf("unexpected order result: %#v", result)
	}
}

func TestTransOrderNormalizesStatusAndFill(t *testing.T) {
	order := transOrder(&bfxorder.Order{
		ID:         42,
		Symbol:     "tBTCF0:USTF0",
		MTSUpdated: 1_700_000_000_000,
		Amount:     -0.25,
		AmountOrig: -1,
		Status:     "EXECUTED @ 100000(1.0)",
		Price:      99_999,
		PriceAvg:   100_000,
	})
	if order.Status != OrderStatusFilled || order.Side != "sell" {
		t.Fatalf("unexpected status/side: %#v", order)
	}
	if order.Amount != 1 || order.Filled != 0.75 || order.Price != 100_000 {
		t.Fatalf("unexpected amount/fill/price: %#v", order)
	}
}

func TestCandleWatchEmitsPreviousCandleOnRollover(t *testing.T) {
	client := &Client{
		candleCallbacks: make(map[string]exchange.WatchFn),
		candleLatest:    make(map[string]*candle.Candle),
	}
	key := candleKey("tBTCUST", bfxmodel.OneMinute)
	var got *Candle
	client.candleCallbacks[key] = func(value interface{}) {
		got = value.(*Candle)
	}
	client.handleCandle(&candle.Candle{Symbol: "tBTCUST", Resolution: bfxmodel.OneMinute, MTS: 60_000, Close: 10})
	if got != nil {
		t.Fatal("first candle update must not emit an unfinished candle")
	}
	client.handleCandle(&candle.Candle{Symbol: "tBTCUST", Resolution: bfxmodel.OneMinute, MTS: 120_000, Close: 11})
	if got == nil || got.Start != 60 || got.Close != 10 {
		t.Fatalf("closed candle = %#v", got)
	}
}

func TestSymbolNormalization(t *testing.T) {
	spot := &Client{kind: KindSpot, currency: "UST"}
	futures := &Client{kind: KindFutures, currency: "UST"}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"spot native", spot.normalizeSymbol("tBTCUST"), "tBTCUST"},
		{"spot usdt alias", spot.normalizeSymbol("BTCUSDT"), "tBTCUST"},
		{"futures native", futures.normalizeSymbol("tBTCF0:USTF0"), "tBTCF0:USTF0"},
		{"futures alias", futures.normalizeSymbol("BTCUSDT"), "tBTCF0:USTF0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("got %q, want %q", test.got, test.want)
			}
		})
	}
}

func TestResolveSymbolRejectsWrongMarketKind(t *testing.T) {
	spot := &Client{kind: KindSpot, currency: "UST"}
	if _, err := spot.resolveSymbol("tBTCF0:USTF0"); err == nil {
		t.Fatal("spot client accepted a derivatives symbol")
	}
	futures := &Client{kind: KindFutures, currency: "UST"}
	if _, err := futures.resolveSymbol("tBTCUST"); err == nil {
		t.Fatal("futures client accepted a spot symbol")
	}
}

func TestRoundSignificant(t *testing.T) {
	if got := roundSignificant(0.001234567, 5); math.Abs(got-0.0012346) > 1e-12 {
		t.Fatalf("roundSignificant = %.12f", got)
	}
}

func symbolNames(symbols []Symbol) []string {
	ret := make([]string, len(symbols))
	for i, symbol := range symbols {
		ret[i] = symbol.Symbol
	}
	return ret
}

func assertQuery(t *testing.T, values url.Values, key, want string) {
	t.Helper()
	if got := values.Get(key); got != want {
		t.Fatalf("query %s = %q, want %q", key, got, want)
	}
}

func derivativeStatusRaw(symbol string) []interface{} {
	return []interface{}{
		symbol, int64(1_700_000_000_000), nil, 100.0, 100.0, nil, 1_000.0, nil,
		int64(1_700_003_600_000), 0.0, 0.0001, nil, 0.0001, nil, nil, 100.0, nil, nil, 10.0,
	}
}
