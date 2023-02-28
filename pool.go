package exchange

import (
	"fmt"
	"sync"
)

var (
	exchangeFactory = map[string]NewExchangeFn{}

	exchangeMutex sync.Mutex
	exchanges     = map[string]Exchange{}
)

type NewExchangeFn func(cfg Config, cltName string) (t Exchange, err error)

func RegisterExchange(name string, fn NewExchangeFn) {
	exchangeFactory[name] = fn
}

func NewExchange(name string, cfg Config, cltName string) (ex Exchange, err error) {
	exchangeMutex.Lock()
	defer exchangeMutex.Unlock()
	if cfg.GetBool("share_exchange") {
		v, ok := exchanges[cltName]
		if ok {
			ex = v
			return
		}
		defer func() {
			if err == nil {
				exchanges[cltName] = ex
			}
		}()
	}
	fn, ok := exchangeFactory[name]
	if !ok {
		err = fmt.Errorf("no such exchange %s", name)
		return
	}
	ex, err = fn(cfg, cltName)
	return
}
