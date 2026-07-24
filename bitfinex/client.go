package bitfinex

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bitfinexcom/bitfinex-api-go/pkg/models/book"
	"github.com/bitfinexcom/bitfinex-api-go/pkg/models/candle"
	bfxmodel "github.com/bitfinexcom/bitfinex-api-go/pkg/models/common"
	"github.com/bitfinexcom/bitfinex-api-go/pkg/models/notification"
	bfxorder "github.com/bitfinexcom/bitfinex-api-go/pkg/models/order"
	bfxposition "github.com/bitfinexcom/bitfinex-api-go/pkg/models/position"
	"github.com/bitfinexcom/bitfinex-api-go/pkg/models/trade"
	"github.com/bitfinexcom/bitfinex-api-go/pkg/models/wallet"
	"github.com/bitfinexcom/bitfinex-api-go/v2/rest"
	bfxws "github.com/bitfinexcom/bitfinex-api-go/v2/websocket"
	log "github.com/sirupsen/logrus"
	"github.com/ztrade/exchange"
	. "github.com/ztrade/trademodel"
)

const (
	productionRESTURL = "https://api-pub.bitfinex.com/v2/"
	productionWSURL   = "wss://api-pub.bitfinex.com/ws/2"
	defaultCurrency   = "UST"
	pricePrecision    = 5
	amountPrecision   = 8
)

var _ exchange.Exchange = (*Client)(nil)

type Client struct {
	kind         Kind
	cfg          BitfinexConfig
	currency     string
	timeout      time.Duration
	klineLimit   int
	rest         *rest.Client
	ws           *bfxws.Client
	cid          atomic.Int64
	connectionMu sync.Mutex
	connected    bool
	stopped      bool

	mu                   sync.RWMutex
	tradeCb              exchange.WatchFn
	positionCb           exchange.WatchFn
	balanceCb            exchange.WatchFn
	candleCallbacks      map[string]exchange.WatchFn
	depthCallbacks       map[string]exchange.WatchFn
	tradeMarketCallbacks map[string]exchange.WatchFn
	candleLatest         map[string]*candle.Candle
	subscriptions        map[string]string
	symbolList           []Symbol
	pairByAsset          map[string]string
}

func NewClient(cfg BitfinexConfig, proxy string, kind Kind) (*Client, error) {
	if kind != KindSpot && kind != KindFutures {
		return nil, fmt.Errorf("unsupported bitfinex kind %q", kind)
	}
	if (cfg.Key == "") != (cfg.Secret == "") {
		return nil, errors.New("bitfinex API key and secret must be configured together")
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	currency := normalizeCurrency(cfg.Currency)
	if currency == "" {
		currency = defaultCurrency
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	proxyURL, err := parseProxy(proxy)
	if err != nil {
		return nil, err
	}
	if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	httpClient := &http.Client{Transport: transport, Timeout: timeout}
	restURL := cfg.RESTURL
	if restURL == "" {
		restURL = productionRESTURL
	}
	if !strings.HasSuffix(restURL, "/") {
		restURL += "/"
	}
	api := rest.NewClientWithURLHttpDo(restURL, func(_ *http.Client, req *http.Request) (*http.Response, error) {
		return httpClient.Do(req)
	}).Credentials(cfg.Key, cfg.Secret)

	params := bfxws.NewDefaultParameters()
	params.ManageOrderbook = true
	params.HeartbeatTimeout = maxDuration(30*time.Second, timeout*3)
	params.ShutdownTimeout = timeout
	if cfg.WSURL != "" {
		params.URL = cfg.WSURL
	} else {
		params.URL = productionWSURL
	}
	var wsClient *bfxws.Client
	if proxyURL != nil {
		factory := newWebsocketFactory(params.URL, proxyURL, timeout)
		wsClient = bfxws.NewWithParamsAsyncFactory(params, factory)
	} else {
		wsClient = bfxws.NewWithParams(params)
	}
	if cfg.Key != "" || cfg.Secret != "" {
		wsClient.Credentials(cfg.Key, cfg.Secret)
	}

	b := &Client{
		kind:                 kind,
		cfg:                  cfg,
		currency:             currency,
		timeout:              timeout,
		klineLimit:           1000,
		rest:                 api,
		ws:                   wsClient,
		candleCallbacks:      make(map[string]exchange.WatchFn),
		depthCallbacks:       make(map[string]exchange.WatchFn),
		tradeMarketCallbacks: make(map[string]exchange.WatchFn),
		candleLatest:         make(map[string]*candle.Candle),
		subscriptions:        make(map[string]string),
		pairByAsset:          make(map[string]string),
	}
	b.cid.Store(time.Now().UnixMilli())
	go b.consumeWebsocket()
	if cfg.IsTest {
		log.Warn("bitfinex paper trading uses the production API endpoints; make sure the API key belongs to a paper account")
	}
	if _, err = b.Symbols(); err != nil {
		b.ws.Close()
		return nil, err
	}
	return b, nil
}

func (b *Client) Info() exchange.ExchangeInfo {
	name := "bitfinex_" + string(b.kind)
	return exchange.ExchangeInfo{
		Name:  name,
		Value: name,
		Desc:  "bitfinex " + string(b.kind) + " api",
		KLineLimit: exchange.FetchLimit{
			Limit: b.klineLimit,
		},
	}
}

func (b *Client) Symbols() ([]Symbol, error) {
	b.mu.RLock()
	if len(b.symbolList) > 0 {
		ret := append([]Symbol(nil), b.symbolList...)
		b.mu.RUnlock()
		return ret, nil
	}
	b.mu.RUnlock()

	pairs, err := b.fetchSymbolPairs()
	if err != nil {
		return nil, err
	}

	symbols := make([]Symbol, 0, len(pairs))
	pairByAsset := make(map[string]string)
	for _, pair := range pairs {
		derivative := isDerivativePair(pair)
		if derivative != (b.kind == KindFutures) {
			continue
		}
		base, quote, ok := pairAssets(pair, b.currency)
		if !ok || quote != b.currency {
			continue
		}
		native := normalizeNativeSymbol(pair)
		typ := SymbolTypeSpot
		if derivative {
			typ = SymbolTypeFutures
		}
		value := Symbol{
			Name:            native,
			Exchange:        "bitfinex",
			Symbol:          native,
			Type:            typ,
			Resolutions:     "1m,5m,15m,30m,1h,3h,6h,12h,1d,1w,14d,1M",
			Precision:       pricePrecision,
			AmountPrecision: amountPrecision,
			PriceStep:       math.Pow10(-pricePrecision),
			AmountStep:      math.Pow10(-amountPrecision),
		}
		symbols = append(symbols, value)
		if !derivative {
			pairByAsset[base] = native
		}
	}
	sort.Slice(symbols, func(i, j int) bool { return symbols[i].Symbol < symbols[j].Symbol })

	b.mu.Lock()
	b.symbolList = append([]Symbol(nil), symbols...)
	b.pairByAsset = pairByAsset
	b.mu.Unlock()
	return append([]Symbol(nil), symbols...), nil
}

func (b *Client) fetchSymbolPairs() ([]string, error) {
	if b.kind == KindFutures {
		statuses, err := b.rest.Status.DerivativeStatusAll()
		if err != nil {
			return nil, err
		}
		pairs := make([]string, 0, len(statuses))
		for _, status := range statuses {
			if status == nil || strings.TrimSpace(status.Symbol) == "" {
				continue
			}
			pairs = append(pairs, strings.TrimPrefix(strings.ToUpper(status.Symbol), "T"))
		}
		return pairs, nil
	}

	req := rest.NewRequestWithMethod("conf/pub:list:pair:exchange", http.MethodGet)
	raw, err := b.rest.Request(req)
	if err != nil {
		return nil, err
	}
	return pairsFromRaw(raw)
}

func (b *Client) Start() error {
	if b.hasCredentials() {
		if err := b.fetchBalanceAndPosition(); err != nil {
			return err
		}
	}
	return b.ensureWebsocket()
}

func (b *Client) Stop() error {
	b.connectionMu.Lock()
	defer b.connectionMu.Unlock()
	if b.stopped {
		return nil
	}
	b.stopped = true
	b.ws.Close()
	b.connected = false
	return nil
}

func (b *Client) ensureWebsocket() error {
	b.connectionMu.Lock()
	defer b.connectionMu.Unlock()
	if b.stopped {
		return errors.New("bitfinex client is stopped")
	}
	if b.connected {
		return nil
	}
	if err := b.ws.Connect(); err != nil {
		return err
	}
	b.connected = true
	return nil
}

func (b *Client) GetKline(symbol, binSize string, start, end time.Time) ([]*Candle, error) {
	resolution, err := candleResolution(binSize)
	if err != nil {
		return nil, err
	}
	native, err := b.resolveSymbol(symbol)
	if err != nil {
		return nil, err
	}
	snapshot, err := b.rest.Candles.HistoryWithQuery(
		native,
		resolution,
		bfxmodel.Mts(start.UnixMilli()),
		bfxmodel.Mts(end.UnixMilli()),
		bfxmodel.QueryLimit(b.klineLimit),
		bfxmodel.OldestFirst,
	)
	if err != nil {
		if isEmptySnapshotError(err) {
			return nil, nil
		}
		if isRateLimitError(err) {
			return nil, fmt.Errorf("%w: %v", exchange.ErrRetry, err)
		}
		return nil, err
	}

	now := time.Now()
	data := make([]*Candle, 0, len(snapshot.Snapshot))
	for _, item := range snapshot.Snapshot {
		if item == nil || !isClosedCandle(item.MTS, binSize, now) {
			continue
		}
		data = append(data, transCandle(item))
	}
	sort.Slice(data, func(i, j int) bool { return data[i].Start < data[j].Start })
	return data, nil
}

func (b *Client) Watch(param exchange.WatchParam, fn exchange.WatchFn) error {
	if fn == nil {
		return errors.New("bitfinex watch callback cannot be nil")
	}
	switch param.Type {
	case exchange.WatchTypeTrade:
		if !b.hasCredentials() {
			return errors.New("bitfinex trade watch requires API credentials")
		}
		b.mu.Lock()
		b.tradeCb = fn
		b.mu.Unlock()
		return b.ensureWebsocket()
	case exchange.WatchTypePosition:
		if !b.hasCredentials() {
			return errors.New("bitfinex position watch requires API credentials")
		}
		b.mu.Lock()
		b.positionCb = fn
		b.mu.Unlock()
		if err := b.fetchBalanceAndPosition(); err != nil {
			return err
		}
		return b.ensureWebsocket()
	case exchange.WatchTypeBalance:
		if !b.hasCredentials() {
			return errors.New("bitfinex balance watch requires API credentials")
		}
		b.mu.Lock()
		b.balanceCb = fn
		b.mu.Unlock()
		if err := b.fetchBalanceAndPosition(); err != nil {
			return err
		}
		return b.ensureWebsocket()
	}

	if err := b.ensureWebsocket(); err != nil {
		return err
	}
	native, err := b.resolveSymbol(param.Param["symbol"])
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	switch param.Type {
	case exchange.WatchTypeCandle:
		binSize := param.Param["bin"]
		if binSize == "" {
			binSize = "1m"
		}
		resolution, err := candleResolution(binSize)
		if err != nil {
			return err
		}
		key := candleKey(native, resolution)
		b.mu.Lock()
		b.candleCallbacks[key] = fn
		_, exists := b.subscriptions["candle:"+key]
		b.mu.Unlock()
		if exists {
			return nil
		}
		id, err := b.ws.SubscribeCandles(ctx, native, resolution)
		if err != nil {
			return err
		}
		b.storeSubscription("candle:"+key, id)
		return nil
	case exchange.WatchTypeDepth:
		b.mu.Lock()
		b.depthCallbacks[native] = fn
		_, exists := b.subscriptions["depth:"+native]
		b.mu.Unlock()
		if exists {
			return nil
		}
		id, err := b.ws.SubscribeBook(ctx, native, bfxmodel.Precision0, bfxmodel.FrequencyRealtime, 25)
		if err != nil {
			return err
		}
		b.storeSubscription("depth:"+native, id)
		return nil
	case exchange.WatchTypeTradeMarket:
		b.mu.Lock()
		b.tradeMarketCallbacks[native] = fn
		_, exists := b.subscriptions["trade:"+native]
		b.mu.Unlock()
		if exists {
			return nil
		}
		id, err := b.ws.SubscribeTrades(ctx, native)
		if err != nil {
			return err
		}
		b.storeSubscription("trade:"+native, id)
		return nil
	default:
		return fmt.Errorf("unknown watch param: %s", param.Type)
	}
}

func (b *Client) ProcessOrder(action TradeAction) (*Order, error) {
	if !b.hasCredentials() {
		return nil, errors.New("bitfinex order submission requires API credentials")
	}
	native, err := b.resolveSymbol(action.Symbol)
	if err != nil {
		return nil, err
	}
	request := b.newOrderRequest(action)
	request.Symbol = native
	result, err := b.rest.Orders.SubmitOrder(request)
	if err != nil {
		return nil, err
	}
	return orderFromNotification(result)
}

func (b *Client) CancelOrder(old *Order) (*Order, error) {
	if !b.hasCredentials() {
		return nil, errors.New("bitfinex order cancellation requires API credentials")
	}
	if old == nil {
		return nil, errors.New("bitfinex cancel order cannot use a nil order")
	}
	if old.Symbol != "" {
		if _, err := b.resolveSymbol(old.Symbol); err != nil {
			return nil, err
		}
	}
	id, err := strconv.ParseInt(old.OrderID, 10, 64)
	if err != nil {
		return nil, err
	}
	current, currentErr := b.rest.Orders.GetByOrderId(id)
	if currentErr != nil && !errors.Is(currentErr, bfxmodel.ErrNotFound) && !isEmptySnapshotError(currentErr) {
		return nil, currentErr
	}
	if current != nil && !b.matchesKind(current.Symbol) {
		return nil, fmt.Errorf("bitfinex %s client cannot cancel order for symbol %s", b.kind, current.Symbol)
	}
	if err = b.rest.Orders.SubmitCancelOrder(&bfxorder.CancelRequest{ID: id}); err != nil {
		return nil, err
	}
	if current != nil {
		ret := transOrder(current)
		ret.Status = OrderStatusCanceled
		ret.Time = time.Now()
		return ret, nil
	}
	ret := *old
	ret.Status = OrderStatusCanceled
	ret.Time = time.Now()
	return &ret, nil
}

func (b *Client) CancelAllOrders() ([]*Order, error) {
	if !b.hasCredentials() {
		return nil, errors.New("bitfinex order cancellation requires API credentials")
	}
	snapshot, err := b.rest.Orders.All()
	if err != nil {
		if isEmptySnapshotError(err) {
			return nil, nil
		}
		return nil, err
	}
	orders := make([]*Order, 0, len(snapshot.Snapshot))
	ids := make(rest.OrderIDs, 0, len(snapshot.Snapshot))
	for _, item := range snapshot.Snapshot {
		if item == nil || !b.matchesKind(item.Symbol) {
			continue
		}
		orders = append(orders, transOrder(item))
		ids = append(ids, int(item.ID))
	}
	if len(ids) == 0 {
		return orders, nil
	}
	if _, err = b.rest.Orders.CancelOrderMulti(rest.CancelOrderMultiRequest{OrderIDs: ids}); err != nil {
		return nil, err
	}
	for _, item := range orders {
		item.Status = OrderStatusCanceled
	}
	return orders, nil
}

func (b *Client) newOrderRequest(action TradeAction) *bfxorder.NewRequest {
	amount := roundDecimals(math.Abs(action.Amount), amountPrecision)
	if !action.Action.IsLong() {
		amount = -amount
	}
	orderType := bfxmodel.OrderTypeLimit
	if action.Action&Market == Market {
		orderType = bfxmodel.OrderTypeMarket
	} else if action.Action.IsStop() {
		orderType = bfxmodel.OrderTypeStop
	}
	if b.kind == KindSpot {
		switch orderType {
		case bfxmodel.OrderTypeMarket:
			orderType = bfxmodel.OrderTypeExchangeMarket
		case bfxmodel.OrderTypeStop:
			orderType = bfxmodel.OrderTypeExchangeStop
		default:
			orderType = bfxmodel.OrderTypeExchangeLimit
		}
	}
	price := roundSignificant(action.Price, pricePrecision)
	if action.Action&Market == Market {
		price = 0
	}
	request := &bfxorder.NewRequest{
		CID:    b.cid.Add(1),
		Type:   string(orderType),
		Symbol: b.normalizeSymbol(action.Symbol),
		Amount: amount,
		Price:  price,
		// Close flag not work,just remove it
		// Close:  b.kind == KindFutures && !action.Action.IsOpen(),
	}
	if b.kind == KindFutures {
		request.Leverage = b.cfg.Leverage
	}
	return request
}

func (b *Client) fetchBalanceAndPosition() error {
	wallets, err := b.rest.Wallet.Wallet()
	if err != nil && !isEmptySnapshotError(err) {
		return err
	}
	if wallets != nil {
		b.handleWalletSnapshot(wallets)
	}
	if b.kind != KindFutures {
		return nil
	}
	positions, err := b.rest.Positions.All()
	if err != nil {
		if isEmptySnapshotError(err) {
			return nil
		}
		return err
	}
	b.handlePositionSnapshot(positions)
	return nil
}

func (b *Client) consumeWebsocket() {
	for message := range b.ws.Listen() {
		switch value := message.(type) {
		case error:
			log.Errorf("bitfinex websocket error: %v", value)
		case *candle.Snapshot:
			b.handleCandleSnapshot(value)
		case *candle.Candle:
			b.handleCandle(value)
		case *book.Snapshot, *book.Book:
			b.handleBook(message)
		case *trade.Trade:
			b.handleMarketTrade(value)
		case *wallet.Snapshot:
			b.handleWalletSnapshot(value)
		case *wallet.Update:
			item := wallet.Wallet(*value)
			b.handleWallet(&item)
		case *bfxposition.Snapshot:
			b.handlePositionSnapshot(value)
		case *bfxposition.New:
			item := bfxposition.Position(*value)
			b.handlePosition(&item)
		case *bfxposition.Update:
			item := bfxposition.Position(*value)
			b.handlePosition(&item)
		case *bfxposition.Cancel:
			item := bfxposition.Position(*value)
			item.Amount = 0
			b.handlePosition(&item)
		case *bfxorder.New:
			item := bfxorder.Order(*value)
			b.emitOrder(&item)
		case *bfxorder.Update:
			item := bfxorder.Order(*value)
			b.emitOrder(&item)
		case *bfxorder.Cancel:
			item := bfxorder.Order(*value)
			b.emitOrder(&item)
		}
	}
}

func (b *Client) handleCandleSnapshot(snapshot *candle.Snapshot) {
	if snapshot == nil || len(snapshot.Snapshot) == 0 {
		return
	}
	latest := snapshot.Snapshot[0]
	for _, item := range snapshot.Snapshot[1:] {
		if item != nil && (latest == nil || item.MTS > latest.MTS) {
			latest = item
		}
	}
	if latest == nil {
		return
	}
	key := candleKey(latest.Symbol, latest.Resolution)
	b.mu.Lock()
	copyValue := *latest
	b.candleLatest[key] = &copyValue
	b.mu.Unlock()
}

func (b *Client) handleCandle(item *candle.Candle) {
	if item == nil {
		return
	}
	key := candleKey(item.Symbol, item.Resolution)
	var closed *candle.Candle
	b.mu.Lock()
	previous := b.candleLatest[key]
	copyValue := *item
	if previous != nil && item.MTS > previous.MTS {
		previousCopy := *previous
		closed = &previousCopy
	}
	if previous == nil || item.MTS >= previous.MTS {
		b.candleLatest[key] = &copyValue
	}
	callback := b.candleCallbacks[key]
	b.mu.Unlock()
	if closed != nil && callback != nil {
		callback(transCandle(closed))
	}
}

func (b *Client) handleBook(value interface{}) {
	var symbol string
	switch item := value.(type) {
	case *book.Snapshot:
		if item != nil && len(item.Snapshot) > 0 && item.Snapshot[0] != nil {
			symbol = item.Snapshot[0].Symbol
		}
	case *book.Book:
		if item != nil {
			symbol = item.Symbol
		}
	}
	if symbol == "" {
		return
	}
	b.mu.RLock()
	callback := b.depthCallbacks[symbol]
	b.mu.RUnlock()
	if callback == nil {
		return
	}
	orderbook, err := b.ws.GetOrderbook(symbol)
	if err != nil {
		log.Warnf("bitfinex get orderbook %s: %v", symbol, err)
		return
	}
	depth := &Depth{UpdateTime: time.Now()}
	for _, item := range orderbook.Asks() {
		depth.Sells = append(depth.Sells, DepthInfo{Price: item.Price, Amount: math.Abs(item.Amount)})
	}
	for _, item := range orderbook.Bids() {
		depth.Buys = append(depth.Buys, DepthInfo{Price: item.Price, Amount: math.Abs(item.Amount)})
	}
	callback(depth)
}

func (b *Client) handleMarketTrade(item *trade.Trade) {
	if item == nil {
		return
	}
	b.mu.RLock()
	callback := b.tradeMarketCallbacks[item.Pair]
	b.mu.RUnlock()
	if callback == nil {
		return
	}
	side := "buy"
	if item.Amount < 0 {
		side = "sell"
	}
	callback(&Trade{
		ID:     strconv.FormatInt(item.ID, 10),
		Time:   time.UnixMilli(item.MTS),
		Price:  item.Price,
		Amount: math.Abs(item.Amount),
		Side:   side,
		Remark: item.Pair,
	})
}

func (b *Client) handleWalletSnapshot(snapshot *wallet.Snapshot) {
	if snapshot == nil {
		return
	}
	for _, item := range snapshot.Snapshot {
		b.handleWallet(item)
	}
}

func (b *Client) handleWallet(item *wallet.Wallet) {
	if item == nil || item.Type != b.walletType() {
		return
	}
	currency := normalizeCurrency(item.Currency)
	if currency == b.currency {
		available := item.BalanceAvailable
		balance := &Balance{
			Currency:  b.currency,
			Balance:   item.Balance,
			Available: available,
			Frozen:    math.Max(0, item.Balance-available),
		}
		b.mu.RLock()
		callback := b.balanceCb
		b.mu.RUnlock()
		if callback != nil {
			callback(balance)
		}
		return
	}
	if b.kind != KindSpot {
		return
	}
	b.mu.RLock()
	symbol := b.pairByAsset[currency]
	callback := b.positionCb
	b.mu.RUnlock()
	if symbol == "" || callback == nil {
		return
	}
	callback(&Position{Symbol: symbol, Hold: item.Balance})
}

func (b *Client) handlePositionSnapshot(snapshot *bfxposition.Snapshot) {
	if snapshot == nil {
		return
	}
	for _, item := range snapshot.Snapshot {
		b.handlePosition(item)
	}
}

func (b *Client) handlePosition(item *bfxposition.Position) {
	if item == nil || b.kind != KindFutures || !isDerivativeSymbol(item.Symbol) {
		return
	}
	position := &Position{
		Symbol:      normalizeNativeSymbol(item.Symbol),
		Hold:        item.Amount,
		Price:       item.BasePrice,
		ProfitRatio: item.ProfitLossPercentage,
	}
	if item.Amount > 0 {
		position.Type = Long
	} else if item.Amount < 0 {
		position.Type = Short
	}
	b.mu.RLock()
	callback := b.positionCb
	b.mu.RUnlock()
	if callback != nil {
		callback(position)
	}
}

func (b *Client) emitOrder(item *bfxorder.Order) {
	if item == nil || !b.matchesKind(item.Symbol) {
		return
	}
	b.mu.RLock()
	callback := b.tradeCb
	b.mu.RUnlock()
	if callback != nil {
		callback(transOrder(item))
	}
}

func (b *Client) storeSubscription(key, id string) {
	b.mu.Lock()
	b.subscriptions[key] = id
	b.mu.Unlock()
}

func (b *Client) walletType() string {
	if b.kind == KindSpot {
		return "exchange"
	}
	return "margin"
}

func (b *Client) matchesKind(symbol string) bool {
	return isDerivativeSymbol(symbol) == (b.kind == KindFutures)
}

func (b *Client) normalizeSymbol(symbol string) string {
	value := strings.ToUpper(strings.TrimSpace(symbol))
	value = strings.TrimPrefix(value, "T")
	value = strings.ReplaceAll(value, "USDTF0", "USTF0")
	if b.kind == KindFutures {
		if isDerivativePair(value) {
			return "t" + value
		}
		value = normalizeQuoteSuffix(value, b.currency)
		base := strings.TrimSuffix(value, b.currency)
		if base != value && base != "" {
			return "t" + base + "F0:" + b.currency + "F0"
		}
		return "t" + value
	}
	value = normalizeQuoteSuffix(value, b.currency)
	return "t" + value
}

func (b *Client) resolveSymbol(symbol string) (string, error) {
	raw := strings.ToUpper(strings.TrimSpace(symbol))
	if b.kind == KindFutures && strings.HasPrefix(raw, "T") && !isDerivativeSymbol(raw) {
		return "", fmt.Errorf("bitfinex futures client cannot use spot symbol %s", symbol)
	}
	native := b.normalizeSymbol(symbol)
	if native == "t" {
		return "", errors.New("bitfinex symbol cannot be empty")
	}
	if !b.matchesKind(native) {
		return "", fmt.Errorf("bitfinex %s client cannot use symbol %s", b.kind, native)
	}
	return native, nil
}

func (b *Client) hasCredentials() bool {
	return b.cfg.Key != "" && b.cfg.Secret != ""
}

func transCandle(item *candle.Candle) *Candle {
	return &Candle{
		Start:  item.MTS / 1000,
		Open:   item.Open,
		High:   item.High,
		Low:    item.Low,
		Close:  item.Close,
		Volume: item.Volume,
	}
}

func transOrder(item *bfxorder.Order) *Order {
	amount := math.Abs(item.AmountOrig)
	remaining := math.Abs(item.Amount)
	filled := math.Max(0, amount-remaining)
	price := item.Price
	if item.PriceAvg > 0 {
		price = item.PriceAvg
	}
	side := "buy"
	if item.AmountOrig < 0 {
		side = "sell"
	}
	return &Order{
		OrderID:  strconv.FormatInt(item.ID, 10),
		Symbol:   normalizeNativeSymbol(item.Symbol),
		Currency: normalizeNativeSymbol(item.Symbol),
		Amount:   amount,
		Filled:   filled,
		Price:    price,
		Status:   normalizeOrderStatus(item.Status),
		Side:     side,
		Time:     time.UnixMilli(item.MTSUpdated),
	}
}

func orderFromNotification(result *notification.Notification) (*Order, error) {
	if result == nil {
		return nil, errors.New("bitfinex returned an empty order notification")
	}
	if !strings.EqualFold(result.Status, "SUCCESS") {
		return nil, fmt.Errorf("bitfinex order rejected: %s (%d)", result.Text, result.Code)
	}
	switch item := result.NotifyInfo.(type) {
	case bfxorder.New:
		orderValue := bfxorder.Order(item)
		return transOrder(&orderValue), nil
	case *bfxorder.New:
		orderValue := bfxorder.Order(*item)
		return transOrder(&orderValue), nil
	case *bfxorder.Snapshot:
		if len(item.Snapshot) > 0 {
			return transOrder(item.Snapshot[0]), nil
		}
	}
	return nil, fmt.Errorf("bitfinex order notification did not contain an order: %T", result.NotifyInfo)
}

func normalizeOrderStatus(status string) string {
	status = strings.ToUpper(strings.TrimSpace(status))
	switch {
	case strings.HasPrefix(status, bfxmodel.OrderStatusExecuted):
		return OrderStatusFilled
	case strings.HasPrefix(status, bfxmodel.OrderStatusCanceled):
		return OrderStatusCanceled
	case strings.HasPrefix(status, bfxmodel.OrderStatusPartiallyFilled):
		return bfxmodel.OrderStatusPartiallyFilled
	case strings.HasPrefix(status, bfxmodel.OrderStatusActive):
		return "NEW"
	default:
		return status
	}
}

func pairsFromRaw(raw []interface{}) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	values, ok := raw[0].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected bitfinex pair list: %#v", raw)
	}
	pairs := make([]string, 0, len(values))
	for _, value := range values {
		pair, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected bitfinex pair: %#v", value)
		}
		pairs = append(pairs, strings.ToUpper(pair))
	}
	return pairs, nil
}

func pairAssets(pair, expectedQuote string) (base, quote string, ok bool) {
	pair = strings.TrimPrefix(strings.ToUpper(pair), "T")
	if strings.Contains(pair, ":") {
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			return "", "", false
		}
		return strings.TrimSuffix(parts[0], "F0"), strings.TrimSuffix(parts[1], "F0"), true
	}
	if expectedQuote != "" && strings.HasSuffix(pair, expectedQuote) {
		return strings.TrimSuffix(pair, expectedQuote), expectedQuote, true
	}
	return "", "", false
}

func isDerivativeSymbol(symbol string) bool {
	return isDerivativePair(strings.TrimPrefix(strings.ToUpper(symbol), "T"))
}

func isDerivativePair(pair string) bool {
	pair = strings.TrimPrefix(strings.ToUpper(pair), "T")
	parts := strings.SplitN(pair, ":", 2)
	return len(parts) == 2 && strings.HasSuffix(parts[0], "F0") && strings.HasSuffix(parts[1], "F0")
}

func normalizeNativeSymbol(symbol string) string {
	value := strings.ToUpper(strings.TrimSpace(symbol))
	value = strings.TrimPrefix(value, "T")
	return "t" + value
}

func normalizeCurrency(currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "USDT" {
		return "UST"
	}
	return currency
}

func normalizeQuoteSuffix(symbol, quote string) string {
	if quote == "UST" && strings.HasSuffix(symbol, "USDT") {
		return strings.TrimSuffix(symbol, "USDT") + "UST"
	}
	return symbol
}

func candleResolution(binSize string) (bfxmodel.CandleResolution, error) {
	switch binSize {
	case "1d":
		binSize = "1D"
	case "1w", "7d":
		binSize = "7D"
	case "14d":
		binSize = "14D"
	}
	return bfxmodel.CandleResolutionFromString(binSize)
}

func candleKey(symbol string, resolution bfxmodel.CandleResolution) string {
	return normalizeNativeSymbol(symbol) + ":" + string(resolution)
}

func isClosedCandle(mts int64, binSize string, now time.Time) bool {
	start := time.UnixMilli(mts)
	if binSize == "1M" {
		return !start.AddDate(0, 1, 0).After(now)
	}
	durations := map[string]time.Duration{
		"1m": time.Minute, "5m": 5 * time.Minute, "15m": 15 * time.Minute,
		"30m": 30 * time.Minute, "1h": time.Hour, "3h": 3 * time.Hour,
		"6h": 6 * time.Hour, "12h": 12 * time.Hour, "1d": 24 * time.Hour,
		"1D": 24 * time.Hour, "1w": 7 * 24 * time.Hour, "7d": 7 * 24 * time.Hour,
		"14d": 14 * 24 * time.Hour,
	}
	duration, ok := durations[binSize]
	return !ok || !start.Add(duration).After(now)
}

func roundSignificant(value float64, digits int) float64 {
	if value == 0 || digits <= 0 {
		return value
	}
	shift := float64(digits-1) - math.Floor(math.Log10(math.Abs(value)))
	factor := math.Pow(10, shift)
	return math.Round(value*factor) / factor
}

func roundDecimals(value float64, digits int) float64 {
	factor := math.Pow10(digits)
	return math.Round(value*factor) / factor
}

func isEmptySnapshotError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "data slice too short") || strings.Contains(message, "not an order snapshot")
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "rate limit") || strings.Contains(message, "too many requests") || strings.Contains(message, " 429 ")
}

func parseProxy(proxy string) (*url.URL, error) {
	if strings.TrimSpace(proxy) == "" {
		return nil, nil
	}
	parsed, err := url.Parse(proxy)
	if err != nil {
		return nil, fmt.Errorf("parse bitfinex proxy: %w", err)
	}
	return parsed, nil
}

func maxDuration(left, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}
