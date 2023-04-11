package ctp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/ztrade/ctp"
	"github.com/ztrade/exchange/ctp/util"
)

type TdSpi struct {
	ctp.CThostFtdcTraderSpiBase
	hasLogin     util.SafeWait
	hasSymbols   atomic.Bool
	symbols      map[string]*ctp.CThostFtdcInstrumentField
	symbolsCache map[string]*ctp.CThostFtdcInstrumentField
	l            *logrus.Entry
	// symbolsMutex sync.RWMutex
	api *ctp.CThostFtdcTraderApi
	cfg *Config

	connectTime time.Time
	frontID     int
	sessionID   int

	tradeFn func(*ctp.CThostFtdcTradeField)
	orderFn func(*ctp.CThostFtdcOrderField)
}

func NewTdSpi(cfg *Config, api *ctp.CThostFtdcTraderApi) *TdSpi {
	td := new(TdSpi)
	td.cfg = cfg
	td.api = api
	td.symbols = make(map[string]*ctp.CThostFtdcInstrumentField)
	td.symbolsCache = make(map[string]*ctp.CThostFtdcInstrumentField)
	td.l = logrus.WithField("module", "tdSpi")
	return td
}

func (s *TdSpi) Connect(ctx context.Context) (err error) {
	s.api = ctp.TdCreateFtdcTraderApi("./td/")
	s.api.RegisterSpi(s)
	s.api.RegisterFront(fmt.Sprintf("tcp://%s", s.cfg.TdServer))
	s.api.Init()
	err = s.WaitSymbols(ctx)
	return
}
func (s *TdSpi) SetTradeFn(fn func(*ctp.CThostFtdcTradeField)) {
	s.tradeFn = fn
}

func (s *TdSpi) SetOrderFn(fn func(*ctp.CThostFtdcOrderField)) {
	s.orderFn = fn
}

func (s *TdSpi) WaitSymbols(ctx context.Context) (err error) {
Out:
	for {
		select {
		case <-ctx.Done():
			return errors.New("deadline")
		default:
			if s.hasSymbols.Load() {
				break Out
			}
			time.Sleep(time.Millisecond)
		}
	}
	return
}

func (s *TdSpi) GetSymbols() (symbols map[string]*ctp.CThostFtdcInstrumentField) {
	return s.symbols
}

func (s *TdSpi) OnFrontConnected() {
	s.symbolsCache = make(map[string]*ctp.CThostFtdcInstrumentField)
	nRet := s.api.ReqAuthenticate(&ctp.CThostFtdcReqAuthenticateField{BrokerID: s.cfg.BrokerID, UserID: s.cfg.User, UserProductInfo: "", AuthCode: s.cfg.AuthCode, AppID: s.cfg.AppID}, util.GetReqID())
	s.l.Info("TdSpi OnFrontConnected, ReqAuthenticate:", nRet)
	s.connectTime = time.Now()
	if nRet != 0 {
		err := fmt.Errorf("ReqAuthenticate failed: %d", nRet)
		s.l.Error(err.Error())
		s.hasLogin.Done(err)
		return
	}
}

func (s *TdSpi) OnFrontDisconnected(nReason int) {
	s.hasSymbols.Store(false)
	s.l.Info("TdSpi OnFrontDisconnected")
}

func (s *TdSpi) OnRspAuthenticate(pRspAuthenticateField *ctp.CThostFtdcRspAuthenticateField, pRspInfo *ctp.CThostFtdcRspInfoField, nRequestID int, bIsLast bool) {
	buf, _ := json.Marshal(pRspAuthenticateField)
	s.l.Debug("OnRspAuthenticate:", string(buf))
	if pRspInfo != nil && pRspInfo.ErrorID != 0 {
		s.l.Errorf("OnRspAuthenticate error %d %s, retry after 10s", pRspInfo.ErrorID, pRspInfo.ErrorMsg)
		t := s.connectTime
		go func() {
			time.Sleep(time.Second * 10)
			connTime := s.connectTime
			if t != s.connectTime {
				s.l.Warnf("connect time not match, OnRspAuthenticate ignore: %s, %s", t, connTime)
				return
			}
			n := s.api.ReqAuthenticate(&ctp.CThostFtdcReqAuthenticateField{BrokerID: s.cfg.BrokerID, UserID: s.cfg.User, UserProductInfo: "", AuthCode: s.cfg.AuthCode, AppID: s.cfg.AppID}, 0)
			s.l.Info("OnRspAuthenticate fail, do ReqAuthenticate:", n)
		}()

		return
	}

	nRet := s.api.ReqUserLogin(&ctp.CThostFtdcReqUserLoginField{UserID: s.cfg.User, BrokerID: s.cfg.BrokerID, Password: s.cfg.Password}, util.GetReqID())
	if nRet != 0 {
		s.hasLogin.Done(fmt.Errorf("ReqUserLogin failed: %d", nRet))
	}
	s.l.Info("TdSpi OnRspAuthenticate, ReqUserLogin:", nRet)
}

func (s *TdSpi) OnRspUserLogin(pRspUserLogin *ctp.CThostFtdcRspUserLoginField, pRspInfo *ctp.CThostFtdcRspInfoField, nRequestID int, bIsLast bool) {
	buf, _ := json.Marshal(pRspUserLogin)
	s.l.Debug("OnRspUserLogin", string(buf))
	if pRspInfo != nil && pRspInfo.ErrorID != 0 {
		s.l.Errorf("OnRspUserLogin error: %d %s, retry after 10s", pRspInfo.ErrorID, pRspInfo.ErrorMsg)
		t := s.connectTime
		go func() {
			time.Sleep(10 * time.Second)
			connTime := s.connectTime
			if t != s.connectTime {
				s.l.Warnf("connect time not match, OnRspUserLogin ignore: %s, %s", t, connTime)
				return
			}
			n := s.api.ReqUserLogin(&ctp.CThostFtdcReqUserLoginField{UserID: s.cfg.User, BrokerID: s.cfg.BrokerID, Password: s.cfg.Password}, 0)
			s.l.Info("OnRspUserLogin fail, do ReqUserLogin:", n)
		}()

		return
	}
	go func() {
		n := s.api.ReqQryInstrument(&ctp.CThostFtdcQryInstrumentField{}, 1)
		s.l.Info("TdSpi OnRspUserLogin, ReqQryInstrument:", n)
	}()

	if pRspInfo != nil && pRspInfo.ErrorID != 0 {
		err := fmt.Errorf("OnRspUserLogin error %d,%s", pRspInfo.ErrorID, pRspInfo.ErrorMsg)
		s.hasLogin.Done(err)
		return
	}
	pSettlementInfoConfirm := &ctp.CThostFtdcSettlementInfoConfirmField{
		BrokerID:   pRspUserLogin.BrokerID,
		InvestorID: pRspUserLogin.UserID,
	}
	s.frontID = pRspUserLogin.FrontID
	s.sessionID = pRspUserLogin.SessionID
	nRet := s.api.ReqSettlementInfoConfirm(pSettlementInfoConfirm, util.GetReqID())
	if nRet != 0 {
		s.hasLogin.Done(fmt.Errorf("SettlementInfoConfirm failed: %d", nRet))
	}
}

func (s *TdSpi) OnRspSettlementInfoConfirm(pSettlementInfoConfirm *ctp.CThostFtdcSettlementInfoConfirmField, pRspInfo *ctp.CThostFtdcRspInfoField, nRequestID int, bIsLast bool) {
	if pRspInfo != nil && pRspInfo.ErrorID != 0 {
		err := fmt.Errorf("OnRspSettlementInfoConfirm error: %s", pRspInfo.ErrorMsg)
		logrus.Error(err.Error())
		s.hasLogin.Done(err)
		return
	}
	buf, _ := json.Marshal(pSettlementInfoConfirm)
	logrus.Info("OnRspSettlementInfoConfirm:", string(buf))
	s.hasLogin.Done(nil)
}

func (s *TdSpi) OnRtnInstrumentStatus(pInstrumentStatus *ctp.CThostFtdcInstrumentStatusField) {
	buf, _ := json.Marshal(pInstrumentStatus)
	s.l.Debug("OnRtnInstrumentStatus:", string(buf))
}

func (s *TdSpi) OnRspQryInstrument(pInstrument *ctp.CThostFtdcInstrumentField, pRspInfo *ctp.CThostFtdcRspInfoField, nRequestID int, bIsLast bool) {
	defer func() {
		if bIsLast {
			if len(s.symbolsCache) != 0 {
				s.symbols = s.symbolsCache
				s.hasSymbols.Store(true)
			} else {
				s.l.Errorf("OnRspQryInstrument return empty, retry after 10s")
				t := s.connectTime
				go func() {
					time.Sleep(10 * time.Second)
					connTime := s.connectTime
					if t != s.connectTime {
						s.l.Warnf("connect time not match, OnRspQryInstrument ignore: %s, %s", t, connTime)
						return
					}
					n := s.api.ReqQryInstrument(&ctp.CThostFtdcQryInstrumentField{}, 1)
					s.l.Info("TdSpi OnRspQryInstrument no symbols, ReqQryInstrument:", n)
				}()
			}
		}
	}()
	s.l.Debug("OnRspQryInstrument:", pInstrument)

	if pRspInfo != nil && pRspInfo.ErrorID != 0 {
		s.l.Error("OnRspQryInstrument error", pRspInfo.ErrorID, pRspInfo.ErrorMsg)
	}
	if pInstrument == nil {
		s.l.Warn("pInstrument is null")
		return
	}
	if pInstrument.ProductClass != '1' {
		return
	}
	s.symbolsCache[pInstrument.InstrumentID] = pInstrument
}

func (s *TdSpi) WaitLogin(ctx context.Context) (err error) {
	return s.hasLogin.Wait(ctx)
}

// OnRspOrderInsert 报单录入请求响应，当执行ReqOrderInsert后有字段填写不对之类的CTP报错则通过此接口返回
func (s *TdSpi) OnRspOrderInsert(pInputOrder *ctp.CThostFtdcInputOrderField, pRspInfo *ctp.CThostFtdcRspInfoField, nRequestID int, bIsLast bool) {
	logrus.Info("OnRspOrderInsert:", nRequestID, bIsLast)
	util.OnRsp[ctp.CThostFtdcInputOrderField](pInputOrder, pRspInfo, nRequestID, bIsLast)
}

// OnErrRtnOrderInsert 此接口仅在报单被 CTP 端拒绝时被调用用来进行报错。
func (s *TdSpi) OnErrRtnOrderInsert(pInputOrder *ctp.CThostFtdcInputOrderField, pRspInfo *ctp.CThostFtdcRspInfoField) {
	if pRspInfo != nil && pRspInfo.ErrorID != 0 {
		logrus.Error("OnErrRtnOrderInsert error:", pRspInfo.ErrorMsg)
		return
	}
	buf, _ := json.Marshal(pInputOrder)
	logrus.Info("OnErrRtnOrderInsert:", string(buf))
}

// {"BrokerID":"9999","InvestorID":"164347","InstrumentID":"al2201","OrderRef":"1","UserID":"164347","OrderPriceType":50,"Direction":48,"CombOffsetFlag":"0","CombHedgeFlag":"1","LimitPrice":18640,"VolumeTotalOriginal":1,"TimeCondition":51,"GTDDate":"","VolumeCondition":49,"MinVolume":1,"ContingentCondition":49,"StopPrice":0,"ForceCloseReason":48,"IsAutoSuspend":0,"BusinessUnit":"9999cac","RequestID":0,"OrderLocalID":"       12405","ExchangeID":"SHFE","ParticipantID":"9999","ClientID":"9999164327","ExchangeInstID":"al2201","TraderID":"9999cac","InstallID":1,"OrderSubmitStatus":48,"NotifySequence":0,"TradingDay":"20211117","SettlementID":1,"OrderSysID":"       29722","OrderSource":48,"OrderStatus":48,"OrderType":48,"VolumeTraded":1,"VolumeTotal":0,"InsertDate":"20211117","InsertTime":"00:08:49","ActiveTime":"","SuspendTime":"","UpdateTime":"","CancelTime":"","ActiveTraderID":"9999cac","ClearingPartID":"","SequenceNo":21573,"FrontID":1,"SessionID":2040216403,"UserProductInfo":"","StatusMsg":"全部成交报单已提交","UserForceClose":0,"ActiveUserID":"","BrokerOrderSeq":32344,"RelativeOrderSysID":"","ZCETotalTradedVolume":0,"IsSwapOrder":0,"BranchID":"","InvestUnitID":"","AccountID":"","CurrencyID":"","IPAddress":"","MacAddress":""}
func (s *TdSpi) OnRtnOrder(pOrder *ctp.CThostFtdcOrderField) {
	if s.orderFn != nil {
		s.orderFn(pOrder)
	}
}
func (s *TdSpi) OnRtnTrade(pTrade *ctp.CThostFtdcTradeField) {
	if s.tradeFn != nil {
		s.tradeFn(pTrade)
	}
}

func (s *TdSpi) OnRspQrySettlementInfo(pSettlementInfo *ctp.CThostFtdcSettlementInfoField, pRspInfo *ctp.CThostFtdcRspInfoField, nRequestID int, bIsLast bool) {
	if pRspInfo != nil && pRspInfo.ErrorID != 0 {
		logrus.Error("OnRspQrySettlementInfo error:", pRspInfo.ErrorMsg)
		return
	}
	buf, _ := json.Marshal(pSettlementInfo)
	logrus.Info("OnRspQrySettlementInfo:", string(buf))
}

func (s *TdSpi) OnRspQryInvestorPosition(pInvestorPosition *ctp.CThostFtdcInvestorPositionField, pRspInfo *ctp.CThostFtdcRspInfoField, nRequestID int, bIsLast bool) {
	util.OnRsp[ctp.CThostFtdcInvestorPositionField](pInvestorPosition, pRspInfo, nRequestID, bIsLast)
}

func (s *TdSpi) OnRspQryInvestorPositionDetail(pInvestorPositionDetail *ctp.CThostFtdcInvestorPositionDetailField, pRspInfo *ctp.CThostFtdcRspInfoField, nRequestID int, bIsLast bool) {
	util.OnRsp[ctp.CThostFtdcInvestorPositionDetailField](pInvestorPositionDetail, pRspInfo, nRequestID, bIsLast)
}

func (s *TdSpi) OnRspOrderAction(pInputOrderAction *ctp.CThostFtdcInputOrderActionField, pRspInfo *ctp.CThostFtdcRspInfoField, nRequestID int, bIsLast bool) {
	util.OnRsp[ctp.CThostFtdcInputOrderActionField](pInputOrderAction, pRspInfo, nRequestID, bIsLast)
}

func (s *TdSpi) OnErrRtnOrderAction(pOrderAction *ctp.CThostFtdcOrderActionField, pRspInfo *ctp.CThostFtdcRspInfoField) {
	buf, _ := json.Marshal(pOrderAction)
	logrus.Info("OnErrRtnOrderActionOnRspOrderAction:", string(buf))
}

func (s *TdSpi) OnRspQryTradingAccount(pTradingAccount *ctp.CThostFtdcTradingAccountField, pRspInfo *ctp.CThostFtdcRspInfoField, nRequestID int, bIsLast bool) {
	util.OnRsp[ctp.CThostFtdcTradingAccountField](pTradingAccount, pRspInfo, nRequestID, bIsLast)
}
