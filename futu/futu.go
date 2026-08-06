package futu

import (
	"fmt"

	futusdk "github.com/hyperjiang/futu"
	futuclient "github.com/hyperjiang/futu/client"
	"github.com/ztrade/exchange"
)

func init() {
	exchange.RegisterExchange("futu", NewFutu)
}

// NewFutu creates a new Futu exchange adapter from the exchange config.
func NewFutu(cfg exchange.Config, cltName string) (exchange.Exchange, error) {
	var fCfg FutuConfig
	if err := cfg.UnmarshalKey(fmt.Sprintf("exchanges.%s", cltName), &fCfg); err != nil {
		return nil, err
	}
	return NewClient(fCfg)
}

// newSDK connects to FutuOpenD. It is a variable so tests can replace it.
var newSDK = func(cfg FutuConfig) (futuSDK, error) {
	addr := cfg.Addr
	if addr == "" {
		addr = ":11111"
	}
	sdk, err := futusdk.NewSDK(
		futuclient.WithAddr(addr),
		futuclient.WithID("ztrade-exchange"),
		futuclient.WithResChanSize(256),
	)
	if err != nil {
		return nil, err
	}
	return sdk, nil
}
