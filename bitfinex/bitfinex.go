package bitfinex

import (
	"fmt"
	"strings"

	"github.com/ztrade/exchange"
)

func init() {
	exchange.RegisterExchange("bitfinex", NewBitfinex)
}

func NewBitfinex(cfg exchange.Config, cltName string) (exchange.Exchange, error) {
	var bCfg BitfinexConfig
	if err := cfg.UnmarshalKey(fmt.Sprintf("exchanges.%s", cltName), &bCfg); err != nil {
		return nil, err
	}

	proxy := cfg.GetString("proxy")
	switch strings.ToLower(bCfg.Kind) {
	case "spot":
		return NewClient(bCfg, proxy, KindSpot)
	case "futures", "future", "derivative", "derivatives", "contract", "swap":
		return NewClient(bCfg, proxy, KindFutures)
	default:
		return nil, fmt.Errorf("bitfinex unsupported kind %q", bCfg.Kind)
	}
}
