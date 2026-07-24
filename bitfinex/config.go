package bitfinex

import "time"

type Kind string

const (
	KindSpot    Kind = "spot"
	KindFutures Kind = "futures"
)

type BitfinexConfig struct {
	Type     string        `mapstructure:"type"`
	Key      string        `mapstructure:"key"`
	Secret   string        `mapstructure:"secret"`
	IsTest   bool          `mapstructure:"is_test"`
	Kind     string        `mapstructure:"kind"`
	Currency string        `mapstructure:"currency"`
	Timeout  time.Duration `mapstructure:"timeout"`
	Leverage int64         `mapstructure:"leverage"`
	RESTURL  string        `mapstructure:"rest_url"`
	WSURL    string        `mapstructure:"ws_url"`
}
