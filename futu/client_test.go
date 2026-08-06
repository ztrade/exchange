package futu

import (
	"context"
	"strings"
	"testing"
	"time"

	futusdk "github.com/hyperjiang/futu"
	"github.com/hyperjiang/futu/adapt"
	futuclient "github.com/hyperjiang/futu/client"
	"github.com/hyperjiang/futu/pb/qotcommon"
	"github.com/hyperjiang/futu/pb/qotgetkl"
	"github.com/hyperjiang/futu/pb/qotgetorderbook"
	"github.com/hyperjiang/futu/pb/qotgetticker"
	"github.com/hyperjiang/futu/pb/qotrequesthistorykl"
	"github.com/hyperjiang/futu/pb/qotupdatekl"
	"github.com/hyperjiang/futu/pb/qotupdateorderbook"
	"github.com/hyperjiang/futu/pb/qotupdateticker"
	"github.com/hyperjiang/futu/pb/trdcommon"
	"github.com/hyperjiang/futu/pb/trdmodifyorder"
	"github.com/hyperjiang/futu/pb/trdplaceorder"
	"github.com/spf13/viper"
	"github.com/ztrade/exchange"
	. "github.com/ztrade/trademodel"
	"google.golang.org/protobuf/proto"
)

type subscribeCall struct {
	codes   []string
	subType int32
}

// fakeSDK implements futuSDK without touching the network.
type fakeSDK struct {
	accList           []*trdcommon.TrdAcc
	funds             *trdcommon.Funds
	positions         []*trdcommon.Position
	openOrders        []*trdcommon.Order
	staticInfos       []*qotcommon.SecurityStaticInfo
	klPages           []*qotrequesthistorykl.S2C
	recentKL          *qotgetkl.S2C
	orderBook         *qotgetorderbook.S2C
	tickers           *qotgetticker.S2C
	placeResult       *trdplaceorder.S2C
	placeCalls        []placeCall
	modifyCalls       []modifyCall
	staticInfoCalls   int
	staticInfoMarkets []int32
	historyErr        error
	subscribes        []subscribeCall
	accPushCalls      [][]uint64
	unlockCalls       int
	handlers          map[uint32]futuclient.Handler
	closed            bool
}

type placeCall struct {
	header    *trdcommon.TrdHeader
	trdSide   int32
	orderType int32
	code      string
	qty       float64
	price     float64
	auxPrice  float64
}

type modifyCall struct {
	header  *trdcommon.TrdHeader
	orderID uint64
	op      int32
}

func newFakeSDK() *fakeSDK {
	return &fakeSDK{
		handlers: make(map[uint32]futuclient.Handler),
	}
}

func trdMarketPrefix(market int32) string {
	switch market {
	case adapt.TrdMarket_HK:
		return "HK"
	case adapt.TrdMarket_US:
		return "US"
	default:
		return ""
	}
}

func (f *fakeSDK) GetAccListWithContext(ctx context.Context, opts ...adapt.Option) ([]*trdcommon.TrdAcc, error) {
	return f.accList, nil
}

func (f *fakeSDK) UnlockTradeWithContext(ctx context.Context, unlock bool, pwdMD5 string, securityFirm int32) error {
	f.unlockCalls++
	return nil
}

func (f *fakeSDK) SubscribeAccPushWithContext(ctx context.Context, accIDList []uint64) error {
	f.accPushCalls = append(f.accPushCalls, accIDList)
	return nil
}

func (f *fakeSDK) GetFundsWithContext(ctx context.Context, header *trdcommon.TrdHeader, opts ...adapt.Option) (*trdcommon.Funds, error) {
	return f.funds, nil
}

func (f *fakeSDK) GetPositionListWithContext(ctx context.Context, header *trdcommon.TrdHeader, opts ...adapt.Option) ([]*trdcommon.Position, error) {
	return f.positions, nil
}

func (f *fakeSDK) GetOpenOrderListWithContext(ctx context.Context, header *trdcommon.TrdHeader, opts ...adapt.Option) ([]*trdcommon.Order, error) {
	if header != nil {
		prefix := trdMarketPrefix(header.GetTrdMarket())
		if prefix != "" {
			var out []*trdcommon.Order
			for _, order := range f.openOrders {
				if order != nil && strings.HasPrefix(order.GetCode(), prefix+".") {
					out = append(out, order)
				}
			}
			return out, nil
		}
	}
	return f.openOrders, nil
}

func (f *fakeSDK) PlaceOrderWithContext(ctx context.Context, header *trdcommon.TrdHeader, trdSide int32, orderType int32, code string, qty float64, price float64, opts ...adapt.Option) (*trdplaceorder.S2C, error) {
	auxPrice := float64(0)
	for _, opt := range opts {
		o := adapt.NewOptions(opt)
		if v, ok := o["auxPrice"]; ok {
			auxPrice = v.(float64)
		}
	}
	f.placeCalls = append(f.placeCalls, placeCall{header: header, trdSide: trdSide, orderType: orderType, code: code, qty: qty, price: price, auxPrice: auxPrice})
	return f.placeResult, nil
}

func (f *fakeSDK) ModifyOrderWithContext(ctx context.Context, header *trdcommon.TrdHeader, orderID uint64, modifyOrderOp int32, opts ...adapt.Option) (*trdmodifyorder.S2C, error) {
	f.modifyCalls = append(f.modifyCalls, modifyCall{header: header, orderID: orderID, op: modifyOrderOp})
	return &trdmodifyorder.S2C{}, nil
}

func (f *fakeSDK) SubscribeWithContext(ctx context.Context, codes []string, subTypes []int32, isSub bool, opts ...adapt.Option) error {
	for _, subType := range subTypes {
		f.subscribes = append(f.subscribes, subscribeCall{codes: codes, subType: subType})
	}
	return nil
}

func (f *fakeSDK) GetKLWithContext(ctx context.Context, code string, klType int32, opts ...adapt.Option) (*qotgetkl.S2C, error) {
	return f.recentKL, nil
}

func (f *fakeSDK) RequestHistoryKLWithContext(ctx context.Context, code string, klType int32, beginTime string, endTime string, opts ...adapt.Option) (*qotrequesthistorykl.S2C, error) {
	if f.historyErr != nil {
		return nil, f.historyErr
	}
	if len(f.klPages) == 0 {
		return &qotrequesthistorykl.S2C{}, nil
	}
	page := f.klPages[0]
	f.klPages = f.klPages[1:]
	return page, nil
}

func (f *fakeSDK) GetStaticInfoWithContext(ctx context.Context, opts ...adapt.Option) ([]*qotcommon.SecurityStaticInfo, error) {
	f.staticInfoCalls++
	o := adapt.NewOptions(opts...)
	if v, ok := o["market"]; ok {
		f.staticInfoMarkets = append(f.staticInfoMarkets, v.(int32))
	}
	return f.staticInfos, nil
}

func (f *fakeSDK) GetPlateSecurityWithContext(ctx context.Context, plateCode string, opts ...adapt.Option) ([]*qotcommon.SecurityStaticInfo, error) {
	return f.staticInfos, nil
}

func (f *fakeSDK) GetOrderBookWithContext(ctx context.Context, code string, opts ...adapt.Option) (*qotgetorderbook.S2C, error) {
	return f.orderBook, nil
}

func (f *fakeSDK) GetTickerWithContext(ctx context.Context, code string, opts ...adapt.Option) (*qotgetticker.S2C, error) {
	return f.tickers, nil
}

func (f *fakeSDK) RegisterHandler(protoID uint32, h futuclient.Handler) *futusdk.SDK {
	f.handlers[protoID] = h
	return nil
}

func (f *fakeSDK) Close() error {
	f.closed = true
	return nil
}

func newTestClient(t *testing.T, cfg FutuConfig, fake *fakeSDK) *Client {
	t.Helper()
	oldNewSDK := newSDK
	newSDK = func(cfg FutuConfig) (futuSDK, error) {
		return fake, nil
	}
	t.Cleanup(func() { newSDK = oldNewSDK })
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("new futu client: %v", err)
	}
	return client
}

func testConfig() FutuConfig {
	return FutuConfig{
		Addr:    ":11111",
		Timeout: time.Second,
		TrdEnv:  "simulate",
		Markets: []string{"US"},
		AccID:   1619199,
	}
}

func TestNewFutuDecodesConfig(t *testing.T) {
	fake := newFakeSDK()
	oldNewSDK := newSDK
	newSDK = func(cfg FutuConfig) (futuSDK, error) {
		if cfg.Addr != "127.0.0.1:11111" {
			t.Fatalf("addr = %q", cfg.Addr)
		}
		return fake, nil
	}
	t.Cleanup(func() { newSDK = oldNewSDK })

	config := viper.New()
	config.Set("exchanges.futu_sim.addr", "127.0.0.1:11111")
	config.Set("exchanges.futu_sim.trd_env", "simulate")
	config.Set("exchanges.futu_sim.markets", []string{"US"})
	config.Set("exchanges.futu_sim.acc_id", 1619199)
	ex, err := NewFutu(exchange.WrapViper(config), "futu_sim")
	if err != nil {
		t.Fatalf("new futu: %v", err)
	}
	client := ex.(*Client)
	if client.trdEnv != int32(trdcommon.TrdEnv_TrdEnv_Simulate) {
		t.Fatalf("trd env = %d", client.trdEnv)
	}
	if len(client.markets) != 1 || client.markets[0] != "US" {
		t.Fatalf("markets = %v", client.markets)
	}
	if err := client.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !fake.closed {
		t.Fatal("sdk not closed")
	}
}

func TestDefaultMarketsAll(t *testing.T) {
	client := newTestClient(t, FutuConfig{}, newFakeSDK())
	defer client.Stop()
	if len(client.markets) != len(allMarkets) {
		t.Fatalf("markets = %v, want all %v", client.markets, allMarkets)
	}
	for i, market := range allMarkets {
		if client.markets[i] != market {
			t.Fatalf("markets = %v, want %v", client.markets, allMarkets)
		}
	}
	// whole-market symbol query covers every default market
	fake := newFakeSDK()
	multi := newTestClient(t, FutuConfig{}, fake)
	defer multi.Stop()
	if _, err := multi.Symbols(); err != nil {
		t.Fatalf("symbols: %v", err)
	}
	if fake.staticInfoCalls != len(allMarkets) {
		t.Fatalf("static info calls = %d, want %d", fake.staticInfoCalls, len(allMarkets))
	}
}

func TestSymbolsFromConfigAndPlates(t *testing.T) {
	fake := newFakeSDK()
	fake.staticInfos = []*qotcommon.SecurityStaticInfo{
		staticInfo("HK", "00700", "Tencent", int32(adapt.SecurityType_Eqty), 100),
		staticInfo("US", "AAPL", "Apple", int32(adapt.SecurityType_Eqty), 1),
	}
	client := newTestClient(t, FutuConfig{Markets: []string{"HK"}, Symbols: []string{"HK.00700"}, Plates: []string{"HK.LIST1059"}}, fake)
	defer client.Stop()

	symbols, err := client.Symbols()
	if err != nil {
		t.Fatalf("symbols: %v", err)
	}
	if len(symbols) != 2 {
		t.Fatalf("symbols len = %d, want 2", len(symbols))
	}
	if symbols[0].Symbol != "HK.00700" {
		t.Fatalf("symbol = %q, want HK.00700", symbols[0].Symbol)
	}
	if symbols[0].AmountStep != 100 {
		t.Fatalf("amount step = %v, want 100", symbols[0].AmountStep)
	}
	if symbols[0].Type != SymbolTypeSpot {
		t.Fatalf("symbol type = %q", symbols[0].Type)
	}
}

func TestSymbolsFromWholeMarket(t *testing.T) {
	fake := newFakeSDK()
	fake.staticInfos = []*qotcommon.SecurityStaticInfo{
		staticInfo("HK", "09988", "Alibaba", int32(adapt.SecurityType_Eqty), 100),
	}
	client := newTestClient(t, FutuConfig{Markets: []string{"HK"}}, fake)
	defer client.Stop()

	symbols, err := client.Symbols()
	if err != nil {
		t.Fatalf("symbols: %v", err)
	}
	if len(symbols) != 1 || symbols[0].Symbol != "HK.09988" {
		t.Fatalf("symbols = %+v", symbols)
	}
}

func TestMultipleMarketsWholeMarketQuery(t *testing.T) {
	fake := newFakeSDK()
	fake.staticInfos = []*qotcommon.SecurityStaticInfo{
		staticInfo("HK", "00700", "Tencent", int32(adapt.SecurityType_Eqty), 100),
		staticInfo("US", "AAPL", "Apple", int32(adapt.SecurityType_Eqty), 1),
	}
	client := newTestClient(t, FutuConfig{Markets: []string{"HK", "US"}}, fake)
	defer client.Stop()

	symbols, err := client.Symbols()
	if err != nil {
		t.Fatalf("symbols: %v", err)
	}
	if fake.staticInfoCalls != 2 {
		t.Fatalf("static info calls = %d, want 2", fake.staticInfoCalls)
	}
	if len(fake.staticInfoMarkets) != 2 ||
		fake.staticInfoMarkets[0] != adapt.QotMarket_HK ||
		fake.staticInfoMarkets[1] != adapt.QotMarket_US {
		t.Fatalf("static info markets = %v", fake.staticInfoMarkets)
	}
	if len(symbols) != 2 {
		t.Fatalf("symbols = %+v", symbols)
	}
	// symbols must carry a market prefix when multiple markets are used
	if code, err := client.normalizeSymbol("00700"); err == nil {
		t.Fatalf("normalize(00700) = %q, want error", code)
	}
	code, err := client.normalizeSymbol("HK.700")
	if err != nil || code != "HK.00700" {
		t.Fatalf("normalize(HK.700) = %q, %v; want HK.00700", code, err)
	}
}

func TestMultipleMarketsTradeHeaders(t *testing.T) {
	fake := newFakeSDK()
	cfg := testConfig()
	cfg.Markets = []string{"HK", "US"}
	cfg.Symbols = []string{"HK.00700", "US.AAPL"}
	fake.accList = []*trdcommon.TrdAcc{
		{TrdEnv: proto.Int32(int32(trdcommon.TrdEnv_TrdEnv_Simulate)), AccID: proto.Uint64(cfg.AccID)},
	}
	fake.placeResult = &trdplaceorder.S2C{OrderID: proto.Uint64(7)}
	client := newTestClient(t, cfg, fake)
	defer client.Stop()

	if _, err := client.ProcessOrder(TradeAction{Symbol: "HK.00700", Action: OpenLong | Limit, Amount: 100, Price: 300}); err != nil {
		t.Fatalf("HK order: %v", err)
	}
	if _, err := client.ProcessOrder(TradeAction{Symbol: "US.AAPL", Action: OpenLong | Limit, Amount: 1, Price: 100}); err != nil {
		t.Fatalf("US order: %v", err)
	}
	if len(fake.placeCalls) != 2 {
		t.Fatalf("place calls = %+v", fake.placeCalls)
	}
	if fake.placeCalls[0].header.GetTrdMarket() != adapt.TrdMarket_HK {
		t.Fatalf("HK header market = %d", fake.placeCalls[0].header.GetTrdMarket())
	}
	if fake.placeCalls[1].header.GetTrdMarket() != adapt.TrdMarket_US {
		t.Fatalf("US header market = %d", fake.placeCalls[1].header.GetTrdMarket())
	}
	if len(fake.accPushCalls) != 1 {
		t.Fatalf("acc push calls = %+v, want a single subscription", fake.accPushCalls)
	}

	fake.openOrders = []*trdcommon.Order{
		order(21, "HK.00700", 1, 1, 300, 100, 0),
		order(22, "US.AAPL", 1, 1, 100, 1, 0),
	}
	all, err := client.CancelAllOrders()
	if err != nil {
		t.Fatalf("cancel all: %v", err)
	}
	if len(all) != 2 || len(fake.modifyCalls) != 2 {
		t.Fatalf("cancel all = %+v, modify calls = %+v", all, fake.modifyCalls)
	}
	if fake.modifyCalls[0].header.GetTrdMarket() != adapt.TrdMarket_HK ||
		fake.modifyCalls[1].header.GetTrdMarket() != adapt.TrdMarket_US {
		t.Fatalf("cancel headers = %+v", fake.modifyCalls)
	}
}

func TestGetKlineHistoryPagination(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	fake := newFakeSDK()
	fake.klPages = []*qotrequesthistorykl.S2C{
		{
			KlList: []*qotcommon.KLine{
				kline("2025-01-01", start.Unix(), 1, 2, 3, 4),
				kline("2025-01-02", start.Add(24*time.Hour).Unix(), 2, 3, 4, 5),
			},
			NextReqKey: []byte("page-2"),
		},
		{
			KlList: []*qotcommon.KLine{
				kline("2025-01-03", start.Add(48*time.Hour).Unix(), 3, 4, 5, 6),
			},
		},
	}
	client := newTestClient(t, FutuConfig{Markets: []string{"HK"}}, fake)
	defer client.Stop()

	data, err := client.GetKline("HK.00700", "1d", start, end)
	if err != nil {
		t.Fatalf("get kline: %v", err)
	}
	if len(data) != 3 {
		t.Fatalf("kline len = %d, want 3", len(data))
	}
	for i := 1; i < len(data); i++ {
		if data[i].Start <= data[i-1].Start {
			t.Fatalf("kline not sorted: %+v", data)
		}
	}
	if data[0].Close != 4 {
		t.Fatalf("first close = %v", data[0].Close)
	}
}

func TestGetKlineFallsBackToGetKL(t *testing.T) {
	now := time.Now()
	start := now.Add(-time.Hour)
	fake := newFakeSDK()
	fake.historyErr = errHistoryUnavailable
	fake.recentKL = &qotgetkl.S2C{
		KlList: []*qotcommon.KLine{
			klineWithTS(start.Unix(), 1, 2, 3, 4, 10),
			klineWithTS(now.Unix(), 2, 3, 4, 5, 20),
		},
	}
	client := newTestClient(t, FutuConfig{Markets: []string{"US"}}, fake)
	defer client.Stop()

	data, err := client.GetKline("US.AAPL", "1m", start, now)
	if err != nil {
		t.Fatalf("get kline: %v", err)
	}
	if len(data) != 2 {
		t.Fatalf("kline len = %d, want 2", len(data))
	}
}

func TestWatchCandleEmitsClosedBars(t *testing.T) {
	fake := newFakeSDK()
	client := newTestClient(t, FutuConfig{Markets: []string{"HK"}}, fake)
	defer client.Stop()

	var received []*Candle
	callback := func(v interface{}) {
		received = append(received, v.(*Candle))
	}
	if err := client.Watch(exchange.WatchCandle("HK.00700", "1m"), callback); err != nil {
		t.Fatalf("watch: %v", err)
	}
	if len(fake.subscribes) != 1 || fake.subscribes[0].subType != adapt.SubType_KL_1Min {
		t.Fatalf("subscribe calls = %+v", fake.subscribes)
	}
	if len(fake.subscribes) != 1 {
		t.Fatal("duplicate subscribe")
	}
	if err := client.Watch(exchange.WatchCandle("HK.00700", "1m"), callback); err != nil {
		t.Fatalf("watch twice: %v", err)
	}
	if len(fake.subscribes) != 1 {
		t.Fatalf("subscribe called twice, calls = %+v", fake.subscribes)
	}

	client.handleUpdateKL(qotupdateklS2C("HK.00700", adapt.KLType_1Min,
		klineWithTS(1000, 1, 2, 3, 4, 10),
	))
	if len(received) != 0 {
		t.Fatalf("first push emitted %d bars, want 0", len(received))
	}
	client.handleUpdateKL(qotupdateklS2C("HK.00700", adapt.KLType_1Min,
		klineWithTS(1060, 2, 3, 4, 5, 20),
	))
	if len(received) != 1 {
		t.Fatalf("second push emitted %d bars, want 1", len(received))
	}
	if received[0].Start != 1000 || received[0].Close != 4 {
		t.Fatalf("closed bar = %+v", received[0])
	}
}

func TestWatchDepthAndTicker(t *testing.T) {
	fake := newFakeSDK()
	fake.orderBook = &qotgetorderbook.S2C{
		OrderBookAskList: []*qotcommon.OrderBook{{Price: proto.Float64(101), Volume: proto.Int64(10)}},
		OrderBookBidList: []*qotcommon.OrderBook{{Price: proto.Float64(100), Volume: proto.Int64(20)}},
	}
	fake.tickers = &qotgetticker.S2C{
		TickerList: []*qotcommon.Ticker{
			{
				Sequence:  proto.Int64(1),
				Dir:       proto.Int32(int32(qotcommon.TickerDirection_TickerDirection_Bid)),
				Price:     proto.Float64(100.5),
				Volume:    proto.Int64(3),
				Timestamp: proto.Float64(float64(time.Now().Unix())),
			},
		},
	}
	client := newTestClient(t, FutuConfig{Markets: []string{"HK"}}, fake)
	defer client.Stop()

	var depths []*Depth
	if err := client.Watch(exchange.WatchParam{Type: exchange.WatchTypeDepth, Param: map[string]string{"symbol": "HK.00700"}}, func(v interface{}) {
		depths = append(depths, v.(*Depth))
	}); err != nil {
		t.Fatalf("watch depth: %v", err)
	}
	if len(depths) != 1 || len(depths[0].Sells) != 1 || len(depths[0].Buys) != 1 {
		t.Fatalf("depth snapshot = %+v", depths)
	}

	var trades []*Trade
	if err := client.Watch(exchange.WatchParam{Type: exchange.WatchTypeTradeMarket, Param: map[string]string{"symbol": "HK.00700"}}, func(v interface{}) {
		trades = append(trades, v.(*Trade))
	}); err != nil {
		t.Fatalf("watch ticker: %v", err)
	}
	if len(trades) != 1 || trades[0].Side != "buy" || trades[0].Price != 100.5 {
		t.Fatalf("ticker snapshot = %+v", trades)
	}

	client.handleUpdateOrderBook(qotupdateOrderBookS2C("HK.00700", 102, 99))
	if len(depths) != 2 || depths[1].Sells[0].Price != 102 {
		t.Fatalf("depth push = %+v", depths)
	}
	client.handleUpdateTicker(qotupdateTickerS2C("HK.00700", 2, 101, 5))
	if len(trades) != 2 || trades[1].Side != "sell" || trades[1].Amount != 5 {
		t.Fatalf("ticker push = %+v", trades)
	}
}

func TestProcessOrderLimitMarketStop(t *testing.T) {
	fake := newFakeSDK()
	fake.accList = []*trdcommon.TrdAcc{
		{
			TrdEnv: proto.Int32(int32(trdcommon.TrdEnv_TrdEnv_Simulate)),
			AccID:  proto.Uint64(1619199),
		},
	}
	fake.placeResult = &trdplaceorder.S2C{OrderID: proto.Uint64(42)}
	client := newTestClient(t, testConfig(), fake)
	defer client.Stop()

	limit, err := client.ProcessOrder(TradeAction{ID: "act-1", Symbol: "US.AAPL", Action: OpenLong | Limit, Amount: 10, Price: 99.5})
	if err != nil {
		t.Fatalf("limit order: %v", err)
	}
	if limit.OrderID != "42" || limit.Symbol != "US.AAPL" || limit.Status != "SUBMITTED" {
		t.Fatalf("limit order = %+v", limit)
	}
	if len(fake.placeCalls) != 1 {
		t.Fatalf("place calls = %+v", fake.placeCalls)
	}
	call := fake.placeCalls[0]
	if call.orderType != adapt.OrderType_Normal || call.price != 99.5 || call.code != "US.AAPL" || call.qty != 10 {
		t.Fatalf("limit call = %+v", call)
	}

	_, err = client.ProcessOrder(TradeAction{Symbol: "US.AAPL", Action: OpenLong | Market, Amount: 10})
	if err != nil {
		t.Fatalf("market order: %v", err)
	}
	if fake.placeCalls[1].orderType != adapt.OrderType_Market || fake.placeCalls[1].price != 0 {
		t.Fatalf("market call = %+v", fake.placeCalls[1])
	}

	_, err = client.ProcessOrder(TradeAction{Symbol: "US.AAPL", Action: StopLong, Amount: 10, Price: 90})
	if err != nil {
		t.Fatalf("stop order: %v", err)
	}
	if fake.placeCalls[2].orderType != adapt.OrderType_Stop || fake.placeCalls[2].auxPrice != 90 {
		t.Fatalf("stop call = %+v", fake.placeCalls[2])
	}

	_, err = client.ProcessOrder(TradeAction{Symbol: "US.AAPL", Action: OpenShort | Limit, Amount: 5, Price: 100})
	if err != nil {
		t.Fatalf("short order: %v", err)
	}
	if fake.placeCalls[3].trdSide != int32(trdcommon.TrdSide_TrdSide_SellShort) {
		t.Fatalf("short side = %d", fake.placeCalls[3].trdSide)
	}
	if len(fake.accPushCalls) != 1 {
		t.Fatalf("acc push calls = %+v", fake.accPushCalls)
	}
}

func TestCancelOrderAndCancelAll(t *testing.T) {
	fake := newFakeSDK()
	fake.accList = []*trdcommon.TrdAcc{
		{TrdEnv: proto.Int32(int32(trdcommon.TrdEnv_TrdEnv_Simulate)), AccID: proto.Uint64(1)},
	}
	fake.openOrders = []*trdcommon.Order{
		order(11, "US.AAPL", 2, 1, 100, 10, 0),
		order(12, "US.TSLA", 2, 1, 200, 5, 0),
	}
	cfg := testConfig()
	fake.accList = []*trdcommon.TrdAcc{
		{TrdEnv: proto.Int32(int32(trdcommon.TrdEnv_TrdEnv_Simulate)), AccID: proto.Uint64(cfg.AccID)},
	}
	client := newTestClient(t, cfg, fake)
	defer client.Stop()

	canceled, err := client.CancelOrder(&Order{OrderID: "11", Symbol: "US.AAPL"})
	if err != nil {
		t.Fatalf("cancel order: %v", err)
	}
	if canceled.Status != OrderStatusCanceled {
		t.Fatalf("canceled status = %q", canceled.Status)
	}
	if len(fake.modifyCalls) != 1 || fake.modifyCalls[0].orderID != 11 || fake.modifyCalls[0].op != adapt.ModifyOrderOp_Cancel {
		t.Fatalf("modify calls = %+v", fake.modifyCalls)
	}

	all, err := client.CancelAllOrders()
	if err != nil {
		t.Fatalf("cancel all: %v", err)
	}
	if len(all) != 2 || len(fake.modifyCalls) != 3 {
		t.Fatalf("cancel all orders = %+v, modify calls = %+v", all, fake.modifyCalls)
	}
}

func TestStartPushesBalanceAndPosition(t *testing.T) {
	fake := newFakeSDK()
	fake.accList = []*trdcommon.TrdAcc{
		{TrdEnv: proto.Int32(int32(trdcommon.TrdEnv_TrdEnv_Simulate)), AccID: proto.Uint64(1619199)},
	}
	fake.funds = &trdcommon.Funds{
		Cash:       proto.Float64(10000),
		FrozenCash: proto.Float64(500),
	}
	fake.positions = []*trdcommon.Position{
		{
			PositionSide: proto.Int32(int32(trdcommon.PositionSide_PositionSide_Long)),
			Code:         proto.String("AAPL"),
			Qty:          proto.Float64(10),
			CostPrice:    proto.Float64(150),
			PlRatio:      proto.Float64(5),
			SecMarket:    proto.Int32(int32(trdcommon.TrdSecMarket_TrdSecMarket_US)),
		},
	}
	client := newTestClient(t, testConfig(), fake)
	defer client.Stop()

	var balances []*Balance
	var positions []*Position
	if err := client.Watch(exchange.WatchParam{Type: exchange.WatchTypeBalance}, func(v interface{}) {
		balances = append(balances, v.(*Balance))
	}); err != nil {
		t.Fatalf("watch balance: %v", err)
	}
	if err := client.Watch(exchange.WatchParam{Type: exchange.WatchTypePosition}, func(v interface{}) {
		positions = append(positions, v.(*Position))
	}); err != nil {
		t.Fatalf("watch position: %v", err)
	}
	if err := client.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	if len(balances) == 0 || balances[len(balances)-1].Currency != "USD" || balances[len(balances)-1].Available != 9500 {
		t.Fatalf("balances = %+v", balances)
	}
	if len(positions) == 0 || positions[len(positions)-1].Symbol != "US.AAPL" || positions[len(positions)-1].Hold != 10 || positions[len(positions)-1].ProfitRatio != 0.05 {
		t.Fatalf("positions = %+v", positions)
	}
	if fake.unlockCalls != 0 {
		t.Fatalf("unlock called without config: %d", fake.unlockCalls)
	}
}

func TestStartUnlocksTrade(t *testing.T) {
	fake := newFakeSDK()
	fake.accList = []*trdcommon.TrdAcc{
		{TrdEnv: proto.Int32(int32(trdcommon.TrdEnv_TrdEnv_Simulate)), AccID: proto.Uint64(1619199)},
	}
	fake.funds = &trdcommon.Funds{Cash: proto.Float64(100)}
	cfg := testConfig()
	cfg.UnlockTrade = true
	cfg.PwdMD5 = "abc"
	client := newTestClient(t, cfg, fake)
	defer client.Stop()
	if err := client.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if fake.unlockCalls != 1 {
		t.Fatalf("unlock calls = %d, want 1", fake.unlockCalls)
	}
}

func TestNormalizeSymbol(t *testing.T) {
	client := &Client{cfg: FutuConfig{Markets: []string{"HK"}}, markets: []string{"HK"}}
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"HK.700", "HK.00700", false},
		{"00700", "", true},
		{"HK.00700", "HK.00700", false},
		{"US.AAPL", "US.AAPL", false},
		{"EU.SAP", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		got, err := client.normalizeSymbol(tc.in)
		if tc.err {
			if err == nil {
				t.Fatalf("normalize(%q) expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("normalize(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestKlineTypeMapping(t *testing.T) {
	cases := map[string]int32{
		"1m":  adapt.KLType_1Min,
		"5m":  adapt.KLType_5Min,
		"1h":  adapt.KLType_60Min,
		"60m": adapt.KLType_60Min,
		"1d":  adapt.KLType_Day,
		"1w":  adapt.KLType_Week,
		"1M":  adapt.KLType_Month,
	}
	for bin, want := range cases {
		got, err := klineType(bin)
		if err != nil || got != want {
			t.Fatalf("klineType(%q) = %d, %v; want %d", bin, got, err, want)
		}
	}
	if _, err := klineType("2m"); err == nil {
		t.Fatal("klineType(2m) expected error")
	}
	canonical, err := canonicalBin("60m")
	if err != nil || canonical != "1h" {
		t.Fatalf("canonicalBin(60m) = %q, %v; want 1h", canonical, err)
	}
}

func TestOrderStatusMapping(t *testing.T) {
	if got := orderStatus(adapt.OrderStatus_Filled_All); got != OrderStatusFilled {
		t.Fatalf("filled status = %q", got)
	}
	if got := orderStatus(adapt.OrderStatus_Cancelled_All); got != OrderStatusCanceled {
		t.Fatalf("canceled status = %q", got)
	}
	if got := orderStatus(adapt.OrderStatus_Filled_Part); got != "PART_FILLED" {
		t.Fatalf("part filled status = %q", got)
	}
}

var errHistoryUnavailable = context.DeadlineExceeded

func staticInfo(market, code, name string, secType int32, lotSize int32) *qotcommon.SecurityStaticInfo {
	return &qotcommon.SecurityStaticInfo{
		Basic: &qotcommon.SecurityStaticBasic{
			Security: &qotcommon.Security{
				Market: adapt.GetMarketID(market),
				Code:   proto.String(code),
			},
			LotSize: proto.Int32(lotSize),
			SecType: proto.Int32(secType),
			Name:    proto.String(name),
		},
	}
}

func kline(date string, ts int64, open, high, low, close float64) *qotcommon.KLine {
	return &qotcommon.KLine{
		Time:       proto.String(date),
		Timestamp:  proto.Float64(float64(ts)),
		OpenPrice:  proto.Float64(open),
		HighPrice:  proto.Float64(high),
		LowPrice:   proto.Float64(low),
		ClosePrice: proto.Float64(close),
		Volume:     proto.Int64(1000),
	}
}

func klineWithTS(ts int64, open, high, low, close float64, volume int64) *qotcommon.KLine {
	return &qotcommon.KLine{
		Timestamp:  proto.Float64(float64(ts)),
		OpenPrice:  proto.Float64(open),
		HighPrice:  proto.Float64(high),
		LowPrice:   proto.Float64(low),
		ClosePrice: proto.Float64(close),
		Volume:     proto.Int64(volume),
	}
}

func qotupdateklS2C(code string, klType int32, kl ...*qotcommon.KLine) *qotupdatekl.S2C {
	return &qotupdatekl.S2C{
		Security: adapt.NewSecurity(code),
		KlType:   proto.Int32(klType),
		KlList:   kl,
	}
}

func qotupdateOrderBookS2C(code string, askPrice, bidPrice float64) *qotupdateorderbook.S2C {
	return &qotupdateorderbook.S2C{
		Security: adapt.NewSecurity(code),
		OrderBookAskList: []*qotcommon.OrderBook{
			{Price: proto.Float64(askPrice), Volume: proto.Int64(1)},
		},
		OrderBookBidList: []*qotcommon.OrderBook{
			{Price: proto.Float64(bidPrice), Volume: proto.Int64(1)},
		},
	}
}

func qotupdateTickerS2C(code string, dir int32, price float64, volume int64) *qotupdateticker.S2C {
	return &qotupdateticker.S2C{
		Security: adapt.NewSecurity(code),
		TickerList: []*qotcommon.Ticker{
			{
				Dir:       proto.Int32(dir),
				Price:     proto.Float64(price),
				Volume:    proto.Int64(volume),
				Timestamp: proto.Float64(float64(time.Now().Unix())),
			},
		},
	}
}

func order(id uint64, code string, side int32, status int32, price float64, qty float64, fill float64) *trdcommon.Order {
	return &trdcommon.Order{
		OrderID:     proto.Uint64(id),
		Code:        proto.String(code),
		TrdSide:     proto.Int32(side),
		OrderStatus: proto.Int32(status),
		Price:       proto.Float64(price),
		Qty:         proto.Float64(qty),
		FillQty:     proto.Float64(fill),
	}
}
