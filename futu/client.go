package futu

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	"github.com/hyperjiang/futu/pb/trdupdateorder"
	"github.com/hyperjiang/futu/pb/trdupdateorderfill"
	"github.com/hyperjiang/futu/protoid"
	log "github.com/sirupsen/logrus"
	"github.com/ztrade/exchange"
	. "github.com/ztrade/trademodel"
	"google.golang.org/protobuf/proto"
)

const (
	defaultAddr      = ":11111"
	defaultTimeout   = 10 * time.Second
	defaultKLineCnt  = 1000
	defaultPrecision = 3

	defaultResolutions = "1m,3m,5m,10m,15m,30m,1h,2h,3h,4h,1d,1w,1M"
)

var _ exchange.Exchange = (*Client)(nil)

// futuSDK is the subset of the Futu SDK used by the adapter. It is an
// interface so tests can run without a FutuOpenD connection.
type futuSDK interface {
	GetAccListWithContext(ctx context.Context, opts ...adapt.Option) ([]*trdcommon.TrdAcc, error)
	UnlockTradeWithContext(ctx context.Context, unlock bool, pwdMD5 string, securityFirm int32) error
	SubscribeAccPushWithContext(ctx context.Context, accIDList []uint64) error
	GetFundsWithContext(ctx context.Context, header *trdcommon.TrdHeader, opts ...adapt.Option) (*trdcommon.Funds, error)
	GetPositionListWithContext(ctx context.Context, header *trdcommon.TrdHeader, opts ...adapt.Option) ([]*trdcommon.Position, error)
	GetOpenOrderListWithContext(ctx context.Context, header *trdcommon.TrdHeader, opts ...adapt.Option) ([]*trdcommon.Order, error)
	PlaceOrderWithContext(ctx context.Context, header *trdcommon.TrdHeader, trdSide int32, orderType int32, code string, qty float64, price float64, opts ...adapt.Option) (*trdplaceorder.S2C, error)
	ModifyOrderWithContext(ctx context.Context, header *trdcommon.TrdHeader, orderID uint64, modifyOrderOp int32, opts ...adapt.Option) (*trdmodifyorder.S2C, error)
	SubscribeWithContext(ctx context.Context, codes []string, subTypes []int32, isSub bool, opts ...adapt.Option) error
	GetKLWithContext(ctx context.Context, code string, klType int32, opts ...adapt.Option) (*qotgetkl.S2C, error)
	RequestHistoryKLWithContext(ctx context.Context, code string, klType int32, beginTime string, endTime string, opts ...adapt.Option) (*qotrequesthistorykl.S2C, error)
	GetStaticInfoWithContext(ctx context.Context, opts ...adapt.Option) ([]*qotcommon.SecurityStaticInfo, error)
	GetPlateSecurityWithContext(ctx context.Context, plateCode string, opts ...adapt.Option) ([]*qotcommon.SecurityStaticInfo, error)
	GetOrderBookWithContext(ctx context.Context, code string, opts ...adapt.Option) (*qotgetorderbook.S2C, error)
	GetTickerWithContext(ctx context.Context, code string, opts ...adapt.Option) (*qotgetticker.S2C, error)
	RegisterHandler(protoID uint32, h futuclient.Handler) *futusdk.SDK
	Close() error
}

// Client implements exchange.Exchange with the Futu OpenAPI.
//
// Futu's OpenAPI is a single long-lived TCP connection to the local
// FutuOpenD process. All market data, trading and push notifications share
// that connection, so there is no separate websocket lifecycle.
type Client struct {
	cfg         FutuConfig
	sdk         futuSDK
	timeout     time.Duration
	klineLimit  int
	qotMarket   int32
	trdMarket   int32
	trdEnv      int32
	resolutions string

	mu             sync.RWMutex
	tradeCb        exchange.WatchFn
	positionCb     exchange.WatchFn
	balanceCb      exchange.WatchFn
	candleCbs      map[string]exchange.WatchFn
	candleLatest   map[string]*qotcommon.KLine
	depthCbs       map[string]exchange.WatchFn
	tradeMarketCbs map[string]exchange.WatchFn
	subscribed     map[string]bool
	started        bool
	stopped        bool

	tradeMu     sync.Mutex
	header      *trdcommon.TrdHeader
	refreshMu   sync.Mutex
	lastRefresh time.Time
}

// NewClient creates a Futu client and connects to FutuOpenD.
func NewClient(cfg FutuConfig) (*Client, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	klineLimit := cfg.KLineLimit
	if klineLimit <= 0 {
		klineLimit = defaultKLineCnt
	}
	market := strings.ToUpper(strings.TrimSpace(cfg.Market))
	if market == "" {
		for _, symbol := range cfg.Symbols {
			parts := strings.SplitN(strings.TrimSpace(symbol), ".", 2)
			if len(parts) == 2 && parts[0] != "" {
				market = strings.ToUpper(parts[0])
				break
			}
		}
	}
	qotMarket, trdMarket, err := marketIDs(market)
	if err != nil {
		return nil, err
	}
	trdEnv := int32(trdcommon.TrdEnv_TrdEnv_Simulate)
	if cfg.TrdEnv == "" {
		trdEnv = -1
	} else if strings.EqualFold(cfg.TrdEnv, "real") {
		trdEnv = int32(trdcommon.TrdEnv_TrdEnv_Real)
	} else if strings.EqualFold(cfg.TrdEnv, "simulate") || strings.EqualFold(cfg.TrdEnv, "sim") {
		trdEnv = int32(trdcommon.TrdEnv_TrdEnv_Simulate)
	} else {
		return nil, fmt.Errorf("futu unsupported trd_env %q, want real or simulate", cfg.TrdEnv)
	}
	resolutions := cfg.Resolutions
	if resolutions == "" {
		resolutions = defaultResolutions
	}

	client := &Client{
		cfg:            cfg,
		timeout:        timeout,
		klineLimit:     klineLimit,
		qotMarket:      qotMarket,
		trdMarket:      trdMarket,
		trdEnv:         trdEnv,
		resolutions:    resolutions,
		candleCbs:      make(map[string]exchange.WatchFn),
		candleLatest:   make(map[string]*qotcommon.KLine),
		depthCbs:       make(map[string]exchange.WatchFn),
		tradeMarketCbs: make(map[string]exchange.WatchFn),
		subscribed:     make(map[string]bool),
	}
	sdk, err := newSDK(cfg)
	if err != nil {
		return nil, err
	}
	client.sdk = sdk
	client.registerHandlers()
	return client, nil
}

func (c *Client) Info() (info exchange.ExchangeInfo) {
	info = exchange.ExchangeInfo{
		Name:  "futu",
		Value: "futu",
		Desc:  "futu openapi via FutuOpenD",
		KLineLimit: exchange.FetchLimit{
			Limit: c.klineLimit,
		},
		OrderLimit: exchange.FetchLimit{
			Limit: 1000,
		},
	}
	return
}

func (c *Client) Start() error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return errors.New("futu client is stopped")
	}
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	if c.tradingEnabled() {
		if err := c.initTrade(); err != nil {
			return err
		}
		if err := c.fetchBalanceAndPosition(); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.started = true
	c.mu.Unlock()
	return nil
}

func (c *Client) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return nil
	}
	c.stopped = true
	return c.sdk.Close()
}

// Symbols returns the configured symbols. The code list comes from the
// explicit "symbols", the "plates" (expanded through QotGetPlateSecurity),
// or the whole configured market when both are empty.
func (c *Client) Symbols() ([]Symbol, error) {
	codes := make(map[string]bool)
	for _, raw := range c.cfg.Symbols {
		code, err := c.normalizeSymbol(raw)
		if err != nil {
			return nil, err
		}
		codes[code] = true
	}

	ctx, cancel := c.requestContext()
	defer cancel()
	for _, plate := range c.cfg.Plates {
		infos, err := c.sdk.GetPlateSecurityWithContext(ctx, plate)
		if err != nil {
			return nil, fmt.Errorf("futu get plate %s securities: %w", plate, err)
		}
		for _, info := range infos {
			if info == nil || info.GetBasic() == nil || info.GetBasic().GetSecurity() == nil {
				continue
			}
			codes[adapt.SecurityToCode(info.GetBasic().GetSecurity())] = true
		}
	}

	var infos []*qotcommon.SecurityStaticInfo
	if len(codes) == 0 && c.cfg.Market != "" {
		secType, err := securityTypeID(c.cfg.SecType)
		if err != nil {
			return nil, err
		}
		infos, err = c.sdk.GetStaticInfoWithContext(ctx,
			adapt.With("market", c.qotMarket),
			adapt.With("secType", secType),
		)
		if err != nil {
			return nil, fmt.Errorf("futu get static info: %w", err)
		}
	} else if len(codes) > 0 {
		list := make([]string, 0, len(codes))
		for code := range codes {
			list = append(list, code)
		}
		sort.Strings(list)
		var err error
		infos, err = c.sdk.GetStaticInfoWithContext(ctx, adapt.WithSecurities(list))
		if err != nil {
			return nil, fmt.Errorf("futu get static info: %w", err)
		}
	}

	symbols := make([]Symbol, 0, len(infos))
	for _, info := range infos {
		if info == nil || info.GetBasic() == nil || info.GetBasic().GetSecurity() == nil {
			continue
		}
		symbols = append(symbols, symbolFromStaticInfo(info, c.resolutions))
	}
	return symbols, nil
}

// GetKline fetches k-lines in [start, end]. History k-lines use
// QotRequestHistoryKL with pagination; when the online history endpoint is
// unavailable, recent ranges fall back to QotGetKL.
func (c *Client) GetKline(symbol, bSize string, start, end time.Time) ([]*Candle, error) {
	code, err := c.normalizeSymbol(symbol)
	if err != nil {
		return nil, err
	}
	klType, err := klineType(bSize)
	if err != nil {
		return nil, err
	}
	ctx, cancel := c.requestContext()
	defer cancel()

	begin := start.Format(futusdk.DateFormat)
	endStr := end.Format(futusdk.DateFormat)
	var data []*Candle
	var next []byte
	for {
		res, err := c.sdk.RequestHistoryKLWithContext(ctx, code, klType, begin, endStr,
			adapt.With("rehabType", adapt.RehabType_None),
			adapt.With("maxAckKLNum", int32(c.klineLimit)),
			adapt.With("nextReqKey", next),
		)
		if err != nil {
			if isRecentRange(start, end) {
				log.Warnf("futu request history kline failed, fallback to GetKL: %v", err)
				return c.getRecentKline(ctx, code, klType, start, end)
			}
			return nil, err
		}
		for _, kl := range res.GetKlList() {
			if kl == nil || kl.GetIsBlank() {
				continue
			}
			candle := klineToCandle(kl, code)
			if candle.Start < start.Unix() || candle.Start > end.Unix() {
				continue
			}
			data = append(data, candle)
		}
		next = res.GetNextReqKey()
		if len(next) == 0 {
			break
		}
	}
	sort.Slice(data, func(i, j int) bool { return data[i].Start < data[j].Start })
	return data, nil
}

func (c *Client) getRecentKline(ctx context.Context, code string, klType int32, start, end time.Time) ([]*Candle, error) {
	res, err := c.sdk.GetKLWithContext(ctx, code, klType,
		adapt.With("rehabType", adapt.RehabType_None),
		adapt.With("reqNum", int32(c.klineLimit)),
	)
	if err != nil {
		return nil, err
	}
	var data []*Candle
	for _, kl := range res.GetKlList() {
		if kl == nil || kl.GetIsBlank() {
			continue
		}
		candle := klineToCandle(kl, code)
		if candle.Start < start.Unix() || candle.Start > end.Unix() {
			continue
		}
		data = append(data, candle)
	}
	sort.Slice(data, func(i, j int) bool { return data[i].Start < data[j].Start })
	return data, nil
}

func (c *Client) Watch(param exchange.WatchParam, fn exchange.WatchFn) error {
	if fn == nil {
		return errors.New("futu watch callback cannot be nil")
	}
	c.mu.RLock()
	stopped := c.stopped
	c.mu.RUnlock()
	if stopped {
		return errors.New("futu client is stopped")
	}
	switch param.Type {
	case exchange.WatchTypeTrade:
		if !c.tradingEnabled() {
			return errors.New("futu trade watch requires trd_env/acc_id config")
		}
		c.mu.Lock()
		c.tradeCb = fn
		c.mu.Unlock()
		return c.ensureTrade()
	case exchange.WatchTypePosition:
		if !c.tradingEnabled() {
			return errors.New("futu position watch requires trd_env/acc_id config")
		}
		c.mu.Lock()
		c.positionCb = fn
		c.mu.Unlock()
		if err := c.ensureTrade(); err != nil {
			return err
		}
		return c.fetchBalanceAndPosition()
	case exchange.WatchTypeBalance:
		if !c.tradingEnabled() {
			return errors.New("futu balance watch requires trd_env/acc_id config")
		}
		c.mu.Lock()
		c.balanceCb = fn
		c.mu.Unlock()
		if err := c.ensureTrade(); err != nil {
			return err
		}
		return c.fetchBalanceAndPosition()
	}

	code, err := c.normalizeSymbol(param.Param["symbol"])
	if err != nil {
		return err
	}
	switch param.Type {
	case exchange.WatchTypeCandle:
		bin := param.Param["bin"]
		if bin == "" {
			bin = "1m"
		}
		bin, err = canonicalBin(bin)
		if err != nil {
			return err
		}
		subType, err := klineSubType(bin)
		if err != nil {
			return err
		}
		key := candleKey(code, bin)
		c.mu.Lock()
		c.candleCbs[key] = fn
		c.mu.Unlock()
		return c.subscribe(code, subType)
	case exchange.WatchTypeDepth:
		c.mu.Lock()
		c.depthCbs[code] = fn
		c.mu.Unlock()
		if err := c.subscribe(code, adapt.SubType_OrderBook); err != nil {
			return err
		}
		return c.pushOrderBook(code)
	case exchange.WatchTypeTradeMarket:
		c.mu.Lock()
		c.tradeMarketCbs[code] = fn
		c.mu.Unlock()
		if err := c.subscribe(code, adapt.SubType_Ticker); err != nil {
			return err
		}
		return c.pushTickers(code)
	default:
		return fmt.Errorf("unknown watch param: %s", param.Type)
	}
}

func (c *Client) ProcessOrder(action TradeAction) (*Order, error) {
	if !c.tradingEnabled() {
		return nil, errors.New("futu order submission requires trd_env/acc_id config")
	}
	if err := c.ensureTrade(); err != nil {
		return nil, err
	}
	code, err := c.normalizeSymbol(action.Symbol)
	if err != nil {
		return nil, err
	}
	trdSide := c.trdSide(action.Action)
	orderType, price, auxPrice := orderTypePrice(action)
	ctx, cancel := c.requestContext()
	defer cancel()
	opts := make([]adapt.Option, 0, 2)
	if action.ID != "" {
		opts = append(opts, adapt.With("remark", action.ID))
	}
	if auxPrice > 0 {
		opts = append(opts, adapt.With("auxPrice", auxPrice))
	}
	res, err := c.sdk.PlaceOrderWithContext(ctx, c.header, trdSide, orderType, code, absFloat(action.Amount), price, opts...)
	if err != nil {
		return nil, err
	}
	order := &Order{
		OrderID: strconv.FormatUint(res.GetOrderID(), 10),
		Symbol:  code,
		Amount:  absFloat(action.Amount),
		Price:   action.Price,
		Status:  "SUBMITTED",
		Side:    sideName(trdSide),
		Time:    time.Now(),
		Remark:  action.ID,
	}
	return order, nil
}

func (c *Client) CancelOrder(old *Order) (*Order, error) {
	if old == nil {
		return nil, errors.New("futu cancel order cannot use a nil order")
	}
	if !c.tradingEnabled() {
		return nil, errors.New("futu order cancellation requires trd_env/acc_id config")
	}
	if err := c.ensureTrade(); err != nil {
		return nil, err
	}
	orderID, err := strconv.ParseUint(old.OrderID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("futu invalid order id %q: %w", old.OrderID, err)
	}
	ctx, cancel := c.requestContext()
	defer cancel()
	if _, err := c.sdk.ModifyOrderWithContext(ctx, c.header, orderID, adapt.ModifyOrderOp_Cancel); err != nil {
		return nil, err
	}
	ret := *old
	ret.Status = OrderStatusCanceled
	ret.Time = time.Now()
	return &ret, nil
}

func (c *Client) CancelAllOrders() ([]*Order, error) {
	if !c.tradingEnabled() {
		return nil, errors.New("futu order cancellation requires trd_env/acc_id config")
	}
	if err := c.ensureTrade(); err != nil {
		return nil, err
	}
	ctx, cancel := c.requestContext()
	defer cancel()
	orders, err := c.sdk.GetOpenOrderListWithContext(ctx, c.header)
	if err != nil {
		return nil, err
	}
	canceled := make([]*Order, 0, len(orders))
	for _, item := range orders {
		if item == nil {
			continue
		}
		if _, err := c.sdk.ModifyOrderWithContext(ctx, c.header, item.GetOrderID(), adapt.ModifyOrderOp_Cancel); err != nil {
			log.Warnf("futu cancel order %d failed: %v", item.GetOrderID(), err)
			continue
		}
		order := c.orderFromFutu(item)
		order.Status = OrderStatusCanceled
		canceled = append(canceled, order)
	}
	return canceled, nil
}

func (c *Client) tradingEnabled() bool {
	return c.cfg.TrdEnv != ""
}

// ensureTrade resolves the trade account, unlocks it if configured, and
// subscribes to account pushes.
func (c *Client) ensureTrade() error {
	c.tradeMu.Lock()
	defer c.tradeMu.Unlock()
	if c.header != nil {
		return nil
	}
	ctx, cancel := c.requestContext()
	defer cancel()
	if err := c.pickAccount(ctx); err != nil {
		return err
	}
	if c.cfg.UnlockTrade && c.cfg.PwdMD5 != "" {
		if err := c.sdk.UnlockTradeWithContext(ctx, true, c.cfg.PwdMD5, c.cfg.SecurityFirm); err != nil {
			return fmt.Errorf("futu unlock trade: %w", err)
		}
	}
	if err := c.sdk.SubscribeAccPushWithContext(ctx, []uint64{c.header.GetAccID()}); err != nil {
		return fmt.Errorf("futu subscribe acc push: %w", err)
	}
	return nil
}

func (c *Client) initTrade() error {
	return c.ensureTrade()
}

func (c *Client) pickAccount(ctx context.Context) error {
	accs, err := c.sdk.GetAccListWithContext(ctx, adapt.With("trdCategory", adapt.TrdCategory_Security))
	if err != nil {
		return fmt.Errorf("futu get account list: %w", err)
	}
	var matched []*trdcommon.TrdAcc
	for _, acc := range accs {
		if acc == nil || acc.GetTrdEnv() != c.trdEnv {
			continue
		}
		if c.cfg.AccID != 0 && acc.GetAccID() == c.cfg.AccID {
			c.header = c.newHeader(acc.GetAccID())
			return nil
		}
		if marketAuthorized(acc, c.trdMarket) {
			matched = append(matched, acc)
		}
	}
	if c.cfg.AccID != 0 {
		return fmt.Errorf("futu account %d not found in %s environment", c.cfg.AccID, c.cfg.TrdEnv)
	}
	if len(matched) == 0 {
		return fmt.Errorf("futu no %s account found for market %s", c.cfg.TrdEnv, c.cfg.Market)
	}
	c.header = c.newHeader(matched[0].GetAccID())
	return nil
}

func (c *Client) newHeader(accID uint64) *trdcommon.TrdHeader {
	if c.trdEnv == int32(trdcommon.TrdEnv_TrdEnv_Real) {
		return adapt.NewTradeHeader(accID, c.trdMarket)
	}
	return adapt.NewSimulationTradeHeader(accID, c.trdMarket)
}

func (c *Client) fetchBalanceAndPosition() error {
	c.tradeMu.Lock()
	header := c.header
	c.tradeMu.Unlock()
	if header == nil {
		return errors.New("futu trade account not initialized")
	}
	ctx, cancel := c.requestContext()
	defer cancel()
	funds, err := c.sdk.GetFundsWithContext(ctx, header)
	if err != nil {
		return err
	}
	c.handleFunds(funds)
	positions, err := c.sdk.GetPositionListWithContext(ctx, header)
	if err != nil {
		return err
	}
	for _, pos := range positions {
		c.handlePosition(pos)
	}
	return nil
}

// refreshAccount schedules a debounced balance/position refresh, used after
// order pushes.
func (c *Client) refreshAccount() {
	c.refreshMu.Lock()
	if time.Since(c.lastRefresh) < time.Second {
		c.refreshMu.Unlock()
		return
	}
	c.lastRefresh = time.Now()
	c.refreshMu.Unlock()
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := c.fetchBalanceAndPosition(); err != nil {
			log.Warnf("futu refresh balance/position: %v", err)
		}
	}()
}

func (c *Client) handleFunds(funds *trdcommon.Funds) {
	if funds == nil {
		return
	}
	balance := &Balance{
		Currency:  c.currency(),
		Available: funds.GetCash() - funds.GetFrozenCash(),
		Frozen:    funds.GetFrozenCash(),
		Balance:   funds.GetCash(),
	}
	c.mu.RLock()
	callback := c.balanceCb
	c.mu.RUnlock()
	if callback != nil {
		callback(balance)
	}
}

func (c *Client) handlePosition(pos *trdcommon.Position) {
	if pos == nil {
		return
	}
	typ := Long
	if pos.GetPositionSide() == int32(trdcommon.PositionSide_PositionSide_Short) {
		typ = Short
	}
	position := &Position{
		Symbol:      c.codeWithMarket(pos.GetSecMarket(), pos.GetCode()),
		Type:        typ,
		Hold:        pos.GetQty(),
		Price:       pos.GetCostPrice(),
		ProfitRatio: pos.GetPlRatio() / 100,
	}
	c.mu.RLock()
	callback := c.positionCb
	c.mu.RUnlock()
	if callback != nil {
		callback(position)
	}
}

func (c *Client) subscribe(code string, subType int32) error {
	key := subscriptionKey(code, subType)
	c.mu.RLock()
	already := c.subscribed[key]
	c.mu.RUnlock()
	if already {
		return nil
	}
	ctx, cancel := c.requestContext()
	defer cancel()
	if err := c.sdk.SubscribeWithContext(ctx, []string{code}, []int32{subType}, true); err != nil {
		return fmt.Errorf("futu subscribe %s type %d: %w", code, subType, err)
	}
	c.mu.Lock()
	c.subscribed[key] = true
	c.mu.Unlock()
	return nil
}

func (c *Client) pushOrderBook(code string) error {
	ctx, cancel := c.requestContext()
	defer cancel()
	res, err := c.sdk.GetOrderBookWithContext(ctx, code, adapt.With("num", int32(10)))
	if err != nil {
		return err
	}
	c.mu.RLock()
	callback := c.depthCbs[code]
	c.mu.RUnlock()
	if callback != nil {
		callback(orderBookToDepth(res.GetOrderBookAskList(), res.GetOrderBookBidList()))
	}
	return nil
}

func (c *Client) pushTickers(code string) error {
	ctx, cancel := c.requestContext()
	defer cancel()
	res, err := c.sdk.GetTickerWithContext(ctx, code, adapt.With("maxRetNum", int32(100)))
	if err != nil {
		return err
	}
	c.mu.RLock()
	callback := c.tradeMarketCbs[code]
	c.mu.RUnlock()
	if callback != nil {
		for _, tk := range res.GetTickerList() {
			if tk != nil {
				callback(tickerToTrade(tk, code))
			}
		}
	}
	return nil
}

func (c *Client) registerHandlers() {
	c.sdk.RegisterHandler(protoid.QotUpdateKL, func(s2c proto.Message) error {
		msg, ok := s2c.(*qotupdatekl.S2C)
		if !ok {
			return nil
		}
		c.handleUpdateKL(msg)
		return nil
	})
	c.sdk.RegisterHandler(protoid.QotUpdateOrderBook, func(s2c proto.Message) error {
		msg, ok := s2c.(*qotupdateorderbook.S2C)
		if !ok {
			return nil
		}
		c.handleUpdateOrderBook(msg)
		return nil
	})
	c.sdk.RegisterHandler(protoid.QotUpdateTicker, func(s2c proto.Message) error {
		msg, ok := s2c.(*qotupdateticker.S2C)
		if !ok {
			return nil
		}
		c.handleUpdateTicker(msg)
		return nil
	})
	c.sdk.RegisterHandler(protoid.TrdUpdateOrder, func(s2c proto.Message) error {
		msg, ok := s2c.(*trdupdateorder.S2C)
		if !ok {
			return nil
		}
		c.handleUpdateOrder(msg)
		return nil
	})
	c.sdk.RegisterHandler(protoid.TrdUpdateOrderFill, func(s2c proto.Message) error {
		msg, ok := s2c.(*trdupdateorderfill.S2C)
		if !ok {
			return nil
		}
		c.handleUpdateOrderFill(msg)
		return nil
	})
}

func (c *Client) handleUpdateKL(s2c *qotupdatekl.S2C) {
	if s2c == nil {
		return
	}
	code := adapt.SecurityToCode(s2c.GetSecurity())
	if code == "" {
		return
	}
	bin := klineName(s2c.GetKlType())
	key := candleKey(code, bin)

	var latest *qotcommon.KLine
	for _, kl := range s2c.GetKlList() {
		if kl == nil || kl.GetIsBlank() {
			continue
		}
		if latest == nil || kl.GetTimestamp() > latest.GetTimestamp() {
			latest = kl
		}
	}
	if latest == nil {
		return
	}

	c.mu.Lock()
	prev := c.candleLatest[key]
	callback := c.candleCbs[key]
	c.candleLatest[key] = latest
	c.mu.Unlock()

	// Emit the previous bar once a newer bar arrives, i.e. only closed bars.
	if callback != nil && prev != nil && latest.GetTimestamp() > prev.GetTimestamp() {
		callback(klineToCandle(prev, code))
	}
}

func (c *Client) handleUpdateOrderBook(s2c *qotupdateorderbook.S2C) {
	if s2c == nil {
		return
	}
	code := adapt.SecurityToCode(s2c.GetSecurity())
	if code == "" {
		return
	}
	c.mu.RLock()
	callback := c.depthCbs[code]
	c.mu.RUnlock()
	if callback != nil {
		callback(orderBookToDepth(s2c.GetOrderBookAskList(), s2c.GetOrderBookBidList()))
	}
}

func (c *Client) handleUpdateTicker(s2c *qotupdateticker.S2C) {
	if s2c == nil {
		return
	}
	code := adapt.SecurityToCode(s2c.GetSecurity())
	if code == "" {
		return
	}
	c.mu.RLock()
	callback := c.tradeMarketCbs[code]
	c.mu.RUnlock()
	if callback == nil {
		return
	}
	for _, tk := range s2c.GetTickerList() {
		if tk != nil {
			callback(tickerToTrade(tk, code))
		}
	}
}

func (c *Client) handleUpdateOrder(s2c *trdupdateorder.S2C) {
	if s2c == nil || s2c.GetOrder() == nil {
		return
	}
	c.mu.RLock()
	callback := c.tradeCb
	c.mu.RUnlock()
	if callback != nil {
		callback(c.orderFromFutu(s2c.GetOrder()))
	}
	c.refreshAccount()
}

func (c *Client) handleUpdateOrderFill(s2c *trdupdateorderfill.S2C) {
	if s2c == nil || s2c.GetOrderFill() == nil {
		return
	}
	c.mu.RLock()
	callback := c.tradeCb
	c.mu.RUnlock()
	fill := s2c.GetOrderFill()
	if callback != nil {
		callback(&Order{
			OrderID: strconv.FormatUint(fill.GetOrderID(), 10),
			Symbol:  c.codeWithMarket(fill.GetSecMarket(), fill.GetCode()),
			Amount:  fill.GetQty(),
			Price:   fill.GetPrice(),
			Status:  OrderStatusFilled,
			Side:    sideName(fill.GetTrdSide()),
			Time:    timeFromTimestamp(fill.GetCreateTimestamp(), fill.GetCreateTime()),
			Filled:  fill.GetQty(),
		})
	}
	c.refreshAccount()
}

func (c *Client) orderFromFutu(order *trdcommon.Order) *Order {
	if order == nil {
		return nil
	}
	return &Order{
		OrderID: strconv.FormatUint(order.GetOrderID(), 10),
		Symbol:  c.codeWithMarket(order.GetSecMarket(), order.GetCode()),
		Amount:  order.GetQty(),
		Price:   order.GetPrice(),
		Status:  orderStatus(order.GetOrderStatus()),
		Side:    sideName(order.GetTrdSide()),
		Time:    timeFromTimestamp(order.GetUpdateTimestamp(), order.GetUpdateTime()),
		Filled:  order.GetFillQty(),
		Remark:  order.GetRemark(),
	}
}

func (c *Client) trdSide(action TradeType) int32 {
	if action.IsLong() {
		return adapt.TrdSide_Buy
	}
	if isFuturesTrdMarket(c.trdMarket) || strings.EqualFold(c.cfg.Market, "US") {
		return int32(trdcommon.TrdSide_TrdSide_SellShort)
	}
	return adapt.TrdSide_Sell
}

func orderTypePrice(action TradeAction) (orderType int32, price float64, auxPrice float64) {
	switch {
	case action.Action&Market == Market:
		return adapt.OrderType_Market, 0, 0
	case action.Action.IsStop():
		return adapt.OrderType_Stop, 0, action.Price
	default:
		return adapt.OrderType_Normal, action.Price, 0
	}
}

func (c *Client) requestContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), c.timeout)
}

func (c *Client) currency() string {
	if c.cfg.Currency != "" {
		return c.cfg.Currency
	}
	currency, ok := marketCurrencies[strings.ToUpper(c.cfg.Market)]
	if !ok {
		return "USD"
	}
	return currency
}

func (c *Client) normalizeSymbol(symbol string) (string, error) {
	raw := strings.TrimSpace(symbol)
	if raw == "" {
		return "", errors.New("futu symbol cannot be empty")
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 {
		return "", fmt.Errorf("futu invalid symbol %q", symbol)
	}
	market := strings.ToUpper(strings.TrimSpace(c.cfg.Market))
	code := raw
	if len(parts) == 2 {
		market = strings.ToUpper(parts[0])
		code = parts[1]
	}
	if market == "" {
		return "", fmt.Errorf("futu symbol %q requires a market prefix or a default market", symbol)
	}
	if _, _, err := marketIDs(market); err != nil {
		return "", err
	}
	if strings.TrimSpace(code) == "" {
		return "", fmt.Errorf("futu invalid symbol %q", symbol)
	}
	return market + "." + normalizeCode(market, strings.TrimSpace(code)), nil
}

func (c *Client) codeWithMarket(secMarket int32, code string) string {
	if code == "" {
		return ""
	}
	market, ok := secMarketNames[secMarket]
	if !ok {
		market = strings.ToUpper(c.cfg.Market)
		if market == "" {
			market = "US"
		}
	}
	return market + "." + code
}
