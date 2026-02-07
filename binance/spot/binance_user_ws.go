package spot

import (
	"context"
	"fmt"
	"strconv"
	"time"

	gobinance "github.com/adshao/go-binance/v2"
	log "github.com/sirupsen/logrus"
	. "github.com/ztrade/trademodel"
)

func (b *BinanceSpot) updateUserListenKey() {
	var err error
	ticker := time.NewTicker(time.Minute * 30)
Out:
	for {
		select {
		case <-b.closeCh:
			break Out
		case <-ticker.C:
			for i := 0; i < 10; i++ {
				ctx, cancel := context.WithTimeout(background, b.timeout)
				err = b.api.NewKeepaliveUserStreamService().ListenKey(b.wsUserListenKey).Do(ctx)
				cancel()
				if err != nil {
					log.Error("update listen key failed:", err.Error())
					time.Sleep(time.Minute)
					continue
				}
				break
			}
			if err != nil {
				log.Error("update listen key failed 10 times, just exit:", err.Error())
				break Out
			}
		}
	}
}

func (b *BinanceSpot) startUserWS() (err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()

	listenKey, err := b.api.NewStartUserStreamService().Do(ctx)
	if err != nil {
		return
	}
	b.wsUserListenKey = listenKey

	doneC, stopC, err := gobinance.WsUserDataServe(listenKey, b.handleUserData, b.handleUserDataError)
	if err != nil {
		return
	}
	go func() {
		select {
		case <-doneC:
		case <-b.closeCh:
		}
		close(stopC)
	}()
	go b.updateUserListenKey()

	return
}

func (b *BinanceSpot) handleUserData(event *gobinance.WsUserDataEvent) {
	switch event.Event {
	case gobinance.UserDataEventTypeOutboundAccountPosition:
		var balance Balance
		for _, v := range event.AccountUpdate.WsAccountUpdates {
			if v.Asset == b.baseCurrency {
				balance.Currency = v.Asset
				balance.Balance = parseFloat(v.Free)
				balance.Available = parseFloat(v.Free)
				if b.balanceCb != nil {
					b.balanceCb(&balance)
				}
				// total = balance.Balance
			} else {
				var pos Position
				pos.Symbol = fmt.Sprintf("%s%s", v.Asset, b.baseCurrency)
				pos.Hold, _ = strconv.ParseFloat(v.Free, 64)
				freeze, _ := strconv.ParseFloat(v.Locked, 64)
				pos.Hold += freeze
				if b.positionCb != nil {
					b.positionCb(&pos)
				}
			}
		}
	case gobinance.UserDataEventTypeExecutionReport:
		var order Order
		order.OrderID = strconv.FormatInt(event.OrderUpdate.Id, 10)
		order.Symbol = event.OrderUpdate.Symbol
		order.Currency = event.OrderUpdate.Symbol

		order.Amount = parseFloat(event.OrderUpdate.Volume)
		order.Filled = parseFloat(event.OrderUpdate.FilledVolume)
		if order.Filled > 0 {
			order.Price = parseFloat(event.OrderUpdate.FilledQuoteVolume) / order.Filled
		} else {
			order.Price = 0
		}
		order.Status = string(event.OrderUpdate.Status)
		order.Side = string(event.OrderUpdate.Side)
		order.Time = time.UnixMilli(event.OrderUpdate.TransactionTime)
		if b.tradeCb != nil {
			b.tradeCb(&order)
		}
	}
}

func (b *BinanceSpot) handleUserDataError(err error) {
	log.Errorf("binance userdata error: %s,reconnect", err.Error())
	doneC, stopC, err := gobinance.WsUserDataServe(b.wsUserListenKey, b.handleUserData, b.handleUserDataError)
	if err != nil {
		return
	}
	go func() {
		select {
		case <-doneC:
		case <-b.closeCh:
		}
		close(stopC)
	}()
}
