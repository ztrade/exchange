package futu

import "time"

// FutuConfig is the configuration for the Futu (富途) adapter.
//
// The adapter talks to a local FutuOpenD process over TCP, so no API key or
// secret is required. Quote data is free to use, while trading requires the
// account to be unlocked in FutuOpenD (trade password) before placing orders.
type FutuConfig struct {
	Type string `mapstructure:"type"`
	// Addr is the FutuOpenD address, e.g. "127.0.0.1:11111". Defaults to ":11111".
	Addr string `mapstructure:"addr"`
	// Timeout for each request. Defaults to 10s.
	Timeout time.Duration `mapstructure:"timeout"`
	// TrdEnv selects the trading environment: "real" or "simulate".
	// Leave empty when only market data is needed.
	TrdEnv string `mapstructure:"trd_env"`
	// Market is the default market: HK / US / SH / SZ / SG / JP.
	// Used to normalize symbols without a market prefix, and for account
	// selection when AccID is not set.
	Market string `mapstructure:"market"`
	// AccID is the Futu business account id. When empty the first account of
	// the selected market and environment is used.
	AccID uint64 `mapstructure:"acc_id"`
	// PwdMD5 is the MD5 of the trade password. Required when UnlockTrade is true.
	PwdMD5 string `mapstructure:"pwd_md5"`
	// SecurityFirm is the broker type, see Trd_Common.SecurityFirm.
	// 0 unknown, 1 FutuSecurities(HK), 2 FutuInc(US), 3 FutuSG, 4 FutuAU.
	SecurityFirm int32 `mapstructure:"security_firm"`
	// UnlockTrade unlocks the trade account on Start when the trade password
	// has been set in FutuOpenD.
	UnlockTrade bool `mapstructure:"unlock_trade"`
	// Symbols is the explicit symbol list, e.g. ["HK.00700", "US.AAPL"].
	// Symbols without a market prefix use the configured Market.
	Symbols []string `mapstructure:"symbols"`
	// Plates is the plate list to expand into symbols, e.g. ["HK.ALL"].
	Plates []string `mapstructure:"plates"`
	// SecType filters the market-wide symbol query: eqty / index / future /
	// warrant / drvt / bond / trust / plate / plate_set / forex / crypto.
	// Defaults to eqty. Only used when Symbols and Plates are both empty.
	SecType string `mapstructure:"sec_type"`
	// KLineLimit is the max number of k-lines fetched per request.
	// Defaults to 1000.
	KLineLimit int `mapstructure:"kline_limit"`
	// Currency overrides the default balance currency derived from Market.
	Currency string `mapstructure:"currency"`
	// Resolutions overrides the supported k-line periods reported by Symbols.
	Resolutions string `mapstructure:"resolutions"`
}
