package util

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"
	"github.com/ztrade/ctp"
)

var (
	reqID uint64
	reqs  sync.Map
)

func GetReqID() int {
	n := atomic.AddUint64(&reqID, 1)
	return int(n)
}

type SyncReq[T any] struct {
	ID         int
	Result     []*T
	isFinished chan struct{}
	err        error
}

func NewSyncReq[T any](id int) *SyncReq[T] {
	r := new(SyncReq[T])
	r.isFinished = make(chan struct{})
	r.ID = id
	return r
}

func (r *SyncReq[T]) cb(result *T, pRspInfo *ctp.CThostFtdcRspInfoField, isLast bool) {
	if result != nil {
		r.Result = append(r.Result, result)
	}
	if pRspInfo != nil && pRspInfo.ErrorID != 0 {
		r.err = fmt.Errorf("[%d]%s", pRspInfo.ErrorID, pRspInfo.ErrorMsg)
	}
	if isLast {
		close(r.isFinished)
	}
}

func (r *SyncReq[T]) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.isFinished:
		return r.err
	}

}

func Req[T any](ctx context.Context, req func(id int) error) (result *T, err error) {
	results, err := ReqWithResps[T](ctx, req)
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

func ReqWithResps[T any](ctx context.Context, req func(id int) error) (result []*T, err error) {
	id := GetReqID()
	r := NewSyncReq[T](id)
	reqs.Store(id, r)
	err = req(id)
	if err != nil {
		reqs.Delete(id)
		return nil, err
	}
	err = r.Wait(ctx)
	if err != nil {
		return nil, err
	}
	return r.Result, nil
}

func OnRsp[T any](resp *T, pRspInfo *ctp.CThostFtdcRspInfoField, nRequestID int, bIsLast bool) {
	buf1, _ := json.Marshal(resp)
	buf2, _ := json.Marshal(pRspInfo)
	logrus.Info("OnRsp:", reflect.TypeOf(resp), string(buf1), string(buf2), nRequestID, bIsLast)
	value, ok := reqs.Load(nRequestID)
	if !ok {
		logrus.Errorf("Sync OnRsp, no such requestID: %d, resp: %#v", nRequestID, resp)
		return
	}
	if bIsLast {
		reqs.Delete(nRequestID)
	}
	req := value.(*SyncReq[T])
	req.cb(resp, pRspInfo, bIsLast)
}
