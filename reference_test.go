package risktrend

import "sort"

// refTimeline 是测试用的独立参照：单名员工按时间升序的测评时间线。
type refTimeline struct {
	weight  int64
	initial int
	times   []int64
	scores  []int
}

// buildRef 由员工名单与全部测评记录（任意顺序，同员工同时间后者覆盖前者）构建参照时间线。
func buildRef(emps []Employee, history []Assessment) []refTimeline {
	idx := make(map[string]int, len(emps))
	tls := make([]refTimeline, len(emps))
	for i, e := range emps {
		idx[e.ID] = i
		tls[i] = refTimeline{weight: int64(e.Weight), initial: e.Initial}
	}
	for _, a := range history {
		tl := &tls[idx[a.EmployeeID]]
		if k := sort.Search(len(tl.times), func(i int) bool { return tl.times[i] >= a.Time }); k < len(tl.times) && tl.times[k] == a.Time {
			tl.scores[k] = a.Score
		} else {
			tl.times = append(tl.times, 0)
			copy(tl.times[k+1:], tl.times[k:])
			tl.times[k] = a.Time
			tl.scores = append(tl.scores, 0)
			copy(tl.scores[k+1:], tl.scores[k:])
			tl.scores[k] = a.Score
		}
	}
	return tls
}

func (tl *refTimeline) scoreAt(tm int64) int {
	k := sort.Search(len(tl.times), func(i int) bool { return tl.times[i] > tm })
	if k == 0 {
		return tl.initial
	}
	return tl.scores[k-1]
}

// refQuery 从全部记录独立重算 [from, to) 内的加权平均分与高风险秒数。
// 方法：收集区间内全部变化时点，逐段扫描加权总分 V（整数），与库实现完全不同的路径。
func refQuery(tls []refTimeline, from, to int64) (float64, int64) {
	var totalWeight, v int64
	type ev struct {
		t, d int64
	}
	var evs []ev
	for i := range tls {
		tl := &tls[i]
		totalWeight += tl.weight
		prev := tl.scoreAt(from)
		v += tl.weight * int64(prev)
		k := sort.Search(len(tl.times), func(i int) bool { return tl.times[i] > from })
		for ; k < len(tl.times) && tl.times[k] < to; k++ {
			d := tl.weight * int64(tl.scores[k]-prev)
			if d != 0 {
				evs = append(evs, ev{tl.times[k], d})
			}
			prev = tl.scores[k]
		}
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].t < evs[j].t })
	threshold := int64(50) * totalWeight
	var integral, risk int64
	cur := from
	for i := 0; i < len(evs); {
		t := evs[i].t
		integral += v * (t - cur)
		if v < threshold {
			risk += t - cur
		}
		for i < len(evs) && evs[i].t == t {
			v += evs[i].d
			i++
		}
		cur = t
	}
	integral += v * (to - cur)
	if v < threshold {
		risk += to - cur
	}
	return float64(integral) / (float64(totalWeight) * float64(to-from)), risk
}
