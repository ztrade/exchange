package futu

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hyperjiang/futu/adapt"
	"github.com/hyperjiang/futu/pb/qotcommon"
	"github.com/hyperjiang/futu/pb/trdcommon"
	. "github.com/ztrade/trademodel"
)

var qotMarketIDs = map[string]int32{
	"HK": adapt.QotMarket_HK,
	"US": adapt.QotMarket_US,
	"SH": adapt.QotMarket_SH,
	"SZ": adapt.QotMarket_SZ,
	"SG": adapt.QotMarket_SG,
	"JP": adapt.QotMarket_JP,
}

var trdMarketIDs = map[string]int32{
	"HK": adapt.TrdMarket_HK,
	"US": adapt.TrdMarket_US,
	"SH": adapt.TrdMarket_HKCC,
	"SZ": adapt.TrdMarket_HKCC,
	"SG": adapt.TrdMarket_SG,
	"JP": int32(trdcommon.TrdMarket_TrdMarket_JP),
}

var marketCurrencies = map[string]string{
	"HK": "HKD",
	"US": "USD",
	"SH": "CNY",
	"SZ": "CNY",
	"SG": "SGD",
	"JP": "JPY",
}

var secMarketNames = map[int32]string{
	int32(trdcommon.TrdSecMarket_TrdSecMarket_HK):    "HK",
	int32(trdcommon.TrdSecMarket_TrdSecMarket_US):    "US",
	int32(trdcommon.TrdSecMarket_TrdSecMarket_CN_SH): "SH",
	int32(trdcommon.TrdSecMarket_TrdSecMarket_CN_SZ): "SZ",
	int32(trdcommon.TrdSecMarket_TrdSecMarket_SG):    "SG",
	int32(trdcommon.TrdSecMarket_TrdSecMarket_JP):    "JP",
	int32(trdcommon.TrdSecMarket_TrdSecMarket_AU):    "AU",
	int32(trdcommon.TrdSecMarket_TrdSecMarket_MY):    "MY",
	int32(trdcommon.TrdSecMarket_TrdSecMarket_CA):    "CA",
	int32(trdcommon.TrdSecMarket_TrdSecMarket_CC):    "CC",
}

// klineTypes maps bin sizes to Futu subscription types and k-line types.
var klineTypes = map[string]struct {
	subType int32
	klType  int32
}{
	"1m":   {adapt.SubType_KL_1Min, adapt.KLType_1Min},
	"3m":   {adapt.SubType_KL_3Min, adapt.KLType_3Min},
	"5m":   {adapt.SubType_KL_5Min, adapt.KLType_5Min},
	"10m":  {adapt.SubType_KL_10Min, adapt.KLType_10Min},
	"15m":  {adapt.SubType_KL_15Min, adapt.KLType_15Min},
	"30m":  {adapt.SubType_KL_30Min, adapt.KLType_30Min},
	"1h":   {adapt.SubType_KL_60Min, adapt.KLType_60Min},
	"60m":  {adapt.SubType_KL_60Min, adapt.KLType_60Min},
	"2h":   {adapt.SubType_KL_120Min, adapt.KLType_120Min},
	"120m": {adapt.SubType_KL_120Min, adapt.KLType_120Min},
	"3h":   {adapt.SubType_KL_180Min, adapt.KLType_180Min},
	"180m": {adapt.SubType_KL_180Min, adapt.KLType_180Min},
	"4h":   {adapt.SubType_KL_240Min, adapt.KLType_240Min},
	"240m": {adapt.SubType_KL_240Min, adapt.KLType_240Min},
	"1d":   {adapt.SubType_KL_Day, adapt.KLType_Day},
	"1w":   {adapt.SubType_KL_Week, adapt.KLType_Week},
	"1M":   {adapt.SubType_KL_Month, adapt.KLType_Month},
	"1mo":  {adapt.SubType_KL_Month, adapt.KLType_Month},
	"1q":   {adapt.SubType_KL_Qurater, adapt.KLType_Quarter},
	"1y":   {adapt.SubType_KL_Year, adapt.KLType_Year},
}

var klineTypeNames = map[int32]string{
	adapt.KLType_1Min:    "1m",
	adapt.KLType_3Min:    "3m",
	adapt.KLType_5Min:    "5m",
	adapt.KLType_10Min:   "10m",
	adapt.KLType_15Min:   "15m",
	adapt.KLType_30Min:   "30m",
	adapt.KLType_60Min:   "1h",
	adapt.KLType_120Min:  "2h",
	adapt.KLType_180Min:  "3h",
	adapt.KLType_240Min:  "4h",
	adapt.KLType_Day:     "1d",
	adapt.KLType_Week:    "1w",
	adapt.KLType_Month:   "1M",
	adapt.KLType_Quarter: "1q",
	adapt.KLType_Year:    "1y",
}

// normalizeMarkets validates and deduplicates the market list, keeping the
// configured order.
func normalizeMarkets(markets []string) ([]string, error) {
	seen := make(map[string]bool)
	out := make([]string, 0, len(markets))
	for _, market := range markets {
		market = strings.ToUpper(strings.TrimSpace(market))
		if market == "" {
			continue
		}
		if _, ok := qotMarketIDs[market]; !ok {
			return nil, fmt.Errorf("futu unsupported market %q, want HK/US/SH/SZ/SG/JP", market)
		}
		if !seen[market] {
			seen[market] = true
			out = append(out, market)
		}
	}
	return out, nil
}

func qotMarketID(market string) int32 {
	return qotMarketIDs[strings.ToUpper(market)]
}

func trdMarketID(market string) int32 {
	return trdMarketIDs[strings.ToUpper(market)]
}

func securityTypeID(secType string) (int32, error) {
	switch strings.ToLower(strings.TrimSpace(secType)) {
	case "", "eqty", "stock", "equity":
		return adapt.SecurityType_Eqty, nil
	case "index":
		return adapt.SecurityType_Index, nil
	case "future", "futures":
		return adapt.SecurityType_Future, nil
	case "warrant", "bwrt":
		return adapt.SecurityType_Warrant, nil
	case "drvt", "option":
		return adapt.SecurityType_Drvt, nil
	case "bond":
		return adapt.SecurityType_Bond, nil
	case "trust":
		return adapt.SecurityType_Trust, nil
	case "plate":
		return adapt.SecurityType_Plate, nil
	case "plate_set":
		return adapt.SecurityType_PlateSet, nil
	case "forex":
		return adapt.SecurityType_Forex, nil
	case "crypto":
		return adapt.SecurityType_Crypto, nil
	default:
		return 0, fmt.Errorf("futu unsupported sec_type %q", secType)
	}
}

func symbolFromStaticInfo(info *qotcommon.SecurityStaticInfo, resolutions string) Symbol {
	basic := info.GetBasic()
	sym := Symbol{
		Name:            basic.GetName(),
		Symbol:          adapt.SecurityToCode(basic.GetSecurity()),
		Exchange:        "futu",
		Type:            symbolType(basic.GetSecType()),
		Precision:       defaultPrecision,
		AmountPrecision: 0,
		AmountStep:      float64(basic.GetLotSize()),
		Resolutions:     resolutions,
	}
	return sym
}

func symbolType(secType int32) string {
	switch secType {
	case adapt.SecurityType_Index:
		return SymbolTypeIndex
	case adapt.SecurityType_Future:
		return SymbolTypeFutures
	default:
		return SymbolTypeSpot
	}
}

func klineType(bin string) (int32, error) {
	info, ok := klineTypes[bin]
	if !ok {
		return 0, fmt.Errorf("futu unsupported kline bin size %q", bin)
	}
	return info.klType, nil
}

func klineSubType(bin string) (int32, error) {
	info, ok := klineTypes[bin]
	if !ok {
		return 0, fmt.Errorf("futu unsupported kline bin size %q", bin)
	}
	return info.subType, nil
}

// canonicalBin maps alias bin sizes (e.g. "60m") to the canonical name
// ("1h") so subscriptions and pushes share the same callback key.
func canonicalBin(bin string) (string, error) {
	klType, err := klineType(bin)
	if err != nil {
		return "", err
	}
	return klineName(klType), nil
}

func klineName(klType int32) string {
	return klineTypeNames[klType]
}

func candleKey(code, bin string) string {
	return code + "|" + bin
}

func subscriptionKey(code string, subType int32) string {
	return code + "|" + strconv.Itoa(int(subType))
}

func klineToCandle(kl *qotcommon.KLine, code string) *Candle {
	ts := int64(kl.GetTimestamp())
	if ts == 0 {
		ts = parseTime(kl.GetTime()).Unix()
	}
	volume := float64(kl.GetVolume())
	if volume == 0 {
		volume = kl.GetHpVolume()
	}
	return &Candle{
		Start:    ts,
		Open:     kl.GetOpenPrice(),
		High:     kl.GetHighPrice(),
		Low:      kl.GetLowPrice(),
		Close:    kl.GetClosePrice(),
		Volume:   volume,
		Turnover: kl.GetTurnover(),
		Table:    code,
	}
}

func orderBookToDepth(asks, bids []*qotcommon.OrderBook) *Depth {
	depth := &Depth{UpdateTime: time.Now()}
	for _, ask := range asks {
		if ask != nil {
			depth.Sells = append(depth.Sells, DepthInfo{Price: ask.GetPrice(), Amount: float64(ask.GetVolume())})
		}
	}
	for _, bid := range bids {
		if bid != nil {
			depth.Buys = append(depth.Buys, DepthInfo{Price: bid.GetPrice(), Amount: float64(bid.GetVolume())})
		}
	}
	return depth
}

func tickerToTrade(tk *qotcommon.Ticker, code string) *Trade {
	side := ""
	switch tk.GetDir() {
	case int32(qotcommon.TickerDirection_TickerDirection_Bid):
		side = "buy"
	case int32(qotcommon.TickerDirection_TickerDirection_Ask):
		side = "sell"
	}
	return &Trade{
		ID:     strconv.FormatInt(tk.GetSequence(), 10),
		Time:   timeFromTimestamp(tk.GetTimestamp(), tk.GetTime()),
		Price:  tk.GetPrice(),
		Amount: float64(tk.GetVolume()),
		Side:   side,
		Remark: code,
	}
}

func sideName(trdSide int32) string {
	switch trdSide {
	case adapt.TrdSide_Buy:
		return "buy"
	case adapt.TrdSide_Sell, int32(trdcommon.TrdSide_TrdSide_SellShort):
		return "sell"
	default:
		return ""
	}
}

func orderStatus(status int32) string {
	switch status {
	case adapt.OrderStatus_Filled_All:
		return OrderStatusFilled
	case adapt.OrderStatus_Filled_Part:
		return "PART_FILLED"
	case adapt.OrderStatus_Cancelled_All, adapt.OrderStatus_Cancelled_Part:
		return OrderStatusCanceled
	case adapt.OrderStatus_Submitted, adapt.OrderStatus_WaitingSubmit, adapt.OrderStatus_Submitting:
		return "SUBMITTED"
	default:
		return "UNKNOWN"
	}
}

func timeFromTimestamp(ts float64, fallback string) time.Time {
	if ts > 0 {
		sec := int64(ts)
		nsec := int64((ts - float64(sec)) * 1e9)
		return time.Unix(sec, nsec)
	}
	return parseTime(fallback)
}

func parseTime(value string) time.Time {
	layouts := []string{
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t
		}
	}
	return time.Now()
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func isRecentRange(start, end time.Time) bool {
	now := time.Now()
	return !end.After(now) && end.After(now.Add(-7*24*time.Hour))
}

func marketAuthorized(acc *trdcommon.TrdAcc, trdMarket int32) bool {
	for _, market := range acc.GetTrdMarketAuthList() {
		if market == trdMarket {
			return true
		}
	}
	return len(acc.GetTrdMarketAuthList()) == 0
}

func isFuturesTrdMarket(market int32) bool {
	switch market {
	case adapt.TrdMarket_Futures,
		adapt.TrdMarket_Futures_Simulate_HK,
		adapt.TrdMarket_Futures_Simulate_US,
		adapt.TrdMarket_Futures_Simulate_SG,
		adapt.TrdMarket_Futures_Simulate_JP:
		return true
	}
	return false
}

func normalizeCode(market, code string) string {
	if market == "HK" && isAllDigits(code) && len(code) < 5 {
		return strings.Repeat("0", 5-len(code)) + code
	}
	return code
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
