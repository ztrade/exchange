# exchange
An easy to use exchange package.

It is mainly used for ztrade.

## Bitfinex

The single Bitfinex adapter supports spot and derivatives trading through the
`kind` field:

```yaml
exchanges:
  bitfinex_spot:
    type: bitfinex
    kind: spot
    key: your-api-key
    secret: your-api-secret
    currency: USDT
    timeout: 10s

  bitfinex_futures:
    type: bitfinex
    kind: futures
    key: your-api-key
    secret: your-api-secret
    currency: USDT
    leverage: 5
    timeout: 10s
```

The adapter returns Bitfinex v2 native symbols. For example, `tBTCUST` is a
spot symbol and `tBTCF0:USTF0` is a derivatives symbol. Order and market-data
methods also accept symbols without the leading `t`; `USDT` aliases are mapped
to Bitfinex's native `UST` code.

Bitfinex paper-trading accounts use the production API endpoints. Setting
`is_test: true` therefore emits a warning but does not switch endpoints. For a
mock or private endpoint, configure `rest_url` and `ws_url` explicitly.

### Bitfinex live integration tests

The live tests are protected by both an `integration` build tag and environment
switches. They are skipped during normal `go test` runs.

Public REST and websocket tests:

```bash
BITFINEX_INTEGRATION=1 \
go test -tags=integration -run 'TestBitfinexIntegration(PublicREST|StartStop|WatchMarketData)$' -v ./bitfinex
```

The candle websocket test waits for a completed one-minute candle:

```bash
BITFINEX_INTEGRATION=1 BITFINEX_ENABLE_CANDLE_WATCH=1 \
go test -tags=integration -run TestBitfinexIntegrationWatchCandle -v ./bitfinex
```

Authenticated balance and position test:

```bash
BITFINEX_INTEGRATION=1 \
BITFINEX_API_KEY=your-key \
BITFINEX_API_SECRET=your-secret \
BITFINEX_PRIVATE_KIND=spot \
go test -tags=integration -run TestBitfinexIntegrationAccount -v ./bitfinex
```

Order tests should use a paper-trading account. No default order quantity is
provided, so `BITFINEX_ORDER_AMOUNT` must be explicitly set. The test submits a
buy limit order at half the recent market price and immediately cancels it:

```bash
BITFINEX_INTEGRATION=1 \
BITFINEX_ENABLE_ORDER_TESTS=1 \
BITFINEX_API_KEY=your-paper-key \
BITFINEX_API_SECRET=your-paper-secret \
BITFINEX_ORDER_KIND=spot \
BITFINEX_ORDER_AMOUNT=0.0001 \
go test -tags=integration -run TestBitfinexIntegrationOrderLifecycle -v ./bitfinex
```

`TestBitfinexIntegrationCancelAllOrders` cancels every active order of the
selected market kind, including orders not created by the test. It additionally
requires `BITFINEX_ENABLE_CANCEL_ALL_TEST=YES_I_UNDERSTAND`.

Useful overrides include `BITFINEX_CURRENCY`, `BITFINEX_SPOT_SYMBOL`,
`BITFINEX_FUTURES_SYMBOL`, `BITFINEX_PROXY`, `BITFINEX_TIMEOUT`,
`BITFINEX_REST_URL`, and `BITFINEX_WS_URL`.

## Futu

The Futu adapter talks to a local FutuOpenD process through the
[github.com/hyperjiang/futu](https://github.com/hyperjiang/futu) SDK, so no
API key or secret is required. Quote data is free; trading uses the account
selected from the account list and can unlock the trade password set in
FutuOpenD.

```yaml
exchanges:
  futu:
    type: futu
    addr: 127.0.0.1:11111   # FutuOpenD 地址，默认 :11111
    trd_env: simulate       # real 或 simulate，留空则只使用行情
    market: US              # HK / US / SH / SZ / SG / JP
    acc_id: 1619199         # 业务账号，留空则按 market 自动选择
    unlock_trade: true      # 交易前是否解锁
    pwd_md5: md5-of-trade-password
    security_firm: 0        # 券商类型：0 未知 / 1 富途证券(香港) / 2 富途(美国)
    timeout: 10s
    symbols:
      - HK.00700
      - US.AAPL
    plates:
      - HK.LIST1059         # 板块代码，自动展开成符号
    sec_type: eqty          # 仅当 symbols/plates 都为空时按整市场拉取
    kline_limit: 1000
```

Symbols use the `MARKET.CODE` format (e.g. `HK.00700`, `US.AAPL`). A symbol
without a market prefix is normalized with the configured `market`, and HK
codes are zero padded to five digits. `Symbols()` is populated from
`symbols`, or from `plates`, or from the whole configured `market` when both
lists are empty.

Historical k-lines use the online history K-line API with pagination and fall
back to the recent-window K-line API for recent ranges. `Watch` subscriptions
go through `QotSub` on the same connection: candles (only closed bars are
emitted), order book, market tickers, and trading pushes for order/position/
balance updates.

`trd_env` defaults to `simulate`; set `trd_env: real` for live trading.

### Futu live integration tests

The live tests are protected by an `integration` build tag and environment
switches, so they are skipped during normal `go test` runs. They require a
running FutuOpenD and the market data / trade permissions of the logged-in
account.

Public market data tests:

```bash
FUTU_INTEGRATION=1 \
go test -tags=integration -run 'TestFutuIntegration(PublicREST|HistoryKL|StartStop|WatchMarketData)$' -v ./futu
```

The candle test waits for a completed one-minute candle (the market must be
open):

```bash
FUTU_INTEGRATION=1 FUTU_ENABLE_CANDLE_WATCH=1 \
go test -tags=integration -run TestFutuIntegrationWatchCandle -v ./futu
```

Account balance/position test:

```bash
FUTU_INTEGRATION=1 FUTU_ENABLE_TRADE_TESTS=1 \
FUTU_TRD_ENV=simulate FUTU_MARKET=US FUTU_ACC_ID=1619199 \
go test -tags=integration -run TestFutuIntegrationAccount -v ./futu
```

Order tests place a buy limit order at half the recent price and immediately
cancel it, so it will not fill. Simulation accounts are recommended:

```bash
FUTU_INTEGRATION=1 FUTU_ENABLE_TRADE_TESTS=1 FUTU_ENABLE_ORDER_TESTS=1 \
FUTU_TRD_ENV=simulate FUTU_MARKET=US FUTU_SYMBOL=US.AAPL FUTU_ORDER_AMOUNT=1 \
go test -tags=integration -run TestFutuIntegrationOrderLifecycle -v ./futu
```

`TestFutuIntegrationCancelAllOrders` cancels every active order of the
account and additionally requires `FUTU_ENABLE_CANCEL_ALL_TEST=YES_I_UNDERSTAND`.

Useful overrides include `FUTU_ADDR`, `FUTU_SYMBOLS`, `FUTU_PLATES`,
`FUTU_PWD_MD5`, `FUTU_SECURITY_FIRM`, `FUTU_UNLOCK_TRADE`,
`FUTU_ORDER_PRICE_FACTOR`, `FUTU_TIMEOUT`, `FUTU_WATCH_TIMEOUT`,
`FUTU_ACCOUNT_TIMEOUT`, `FUTU_ORDER_EVENT_TIMEOUT`, and
`FUTU_CANDLE_WATCH_TIMEOUT`.
