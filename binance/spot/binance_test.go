package spot

import (
	"log"
	"testing"
	"time"

	"github.com/ztrade/exchange"
	"github.com/ztrade/exchange/binance/common"
	"github.com/ztrade/trademodel"
)

var (
	testSpotClt *BinanceSpot
)

func getTestSpotClt() *BinanceSpot {
	cfg := common.BinanceConfig{
		Type:    "binance",
		Kind:    "futures",
		Key:     "adf",
		Secret:  "asf",
		IsTest:  true,
		Timeout: 30 * time.Second,
	}
	var err error
	testSpotClt, err = NewBinanceSpot(cfg, "binance_spot", "")

	if err != nil {
		log.Fatal("create client failed:" + err.Error())
	}
	return testSpotClt
}

func TestMain(m *testing.M) {
	getTestSpotClt()
	m.Run()
}

func TestSpotProcessOrder(t *testing.T) {
	act := trademodel.TradeAction{
		Action: trademodel.OpenLong,
		Amount: 0.01,
		Price:  19000,
		Time:   time.Now(),
		Symbol: "BTCUSDT",
	}
	ret, err := testSpotClt.ProcessOrder(act)
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log(*ret)
}

func TestSpotProcessOrderStop(t *testing.T) {
	act := trademodel.TradeAction{
		Action: trademodel.StopLong,
		Amount: 0.001,
		Price:  20410.45,
		Time:   time.Now(),
		Symbol: "BTCUSDT",
	}
	ret, err := testSpotClt.ProcessOrder(act)
	if err != nil {
		t.Fatal(err.Error())
	}
	t.Log(*ret)
}

func TestSpotCancelAllOrders(t *testing.T) {
	orders, err := testSpotClt.CancelAllOrders()
	if err != nil {
		t.Fatal(err.Error())
	}
	for _, v := range orders {
		t.Log(v)
	}
}

func TestSpotBalance(t *testing.T) {
	testSpotClt.Watch(exchange.WatchParam{Type: exchange.WatchTypeBalance}, func(data interface{}) {
		balance, ok := data.(*trademodel.Balance)
		if !ok {
			t.Fatalf("watch balance type error: %##v", data)
		}
		t.Log("balance:", *balance)
	})
	testSpotClt.Watch(exchange.WatchParam{Type: exchange.WatchTypePosition}, func(data interface{}) {
		pos, ok := data.(*trademodel.Position)
		if !ok {
			t.Fatalf("watch balance type error: %##v", data)
		}
		t.Log("position:", *pos)
	})
	// t.Log("balance:", v.Name, v.Symbol, bl.Currency, bl.Available, bl.Balance, bl.Frozen)
	// t.Log("pos:", v.Name, v.Symbol, pos.Symbol, pos.Hold, pos.Price)
}

func TestSymbols(t *testing.T) {
	symbols, err := testSpotClt.Symbols()
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
