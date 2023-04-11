package okx

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	"github.com/ztrade/exchange"
	"github.com/ztrade/exchange/okx/api/account"
	"github.com/ztrade/exchange/okx/api/market"
	"github.com/ztrade/exchange/okx/api/public"
	"github.com/ztrade/exchange/okx/api/trade"
	"github.com/ztrade/exchange/ws"
	. "github.com/ztrade/trademodel"
)

var (
	background = context.Background()

	ApiAddr       = "https://aws.okx.com/"
	WSOkexPUbilc  = "wss://wsaws.okx.com:8443/ws/v5/public"
	WSOkexPrivate = "wss://wsaws.okx.com:8443/ws/v5/private"

	TypeSPOT    = "SPOT"    //币币
	TypeMARGIN  = "MARGIN"  // 币币杠杆
	TypeSWAP    = "SWAP"    //永续合约
	TypeFUTURES = "FUTURES" //交割合约
	TypeOption  = "OPTION"  //期权

	PosNetMode       = "net_mode"
	PosLongShortMode = "long_short_mode"
)

var _ exchange.Exchange = &OkxTrader{}

func init() {
	exchange.RegisterExchange("okx", NewOkexExchange)
}

type OkxTrader struct {
	Name       string
	tradeApi   *trade.ClientWithResponses
	marketApi  *market.ClientWithResponses
	publicApi  *public.ClientWithResponses
	accountApi *account.ClientWithResponses

	tradeCb       exchange.WatchFn
	positionCb    exchange.WatchFn
	balanceCb     exchange.WatchFn
	depthCb       exchange.WatchFn
	tradeMarketCb exchange.WatchFn
	klineCb       exchange.WatchFn

	closeCh chan bool

	cfg *OkxConfig

	klineLimit int
	wsUser     *ws.WSConn
	wsPublic   *ws.WSConn

	ordersCache     sync.Map
	stopOrdersCache sync.Map

	posMode string

	watchPublics []OPParam

	instType string
	timeout  time.Duration
	symbols  map[string]Symbol
}

func NewOkexExchange(cfg exchange.Config, cltName string) (e exchange.Exchange, err error) {
	b, err := NewOkxTrader(cfg, cltName)
	if err != nil {
		return
	}
	e = b
	return
}

func NewOkxTrader(cfg exchange.Config, cltName string) (b *OkxTrader, err error) {
	b = new(OkxTrader)
	b.Name = "okx"
	b.instType = "SWAP"
	if cltName == "" {
		cltName = "okx"
	}
	b.symbols = make(map[string]Symbol)
	b.klineLimit = 100
	b.timeout = time.Second * 5
	var okxCfg OkxConfig
	err = cfg.UnmarshalKey(fmt.Sprintf("exchanges.%s", cltName), &okxCfg)
	if err != nil {
		return nil, err
	}
	b.cfg = &okxCfg

	// isDebug := cfg.GetBool(fmt.Sprintf("exchanges.%s.debug", cltName))
	if b.cfg.Tdmode == "" {
		b.cfg.Tdmode = "isolated"
	}
	log.Infof("okex %s,  tdMode: %s, isTest: %t", cltName, b.cfg.Tdmode, b.cfg.IsTest)

	b.closeCh = make(chan bool)

	b.tradeApi, err = trade.NewClientWithResponses(ApiAddr)
	if err != nil {
		return
	}
	b.marketApi, err = market.NewClientWithResponses(ApiAddr)
	if err != nil {
		return
	}
	b.publicApi, err = public.NewClientWithResponses(ApiAddr)
	if err != nil {
		return
	}
	b.accountApi, err = account.NewClientWithResponses(ApiAddr)
	if err != nil {
		return
	}
	clientProxy := cfg.GetString("proxy")
	if clientProxy != "" {
		var proxyURL *url.URL
		proxyURL, err = url.Parse(clientProxy)
		if err != nil {
			return
		}
		clt := b.marketApi.ClientInterface.(*market.Client).Client.(*http.Client)
		*clt = http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
		clt = b.tradeApi.ClientInterface.(*trade.Client).Client.(*http.Client)
		*clt = http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
		websocket.DefaultDialer.Proxy = http.ProxyURL(proxyURL)
		websocket.DefaultDialer.HandshakeTimeout = time.Second * 60
	}
	_, err = b.Symbols()
	if err != nil {
		return nil, err
	}
	if b.cfg.Key != "" {
		err = b.getAccountConfig()
	}
	return
}

func (b *OkxTrader) Info() exchange.ExchangeInfo {
	info := exchange.ExchangeInfo{
		Name:  "okx",
		Value: "okx",
		Desc:  "okx api",
		KLineLimit: exchange.FetchLimit{
			Limit: b.klineLimit,
		},
	}
	return info
}

func (b *OkxTrader) SetInstType(instType string) {
	b.instType = instType
}

func (b *OkxTrader) auth(ctx context.Context, req *http.Request) (err error) {
	var temp []byte
	if req.Method != "GET" {
		temp, err = io.ReadAll(req.Body)
		if err != nil {
			return
		}
		req.Body.Close()
		buf := bytes.NewBuffer(temp)
		req.Body = io.NopCloser(buf)
	} else {
		if req.URL.RawQuery != "" {
			temp = []byte(fmt.Sprintf("?%s", req.URL.RawQuery))
		}
	}
	var signStr string
	tmStr := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	signStr = fmt.Sprintf("%s%s%s%s", tmStr, req.Method, req.URL.Path, string(temp))
	h := hmac.New(sha256.New, []byte(b.cfg.Secret))
	h.Write([]byte(signStr))
	ret := h.Sum(nil)
	n := base64.StdEncoding.EncodedLen(len(ret))
	dst := make([]byte, n)
	base64.StdEncoding.Encode(dst, ret)
	sign := string(dst)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OK-ACCESS-KEY", b.cfg.Key)
	req.Header.Set("OK-ACCESS-SIGN", sign)

	req.Header.Set("OK-ACCESS-TIMESTAMP", tmStr)
	req.Header.Set("OK-ACCESS-PASSPHRASE", b.cfg.Pwd)
	return
}

func (b *OkxTrader) Start() (err error) {
	fmt.Println("start okx")
	err = b.runPublic()
	if err != nil {
		return
	}
	err = b.runPrivate()
	if err != nil {
		return
	}
	fmt.Println("start okx finished")
	return
}
func (b *OkxTrader) Stop() (err error) {
	b.wsPublic.Close()
	b.wsUser.Close()
	close(b.closeCh)
	return
}

func (b *OkxTrader) customReq(ctx context.Context, req *http.Request) error {
	if b.cfg.IsTest {
		req.Header.Set("x-simulated-trading", "1")
	}
	return nil
}
func (b *OkxTrader) getAccountConfig() (err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()
	resp, err := b.accountApi.GetApiV5AccountConfigWithResponse(ctx, b.auth, b.customReq)
	if err != nil {
		return err
	}
	var accountCfg AccountConfig
	err = json.Unmarshal(resp.Body, &accountCfg)
	if err != nil {
		return err
	}
	if accountCfg.Code != "0" {
		err = fmt.Errorf("[%s]%s", accountCfg.Code, accountCfg.Msg)
		return
	}
	// long_short_mode：双向持仓 net_mode：单向持仓
	// 仅适用交割/永续
	b.posMode = accountCfg.Data[0].PosMode
	log.Infof("posMode: %s", b.posMode)
	if b.posMode != PosNetMode {
		log.Warnf("account posmode is %s, stop order will failed if no position", b.posMode)
	}
	return nil
}

// KlineChan get klines
func (b *OkxTrader) GetKline(symbol, bSize string, start, end time.Time) (data []*Candle, err error) {
	// fmt.Println("GetKline:", symbol, bSize, start, end)

	nStart := start.Unix() * 1000
	nEnd := end.UnixMilli()
	tempEnd := nEnd
	var resp *market.GetApiV5MarketHistoryCandlesResponse
	var startStr, endStr string

	ctx, cancel := context.WithTimeout(background, time.Second*3)
	startStr = strconv.FormatInt(nStart, 10)
	tempEnd = nStart + 100*60*1000
	if tempEnd > nEnd {
		tempEnd = nEnd
	}
	endStr = strconv.FormatInt(tempEnd, 10)
	var params = market.GetApiV5MarketHistoryCandlesParams{InstId: symbol, Bar: &bSize, Before: &startStr, After: &endStr}
	resp, err = b.marketApi.GetApiV5MarketHistoryCandlesWithResponse(ctx, &params, b.customReq)
	cancel()
	if err != nil {
		return
	}
	data, err = parseCandles(resp)
	if err != nil {
		if strings.Contains(err.Error(), "Requests too frequent.") {
			err = fmt.Errorf("requests too frequent %w", exchange.ErrRetry)
		}
		return
	}
	sort.Slice(data, func(i, j int) bool {
		return data[i].Start < data[j].Start
	})

	return
}

func (b *OkxTrader) Watch(param exchange.WatchParam, fn exchange.WatchFn) (err error) {
	symbol := param.Param["symbol"]
	log.Info("okex watch:", param)
	switch param.Type {
	case exchange.WatchTypeCandle:
		var p = OPParam{
			OP: "subscribe",
			Args: []interface{}{
				OPArg{Channel: "candle1m", InstType: b.instType, InstID: symbol},
			},
		}
		b.klineCb = fn
		b.watchPublics = append(b.watchPublics, p)
		err = b.wsPublic.WriteMsg(p)
	case exchange.WatchTypeDepth:
		var p = OPParam{
			OP: "subscribe",
			Args: []interface{}{
				OPArg{Channel: "books5", InstType: b.instType, InstID: symbol},
			},
		}
		b.depthCb = fn
		b.watchPublics = append(b.watchPublics, p)
		err = b.wsPublic.WriteMsg(p)
	case exchange.WatchTypeTradeMarket:
		var p = OPParam{
			OP: "subscribe",
			Args: []interface{}{
				OPArg{Channel: "trades", InstType: b.instType, InstID: symbol},
			},
		}
		b.tradeMarketCb = fn
		b.watchPublics = append(b.watchPublics, p)
		err = b.wsPublic.WriteMsg(p)
	case exchange.WatchTypeTrade:
		b.tradeCb = fn
	case exchange.WatchTypePosition:
		b.positionCb = fn
		err = b.fetchBalanceAndPosition()
	case exchange.WatchTypeBalance:
		b.balanceCb = fn
		err = b.fetchBalanceAndPosition()
	default:
		err = fmt.Errorf("unknown wath param: %s", param.Type)
	}
	return
}
func (b *OkxTrader) fetchBalanceAndPosition() (err error) {
	err = b.fetchBalance()
	if err != nil {
		return err
	}
	err = b.fetchPosition()
	return
}

func (b *OkxTrader) fetchPosition() (err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()
	var params = account.GetApiV5AccountPositionsParams{InstType: &b.instType}
	resp, err := b.accountApi.GetApiV5AccountPositionsWithResponse(ctx, &params, b.auth, b.customReq)
	if err != nil {
		return err
	}
	if b.positionCb == nil {
		return
	}
	var data AccountPositionResp
	err = json.Unmarshal(resp.Body, &data)
	if err != nil {
		return err
	}
	for _, v := range data.Data {
		var pos Position
		pos.Hold = parseFloat(v.Pos)
		pos.Price = parseFloat(v.AvgPx)
		pos.Symbol = v.InstID
		if pos.Hold > 0 {
			pos.Type = Long
		} else {
			pos.Type = Short
		}
		pos.ProfitRatio = parseFloat(v.UplRatio)
		if pos.Hold == 0 {
			log.Warnf("fetch position return 0: %s", string(resp.Body))
			continue
		}
		b.positionCb(&pos)
	}

	return
}

func (b *OkxTrader) fetchBalance() (err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()
	var ccy = "USDT"
	var param = account.GetApiV5AccountBalanceParams{Ccy: &ccy}
	resp, err := b.accountApi.GetApiV5AccountBalanceWithResponse(ctx, &param, b.auth, b.customReq)
	if err != nil {
		return err
	}
	if b.balanceCb == nil {
		return
	}
	var balance AccountBalanceResp
	err = json.Unmarshal(resp.Body, &balance)
	if err != nil {
		return err
	}
	for _, v := range balance.Data {
		for _, d := range v.Details {
			if d.Ccy == ccy {
				var bal Balance
				bal.Available = parseFloat(d.AvailBal)
				bal.Balance = parseFloat(d.CashBal)
				bal.Currency = ccy
				bal.Frozen = parseFloat(d.OrdFrozen)
				b.balanceCb(&bal)
				break
			}
		}
	}
	return
}

func (b *OkxTrader) processStopOrder(act TradeAction) (ret *Order, err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()
	var side, posSide string
	// open: side = posSide, close: side!=posSide
	if act.Action.IsLong() {
		side = "buy"
		posSide = "short"
	} else {
		side = "sell"
		posSide = "long"
	}
	reduceOnly := true
	var orderPx = "-1"
	triggerPx := fmt.Sprintf("%f", act.Price)
	// PostApiV5TradeOrderAlgoJSONBody defines parameters for PostApiV5TradeOrderAlgo.
	params := trade.PostApiV5TradeOrderAlgoJSONBody{
		// 非必填<br>保证金币种，如：USDT<br>仅适用于单币种保证金模式下的全仓杠杆订单
		//	Ccy *string `json:"ccy,omitempty"`

		// 必填<br>产品ID，如：`BTC-USDT`
		InstId: act.Symbol,

		// 必填<br>订单类型。<br>`conditional`：单向止盈止损<br>`oco`：双向止盈止损<br>`trigger`：计划委托<br>`iceberg`：冰山委托<br>`twap`：时间加权委托
		OrdType: "conditional",

		// 非必填<br>委托价格<br>委托价格为-1时，执行市价委托<br>适用于`计划委托`
		OrderPx: &orderPx,

		// 可选<br>持仓方向<br>在双向持仓模式下必填，且仅可选择 `long` 或 `short`
		PosSide: &posSide,

		// 非必填<br>挂单限制价<br>适用于`冰山委托`和`时间加权委托`
		//	PxLimit *string `json:"pxLimit,omitempty"`

		// 非必填<br>距离盘口的比例价距<br>适用于`冰山委托`和`时间加权委托`
		//	PxSpread *string `json:"pxSpread,omitempty"`

		// 非必填<br>距离盘口的比例<br>pxVar和pxSpread只能传入一个<br>适用于`冰山委托`和`时间加权委托`
		//	PxVar *string `json:"pxVar,omitempty"`

		// 非必填<br>是否只减仓，`true` 或 `false`，默认`false`
		// 仅适用于币币杠杆，以及买卖模式下的交割/永续
		ReduceOnly: &reduceOnly,

		// 必填<br>订单方向。买：`buy` 卖：`sell`
		Side: side,

		// 非必填<br>止损委托价，如果填写此参数，必须填写止损触发价<br>委托价格为-1时，执行市价止损<br>适用于`止盈止损委托`
		SlOrdPx: &orderPx,

		// 非必填<br>止损触发价，如果填写此参数，必须填写止损委托价<br>适用于`止盈止损委托`
		SlTriggerPx: &triggerPx,

		// 必填<br>委托数量
		Sz: fmt.Sprintf("%d", int(act.Amount)),

		// 非必填<br>单笔数量<br>适用于`冰山委托`和`时间加权委托`
		//	SzLimit *string `json:"szLimit,omitempty"`

		// 必填<br>交易模式<br>保证金模式：`isolated`：逐仓 ；`cross`<br>全仓非保证金模式：`cash`：非保证金
		TdMode: b.cfg.Tdmode,

		// 非必填<br>市价单委托数量的类型<br>交易货币：`base_ccy`<br>计价货币：`quote_ccy`<br>仅适用于币币订单
		//	TgtCcy *string `json:"tgtCcy,omitempty"`

		// 非必填<br>挂单限制价<br>适用于`时间加权委托`
		//	TimeInterval *string `json:"timeInterval,omitempty"`

		// 非必填<br>止盈委托价，如果填写此参数，必须填写止盈触发价<br>委托价格为-1时，执行市价止盈<br>适用于`止盈止损委托`
		//        TpOrdPx ,

		// 非必填<br>止盈触发价，如果填写此参数，必须填写止盈委托价<br>适用于`止盈止损委托`
		//	TpTriggerPx *string `json:"tpTriggerPx,omitempty"`

		// 非必填<br>计划委托触发价格<br>适用于`计划委托`
		//	TriggerPx *string `json:"triggerPx,omitempty"`
	}
	if b.posMode == PosNetMode {
		params.PosSide = nil
	}
	resp, err := b.tradeApi.PostApiV5TradeOrderAlgoWithResponse(ctx, params, b.auth, b.customReq)
	if err != nil {
		return
	}

	orders, err := parsePostAlgoOrders(act.Symbol, "open", side, act.Price, act.Amount, resp.Body)
	if err != nil {
		return
	}
	if len(orders) != 1 {
		err = fmt.Errorf("orders len not match: %#v", orders)
		log.Warnf(err.Error())
		return
	}
	ret = orders[0]
	ret.Remark = "stop"
	return
}
func (b *OkxTrader) CancelOrder(old *Order) (order *Order, err error) {
	_, ok := b.ordersCache.Load(old.OrderID)
	if ok {
		order, err = b.cancelNormalOrder(old)
		if err != nil {
			return
		}
		b.ordersCache.Delete(old.OrderID)
	}
	_, ok = b.stopOrdersCache.Load(old.OrderID)
	if ok {
		order, err = b.cancelAlgoOrder(old)
		b.stopOrdersCache.Delete(old.OrderID)
	}
	return
}

func (b *OkxTrader) cancelNormalOrder(old *Order) (order *Order, err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()

	var body trade.PostApiV5TradeCancelOrderJSONRequestBody
	body.InstId = old.Symbol
	body.OrdId = &old.OrderID

	cancelResp, err := b.tradeApi.PostApiV5TradeCancelOrderWithResponse(ctx, body, b.auth, b.customReq)
	if err != nil {
		return
	}
	temp := OKEXOrder{}
	err = json.Unmarshal(cancelResp.Body, &temp)
	if err != nil {
		return
	}
	if temp.Code != "0" {
		err = errors.New(string(cancelResp.Body))
	}
	order = old
	if len(temp.Data) > 0 {
		order.OrderID = temp.Data[0].OrdID
	}
	return
}

func (b *OkxTrader) cancelAlgoOrder(old *Order) (order *Order, err error) {
	ctx, cancel := context.WithTimeout(background, time.Second*2)
	defer cancel()

	var body = make(trade.PostApiV5TradeCancelAlgosJSONBody, 1)
	body[0] = trade.CancelAlgoOrder{AlgoId: old.OrderID, InstId: old.Symbol}

	cancelResp, err := b.tradeApi.PostApiV5TradeCancelAlgosWithResponse(ctx, body, b.auth, b.customReq)
	if err != nil {
		return
	}
	temp := OKEXAlgoOrder{}
	err = json.Unmarshal(cancelResp.Body, &temp)
	if err != nil {
		return
	}
	if temp.Code != "0" {
		err = errors.New(string(cancelResp.Body))
	}
	order = old
	if len(temp.Data) > 0 {
		order.OrderID = temp.Data[0].AlgoID
	}
	return
}

func (b *OkxTrader) ProcessOrder(act TradeAction) (ret *Order, err error) {
	symbol, ok := b.symbols[act.Symbol]
	if ok {
		price := symbol.FixPrice(act.Price)
		if price != act.Price {
			log.Infof("okx change order price form %f to %f", act.Price, price)
			act.Price = price
		}
	}
	if act.Action.IsStop() {
		// if no position:
		// stopOrder will fail when  posMode = long_short_mode
		// stopOrder will success when posMode =net_mode
		ret, err = b.processStopOrder(act)
		if err != nil {
			return
		}
		b.stopOrdersCache.Store(ret.OrderID, ret)
		return
	}
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()
	var side, posSide, px string
	if act.Action.IsLong() {
		side = "buy"
		if act.Action.IsOpen() {
			posSide = "long"
		} else {
			posSide = "short"
		}
	} else {
		side = "sell"
		if act.Action.IsOpen() {
			posSide = "short"
		} else {
			posSide = "long"
		}
	}
	ordType := "limit"
	tag := "ztrade"
	px = fmt.Sprintf("%f", act.Price)
	params := trade.PostApiV5TradeOrderJSONRequestBody{
		//ClOrdId *string `json:"clOrdId,omitempty"`
		// 必填<br>产品ID，如：`BTC-USDT`
		InstId: act.Symbol,
		// 必填<br>订单类型。<br>市价单：`market`<br>限价单：`limit`<br>只做maker单：`post_only`<br>全部成交或立即取消：`fok`<br>立即成交并取消剩余：`ioc`<br>市价委托立即成交并取消剩余：`optimal_limit_ioc`（仅适用交割、永续）
		OrdType: ordType,

		// 可选<br>持仓方向<br>在双向持仓模式下必填，且仅可选择 `long` 或 `short`
		PosSide: &posSide,

		// 可选<br>委托价格<br>仅适用于`limit`、`post_only`、`fok`、`ioc`类型的订单
		Px: &px,

		// 非必填<br>是否只减仓，`true` 或 `false`，默认`false`<br>仅适用于币币杠杆订单
		//		ReduceOnly: &reduceOnly,
		// 必填<br>订单方向。买：`buy` 卖：`sell`
		Side: side,
		// 必填<br>委托数量
		Sz: fmt.Sprintf("%d", int(act.Amount)),
		// 非必填<br>订单标签<br>字母（区分大小写）与数字的组合，可以是纯字母、纯数字，且长度在1-8位之间。
		Tag: &tag,
		// 必填<br>交易模式<br>保证金模式：`isolated`：逐仓 ；`cross`<br>全仓非保证金模式：`cash`：非保证金
		TdMode: b.cfg.Tdmode,
		// 非必填<br>市价单委托数量的类型<br>交易货币：`base_ccy`<br>计价货币：`quote_ccy`<br>仅适用于币币订单
		//	TgtCcy *string `json:"tgtCcy,omitempty"`
	}
	if b.posMode == PosNetMode {
		params.PosSide = nil
	}
	resp, err := b.tradeApi.PostApiV5TradeOrderWithResponse(ctx, params, b.auth, b.customReq)
	if err != nil {
		return
	}
	orders, err := parsePostOrders(act.Symbol, "open", side, act.Price, act.Amount, resp.Body)
	if err != nil {
		return
	}
	if len(orders) != 1 {
		err = fmt.Errorf("orders len not match: %#v", orders)
		log.Warnf(err.Error())
		return
	}
	ret = orders[0]
	b.ordersCache.Store(ret.OrderID, ret)
	return
}

func (b *OkxTrader) cancelAllNormal() (orders []*Order, err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()
	instType := b.instType
	var params = trade.GetApiV5TradeOrdersPendingParams{
		// InstId:   &b.symbol,
		InstType: &instType,
	}
	resp, err := b.tradeApi.GetApiV5TradeOrdersPendingWithResponse(ctx, &params, b.auth, b.customReq)
	if err != nil {
		return
	}
	var orderResp CancelNormalResp
	err = json.Unmarshal(resp.Body, &orderResp)
	if err != nil {
		return
	}
	if orderResp.Code != "0" {
		err = errors.New(string(resp.Body))
		return
	}
	if len(orderResp.Data) == 0 {
		return
	}

	var body trade.PostApiV5TradeCancelBatchOrdersJSONRequestBody
	for _, v := range orderResp.Data {
		temp := v.OrdID
		body = append(body, trade.CancelBatchOrder{
			InstId: v.InstID,
			OrdId:  &temp,
		})
	}

	cancelResp, err := b.tradeApi.PostApiV5TradeCancelBatchOrdersWithResponse(ctx, body, b.auth, b.customReq)
	if err != nil {
		return
	}
	temp := OKEXOrder{}
	err = json.Unmarshal(cancelResp.Body, &temp)
	if err != nil {
		return
	}
	if temp.Code != "0" {
		err = errors.New(string(cancelResp.Body))
	}
	return
}

func (b *OkxTrader) cancelAllAlgo() (orders []*Order, err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()
	instType := b.instType
	var params = trade.GetApiV5TradeOrdersAlgoPendingParams{
		OrdType: "conditional",
		// InstId:   &b.symbol,
		InstType: &instType,
	}
	resp, err := b.tradeApi.GetApiV5TradeOrdersAlgoPendingWithResponse(ctx, &params, b.auth, b.customReq)
	if err != nil {
		return
	}
	var orderResp CancelAlgoResp
	err = json.Unmarshal(resp.Body, &orderResp)
	if err != nil {
		return
	}
	if orderResp.Code != "0" {
		err = errors.New(string(resp.Body))
		return
	}
	if len(orderResp.Data) == 0 {
		return
	}

	var body trade.PostApiV5TradeCancelAlgosJSONRequestBody
	for _, v := range orderResp.Data {
		body = append(body, trade.CancelAlgoOrder{
			InstId: v.InstID,
			AlgoId: v.AlgoID,
		})
	}

	cancelResp, err := b.tradeApi.PostApiV5TradeCancelAlgosWithResponse(ctx, body, b.auth, b.customReq)
	if err != nil {
		return
	}
	temp := OKEXAlgoOrder{}
	err = json.Unmarshal(cancelResp.Body, &temp)
	if err != nil {
		return
	}
	if temp.Code != "0" {
		err = errors.New(string(cancelResp.Body))
	}
	return
}
func (b *OkxTrader) CancelAllOrders() (orders []*Order, err error) {
	temp, err := b.cancelAllNormal()
	if err != nil {
		return
	}
	orders, err = b.cancelAllAlgo()
	if err != nil {
		return
	}
	orders = append(temp, orders...)
	return
}

func (b *OkxTrader) Symbols() (symbols []Symbol, err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()
	resp, err := b.publicApi.GetApiV5PublicInstrumentsWithResponse(ctx, &public.GetApiV5PublicInstrumentsParams{InstType: b.instType}, b.customReq)
	if err != nil {
		return
	}
	var instruments InstrumentResp
	err = json.Unmarshal(resp.Body, &instruments)
	if instruments.Code != "0" {
		err = errors.New(string(resp.Body))
		return
	}
	var value, amountPrecision float64
	symbols = make([]Symbol, len(instruments.Data))
	for i, v := range instruments.Data {
		value, err = strconv.ParseFloat(v.TickSz, 64)
		if err != nil {
			return
		}
		amountPrecision, err = strconv.ParseFloat(v.LotSz, 64)
		if err != nil {
			return
		}
		symbolInfo := Symbol{
			Name:            v.InstID,
			Exchange:        "okx",
			Symbol:          v.InstID,
			Resolutions:     "1m,5m,15m,30m,1h,4h,1d,1w",
			Precision:       int(float64(1) / value),
			AmountPrecision: int(float64(1) / amountPrecision),
			PriceStep:       value,
			AmountStep:      0,
		}
		value, err = strconv.ParseFloat(v.MinSz, 64)
		if err != nil {
			return
		}
		symbolInfo.AmountStep = value
		symbols[i] = symbolInfo
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
