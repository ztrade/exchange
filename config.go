package exchange

import "github.com/spf13/viper"

type Config interface {
	Get(string) interface{}
	GetBool(string) bool
	GetInt(string) int
	GetString(string) string
	UnmarshalKey(string, interface{}) error
}

type ExchangeConfig struct {
	Type   string
	Key    string
	Secret string
	IsTest bool
}

func WrapViper(cfg *viper.Viper) Config {
	return &ViperCfg{Viper: cfg}
}

type ViperCfg struct {
	*viper.Viper
}

func (c *ViperCfg) UnmarshalKey(key string, rawVal interface{}) error {
	return c.Viper.UnmarshalKey(key, rawVal)
}
