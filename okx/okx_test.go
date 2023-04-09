package okx

import (
	"fmt"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/ztrade/exchange"
	"github.com/ztrade/trademodel"
)

var (
	testClt *OkxTrader
)

func getTestClt() *OkxTrader {
	cfgPath := "../test/test.yaml"
	cfg := viper.New()
	cfg.SetConfigFile(cfgPath)
	err := cfg.ReadInConfig()
	if err != nil {
		log.Fatal("ReadInConfig failed:" + err.Error())
	}
	testClt, err = NewOkxTrader(exchange.WrapViper(cfg), "okx")
	if err != nil {
		log.Fatal("create client failed:" + err.Error())
	}
	testClt.Start()
	return testClt
}

func TestMain(m *testing.M) {
	testClt = getTestClt()
	m.Run()
}

func TestSymbols(t *testing.T) {
	symbols, err := testClt.Symbols()
	if err != nil {
		t.Fatal(err.Error())
	}
	for _, v := range symbols {
		t.Log(v.Symbol, v.Precision, v.PriceStep, v.AmountPrecision, v.AmountStep)
	}
}

func TestKline(t *testing.T) {
	// start, _ := time.Parse("2006-01-02 15:04:05", "2023-01-01 00:00:00")
	start := time.Now().Add(time.Minute * -5)
	end := start.Add(time.Hour + time.Second)
	datas, err := testClt.GetKline("BTC-USDT-SWAP", "1m", start, end)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range datas {
		t.Log(v)
	}
	if len(datas) != 60 {
		t.Fatal("Kline resp not match:", len(datas))
	}
}

func TestOrder(t *testing.T) {
	order, err := testClt.ProcessOrder(trademodel.TradeAction{
		Symbol: "APT-USDT-SWAP",
		Action: trademodel.OpenShort,
		Amount: 1,
		Price:  20,
		Time:   time.Now(),
	})
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log(*order)
	time.Sleep(time.Second)
	_, err = testClt.CancelAllOrders()
	if err != nil {
		t.Fatal(err.Error())
	}
}

func TestStopOrder(t *testing.T) {
	order, err := testClt.ProcessOrder(trademodel.TradeAction{
		Symbol: "APT-USDT-SWAP",
		Action: trademodel.StopShort,
		Amount: 1,
		Price:  17,
		Time:   time.Now(),
	})
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log(*order)
	time.Sleep(time.Second)
	_, err = testClt.CancelAllOrders()
	if err != nil {
		t.Fatal(err.Error())
	}
}

func TestCancelAllOrder(t *testing.T) {
	_, err := testClt.CancelAllOrders()
	if err != nil {
		t.Fatal(err.Error())
	}
}

func TestCancelOrder(t *testing.T) {
	act := trademodel.TradeAction{
		Action: trademodel.OpenLong,
		Amount: 1,
		Price:  2000,
		Time:   time.Now(),
	}
	order, err := testClt.ProcessOrder(act)
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log(*order)
	time.Sleep(time.Second * 5)
	ret, err := testClt.CancelOrder(order)
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log("ret:", *ret)
}

func TestCancelStopOrder(t *testing.T) {
	act := trademodel.TradeAction{
		Action: trademodel.StopLong,
		Amount: 1,
		Price:  2000,
		Time:   time.Now(),
	}
	order, err := testClt.ProcessOrder(act)
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log(*order)
	time.Sleep(time.Second * 5)
	ret, err := testClt.CancelOrder(order)
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log("ret:", *ret)
}

func TestDepth(t *testing.T) {
	param := exchange.WatchParam{Type: exchange.WatchTypeDepth, Param: map[string]string{"symbol": "ETH-USDT-SWAP", "name": "depth"}}
	testClt.Watch(param, func(data interface{}) {
		fmt.Println(data)
	})
	time.Sleep(time.Second * 10)
}
