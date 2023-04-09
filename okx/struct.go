package okx

type OkxConfig struct {
	Key    string
	Secret string
	Pwd    string
	Tdmode string
	IsTest bool
}

type CandleResp struct {
	Code string      `json:"code"`
	Msg  string      `json:"msg"`
	Data [][9]string `json:"data"`
}

type OKEXOrder struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []struct {
		ClOrdID string `json:"clOrdId"`
		OrdID   string `json:"ordId"`
		Tag     string `json:"tag"`
		SCode   string `json:"sCode"`
		SMsg    string `json:"sMsg"`
	} `json:"data"`
}

type OKEXAlgoOrder struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []struct {
		AlgoID string `json:"algoId"`
		SCode  string `json:"sCode"`
		SMsg   string `json:"sMsg"`
	} `json:"data"`
}

type InstrumentResp struct {
	Code string       `json:"code"`
	Msg  string       `json:"msg"`
	Data []Instrument `json:"data"`
}

type Instrument struct {
	InstType  string `json:"instType"`  // 产品类型
	InstID    string `json:"instId"`    // 产品id， 如 BTC-USD-SWAP
	Uly       string `json:"uly"`       // 标的指数，如 BTC-USD，仅适用于交割/永续/期权
	Category  string `json:"category"`  // 手续费档位，每个交易产品属于哪个档位手续费
	BaseCcy   string `json:"baseCcy"`   // 交易货币币种，如 BTC-USDT 中的 BTC ，仅适用于币币
	QuoteCcy  string `json:"quoteCcy"`  // 计价货币币种，如 BTC-USDT 中的USDT ，仅适用于币币
	SettleCcy string `json:"settleCcy"` // 盈亏结算和保证金币种，如 BTC 仅适用于交割/永续/期权
	CtVal     string `json:"ctVal"`     // 合约面值，仅适用于交割/永续/期权
	CtMult    string `json:"ctMult"`    // 合约乘数，仅适用于交割/永续/期权
	CtValCcy  string `json:"ctValCcy"`  // 合约面值计价币种，仅适用于交割/永续/期权
	OptType   string `json:"optType"`   // 期权类型，C或P 仅适用于期权
	Stk       string `json:"stk"`       // 行权价格，仅适用于期权
	ListTime  string `json:"listTime"`  // 上线日期 Unix时间戳的毫秒数格式，如 1597026383085
	ExpTime   string `json:"expTime"`   // 交割/行权日期，仅适用于交割 和 期权 Unix时间戳的毫秒数格式，如 1597026383085
	Lever     string `json:"lever"`     // 该instId支持的最大杠杆倍数，不适用于币币、期权
	TickSz    string `json:"tickSz"`    // 下单价格精度，如 0.0001
	LotSz     string `json:"lotSz"`     // 下单数量精度，如 BTC-USDT-SWAP：1
	MinSz     string `json:"minSz"`     // 最小下单数量
	CtType    string `json:"ctType"`    // linear：正向合约 inverse：反向合约 仅适用于交割/永续
	Alias     string `json:"alias"`     // 合约日期别名 this_week：本周 next_week：次周 quarter：季度 next_quarter：次季度 仅适用于交割
	State     string `json:"state"`     // 产品状态 live：交易中 suspend：暂停中 preopen：预上线settlement：资金费结算
}

type AccountConfig struct {
	Code string `json:"code"`
	Data []struct {
		AcctLv         string `json:"acctLv"`
		AutoLoan       bool   `json:"autoLoan"`
		CtIsoMode      string `json:"ctIsoMode"`
		GreeksType     string `json:"greeksType"`
		Level          string `json:"level"`
		LevelTmp       string `json:"levelTmp"`
		MgnIsoMode     string `json:"mgnIsoMode"`
		PosMode        string `json:"posMode"`
		SpotOffsetType string `json:"spotOffsetType"`
		UID            string `json:"uid"`
		Label          string `json:"label"`
		RoleType       string `json:"roleType"`
		TraderInsts    []any  `json:"traderInsts"`
		OpAuth         string `json:"opAuth"`
		IP             string `json:"ip"`
	} `json:"data"`
	Msg string `json:"msg"`
}

type AccountPosition struct {
	Adl            string `json:"adl"`
	AvailPos       string `json:"availPos"`
	AvgPx          string `json:"avgPx"`
	CTime          string `json:"cTime"`
	Ccy            string `json:"ccy"`
	DeltaBS        string `json:"deltaBS"`
	DeltaPA        string `json:"deltaPA"`
	GammaBS        string `json:"gammaBS"`
	GammaPA        string `json:"gammaPA"`
	Imr            string `json:"imr"`
	InstID         string `json:"instId"`
	InstType       string `json:"instType"`
	Interest       string `json:"interest"`
	Last           string `json:"last"`
	UsdPx          string `json:"usdPx"`
	Lever          string `json:"lever"`
	Liab           string `json:"liab"`
	LiabCcy        string `json:"liabCcy"`
	LiqPx          string `json:"liqPx"`
	MarkPx         string `json:"markPx"`
	Margin         string `json:"margin"`
	MgnMode        string `json:"mgnMode"`
	MgnRatio       string `json:"mgnRatio"`
	Mmr            string `json:"mmr"`
	NotionalUsd    string `json:"notionalUsd"`
	OptVal         string `json:"optVal"`
	PTime          string `json:"pTime"`
	Pos            string `json:"pos"`
	PosCcy         string `json:"posCcy"`
	PosID          string `json:"posId"`
	PosSide        string `json:"posSide"`
	SpotInUseAmt   string `json:"spotInUseAmt"`
	SpotInUseCcy   string `json:"spotInUseCcy"`
	ThetaBS        string `json:"thetaBS"`
	ThetaPA        string `json:"thetaPA"`
	TradeID        string `json:"tradeId"`
	BizRefID       string `json:"bizRefId"`
	BizRefType     string `json:"bizRefType"`
	QuoteBal       string `json:"quoteBal"`
	BaseBal        string `json:"baseBal"`
	BaseBorrowed   string `json:"baseBorrowed"`
	BaseInterest   string `json:"baseInterest"`
	QuoteBorrowed  string `json:"quoteBorrowed"`
	QuoteInterest  string `json:"quoteInterest"`
	UTime          string `json:"uTime"`
	Upl            string `json:"upl"`
	UplRatio       string `json:"uplRatio"`
	VegaBS         string `json:"vegaBS"`
	VegaPA         string `json:"vegaPA"`
	CloseOrderAlgo []struct {
		AlgoID          string `json:"algoId"`
		SlTriggerPx     string `json:"slTriggerPx"`
		SlTriggerPxType string `json:"slTriggerPxType"`
		TpTriggerPx     string `json:"tpTriggerPx"`
		TpTriggerPxType string `json:"tpTriggerPxType"`
		CloseFraction   string `json:"closeFraction"`
	} `json:"closeOrderAlgo"`
}

type AccountPositionResp struct {
	Code string            `json:"code"`
	Msg  string            `json:"msg"`
	Data []AccountPosition `json:"data"`
}

type AccountBalance struct {
	AdjEq   string `json:"adjEq"`
	Details []struct {
		AvailBal      string `json:"availBal"`
		AvailEq       string `json:"availEq"`
		CashBal       string `json:"cashBal"`
		Ccy           string `json:"ccy"`
		CrossLiab     string `json:"crossLiab"`
		DisEq         string `json:"disEq"`
		Eq            string `json:"eq"`
		EqUsd         string `json:"eqUsd"`
		FrozenBal     string `json:"frozenBal"`
		Interest      string `json:"interest"`
		IsoEq         string `json:"isoEq"`
		IsoLiab       string `json:"isoLiab"`
		IsoUpl        string `json:"isoUpl"`
		Liab          string `json:"liab"`
		MaxLoan       string `json:"maxLoan"`
		MgnRatio      string `json:"mgnRatio"`
		NotionalLever string `json:"notionalLever"`
		OrdFrozen     string `json:"ordFrozen"`
		Twap          string `json:"twap"`
		UTime         string `json:"uTime"`
		Upl           string `json:"upl"`
		UplLiab       string `json:"uplLiab"`
		StgyEq        string `json:"stgyEq"`
		SpotInUseAmt  string `json:"spotInUseAmt"`
	} `json:"details"`
	Imr         string `json:"imr"`
	IsoEq       string `json:"isoEq"`
	MgnRatio    string `json:"mgnRatio"`
	Mmr         string `json:"mmr"`
	NotionalUsd string `json:"notionalUsd"`
	OrdFroz     string `json:"ordFroz"`
	TotalEq     string `json:"totalEq"`
	UTime       string `json:"uTime"`
}

type AccountBalanceResp struct {
	Code string           `json:"code"`
	Data []AccountBalance `json:"data"`
	Msg  string           `json:"msg"`
}

type CancelNormalResp struct {
	Code string        `json:"code"`
	Msg  string        `json:"msg"`
	Data []OrderNormal `json:"data"`
}

type CancelAlgoResp struct {
	Code string      `json:"code"`
	Msg  string      `json:"msg"`
	Data []AlgoOrder `json:"data"`
}
