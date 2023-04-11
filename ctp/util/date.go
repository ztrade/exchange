package util

import (
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

type MinuteOfDay int

type MinuteOfDayRange struct {
	Start MinuteOfDay
	End   MinuteOfDay
}

type TradeTimeRange struct {
	Weekdays []time.Weekday
	Ranges   []MinuteOfDayRange
}

var (
	TradeTimes = TradeTime{Range: map[time.Weekday]TimeRangeList{
		time.Monday: TimeRangeList{
			{Start: DayMinute(8, 50), End: DayMinute(11, 35)},
			{Start: DayMinute(13, 30), End: DayMinute(15, 5)},
			{Start: DayMinute(20, 50), End: DayMinute(24, 0)},
		},
		time.Tuesday: TimeRangeList{
			{Start: DayMinute(0, 0), End: DayMinute(03, 5)},
			{Start: DayMinute(8, 50), End: DayMinute(11, 35)},
			{Start: DayMinute(13, 30), End: DayMinute(15, 5)},
			{Start: DayMinute(20, 50), End: DayMinute(24, 0)},
		},
		time.Wednesday: TimeRangeList{
			{Start: DayMinute(0, 0), End: DayMinute(03, 5)},
			{Start: DayMinute(8, 50), End: DayMinute(11, 35)},
			{Start: DayMinute(13, 30), End: DayMinute(15, 5)},
			{Start: DayMinute(20, 50), End: DayMinute(24, 0)},
		},
		time.Thursday: TimeRangeList{
			{Start: DayMinute(0, 0), End: DayMinute(03, 5)},
			{Start: DayMinute(8, 50), End: DayMinute(11, 35)},
			{Start: DayMinute(13, 30), End: DayMinute(15, 5)},
			{Start: DayMinute(20, 50), End: DayMinute(24, 0)},
		},
		time.Friday: TimeRangeList{
			{Start: DayMinute(0, 0), End: DayMinute(03, 5)},
			{Start: DayMinute(8, 50), End: DayMinute(11, 35)},
			{Start: DayMinute(13, 30), End: DayMinute(15, 5)},
		},
	}}
	nextDay = DayMinute(24, 0)
)

func DayMinute(hour, minute int) MinuteOfDay {
	return (MinuteOfDay)(hour*60 + minute)
}

type TimeRangeList []MinuteOfDayRange

func (tl TimeRangeList) NeedWait(tm time.Time) time.Duration {
	minutes := DayMinute(tm.Hour(), tm.Minute())
	for _, v := range tl {
		fmt.Println(minutes, v.Start, v.End)
		if v.End < minutes {
			continue
		}
		if v.Start < minutes {
			return 0
		}
		n := v.Start - minutes - 1
		if n <= 0 {
			n = 0
		}
		return time.Duration(n) * time.Minute
	}
	dur := nextDay - minutes
	return time.Duration(dur) * time.Minute
}

type TradeTime struct {
	Range map[time.Weekday]TimeRangeList
}

func (t *TradeTime) Wait() {
	for {
		tm := time.Now()
		rg, ok := t.Range[tm.Weekday()]
		var wait time.Duration
		if !ok {
			wait = time.Duration(nextDay-DayMinute(tm.Hour(), tm.Hour())) * time.Minute
		} else {
			wait = rg.NeedWait(tm)
		}
		if wait == 0 {
			break
		}
		logrus.Infof("%s no trade time,wait for: %s", tm, wait)
		time.Sleep(wait)
	}
	fmt.Println("wait finished")

}

func WaitTradeTime() {
	TradeTimes.Wait()
}
