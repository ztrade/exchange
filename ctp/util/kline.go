package util

import (
	"fmt"
	"time"

	"github.com/lunny/log"
	"github.com/ztrade/ctp"
	"github.com/ztrade/trademodel"
)

type CTPKline struct {
	cur          *trademodel.Candle
	prevVolume   int64
	prevTurnover float64
	cb           func(*trademodel.Candle)
	bFirst       bool
}

func NewCTPKline() *CTPKline {
	k := new(CTPKline)
	k.bFirst = true
	return k
}

func (k *CTPKline) SetCb(cb func(*trademodel.Candle)) {
	k.cb = cb
}

func (k *CTPKline) Update(data *ctp.CThostFtdcDepthMarketDataField) (candle *trademodel.Candle) {
	defer func() {
		k.prevTurnover = data.Turnover
		k.prevVolume = int64(data.Volume)

		if candle != nil && k.cb != nil {
			// 第一根K线可能不全，所以忽略
			if k.bFirst {
				k.bFirst = false
				return
			}
			k.cb(candle)
		}
	}()
	now := time.Now()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	date := now.Format("20060102")
	timeStr := fmt.Sprintf("%s %s.%03d", date, data.UpdateTime, data.UpdateMillisec)
	tm, err := time.ParseInLocation("20060102 15:04:05.000", timeStr, loc)
	if err != nil {
		log.Errorf("CtpExchange Parse MarketData timestamp %s failed %s", timeStr, err.Error())
	}

	price := data.LastPrice
	tStart := (tm.Unix() / 60) * 60
	// 第一条记录不知道prevVolume和prevTurnover，无法计算这两个值
	if k.cur == nil {
		k.cur = &trademodel.Candle{
			Start:    tStart,
			Open:     price,
			High:     price,
			Low:      price,
			Close:    price,
			Volume:   0,
			Turnover: 0,
		}
		return
	}

	volume := data.Volume - int(k.prevVolume)
	turnover := data.Turnover - k.prevTurnover
	if tm.Sub(k.cur.Time()) >= time.Minute {
		candle = k.cur
		k.cur = &trademodel.Candle{
			Start:    tStart,
			Open:     price,
			High:     price,
			Low:      price,
			Close:    price,
			Volume:   float64(volume),
			Turnover: turnover,
		}
		return
	}
	k.cur.Close = price
	k.cur.Volume += float64(volume)
	k.cur.Turnover += turnover

	if price > k.cur.High {
		k.cur.High = price
	}
	if price < k.cur.Low {
		k.cur.Low = price
	}
	return
}
