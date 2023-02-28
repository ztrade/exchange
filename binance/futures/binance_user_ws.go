package futures

import (
	"context"
	"strconv"
	"time"

	bfutures "github.com/adshao/go-binance/v2/futures"
	log "github.com/sirupsen/logrus"
	. "github.com/ztrade/trademodel"
)

func (b *BinanceTrade) updateUserListenKey() {
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
					continue
				}
				break
			}
			if err != nil {
				log.Error("update listen key failed 10 times,just exist::", err.Error())
				break Out
			}
		}
	}
}

func (b *BinanceTrade) startUserWS() (err error) {
	ctx, cancel := context.WithTimeout(background, b.timeout)
	defer cancel()

	listenKey, err := b.api.NewStartUserStreamService().Do(ctx)
	if err != nil {
		return
	}
	b.wsUserListenKey = listenKey

	doneC, stopC, err := bfutures.WsUserDataServe(listenKey, b.handleUserData, b.handleUserDataError)
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

func (b *BinanceTrade) handleUserData(event *bfutures.WsUserDataEvent) {
	switch event.Event {
	case bfutures.UserDataEventTypeAccountUpdate:
		var profit, total float64
		var balance Balance
		for _, v := range event.AccountUpdate.Balances {
			if v.Asset == b.baseCurrency {
				balance.Balance = parseFloat(v.Balance)
				balance.Available = parseFloat(v.CrossWalletBalance)
				if b.balanceCb != nil {
					b.balanceCb(&balance)
				}
				total = balance.Balance
			}
		}
		var pos Position
		for _, v := range event.AccountUpdate.Positions {
			pos.Symbol = v.Symbol
			profit = parseFloat(v.UnrealizedPnL)
			if v.IsolatedWallet != "" {
				total = parseFloat(v.IsolatedWallet)
			}
			if total > 0 {
				pos.ProfitRatio = profit / total
			}
			pos.Price = parseFloat(v.EntryPrice)
			pos.Hold = parseFloat(v.Amount)
			if pos.Hold > 0 {
				pos.Type = Long
			} else if pos.Hold < 0 {
				pos.Type = Short
			}
			if b.positionCb != nil {
				b.positionCb(&pos)
			}
		}
	case bfutures.UserDataEventTypeOrderTradeUpdate:
		var order Order
		order.OrderID = strconv.FormatInt(event.OrderTradeUpdate.ID, 10)
		order.Symbol = event.OrderTradeUpdate.Symbol
		order.Currency = event.OrderTradeUpdate.Symbol

		order.Amount = parseFloat(event.OrderTradeUpdate.OriginalQty)
		order.Filled = parseFloat(event.OrderTradeUpdate.AccumulatedFilledQty)
		if order.Filled == order.Amount {
			order.Price = parseFloat(event.OrderTradeUpdate.AveragePrice)
		} else {
			order.Price = parseFloat(event.OrderTradeUpdate.OriginalPrice)
		}
		order.Status = string(event.OrderTradeUpdate.Status)
		order.Side = string(event.OrderTradeUpdate.Side)
		order.Time = time.UnixMilli(event.OrderTradeUpdate.TradeTime)
		if b.tradeCb != nil {
			b.tradeCb(&order)
		}
	}
}

func (b *BinanceTrade) handleUserDataError(err error) {
	log.Errorf("binance userdata error: %s,reconnect", err.Error())
	doneC, stopC, err := bfutures.WsUserDataServe(b.wsUserListenKey, b.handleUserData, b.handleUserDataError)
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
