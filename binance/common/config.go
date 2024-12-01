package common

import "time"

type BinanceConfig struct {
	Type     string
	Key      string
	Secret   string
	IsTest   bool
	Kind     string
	Currency string
	Timeout  time.Duration
}
