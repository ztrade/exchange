package exchange

import (
	"fmt"
	"sync"

	"github.com/spf13/viper"
)

var (
	exchangeFactory      = map[string]NewExchangeFn{}
	exchangeFactoryMutex sync.Mutex

	exchangeMutex sync.Mutex
	exchanges     = map[string]Exchange{}
)

type NewExchangeFn func(cfg Config, cltName string) (t Exchange, err error)

func RegisterExchange(name string, fn NewExchangeFn) {
	exchangeFactoryMutex.Lock()
	defer exchangeFactoryMutex.Unlock()
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
	exchangeFactoryMutex.Lock()
	fn, ok := exchangeFactory[name]
	exchangeFactoryMutex.Unlock()
	if !ok {
		err = fmt.Errorf("no such exchange %s", name)
		return
	}
	ex, err = fn(cfg, cltName)
	return
}

func NewExchangeViper(name, cltName string) (ex Exchange, err error) {
	cfg := WrapViper(viper.GetViper())
	return NewExchange(name, cfg, cltName)
}
