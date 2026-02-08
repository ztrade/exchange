package futures

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	gobinance "github.com/adshao/go-binance/v2"
	bfutures "github.com/adshao/go-binance/v2/futures"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	"github.com/ztrade/exchange"
	bcommon "github.com/ztrade/exchange/binance/common"
	. "github.com/ztrade/trademodel"
)

var (
	background = context.Background()
	newLock    sync.Mutex
)

var _ exchange.Exchange = &BinanceTrade{}

type BinanceTrade struct {
	name string
	api  *bfutures.Client

	tradeCb    exchange.WatchFn
	positionCb exchange.WatchFn
	balanceCb  exchange.WatchFn
	closeCh    chan bool

	cancelService    *bfutures.CancelAllOpenOrdersService
	cancelOneService *bfutures.CancelOrderService
	timeService      *bfutures.ServerTimeService

	klineLimit int
	timeout    time.Duration

	wsUserListenKey string

	baseCurrency string
	symbols      map[string]Symbol
}

func NewBinanceTrader(cfg bcommon.BinanceConfig, cltName, clientProxy string) (b *BinanceTrade, err error) {
	b = new(BinanceTrade)
	b.name = "binance"
	if cltName == "" {
		cltName = "binance"
	}
	b.klineLimit = 1500
	b.baseCurrency = "USDT"
	if cfg.Currency != "" {
		b.baseCurrency = cfg.Currency
	}
	b.timeout = cfg.Timeout
	if b.timeout == 0 {
		b.timeout = time.Second * 5
	}
	b.closeCh = make(chan bool)

	newLock.Lock()
	defer func() {
		bfutures.UseTestnet = false
		newLock.Unlock()
	}()
	if cfg.IsTest {
		bfutures.UseTestnet = true
		log.Warnf("binance trade connecting to testnet")
	}
	bfutures.WebsocketKeepalive = true
	b.api = gobinance.NewFuturesClient(cfg.Key, cfg.Secret)
	if clientProxy != "" {
		var proxyURL *url.URL
		proxyURL, err = url.Parse(clientProxy)
		if err != nil {
			return
		}
		b.api.HTTPClient = &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

		websocket.DefaultDialer.Proxy = http.ProxyURL(proxyURL)
		websocket.DefaultDialer.HandshakeTimeout = time.Second * 60
	}
	b.cancelService = b.api.NewCancelAllOpenOrdersService()
	b.cancelOneService = b.api.NewCancelOrderService()
	b.timeService = b.api.NewServerTimeService()
	_, err = b.Symbols()
	if err != nil {
		return nil, err
	}
	// err = b.Start()
	return
}

// fetchBalance different with spot
func (b *BinanceTrade) fetchBalanceAndPosition() (err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()
	account, err := b.api.NewGetAccountService().Do(ctx)
	if err != nil {
		return
	}

	var balance Balance
	balance.Balance = parseFloat(account.TotalWalletBalance)
	balance.Available = parseFloat(account.TotalCrossWalletBalance)
	// balance.Frozen =
	if b.balanceCb != nil {
		b.balanceCb(&balance)
	}
	if b.positionCb != nil {
		var amount, profit, initMargin float64
		for _, v := range account.Positions {
			amount = parseFloat(v.PositionAmt)
			if amount == 0 {
				continue
			}
			var position Position
			position.Symbol = v.Symbol
			position.Hold = amount
			position.Price = parseFloat(v.EntryPrice)
			profit = parseFloat(v.UnrealizedProfit)
			initMargin = parseFloat(v.PositionInitialMargin)
			position.ProfitRatio = profit / initMargin
			if position.Hold > 0 {
				position.Type = Long
			} else {
				position.Type = Short
			}
			b.positionCb(&position)

		}
	}
	return
}

func (b *BinanceTrade) Info() (info exchange.ExchangeInfo) {
	info = exchange.ExchangeInfo{
		Name:  "binance_futures",
		Value: "binance_futures",
		Desc:  "binance futures api",
		KLineLimit: exchange.FetchLimit{
			Limit: b.klineLimit,
		},
	}
	return
}

func (b *BinanceTrade) Symbols() (symbols []Symbol, err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()
	resp, err := b.api.NewExchangeInfoService().Do(ctx)
	if err != nil {
		return
	}
	symbols = make([]Symbol, len(resp.Symbols))
	for i, v := range resp.Symbols {
		value := Symbol{
			Name:            v.Symbol,
			Exchange:        "binance",
			Symbol:          v.Symbol,
			Resolutions:     "1m,5m,15m,30m,1h,4h,1d,1w",
			Precision:       v.PricePrecision,
			AmountPrecision: v.QuantityPrecision,
			PriceStep:       0,
			AmountStep:      0,
		}
		for _, f := range v.Filters {
			switch f["filterType"] {
			case "PRICE_FILTER":
				value.PriceStep = parseFloat(f["tickSize"].(string))
			case "LOT_SIZE":
				value.AmountStep = parseFloat(f["stepSize"].(string))
			default:
			}
		}
		symbols[i] = value
	}
	if len(symbols) > 0 {
		symbolMap := make(map[string]Symbol)
		for _, v := range symbols {
			symbolMap[v.Symbol] = v
		}
		b.symbols = symbolMap
	}
	return
}

func (b *BinanceTrade) Start() (err error) {
	err = b.fetchBalanceAndPosition()
	if err != nil {
		return
	}
	// watch position and order changed
	err = b.startUserWS()
	return
}
func (b *BinanceTrade) Stop() (err error) {
	close(b.closeCh)
	return
}

// KlineChan get klines
func (b *BinanceTrade) GetKline(symbol, bSize string, start, end time.Time) (data []*Candle, err error) {
	var temp *Candle
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()
	defer func() {
		if err != nil && strings.Contains(err.Error(), "Too many requests") {
			err = fmt.Errorf("%w, retry: %s", exchange.ErrRetry, err.Error())
		}
	}()
	// get server time
	nTime, err := b.timeService.Do(ctx)
	if err != nil {
		return
	}
	nStart := start.Unix() * 1000
	nEnd := end.Unix() * 1000

	klines, err := b.api.NewKlinesService().Interval(bSize).Symbol(symbol).StartTime(nStart).EndTime(nEnd).Limit(b.klineLimit).Do(ctx)
	if err != nil {
		return
	}
	sort.Slice(klines, func(i, j int) bool {
		return klines[i].OpenTime < klines[j].OpenTime
	})
	if len(klines) == 0 {
		log.Warnf("GetKline once, param: [%s]-[%s] no data", start, end)
		return
	}
	log.Infof("GetKline once, param: [%s]-[%s], total: %d, first: %s, last: %s", start, end, len(klines), time.UnixMilli(klines[0].OpenTime), time.UnixMilli(klines[len(klines)-1].OpenTime))
	data = []*Candle{}
	for k, v := range klines {
		temp = transCandle(v)
		if k == len(klines)-1 {
			// check if candle is unfinished
			if v.CloseTime > nTime {
				log.Infof("skip unfinished candle: %##v\n", *v)
				break
			}
		}
		data = append(data, temp)
	}
	return
}

func (b *BinanceTrade) handleError(typ string, cb func() error) func(error) {
	return func(err error) {
		log.Errorf("binance %s error:%s, call callback", typ, err.Error())
		if cb != nil {
			cb()
		}
	}
}

func (b *BinanceTrade) handleAggTradeEvent(fn exchange.WatchFn) func(evt *bfutures.WsAggTradeEvent) {
	return func(evt *bfutures.WsAggTradeEvent) {
		var err error
		var trade Trade
		trade.ID = fmt.Sprintf("%d", evt.AggregateTradeID)
		trade.Amount, err = strconv.ParseFloat(evt.Quantity, 64)
		if err != nil {
			log.Errorf("AggTradeEvent parse amount failed: %s", evt.Quantity)
		}
		trade.Price, err = strconv.ParseFloat(evt.Price, 64)
		if err != nil {
			log.Errorf("AggTradeEvent parse price failed: %s", evt.Price)
		}
		trade.Time = time.Unix(evt.Time/1000, (evt.Time%1000)*int64(time.Millisecond))
		if fn != nil {
			fn(&trade)
		}
	}
}

func (b *BinanceTrade) handleDepth(fn exchange.WatchFn) func(evt *bfutures.WsDepthEvent) {
	return func(evt *bfutures.WsDepthEvent) {
		var depth Depth
		var err error
		var price, amount float64
		depth.UpdateTime = time.Unix(evt.TransactionTime/1000, (evt.TransactionTime%1000)*int64(time.Millisecond))
		for _, v := range evt.Asks {
			// depth.Sells
			price, err = strconv.ParseFloat(v.Price, 64)
			if err != nil {
				log.Errorf("handleDepth parse price failed: %s", v.Price)
			}
			amount, err = strconv.ParseFloat(v.Quantity, 64)
			if err != nil {
				log.Errorf("handleDepth parse amount failed: %s", v.Quantity)
			}
			depth.Sells = append(depth.Sells, DepthInfo{Price: price, Amount: amount})
		}
		for _, v := range evt.Bids {
			// depth.Sells
			price, err = strconv.ParseFloat(v.Price, 64)
			if err != nil {
				log.Errorf("handleDepth parse price failed: %s", v.Price)
			}
			amount, err = strconv.ParseFloat(v.Quantity, 64)
			if err != nil {
				log.Errorf("handleDepth parse amount failed: %s", v.Quantity)
			}
			depth.Buys = append(depth.Buys, DepthInfo{Price: price, Amount: amount})
		}
		if fn != nil {
			fn(&depth)
		}
	}
}

func (b *BinanceTrade) retry(param exchange.WatchParam, fn exchange.WatchFn) func() error {
	return func() error {
		// retry when error cause
		select {
		case <-b.closeCh:
			return nil
		default:
		}
		return b.Watch(param, fn)
	}
}

func (b *BinanceTrade) Watch(param exchange.WatchParam, fn exchange.WatchFn) (err error) {
	symbol := param.Param["symbol"]
	var stopC chan struct{}
	switch param.Type {
	case exchange.WatchTypeCandle:
		binSize := param.Param["bin"]
		if binSize == "" {
			binSize = "1m"
		}
		var doneC chan struct{}
		finishC := make(chan struct{})
		doneC, stopC, err = bfutures.WsKlineServe(symbol, binSize, processWsCandle(finishC, fn), b.handleError("watchKline", b.retry(param, fn)))
		if err != nil {
			log.Error("exchange emitCandle error:", err.Error())
		}
		go func() {
			<-doneC
			close(finishC)
		}()
	case exchange.WatchTypeDepth:
		_, stopC, err = bfutures.WsPartialDepthServeWithRate(symbol, 10, 100*time.Millisecond, b.handleDepth(fn), b.handleError("depth", b.retry(param, fn)))
	case exchange.WatchTypeTradeMarket:
		_, stopC, err = bfutures.WsAggTradeServe(symbol, b.handleAggTradeEvent(fn), b.handleError("aggTrade", b.retry(param, fn)))
	case exchange.WatchTypeTrade:
		b.tradeCb = fn
	case exchange.WatchTypePosition:
		b.positionCb = fn
		err = b.fetchBalanceAndPosition()
	case exchange.WatchTypeBalance:
		b.balanceCb = fn
		err = b.fetchBalanceAndPosition()
	default:
		err = fmt.Errorf("unknown watch param: %s", param.Type)
	}
	if err != nil {
		return
	}
	if stopC != nil {
		go func() {
			<-b.closeCh
			close(stopC)
		}()
	}
	return
}

func (b *BinanceTrade) CancelOrder(old *Order) (order *Order, err error) {
	orderID, err := strconv.ParseInt(old.OrderID, 10, 64)
	if err != nil {
		return
	}
	resp, err := b.cancelOneService.Symbol(old.Symbol).OrderID(orderID).Do(context.Background())
	if err != nil {
		return
	}
	price, err := strconv.ParseFloat(resp.Price, 64)
	if err != nil {
		log.Errorf("CancelOrder parse price %s error: %s", resp.Price, err.Error())
	}
	amount, err := strconv.ParseFloat(resp.OrigQuantity, 64)
	if err != nil {
		log.Errorf("CancelOrder parse amount %s error: %s", resp.OrigQuantity, err.Error())
	}
	order = &Order{
		OrderID:  strconv.FormatInt(resp.OrderID, 10),
		Symbol:   resp.Symbol,
		Currency: resp.Symbol,
		Amount:   amount,
		Price:    price,
		Status:   strings.ToUpper(string(resp.Status)),
		Side:     strings.ToLower(string(resp.Side)),
		Time:     time.Unix(resp.UpdateTime/1000, 0),
	}

	return
}

func (b *BinanceTrade) ProcessOrder(act TradeAction) (ret *Order, err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()
	orderType := bfutures.OrderTypeLimit
	if act.Action.IsStop() {
		orderType = bfutures.OrderType(bfutures.AlgoOrderTypeStopMarket)
	}
	var side bfutures.SideType
	if act.Action.IsLong() {
		side = bfutures.SideTypeBuy
	} else {
		side = bfutures.SideTypeSell
	}
	symbol, ok := b.symbols[act.Symbol]
	if ok {
		price := symbol.FixPrice(act.Price)
		if price != act.Price {
			log.Infof("binance change order price form %f to %f", act.Price, price)
			act.Price = price
		}
	}

	sent := b.api.NewCreateOrderService().Symbol(act.Symbol)
	if act.Action.IsStop() {
		sent = sent.StopPrice(fmt.Sprintf("%f", act.Price))
	} else {
		sent = sent.Price(fmt.Sprintf("%f", act.Price))
	}
	if !act.Action.IsOpen() {
		sent = sent.ReduceOnly(true)
	}
	resp, err := sent.
		Quantity(fmt.Sprintf("%f", act.Amount)).
		TimeInForce(bfutures.TimeInForceTypeGTC).
		Type(orderType).
		Side(side).
		Do(ctx)
	if err != nil {
		return
	}
	ret = transCreateOrder(resp)
	return
}

func (b *BinanceTrade) CancelAllOrders() (orders []*Order, err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()
	ret, err := b.api.NewListOpenOrdersService().Do(ctx)
	if err != nil {
		err = fmt.Errorf("CancelOrder failed with list: %w", err)
		return
	}
	symbolMap := make(map[string]bool)
	var ok bool
	for _, v := range ret {
		od := transOrder(v)
		orders = append(orders, od)
		_, ok = symbolMap[od.Symbol]
		if ok {
			continue
		}
		symbolMap[od.Symbol] = true
		err = b.cancelService.Symbol(od.Symbol).Do(ctx)
		if err != nil {
			return nil, err
		}
	}
	return
}

func transOrder(fo *bfutures.Order) (o *Order) {
	price, err := strconv.ParseFloat(fo.Price, 64)
	if err != nil {
		log.Errorf("transOrder parse price %s error: %s", fo.Price, err.Error())
	}
	amount, err := strconv.ParseFloat(fo.OrigQuantity, 64)
	if err != nil {
		log.Errorf("transOrder parse amount %s error: %s", fo.OrigQuantity, err.Error())
	}
	o = &Order{
		OrderID:  strconv.FormatInt(fo.OrderID, 10),
		Symbol:   fo.Symbol,
		Currency: fo.Symbol,
		Amount:   amount,
		Price:    price,
		Status:   strings.ToUpper(string(fo.Status)),
		Side:     strings.ToLower(string(fo.Side)),
		Time:     time.Unix(fo.Time/1000, 0),
	}
	return
}

func transCreateOrder(fo *bfutures.CreateOrderResponse) (o *Order) {
	price, err := strconv.ParseFloat(fo.Price, 64)
	if err != nil {
		log.Errorf("transCreateOrder parse price %s error: %s", fo.Price, err.Error())
	}
	amount, err := strconv.ParseFloat(fo.OrigQuantity, 64)
	if err != nil {
		log.Errorf("transCreateOrder parse amount %s error: %s", fo.OrigQuantity, err.Error())
	}
	o = &Order{
		OrderID:  strconv.FormatInt(fo.OrderID, 10),
		Symbol:   fo.Symbol,
		Currency: fo.Symbol,
		Amount:   amount,
		Price:    price,
		Status:   strings.ToUpper(string(fo.Status)),
		Side:     strings.ToLower(string(fo.Side)),
		Time:     time.Unix(fo.UpdateTime/1000, 0),
	}
	return
}

func transCandle(candle *bfutures.Kline) (ret *Candle) {
	ret = &Candle{
		ID:       0,
		Start:    candle.OpenTime / 1000,
		Open:     parseFloat(candle.Open),
		High:     parseFloat(candle.High),
		Low:      parseFloat(candle.Low),
		Close:    parseFloat(candle.Close),
		Turnover: parseFloat(candle.QuoteAssetVolume),
		Volume:   parseFloat(candle.Volume),
		Trades:   candle.TradeNum,
	}
	return
}

func parseFloat(str string) float64 {
	if str == "" {
		return 0
	}
	f, err := strconv.ParseFloat(str, 64)
	if err != nil {
		log.Errorf("binance parseFloat error: %s, input: %s", err.Error(), str)
		return 0
	}
	return f
}
