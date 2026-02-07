package futures

import (
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/ztrade/exchange"
	"github.com/ztrade/exchange/binance/common"
	"github.com/ztrade/trademodel"
)

var (
	testClt *BinanceTrade
)

func getTestClt() *BinanceTrade {
	cfg := common.BinanceConfig{
		Type:   "binance",
		Kind:   "futures",
		Key:    os.Getenv("BINANCE_API_KEY"),
		Secret: os.Getenv("BINANCE_API_SECRET"),
		// IsTest: true,
	}
	var err error
	testClt, err = NewBinanceTrader(cfg, "binance", "")

	if err != nil {
		log.Fatal("create client failed:" + err.Error())
	}
	return testClt
}

func TestMain(m *testing.M) {
	testClt = getTestClt()
	m.Run()
}

func TestKline(t *testing.T) {
	end := time.Now()
	start := end.Add(0 - time.Hour)
	datas, err := testClt.GetKline("BTCUSDT", "1m", start, end)
	if err != nil {
		t.Fatal(err.Error())
	}
	for _, v := range datas {
		_ = v
		// t.Logf("%v", v)
		// fmt.Printf("%v\n", v)
	}

	t.Log("total:", len(datas))

}

func TestKlineChan(t *testing.T) {
	end := time.Now()
	start := end.Add(0 - 48*time.Hour)
	dataCh, errCh := exchange.KlineChan(testClt, "BTCUSDT", "1m", start, end)
	var n int
	var prevStart int64
	for v := range dataCh {
		n++
		if prevStart != 0 {
			if (v.Start - prevStart) != 60 {
				t.Fatal("klineChan error")
			}
		}
		prevStart = v.Start
		// t.Logf("%v", v)
		// fmt.Printf("%v\n", v)
	}
	err := <-errCh
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log("total:", n)
}

func TestWSUser(t *testing.T) {
	testClt.Watch(exchange.WatchParam{Type: exchange.WatchTypeBalance}, func(data interface{}) {
		balance, ok := data.(*trademodel.Balance)
		if !ok {
			t.Fatalf("watch balance type error: %##v", data)
		}
		fmt.Println("balance:", *balance)
	})
	testClt.Watch(exchange.WatchParam{Type: exchange.WatchTypePosition}, func(data interface{}) {
		pos, ok := data.(*trademodel.Position)
		if !ok {
			t.Fatalf("watch position type error: %##v", data)
		}
		fmt.Println("position:", *pos)
	})
	time.Sleep(time.Minute * 3)
}

func getLastCandle(symbol string, t *testing.T) *trademodel.Candle {
	end := time.Now()
	start := end.Add(0 - 5*time.Minute)
	datas, err := testClt.GetKline(symbol, "1m", start, end)
	if err != nil {
		t.Fatal(err)
	}
	return datas[0]
}

func TestProcessOrder(t *testing.T) {
	//last := getLastCandle("SUIUSDT", t)
	act := trademodel.TradeAction{
		Action: trademodel.OpenLong,
		Amount: 5,
		Price:  3.6,
		Symbol: "SUIUSDT",
		Time:   time.Now(),
	}
	ret, err := testClt.ProcessOrder(act)
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log(*ret)
}

func TestProcessStopOrder(t *testing.T) {
	last := getLastCandle("ETHUSDT", t)
	act := trademodel.TradeAction{
		Action: trademodel.OpenLong,
		Amount: 1,
		Price:  last.Low * 1.01,
		Symbol: "ETHUSDT",
		Time:   time.Now(),
	}
	ret, err := testClt.ProcessOrder(act)
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log(*ret)
	act = trademodel.TradeAction{
		Action: trademodel.StopLong,
		Amount: 1,
		Price:  last.Low * 0.9,
		Symbol: "ETHUSDT",
		Time:   time.Now(),
	}
	ret, err = testClt.ProcessOrder(act)
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log("stop order:", act.Price, *ret)
}

func TestCancelAllOrders(t *testing.T) {
	orders, err := testClt.CancelAllOrders()
	if err != nil {
		t.Fatal(err.Error())
	}
	for _, v := range orders {
		t.Log("cancel:", v)
	}
}

func TestTradeList(t *testing.T) {
	orders, err := testClt.api.NewListAccountTradeService().Do(t.Context())
	if err != nil {
		t.Fatal(err.Error())
	}
	for _, v := range orders {
		t.Log("cancel:", v)
	}
}

func TestWatchKline(t *testing.T) {
	var n int
	testClt.Watch(exchange.WatchCandle("ETHUSDT", "1m"), func(data interface{}) {
		v, ok := data.(*trademodel.Candle)
		if !ok {
			t.Fatalf("watch candle type not match: %##v", data)
		}
		t.Log("candle:", *v)
		n++
	})
	<-time.After(time.Minute * 3)
	t.Log("total:", n)

}

func TestCancelOrder(t *testing.T) {
	last := getLastCandle("ETHUSDT", t)
	act := trademodel.TradeAction{
		Action: trademodel.OpenLong,
		Amount: 1,
		Price:  last.Low * 0.9,
		Symbol: "ETHUSDT",
		Time:   time.Now(),
	}
	ret, err := testClt.ProcessOrder(act)
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log("create order:", *ret)
	newOrder, err := testClt.CancelOrder(ret)
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log("new order:", *newOrder)
}

func TestSymbols(t *testing.T) {
	symbols, err := testClt.Symbols()
	if err != nil {
		t.Fatal(err.Error())
	}
	for _, v := range symbols {
		t.Log(v)
	}
}

// func TestWatchCandle(t *testing.T) {
// symbol := core.SymbolInfo{Symbol: "ETHUSDT", Resolutions: "1m"}
// datas, stopC, err := testClt.Watch(exchange.WatchParam{Symbol: "ETHUSDT", Param: CandleParam{}})
// 	go func() {
// 		<-time.After(time.Minute * 3)
// 		stopC <- struct{}{}
// 	}()
// 	var n int
// 	for v := range datas {
// 		n++
// 		_ = v
// 		// t.Logf("%v", v)
// 		fmt.Printf("%v\n", v)
// 	}
// 	if err != nil {
// 		t.Fatal(err.Error())
// 	}
// 	t.Log("total:", n)

// }
