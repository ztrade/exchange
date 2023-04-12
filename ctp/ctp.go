package ctp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	"github.com/ztrade/base/common"
	"github.com/ztrade/ctp"
	"github.com/ztrade/exchange"
	"github.com/ztrade/exchange/ctp/util"

	. "github.com/ztrade/trademodel"
)

type Config struct {
	TdServer string
	MdServer string
	BrokerID string
	User     string
	Password string
	AppID    string
	AuthCode string
	KlineUrl string
}

type CtpExchange struct {
	name  string
	mdApi *ctp.CThostFtdcMdApi
	mdSpi *MdSpi
	tdApi *ctp.CThostFtdcTraderApi
	tdSpi *TdSpi
	cfg   *Config

	tradeCb       exchange.WatchFn
	positionCb    exchange.WatchFn
	balanceCb     exchange.WatchFn
	depthCb       exchange.WatchFn
	tradeMarketCb exchange.WatchFn

	prevVolume  float64
	preTurnover float64
	orderID     uint64
	orders      map[string]*Order

	strStart  string
	startOnce sync.Once

	watchSymbols []string
	symbolMap    sync.Map
	klines       map[string]*util.CTPKline
	timeout      time.Duration
	klineLimit   int

	isConnect atomic.Bool
	closed    chan struct{}
	clt       http.Client
}

func NewCtp(cfg exchange.Config, cltName string) (e exchange.Exchange, err error) {
	b, err := NewCtpExchange(cfg, cltName)
	if err != nil {
		return
	}
	e = b
	return
}

func init() {
	exchange.RegisterExchange("ctp", NewCtp)
}

func parseSymbol(str string) (exchange, symbol string, err error) {
	strs := strings.Split(str, ".")
	if len(strs) != 2 {
		err = errors.New("symbol format error {EXCHANGE}.{SYMBOL}")
		return
	}
	exchange, symbol = strs[0], strs[1]
	return
}

func formatSymbol(exchange, symbol string) string {
	return fmt.Sprintf("%s.%s", exchange, symbol)
}

func NewCtpExchange(cfg exchange.Config, cltName string) (c *CtpExchange, err error) {
	var ctpConfig Config
	err = cfg.UnmarshalKey(fmt.Sprintf("exchanges.%s", cltName), &ctpConfig)
	if err != nil {
		return
	}
	c = &CtpExchange{name: cltName, cfg: &ctpConfig, closed: make(chan struct{})}
	c.klineLimit = 500
	c.timeout = time.Second * 30
	t := time.Now()
	c.strStart = t.Format("01021504")
	c.orders = make(map[string]*Order)
	c.klines = make(map[string]*util.CTPKline)
	return
}

func (c *CtpExchange) Info() exchange.ExchangeInfo {
	info := exchange.ExchangeInfo{
		Name:  "ctp",
		Value: "ctp",
		Desc:  "ctp api",
		KLineLimit: exchange.FetchLimit{
			Limit: c.klineLimit,
		},
	}
	return info
}

func (c *CtpExchange) HasInit() bool {
	return c.isConnect.Load()
}

func (c *CtpExchange) connLoop() {
	var weekDay time.Weekday
	var err error
	var t time.Time
	// var needConnect bool
Out:
	for {
		// needConnect = false
		select {
		case <-c.closed:
			break Out
		default:
		}
		t = time.Now()
		weekDay = t.Weekday()
		if weekDay == time.Sunday || weekDay == time.Saturday {
			time.Sleep(time.Hour)
			continue
		}
		if c.isConnect.Load() {
			time.Sleep(time.Minute)
			continue
		}
		util.WaitTradeTime()
		// if !needConnect {
		// 	logrus.Info("ctp wait time")
		// 	time.Sleep(time.Minute)
		// 	continue
		// }
		// 重连
		logrus.Infof("ctp reconnect")
		for {
			err = c.reconnect()
			logrus.Info("ctp reconnect status:", err)
			if err == nil {
				time.Sleep(time.Minute)
				break
			}
		}
	}
}

func (c *CtpExchange) reconnect() (err error) {
	if c.tdApi != nil {
		c.tdApi.Release()
		c.tdApi = nil
	}
	if c.mdApi != nil {
		c.mdApi.Release()
		c.mdApi = nil
	}
	err = c.initTdApi()
	if err != nil {
		return err
	}
	err = c.initMdApi()
	if err != nil {
		return err
	}
	c.isConnect.Store(true)
	return
}

func (c *CtpExchange) initMdApi() (err error) {
	util.WaitTradeTime()
	c.mdApi = ctp.MdCreateFtdcMdApi("./ctp/md", false, false)
	c.mdApi.RegisterFront(fmt.Sprintf("tcp://%s", c.cfg.MdServer))
	c.mdSpi, err = NewMdSpi(c.cfg, c.mdApi)
	if err != nil {
		return
	}
	c.mdSpi.SetMarketFn(c.onDepthData)
	c.mdApi.RegisterSpi(c.mdSpi)
	c.mdApi.Init()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	err = c.mdSpi.WaitLogin(ctx)
	cancel()
	if err != nil {
		return
	}
	return
}

func (c *CtpExchange) initTdApi() (err error) {
	util.WaitTradeTime()
	err = os.MkdirAll("./ctp", os.ModePerm)
	if err != nil {
		return
	}
	tdApi := ctp.TdCreateFtdcTraderApi("./ctp/td")
	tdApi.SubscribePrivateTopic(ctp.THOST_TERT_QUICK)
	tdApi.SubscribePublicTopic(ctp.THOST_TERT_QUICK)
	tdSpi := NewTdSpi(c.cfg, tdApi)
	tdSpi.SetTradeFn(c.onTrade)
	tdSpi.SetOrderFn(c.onOrder)
	c.tdSpi = tdSpi
	c.tdApi = tdApi
	tdApi.RegisterSpi(tdSpi)
	tdApi.RegisterFront(fmt.Sprintf("tcp://%s", c.cfg.TdServer))
	tdApi.Init()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
	defer cancel()
	log.Info("wait TdApi login")
	err = tdSpi.WaitSymbols(ctx)
	if err != nil {
		log.Errorf("login error: %s", err.Error())
		return
	}
	log.Info("TdApi login success")
	return
}

func (c *CtpExchange) Start() error {
	c.startOnce.Do(func() {
		go c.connLoop()
		go c.syncPosition()
	})
	for !c.isConnect.Load() {
		log.Info("start ctp exchange, wait for connect ")
		time.Sleep(time.Second * 30)
		continue
	}
	return nil
}

func (c *CtpExchange) Stop() error {
	close(c.closed)
	return nil
}

func (c *CtpExchange) syncPosition() (err error) {
	tick := time.NewTicker(time.Second * 10)
	defer tick.Stop()
	var resps []*ctp.CThostFtdcInvestorPositionField
	var balanceResp *ctp.CThostFtdcTradingAccountField
	for {
		select {
		case <-tick.C:
			if !c.HasInit() {
				continue
			}
			// log.Info("query positions")
			ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
			resps, err = util.ReqWithResps[ctp.CThostFtdcInvestorPositionField](ctx, func(id int) error {
				pQryInvestorPosition := &ctp.CThostFtdcQryInvestorPositionField{BrokerID: c.cfg.BrokerID, InvestorID: c.cfg.User}
				nRet := c.tdApi.ReqQryInvestorPosition(pQryInvestorPosition, id)
				if nRet != 0 {
					err1 := fmt.Errorf("ReqQryInvestorPosition failed: %d", nRet)
					return err1
				}
				return nil
			})
			cancel()
			if err != nil {
				log.Error("query PositionDetail failed:", err.Error())
				continue
			}
			c.updatePosition(resps)
			// 流控
			time.Sleep(time.Second)
			// update balance
			// CThostFtdcTradingAccountField
			// log.Info("query balances")
			ctx, cancel = context.WithTimeout(context.Background(), c.timeout)
			balanceResp, err = util.Req[ctp.CThostFtdcTradingAccountField](ctx, func(id int) error {
				var pQryTradingAccount = ctp.CThostFtdcQryTradingAccountField{
					BrokerID:   c.cfg.BrokerID,
					InvestorID: c.cfg.User,
					CurrencyID: "CNY",
					BizType:    '1',
					// AccountID: c.cfg.User,
				}
				// buf, _ := json.Marshal(pQryTradingAccount)
				// fmt.Println("req:", string(buf))
				nRet := c.tdApi.ReqQryTradingAccount(&pQryTradingAccount, id)
				if nRet != 0 {
					err1 := fmt.Errorf("ReqQryTradingAccount failed: %d", nRet)
					return err1
				}
				return nil

			})
			cancel()
			if err != nil {
				log.Error("query balance failed:", err.Error())
				continue
			}
			bal := Balance{
				Currency:  "CNY",
				Available: balanceResp.Available,
				Frozen:    balanceResp.FrozenCash,
				Balance:   balanceResp.Balance,
			}
			if c.balanceCb != nil {
				c.balanceCb(&bal)
			}
			buf, _ := json.Marshal(balanceResp)
			log.Debug("balance:", string(buf))

		case <-c.closed:
			return
		}
	}
	return
}

// Kline get klines
func (c *CtpExchange) GetKline(symbol, bSize string, start, end time.Time) (data []*Candle, err error) {
	fetchUrl := fmt.Sprintf("%s?symbol=%s&bin=%s&start=%d&end=%d", c.cfg.KlineUrl, symbol, bSize, start.Unix(), end.Unix())
	resp, err := c.clt.Get(fetchUrl)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	d := json.NewDecoder(resp.Body)
	err = d.Decode(&data)
	return
}

func (c *CtpExchange) Watch(param exchange.WatchParam, fn exchange.WatchFn) (err error) {
	str := param.Param["symbol"]
	exchangeID, symbol, err := parseSymbol(str)
	if err != nil {
		log.Warnf("ctp watch parse symbol error: %s,type: %s,param: %v", err.Error(), param.Type, param.Param)
	}
	log.Info("Ctp watch:", exchangeID, symbol)
	switch param.Type {
	case exchange.WatchTypeCandle:
		_, ok := c.klines[symbol]
		if ok {
			return
		}
		kl := util.NewCTPKline()
		c.klines[symbol] = kl
		kl.SetCb(func(data *Candle) { fn(data) })
		c.symbolMap.Store(symbol, str)
		c.mdApi.SubscribeMarketData([]string{symbol})
		c.watchSymbols = append(c.watchSymbols, symbol)
	case exchange.WatchTypeDepth:
		c.depthCb = fn
		c.mdApi.SubscribeMarketData([]string{symbol})
		c.watchSymbols = append(c.watchSymbols, symbol)
	case exchange.WatchTypeTradeMarket:
		c.tradeMarketCb = fn
		c.mdApi.SubscribeMarketData([]string{symbol})
		c.watchSymbols = append(c.watchSymbols, symbol)
	case exchange.WatchTypeTrade:
		c.tradeCb = fn
	case exchange.WatchTypePosition:
		c.positionCb = fn
		// err = b.fetchBalanceAndPosition()
	case exchange.WatchTypeBalance:
		c.balanceCb = fn
		// err = b.fetchBalanceAndPosition()
	default:
		err = fmt.Errorf("unknown wath param: %s", param.Type)
	}
	return
}

func (c *CtpExchange) CancelOrder(old *Order) (order *Order, err error) {
	err = c.cancelOrder(old.Remark, old)
	order = old
	return
}

// for trade
// ProcessOrder process order
func (c *CtpExchange) ProcessOrder(act TradeAction) (ret *Order, err error) {
	exchangeID, symbol, err := parseSymbol(act.Symbol)
	if err != nil {
		return
	}
	orderID := atomic.AddUint64(&c.orderID, 1)
	strOrderID := fmt.Sprintf("%s%d", c.strStart, orderID)
	var action ctp.CThostFtdcInputOrderField
	// ----------必填字段----------------
	action.BrokerID = c.cfg.BrokerID
	///投资者代码
	action.InvestorID = c.cfg.User
	///交易所代码
	action.ExchangeID = exchangeID
	///合约代码
	action.InstrumentID = symbol
	///报单价格条件 THOST_FTDC_OPT_LimitPrice
	action.OrderPriceType = '2'
	if act.Action.IsLong() {
		///买卖方向
		action.Direction = '0'
	} else {
		action.Direction = '1'
	}
	///价格
	action.LimitPrice = act.Price
	///数量
	action.VolumeTotalOriginal = int(act.Amount)
	if act.Action.IsOpen() {
		///组合开平标志
		action.CombOffsetFlag = "0"
	} else {
		action.CombOffsetFlag = "1"
	}
	///组合投机套保标志 THOST_FTDC_ECIDT_Speculation
	action.CombHedgeFlag = "1"
	///触发条件 THOST_FTDC_CC_Immediately
	action.ContingentCondition = '1'
	///有效期类型THOST_FTDC_TC_GFD
	action.TimeCondition = '3'
	///成交量类型 THOST_FTDC_VC_AV
	action.VolumeCondition = '1'
	///最小成交量
	action.MinVolume = 0
	// -----------------选填字段------------------------
	///报单引用
	action.OrderRef = strOrderID
	///用户代码
	action.UserID = c.cfg.User
	///GTD日期
	// action.GTDDate;
	///止损价
	// action.StopPrice;
	///强平原因
	action.ForceCloseReason = '0'
	///自动挂起标志
	// action.IsAutoSuspend;
	///业务单元
	// action.BusinessUnit;
	///请求编号
	action.RequestID = int(orderID)
	///用户强评标志
	// action.UserForceClose;
	///互换单标志
	// action.IsSwapOrder;

	///投资单元代码
	// action.InvestUnitID
	///资金账号
	// TThostFtdcAccountIDType	AccountID;
	///币种代码
	// action.CurrencyID
	///交易编码
	// action.ClientID;
	///IP地址
	// action.IPAddress;
	///Mac地址
	// action.MacAddress;
	log.Info("processOrder ReqOrderInsert", action)

	// 如果参数都正确，则onOrder会被调用，如果参数错误，则OnRspOrderInsert会被调用
	nRet := c.tdApi.ReqOrderInsert(&action, util.GetReqID())
	if nRet != 0 {
		err = fmt.Errorf("ReqOrderInsert error: %d", nRet)
		return
	}
	ret = &Order{
		OrderID:  strconv.FormatUint(orderID, 10),
		Symbol:   act.Symbol,
		Currency: "cny",
		Amount:   act.Amount,
		Price:    act.Price,
		Status:   "send",
		Time:     time.Now(),
		Remark:   strOrderID,
	}
	if act.Action.IsLong() {
		ret.Side = "buy"
	} else {
		ret.Side = "sell"
	}
	c.orders[strOrderID] = ret
	log.Info("processOrder finished:", strOrderID)
	return
}

func (c *CtpExchange) CancelAllOrders() (orders []*Order, err error) {
	for k, v := range c.orders {
		if v.Status == OrderStatusFilled || v.Status == OrderStatusCanceled {
			continue
		}
		err = c.cancelOrder(k, v)
		if err != nil {
			log.Errorf("cancel order %s failed: %s", k, err.Error())
		}
	}

	return
}

func (c *CtpExchange) cancelOrder(ref string, o *Order) (err error) {
	exchangeID, _, err := parseSymbol(o.Symbol)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	resp, err := util.Req[ctp.CThostFtdcInputOrderActionField](ctx, func(id int) error {
		pInputOrderAction := &ctp.CThostFtdcInputOrderActionField{
			BrokerID:   c.cfg.BrokerID,
			InvestorID: c.cfg.User,
			// OrderActionRef
			OrderRef:   ref,
			RequestID:  id,
			FrontID:    c.tdSpi.frontID,
			SessionID:  c.tdSpi.sessionID,
			ExchangeID: exchangeID,
			OrderSysID: o.OrderID,
			ActionFlag: '0',
			// LimitPrice     float64
			// VolumeChange   int
			// UserID         string
			// InstrumentID   string
			// InvestUnitID   string
			// IPAddress      string
			// MacAddress     string
		}
		nRet := c.tdApi.ReqOrderAction(pInputOrderAction, id)
		if nRet != 0 {
			err = fmt.Errorf("cancel order failed: %d", nRet)
			return err
		}
		return nil
	})
	buf, _ := json.Marshal(resp)
	log.Info("cancelOrder resp:", string(buf))
	return
}

func (b *CtpExchange) Symbols() (symbols []Symbol, err error) {
	rawSymbols := b.tdSpi.GetSymbols()
	for _, v := range rawSymbols {
		expireDate, err := time.Parse("20060102", v.ExpireDate)
		if err != nil {
			return nil, err
		}
		symbol := Symbol{
			Name:        fmt.Sprintf("%s.%s", v.ExchangeID, v.InstrumentID),
			Exchange:    b.name,
			Symbol:      fmt.Sprintf("%s.%s", v.ExchangeID, v.InstrumentID),
			Description: "",
			Type:        SymbolTypeFutures,

			ExpirationDate: expireDate,

			Resolutions:     "1m,5m,15m,30m,1h,4h,1d,1w",
			Precision:       int(1 / v.PriceTick),
			AmountPrecision: int(1 / v.MinLimitOrderVolume),
			PriceStep:       v.PriceTick,
			AmountStep:      float64(v.MinLimitOrderVolume),
		}
		if symbol.Precision == 0 {
			symbol.Precision = 1
		}
		if symbol.AmountPrecision == 0 {
			symbol.AmountPrecision = 1
		}
		symbols = append(symbols, symbol)

	}
	sort.Slice(symbols, func(i, j int) bool {
		return symbols[i].Symbol < symbols[j].Symbol
	})
	return
}

// {"TradingDay":"20211119","InstrumentID":"al2201","ExchangeID":"","ExchangeInstID":"","LastPrice":18350,"PreSettlementPrice":18580,"PreClosePrice":18475,"PreOpenInterest":236229,"OpenPrice":18475,"HighestPrice":18490,"LowestPrice":18230,"Volume":165674,"Turnover":15192326925,"OpenInterest":250293,"ClosePrice":0,"SettlementPrice":0,"UpperLimitPrice":20065,"LowerLimitPrice":17090,"PreDelta":0,"CurrDelta":0,"UpdateTime":"23:40:51","UpdateMillisec":500,"BidPrice1":18350,"BidVolume1":25,"AskPrice1":18355,"AskVolume1":71,"BidPrice2":18345,"BidVolume2":43,"AskPrice2":18360,"AskVolume2":83,"BidPrice3":18340,"BidVolume3":59,"AskPrice3":18365,"AskVolume3":48,"BidPrice4":18335,"BidVolume4":61,"AskPrice4":18370,"AskVolume4":75,"BidPrice5":18330,"BidVolume5":69,"AskPrice5":18375,"AskVolume5":155,"AveragePrice":91700.12750944626,"ActionDay":"20211118"}
func (c *CtpExchange) onDepthData(pDepthMarketData *ctp.CThostFtdcDepthMarketDataField) {
	if !c.HasInit() {
		return
	}
	if c.prevVolume == 0 {
		c.prevVolume = float64(pDepthMarketData.Volume)
		// c.preTurnover = float64(pDepthMarketData.Turnover)
		return
	}
	now := time.Now()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	date := now.Format("20060102")
	timeStr := fmt.Sprintf("%s %s.%03d", date, pDepthMarketData.UpdateTime, pDepthMarketData.UpdateMillisec)
	tm, err := time.ParseInLocation("20060102 15:04:05.000", timeStr, loc)
	if err != nil {
		log.Errorf("CtpExchange Parse MarketData timestamp %s failed %s", timeStr, err.Error())
	}
	var depth Depth
	depth.UpdateTime = tm
	depth.Buys = append(depth.Buys, DepthInfo{
		Price:  pDepthMarketData.BidPrice1,
		Amount: float64(pDepthMarketData.BidVolume1)})
	depth.Sells = append(depth.Sells, DepthInfo{
		Price:  pDepthMarketData.AskPrice1,
		Amount: float64(pDepthMarketData.AskVolume1)})
	var symbol = pDepthMarketData.InstrumentID
	ret, ok := c.symbolMap.Load(symbol)
	if ok {
		symbol = ret.(string)
	}
	if c.depthCb != nil {
		c.depthCb(&depth)
	}
	var trade Trade
	trade.Time = tm
	trade.Amount = float64(pDepthMarketData.Volume) - c.prevVolume
	c.prevVolume = float64(pDepthMarketData.Volume)
	// turnover := pDepthMarketData.Turnover - c.preTurnover
	// c.preTurnover = pDepthMarketData.Turnover
	trade.Price = pDepthMarketData.LastPrice
	if trade.Amount != 0 {
		if c.tradeMarketCb != nil {
			c.tradeMarketCb(&trade)
		}
	}

	kl, ok := c.klines[pDepthMarketData.InstrumentID]
	if !ok {
		log.Warnf("CtpExchange no kline found: %s", symbol)
		return
	}
	kl.Update(pDepthMarketData)
}

func (c *CtpExchange) onTrade(pTrade *ctp.CThostFtdcTradeField) {
	v, ok := c.orders[pTrade.OrderRef]
	if ok {
		if pTrade.Volume == int(v.Amount) {
			v.Status = OrderStatusFilled
		} else {
			v.Amount -= float64(pTrade.Volume)
		}
	}
	var trade Trade
	trade.ID = pTrade.TradeID
	// trade.Action
	// trade.Time   = pTrade.TradeDate + pTrade.TradeTime
	trade.Price = pTrade.Price
	trade.Amount = float64(pTrade.Volume)
	// trade.Side = pTrade.TradeType
	trade.Remark = pTrade.OrderRef
	buf, err := json.Marshal(pTrade)
	log.Info("trade:", string(buf), err)
	if c.tradeCb != nil {
		c.tradeCb(&trade)
	}

}

func (c *CtpExchange) onOrder(p *ctp.CThostFtdcOrderField) {
	buf, _ := json.Marshal(p)
	log.Info("onOrder:", string(buf))
	o, ok := c.orders[p.OrderRef]
	if !ok {
		log.Warnf("updateOrderStatus  not exist, %s", string(buf))
		return
	}
	o.OrderID = p.OrderSysID
	if p.OrderStatus == '0' {
		o.Status = OrderStatusFilled
	} else if p.OrderStatus == '5' {
		o.Status = OrderStatusCanceled
	}
}

// [{"InstrumentID":"c2307","BrokerID":"9099","InvestorID":"6126689","HedgeFlag":49,"Direction":48,"OpenDate":"20230410","TradeID":"   103398157","Volume":1,"OpenPrice":2763,"TradingDay":"20230410","SettlementID":1,"TradeType":48,"CombInstrumentID":"","ExchangeID":"DCE","CloseProfitByDate":0,"CloseProfitByTrade":0,"PositionProfitByDate":0,"PositionProfitByTrade":0,"Margin":4973.4,"ExchMargin":3315.6,"MarginRateByMoney":0.18,"MarginRateByVolume":0,"LastSettlementPrice":2713,"SettlementPrice":2763,"CloseVolume":0,"CloseAmount":0,"TimeFirstVolume":1,"InvestUnitID":"","SpecPosiType":35}]
func (c *CtpExchange) updatePosition(resps []*ctp.CThostFtdcInvestorPositionField) {
	if len(resps) == 0 {
		log.Warn("updatePosition resp empty")
		return
	}
	buf, _ := json.Marshal(resps)
	log.Info("updatePosition:", c.positionCb, string(buf))
	if c.positionCb == nil {
		return
	}
	// TODO: when no position?
	posCache := make(map[string][]*Position)
	for _, v := range resps {
		if v == nil {
			continue
		}
		symbol := formatSymbol(v.ExchangeID, v.InstrumentID)
		var pos Position
		pos.Hold = float64(v.Position)
		if v.PosiDirection == '3' {
			pos.Hold = 0 - pos.Hold
		}
		if pos.Hold == 0 {
			continue
		}
		pos.Symbol = symbol
		pos.Price = v.PositionCost / float64(v.Position)
		posCache[symbol] = append(posCache[symbol], &pos)

	}
	for k, v := range posCache {
		var posMerge Position
		var totalPrice float64
		for _, p := range v {
			totalPrice += p.Price
			posMerge.Hold += p.Hold
		}
		posMerge.Price = common.FloatMul(totalPrice, posMerge.Hold)
		posMerge.Symbol = k
		fmt.Println("pos:", posMerge)
		c.positionCb(&posMerge)
	}
}
