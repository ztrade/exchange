package exchange

import (
	"errors"
	"sort"
	"time"

	"github.com/sirupsen/logrus"
	. "github.com/ztrade/trademodel"
)

const (
	WatchTypeCandle      = "candle"
	WatchTypeDepth       = "depth"
	WatchTypeTradeMarket = "trade_market"
	WatchTypeTrade       = "trade"
	WatchTypePosition    = "position"
	WatchTypeBalance     = "balance"
)

var (
	ErrRetry = errors.New("need retry")
)

type WatchParam struct {
	Type  string
	Param map[string]string
}

func WatchCandle(symbol, binSize string) WatchParam {
	return WatchParam{
		Type:  WatchTypeCandle,
		Param: map[string]string{"symbol": symbol, "bin": binSize},
	}
}

type WatchFn func(interface{})

type FetchLimit struct {
	Duration string
	Limit    int
}

// ExxchangeInfo exchange info
type ExchangeInfo struct {
	Name       string
	Value      string
	Desc       string
	KLineLimit FetchLimit
	OrderLimit FetchLimit
}

type Exchange interface {
	Info() ExchangeInfo
	Symbols() ([]Symbol, error)

	Start() error
	Stop() error

	// Watch exchange event, call multiple times for different event
	Watch(WatchParam, WatchFn) error

	// Kline get klines
	GetKline(symbol, bSize string, start, end time.Time) (data []*Candle, err error)

	// for trade
	ProcessOrder(act TradeAction) (ret *Order, err error)
	CancelAllOrders() (orders []*Order, err error)
	CancelOrder(old *Order) (orders *Order, err error)
}

func KlineChan(e Exchange, symbol, bSize string, start, end time.Time) (dataCh chan *Candle, errCh chan error) {
	dataCh = make(chan *Candle, 1024*10)
	errCh = make(chan error, 1)
	go func() {
		defer func() {
			close(dataCh)
			close(errCh)
		}()
		tStart := start
		tEnd := end
		var nPrevStart int64
		for {
			klines, err := e.GetKline(symbol, bSize, tStart, tEnd)
			if err != nil {
				if errors.Is(err, ErrRetry) {
					time.Sleep(time.Second * 2)
					continue
				}
				errCh <- err
				return
			}
			sort.Slice(klines, func(i, j int) bool {
				return klines[i].Start < klines[j].Start
			})
			for _, v := range klines {
				if v.Start <= nPrevStart {
					continue
				}
				dataCh <- v
				tStart = v.Time()
			}
			if tStart.Sub(tEnd) >= 0 || tStart.Unix() <= nPrevStart || len(klines) == 0 {
				logrus.Info("KlineChan finished: [%s]-[%s], last datatime: %s", start, end, tStart)
				break
			}
			nPrevStart = tStart.Unix()
		}
	}()
	return
}
