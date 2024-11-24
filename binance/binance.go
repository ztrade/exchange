package binance

import (
	"fmt"

	"github.com/ztrade/exchange"
	bcommon "github.com/ztrade/exchange/binance/common"
	"github.com/ztrade/exchange/binance/futures"
)

func init() {
	exchange.RegisterExchange("binance", NewBinance)
}

func NewBinance(cfg exchange.Config, cltName string) (e exchange.Exchange, err error) {
	var eCfg bcommon.BinanceConfig
	err = cfg.UnmarshalKey(fmt.Sprintf("exchanges.%s", cltName), &eCfg)
	if err != nil {
		return
	}
	clientProxy := cfg.GetString("proxy")
	switch eCfg.Kind {
	case "futures":
		e, err = futures.NewBinanceTrader(eCfg, cltName, clientProxy)
	default:
		err = fmt.Errorf("binance unsupport kind %s", eCfg.Kind)
	}
	return
}
