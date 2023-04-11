package ctp

import (
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/ztrade/exchange"
	. "github.com/ztrade/trademodel"
)

var (
	testClt *CtpExchange
)

func getTestClt() *CtpExchange {
	cfgPath := "../test/test.yaml"
	cfg := viper.New()
	cfg.SetConfigFile(cfgPath)
	err := cfg.ReadInConfig()
	if err != nil {
		log.Fatal("ReadInConfig failed:" + err.Error())
	}
	testClt, err = NewCtpExchange(exchange.WrapViper(cfg), "ctp")
	if err != nil {
		log.Fatal("create client failed:" + err.Error())
	}
	// testClt.Start()
	return testClt
}

func TestMain(m *testing.M) {
	testClt = getTestClt()
	m.Run()
}

func TestOrder(t *testing.T) {
	var act TradeAction
	act.Action = CloseShort
	act.Price = 2769
	act.Amount = 1
	act.Symbol = "DCE.c2307"
	ret, err := testClt.ProcessOrder(act)
	if err != nil {
		t.Fatal(err.Error())
	}
	logrus.Warn("order data:", ret)
	time.Sleep(time.Second * 20)
	ret, err = testClt.CancelOrder(ret)
	if err != nil {
		t.Fatal()
	}
	logrus.Warn("cancel order:", ret)
	time.Sleep(time.Minute * 2)
}

func TestMarketData(t *testing.T) {
	testClt.Watch(exchange.WatchParam{Type: exchange.WatchTypeDepth, Param: map[string]string{"symbol": "DCE.c2307"}}, func(value interface{}) {
		dep := value.(*Depth)
		t.Log("depth:", dep.Buys, dep.Sells)
	})
	time.Sleep(time.Minute)
}

func TestMarketTrade(t *testing.T) {
	testClt.Watch(exchange.WatchParam{Type: exchange.WatchTypeTradeMarket, Param: map[string]string{"symbol": "DCE.c2307"}}, func(value interface{}) {
		tr := value.(*Trade)
		t.Log("trade:", *tr)
	})
	time.Sleep(time.Minute)
}

func TestBalance(t *testing.T) {
	testClt.Watch(exchange.WatchParam{Type: exchange.WatchTypeBalance, Param: map[string]string{}}, func(value interface{}) {
		bal := value.(*Balance)
		t.Log("balance:", *bal)
	})
	time.Sleep(time.Minute)
}

func TestPos(t *testing.T) {
	testClt.Watch(exchange.WatchParam{Type: exchange.WatchTypePosition, Param: map[string]string{}}, func(value interface{}) {
		pos := value.(*Position)
		t.Log("pos:", *pos)
	})
	time.Sleep(time.Minute)
}

func TestSymbols(t *testing.T) {
	symbols, err := testClt.Symbols()
	if err != nil {
		t.Fatal(err.Error())
	}
	for _, v := range symbols {
		t.Log(v.Symbol, v.Precision, v.AmountPrecision, v.PriceStep, v.AmountStep)
	}
}

func TestKline(t *testing.T) {
	testClt.Watch(exchange.WatchParam{Type: exchange.WatchTypeCandle, Param: map[string]string{"symbol": "DCE.c2307", "bin": "1m"}}, func(value interface{}) {
		candle := value.(*Candle)
		fmt.Println("Candle:", *candle)
	})
	time.Sleep(time.Minute * 2)
}

func TestGetKline(t *testing.T) {
	start, _ := time.Parse(time.RFC3339, "2023-02-27T09:00:00+08:00")
	end, _ := time.Parse(time.RFC3339, "2023-02-27T16:00:00+08:00")
	candles, err := testClt.GetKline("DCE.c2307", "1m", start, end)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range candles {
		t.Log(v)
	}
	t.Log(len(candles))
}
