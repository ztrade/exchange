package okx

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/ztrade/exchange/okx/api/market"
	. "github.com/ztrade/trademodel"
)

func transCandle(values [9]string) (ret *Candle) {
	nTs, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		log.Errorf("transCandle parse timestamp error: %#v, %s", values, err.Error())
		return nil
	}
	ret = &Candle{
		ID:       0,
		Start:    nTs / 1000,
		Open:     parseFloat(values[1]),
		High:     parseFloat(values[2]),
		Low:      parseFloat(values[3]),
		Close:    parseFloat(values[4]),
		Volume:   parseFloat(values[5]),
		Turnover: parseFloat(values[7]),
	}
	return
}

func parseFloat(str string) float64 {
	if str == "" {
		return 0
	}
	f, err := strconv.ParseFloat(str, 64)
	if err != nil {
		log.Errorf("okex parseFloat error: %s, input: %s", err.Error(), str)
		return 0
	}
	return f
}

func parseCandles(resp *market.GetApiV5MarketHistoryCandlesResponse) (candles []*Candle, err error) {
	var candleResp CandleResp
	err = json.Unmarshal(resp.Body, &candleResp)
	if err != nil {
		return
	}
	if candleResp.Code != "0" {
		err = errors.New(string(resp.Body))
		return
	}
	for _, v := range candleResp.Data {
		// unfinished candle
		if v[8] == "0" {
			continue
		}
		temp := transCandle(v)
		candles = append(candles, temp)
	}
	return
}

func parsePostOrders(symbol, status, side string, amount, price float64, body []byte) (ret []*Order, err error) {
	temp := OKEXOrder{}
	err = json.Unmarshal(body, &temp)
	if err != nil {
		return
	}
	if temp.Code != "0" {
		err = fmt.Errorf("error resp: %s", string(body))
		return
	}
	for _, v := range temp.Data {
		if v.SCode != "0" {
			err = fmt.Errorf("%s %s", v.SCode, v.SMsg)
			return
		}

		temp := &Order{
			OrderID: v.OrdID,
			Symbol:  symbol,
			// Currency
			Side:   side,
			Status: status,
			Price:  price,
			Amount: amount,
			Time:   time.Now(),
		}
		ret = append(ret, temp)
	}
	return
}

func parsePostAlgoOrders(symbol, status, side string, amount, price float64, body []byte) (ret []*Order, err error) {
	temp := OKEXAlgoOrder{}
	err = json.Unmarshal(body, &temp)
	if err != nil {
		return
	}
	if temp.Code != "0" {
		err = fmt.Errorf("error resp: %s", string(body))
		return
	}
	for _, v := range temp.Data {
		if v.SCode != "0" {
			err = fmt.Errorf("%s %s", v.SCode, v.SMsg)
			return
		}

		temp := &Order{
			OrderID: v.AlgoID,
			Symbol:  symbol,
			// Currency
			Side:   side,
			Status: status,
			Price:  price,
			Amount: amount,
			Time:   time.Now(),
		}
		ret = append(ret, temp)
	}
	return
}
