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
