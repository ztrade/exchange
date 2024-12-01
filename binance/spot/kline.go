package spot

import (
	gobinance "github.com/adshao/go-binance/v2"
	"github.com/ztrade/exchange"
	. "github.com/ztrade/trademodel"
)

func processWsCandle(doneC chan struct{}, cb exchange.WatchFn) func(event *gobinance.WsKlineEvent) {
	return func(event *gobinance.WsKlineEvent) {
		select {
		case <-doneC:
			return
		default:
		}
		if event.Kline.IsFinal {
			candle := Candle{
				ID:       0,
				Start:    event.Kline.StartTime / 1000,
				Open:     parseFloat(event.Kline.Open),
				High:     parseFloat(event.Kline.High),
				Low:      parseFloat(event.Kline.Low),
				Close:    parseFloat(event.Kline.Close),
				Turnover: parseFloat(event.Kline.QuoteVolume),
				Volume:   parseFloat(event.Kline.Volume),
				Trades:   event.Kline.TradeNum,
			}
			cb(&candle)
		}
	}
}
